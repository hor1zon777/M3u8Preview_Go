// probe.go 实现对端直连探测——决策规则里独立于 Cloudflare 的第二信号。
//
// 为什么必须有它：只看租约的话，Cloudflare API 故障会被误判成"对端宕机"，
// 触发一次毫无必要的切换；只看直连的话，主备之间断网（但两台机器都活着）
// 会被双方互相误判成对端宕机，触发脑裂。两个信号交叉验证后，
// 这两种故障都退化为"维持现状"的安全状态。
package ha

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"sync"
	"time"
)

// probeTimeout 单次探测超时。取 5s：远小于探测周期（10s），
// 保证连续 3 次失败能在 30s 左右判定完成，不会被慢响应拖长。
const probeTimeout = 5 * time.Second

// PeerStatus 是对端 /api/health 的解析结果。
type PeerStatus struct {
	// Reachable 是否成功拿到了对端的健康响应。
	Reachable bool
	// Role 对端自报的角色（"primary" / "replica"）。Reachable 为 false 时无意义。
	Role string
	// NodeID 对端自报的节点 ID，用于发现配置写反了的情况。
	NodeID string
	// TXID 对端的 LiteFS 复制位点，回切前用它判断副本是否追平。
	TXID string
	// Draining 对端是否正在交接停写。
	Draining bool
	// BusyStreams 对端正在进行的 audio 桥接流数量。
	BusyStreams int
	// Epoch 对端观察到的租约世代号。
	Epoch int64
	// Version 对端自报的应用版本（滚动升级时管理面板展示双端版本用）。
	Version string
	// Err 探测失败原因，供日志排查。
	Err error
}

// IsPrimary 报告对端是否自称 primary。
func (s PeerStatus) IsPrimary() bool { return s.Reachable && s.Role == "primary" }

// IsReplica 报告对端是否明确自称 replica。
//
// 注意这与 !IsPrimary() 不同：探测失败时两者都不成立。
// 决策规则里"对端明确说自己是 replica"是放行继续当主的**正面证据**，
// 不能用"没证据说明它是主"来替代。
func (s PeerStatus) IsReplica() bool { return s.Reachable && s.Role == "replica" }

// Prober 探测对端健康状态，并维护连续失败计数。并发安全。
type Prober struct {
	baseURL string
	http    *http.Client

	mu       sync.RWMutex
	last     PeerStatus
	failures int
	lastAt   time.Time
}

// NewProber 构造探测器。
//
// caFile 非空时把该 PEM 追加进信任链——节点之间用自签证书直连时用它，
// 从而不必退化成 InsecureSkipVerify（那会让这条通道可被中间人劫持，
// 而这条通道恰恰是判定"要不要切主"的依据之一）。
func NewProber(baseURL, caFile string) (*Prober, error) {
	tr := http.DefaultTransport.(*http.Transport).Clone()
	if caFile != "" {
		pem, err := os.ReadFile(caFile)
		if err != nil {
			return nil, fmt.Errorf("ha: 读取对端 CA 证书 %s: %w", caFile, err)
		}
		pool, err := x509.SystemCertPool()
		if err != nil || pool == nil {
			pool = x509.NewCertPool()
		}
		if !pool.AppendCertsFromPEM(pem) {
			return nil, fmt.Errorf("ha: 对端 CA 证书 %s 不是有效的 PEM", caFile)
		}
		tr.TLSClientConfig = &tls.Config{RootCAs: pool, MinVersion: tls.VersionTLS12}
	}
	return &Prober{
		baseURL: baseURL,
		http:    &http.Client{Timeout: probeTimeout, Transport: tr},
	}, nil
}

// Probe 执行一次探测并更新连续失败计数。
func (p *Prober) Probe(ctx context.Context) PeerStatus {
	st := p.fetch(ctx)

	p.mu.Lock()
	p.last = st
	p.lastAt = time.Now()
	if st.Reachable {
		p.failures = 0
	} else {
		p.failures++
	}
	p.mu.Unlock()

	return st
}

func (p *Prober) fetch(ctx context.Context) PeerStatus {
	ctx, cancel := context.WithTimeout(ctx, probeTimeout)
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, p.baseURL+"/api/health", nil)
	if err != nil {
		return PeerStatus{Err: err}
	}
	resp, err := p.http.Do(req)
	if err != nil {
		return PeerStatus{Err: err}
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		return PeerStatus{Err: fmt.Errorf("对端 /api/health 返回 HTTP %d", resp.StatusCode)}
	}

	var body struct {
		Status      string `json:"status"`
		Role        string `json:"role"`
		NodeID      string `json:"nodeId"`
		TXID        string `json:"txid"`
		Draining    bool   `json:"draining"`
		BusyStreams int    `json:"busyStreams"`
		Epoch       int64  `json:"epoch"`
		Version     string `json:"version"`
	}
	raw, err := io.ReadAll(io.LimitReader(resp.Body, 1<<16))
	if err != nil {
		return PeerStatus{Err: err}
	}
	if err := json.Unmarshal(raw, &body); err != nil {
		return PeerStatus{Err: fmt.Errorf("解析对端健康响应: %w", err)}
	}

	return PeerStatus{
		Reachable:   true,
		Role:        body.Role,
		NodeID:      body.NodeID,
		TXID:        body.TXID,
		Draining:    body.Draining,
		BusyStreams: body.BusyStreams,
		Epoch:       body.Epoch,
		Version:     body.Version,
	}
}

// Last 返回最近一次探测结果与连续失败次数。
func (p *Prober) Last() (PeerStatus, int) {
	p.mu.RLock()
	defer p.mu.RUnlock()
	return p.last, p.failures
}

// Snapshot 在 Last 之上追加最近一次探测的时刻，供状态 API 显示数据新鲜度。
func (p *Prober) Snapshot() (PeerStatus, int, time.Time) {
	p.mu.RLock()
	defer p.mu.RUnlock()
	return p.last, p.failures, p.lastAt
}

// Down 报告对端是否已被判定为宕机（连续失败达到阈值）。
func (p *Prober) Down() bool {
	p.mu.RLock()
	defer p.mu.RUnlock()
	return p.failures >= probeFailureThreshold
}
