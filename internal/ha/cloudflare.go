// cloudflare.go 是仲裁所需的最小 Cloudflare DNS API 客户端。
//
// 只用到三个能力：读 TXT 记录（租约）、写 TXT 记录（续租 / 夺取 / 交还）、
// 维护 A 记录（把用户流量指向当前 primary）。刻意不引入官方 SDK——
// 需要的表面积只有三个端点，而多一个依赖就多一份供应链与版本维护成本。
package ha

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"
)

const (
	cfAPIBase = "https://api.cloudflare.com/client/v4"
	// cfTimeout 单次 API 调用超时。
	// 取 10s：明显短于自降级死线（45s），保证"续租失败"能在死线前被判定出来，
	// 而不是被一个卡住的 HTTP 请求拖过死线导致降级判断迟到。
	cfTimeout = 10 * time.Second
	// cfRecordTTL 写入记录时使用的 TTL。
	// 我们通过 API 读租约而非 DNS 解析，所以这个值对仲裁没有影响；
	// 取 60（Cloudflare 允许的最小值）只是为了方便用 dig 人工排查时能看到最新值。
	cfRecordTTL = 60
)

// CFClient 是 Cloudflare DNS API 的最小客户端，并发安全。
type CFClient struct {
	token  string
	zoneID string
	http   *http.Client

	// mu 保护 offset。
	mu sync.RWMutex
	// offset 是 Cloudflare 服务端时钟与本机时钟的差值，每次 API 调用后刷新。
	//
	// 这是本方案回避时钟漂移的关键：租约的安全边界（自降级死线 45s < 租约 TTL 60s
	// < 夺取保护期 75s）建立在两节点对"现在几点"的共识上。两台 VPS 各自的 NTP
	// 精度无法保证，但它们调用的是同一个 Cloudflare API，响应里的 Date 头天然
	// 就是一个双方共享的权威时钟。
	offset    time.Duration
	offsetSet bool
}

// NewCFClient 构造客户端。token 需要 Zone → DNS → Edit 权限。
func NewCFClient(token, zoneID string) *CFClient {
	return &CFClient{
		token:  token,
		zoneID: zoneID,
		http:   &http.Client{Timeout: cfTimeout},
	}
}

// Now 返回以 Cloudflare 服务端时钟为准的当前时间。
//
// 尚未成功调用过任何 API（offset 未知）时退回本机时钟——此时也做不了任何
// 需要授时的决策，因为决策的前提是先读到租约。
func (c *CFClient) Now() time.Time {
	c.mu.RLock()
	defer c.mu.RUnlock()
	if !c.offsetSet {
		return time.Now()
	}
	return time.Now().Add(c.offset)
}

// SyncedClock 报告是否已经与 Cloudflare 对过时。
func (c *CFClient) SyncedClock() bool {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.offsetSet
}

func (c *CFClient) noteServerTime(h http.Header) {
	raw := h.Get("Date")
	if raw == "" {
		return
	}
	t, err := http.ParseTime(raw)
	if err != nil {
		return
	}
	c.mu.Lock()
	c.offset = time.Until(t)
	c.offsetSet = true
	c.mu.Unlock()
}

// cfRecord 是 DNS 记录的部分字段（只取本包需要的）。
type cfRecord struct {
	ID      string `json:"id"`
	Name    string `json:"name"`
	Type    string `json:"type"`
	Content string `json:"content"`
	TTL     int    `json:"ttl"`
	Proxied bool   `json:"proxied"`
}

type cfEnvelope struct {
	Success bool            `json:"success"`
	Errors  []cfAPIError    `json:"errors"`
	Result  json.RawMessage `json:"result"`
}

type cfAPIError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

func (e cfAPIError) String() string { return fmt.Sprintf("%d %s", e.Code, e.Message) }

func joinErrors(errs []cfAPIError) string {
	if len(errs) == 0 {
		return "未知错误"
	}
	parts := make([]string, 0, len(errs))
	for _, e := range errs {
		parts = append(parts, e.String())
	}
	return strings.Join(parts, "; ")
}

// do 执行一次 API 调用并解析统一响应信封。
func (c *CFClient) do(ctx context.Context, method, path string, body any) (json.RawMessage, error) {
	var rdr io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			return nil, fmt.Errorf("ha/cf: 序列化请求体: %w", err)
		}
		rdr = bytes.NewReader(b)
	}

	req, err := http.NewRequestWithContext(ctx, method, cfAPIBase+path, rdr)
	if err != nil {
		return nil, fmt.Errorf("ha/cf: 构造请求: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+c.token)
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}

	resp, err := c.http.Do(req)
	if err != nil {
		return nil, fmt.Errorf("ha/cf: %s %s: %w", method, path, err)
	}
	defer func() { _ = resp.Body.Close() }()

	// 即便是错误响应，Date 头依然是权威时钟，先记下来。
	c.noteServerTime(resp.Header)

	raw, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return nil, fmt.Errorf("ha/cf: 读取响应: %w", err)
	}

	var env cfEnvelope
	if err := json.Unmarshal(raw, &env); err != nil {
		return nil, fmt.Errorf("ha/cf: %s %s 返回非 JSON（HTTP %d）", method, path, resp.StatusCode)
	}
	if !env.Success {
		return nil, fmt.Errorf("ha/cf: %s %s 失败（HTTP %d）: %s", method, path, resp.StatusCode, joinErrors(env.Errors))
	}
	return env.Result, nil
}

// findRecord 按名字与类型查记录；不存在时返回 nil 且不报错。
func (c *CFClient) findRecord(ctx context.Context, name, typ string) (*cfRecord, error) {
	q := url.Values{}
	q.Set("type", typ)
	q.Set("name", name)
	raw, err := c.do(ctx, http.MethodGet, "/zones/"+c.zoneID+"/dns_records?"+q.Encode(), nil)
	if err != nil {
		return nil, err
	}
	var list []cfRecord
	if err := json.Unmarshal(raw, &list); err != nil {
		return nil, fmt.Errorf("ha/cf: 解析记录列表: %w", err)
	}
	if len(list) == 0 {
		return nil, nil
	}
	return &list[0], nil
}

// GetTXT 读取 TXT 记录内容。记录不存在时返回空串且不报错。
func (c *CFClient) GetTXT(ctx context.Context, name string) (string, error) {
	rec, err := c.findRecord(ctx, name, "TXT")
	if err != nil || rec == nil {
		return "", err
	}
	return rec.Content, nil
}

// PutTXT 写入 TXT 记录内容（不存在则创建）。
//
// Cloudflare DNS API 没有 compare-and-swap，所以这里是无条件覆盖。
// 本方案通过"每条记录只有一个写入者"（租约只由 owner 写、交还请求只由挑战者写）
// 在协议层规避了并发覆盖，而不是依赖 API 层的原子性。
func (c *CFClient) PutTXT(ctx context.Context, name, content string) error {
	rec, err := c.findRecord(ctx, name, "TXT")
	if err != nil {
		return err
	}
	if rec == nil {
		_, err = c.do(ctx, http.MethodPost, "/zones/"+c.zoneID+"/dns_records", map[string]any{
			"type":    "TXT",
			"name":    name,
			"content": content,
			"ttl":     cfRecordTTL,
		})
		return err
	}
	_, err = c.do(ctx, http.MethodPatch, "/zones/"+c.zoneID+"/dns_records/"+rec.ID, map[string]any{
		"content": content,
	})
	return err
}

// GetA 读取 A 记录当前指向的 IP。记录不存在时返回空串。
func (c *CFClient) GetA(ctx context.Context, name string) (string, error) {
	rec, err := c.findRecord(ctx, name, "A")
	if err != nil || rec == nil {
		return "", err
	}
	return rec.Content, nil
}

// EnsureA 把 A 记录指向 ip；已经正确时不发写请求，返回 false 表示无需变更。
//
// 用 PATCH 只改 content，从而保留记录原有的 proxied（橙云/灰云）与其它设置——
// 用 PUT 全量覆盖会在切换时意外把橙云打回灰云，导致源站 IP 泄露。
func (c *CFClient) EnsureA(ctx context.Context, name, ip string) (bool, error) {
	rec, err := c.findRecord(ctx, name, "A")
	if err != nil {
		return false, err
	}
	if rec == nil {
		return false, fmt.Errorf("ha/cf: A 记录 %s 不存在，请先在 Cloudflare 手工创建", name)
	}
	if rec.Content == ip {
		return false, nil
	}
	if _, err := c.do(ctx, http.MethodPatch, "/zones/"+c.zoneID+"/dns_records/"+rec.ID, map[string]any{
		"content": ip,
	}); err != nil {
		return false, err
	}
	return true, nil
}
