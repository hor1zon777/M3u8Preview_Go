package service

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"gorm.io/gorm"

	"github.com/hor1zon777/m3u8-preview-go/internal/middleware"
	"github.com/hor1zon777/m3u8-preview-go/internal/model"
	"github.com/hor1zon777/m3u8-preview-go/internal/util"
)

type CaptchaService struct {
	db     *gorm.DB
	client *http.Client
	// allowedHostnames 是 siteverify 响应里 hostname 字段允许匹配的值（小写）。
	// 为空时表示跳过 hostname 校验（向后兼容旧 captcha 服务）。
	allowedHostnames []string

	mu          sync.Mutex
	cachedCSP   string
	cacheExpiry time.Time
}

type CaptchaPublicConfig struct {
	Enabled  bool   `json:"enabled"`
	Endpoint string `json:"endpoint,omitempty"`
	SiteKey  string `json:"siteKey,omitempty"`
}

// NewCaptchaService 构造。
// allowedHostnames 用于 siteverify 响应 hostname 字段的等值校验，
// 通常由 CORS_ORIGIN 的 host 列表推导而来，避免跨站重放 captcha token。
func NewCaptchaService(db *gorm.DB, allowedHostnames []string) *CaptchaService {
	normalized := make([]string, 0, len(allowedHostnames))
	seen := make(map[string]struct{}, len(allowedHostnames))
	for _, h := range allowedHostnames {
		h = strings.ToLower(strings.TrimSpace(h))
		if h == "" {
			continue
		}
		if _, dup := seen[h]; dup {
			continue
		}
		seen[h] = struct{}{}
		normalized = append(normalized, h)
	}
	return &CaptchaService{
		db:               db,
		client:           &http.Client{Timeout: 5 * time.Second},
		allowedHostnames: normalized,
	}
}

type captchaSettings struct {
	enabled     bool
	endpoint    string   // 已 trimRight("/")、校验通过的 origin+path
	endpointURL *url.URL // 解析后的 URL（Scheme/Host 已校验）
	siteKey     string
	secretKey   string
}

// ValidateCaptchaEndpoint 校验一个 captchaEndpoint 配置值是否安全可用。
// 供 admin 写入设置时的前置校验复用。
// 只做字符串/IP 字面量/保留字段的同步校验，不做 DNS 解析（DNS 绑定在 SafeFetch 阶段处理）。
func ValidateCaptchaEndpoint(raw string) (*url.URL, error) {
	raw = strings.TrimSpace(raw)
	raw = strings.TrimRight(raw, "/")
	if raw == "" {
		return nil, errors.New("captcha endpoint 不能为空")
	}
	u, err := url.Parse(raw)
	if err != nil {
		return nil, fmt.Errorf("captcha endpoint 解析失败: %w", err)
	}
	scheme := strings.ToLower(u.Scheme)
	if scheme != "http" && scheme != "https" {
		return nil, errors.New("captcha endpoint 必须是 http 或 https")
	}
	if u.Host == "" {
		return nil, errors.New("captcha endpoint 缺少 host")
	}
	if u.User != nil {
		return nil, errors.New("captcha endpoint 不允许携带 userinfo")
	}
	// 内网 / 保留段 / 链路本地拦截
	if util.IsPrivateHostname(u.Hostname()) {
		return nil, errors.New("captcha endpoint 不允许指向内网或保留地址")
	}
	u.Scheme = scheme
	return u, nil
}

func (s *CaptchaService) loadSettings() (captchaSettings, error) {
	keys := []string{
		model.SettingEnableCaptcha,
		model.SettingCaptchaEndpoint,
		model.SettingCaptchaSiteKey,
		model.SettingCaptchaSecretKey,
	}
	var rows []model.SystemSetting
	if err := s.db.Where("key IN ?", keys).Find(&rows).Error; err != nil {
		return captchaSettings{}, fmt.Errorf("load captcha settings: %w", err)
	}

	m := make(map[string]string, len(rows))
	for _, r := range rows {
		m[r.Key] = r.Value
	}

	cs := captchaSettings{
		siteKey:   m[model.SettingCaptchaSiteKey],
		secretKey: m[model.SettingCaptchaSecretKey],
	}
	if rawEndpoint := m[model.SettingCaptchaEndpoint]; rawEndpoint != "" {
		// 解析失败视为未配置；enabled 判定会因 endpoint 为空而 false
		if u, err := ValidateCaptchaEndpoint(rawEndpoint); err == nil {
			cs.endpointURL = u
			cs.endpoint = u.Scheme + "://" + u.Host + strings.TrimRight(u.Path, "/")
		}
	}
	cs.enabled = m[model.SettingEnableCaptcha] == "true" &&
		cs.endpoint != "" && cs.siteKey != "" && cs.secretKey != ""
	return cs, nil
}

func (s *CaptchaService) GetPublicConfig() CaptchaPublicConfig {
	cs, err := s.loadSettings()
	if err != nil || !cs.enabled {
		return CaptchaPublicConfig{Enabled: false}
	}
	return CaptchaPublicConfig{
		Enabled:  true,
		Endpoint: cs.endpoint,
		SiteKey:  cs.siteKey,
	}
}

const (
	maxCaptchaTokenLen    = 4096
	cspCacheTTL           = 30 * time.Second
	captchaTimestampSkew  = 60 * time.Second // siteverify 返回 challenge_ts 的允许时间漂移
	captchaSiteverifyPath = "/api/v1/siteverify"
)

func (s *CaptchaService) CSPOrigin() string {
	s.mu.Lock()
	defer s.mu.Unlock()

	if time.Now().Before(s.cacheExpiry) {
		return s.cachedCSP
	}

	origin := ""
	cs, err := s.loadSettings()
	if err == nil && cs.endpointURL != nil {
		// 只输出经过 ValidateCaptchaEndpoint 白名单过滤的 scheme://host
		origin = cs.endpointURL.Scheme + "://" + cs.endpointURL.Host
	}
	s.cachedCSP = origin
	s.cacheExpiry = time.Now().Add(cspCacheTTL)
	return origin
}

// siteverifyResponse 对齐 captcha 服务 /api/v1/siteverify 响应。
// Hostname / ChallengeTS 是新增字段，老版本可能不返回——空值时跳过对应校验（向后兼容）。
type siteverifyResponse struct {
	Success      bool     `json:"success"`
	Error        string   `json:"error,omitempty"`
	ErrorCodes   []string `json:"error-codes,omitempty"`
	Hostname     string   `json:"hostname,omitempty"`
	ChallengeTS  string   `json:"challenge_ts,omitempty"`
	ChallengeTs2 string   `json:"challengeTs,omitempty"` // 兼容驼峰 key
}

func (s *CaptchaService) VerifyIfEnabled(ctx context.Context, token string) error {
	cs, err := s.loadSettings()
	if err != nil {
		return middleware.WrapAppError(http.StatusInternalServerError, "读取验证码配置失败", err)
	}
	if !cs.enabled {
		return nil
	}
	if token == "" {
		return middleware.NewAppError(http.StatusBadRequest, "请完成验证码")
	}
	if len(token) > maxCaptchaTokenLen {
		return middleware.NewAppError(http.StatusBadRequest, "验证码 token 无效")
	}

	reqBody, _ := json.Marshal(map[string]string{
		"token":      token,
		"secret_key": cs.secretKey,
	})

	// 使用 SafeFetch 抵御 DNS rebinding / 内网重定向：
	// 校验阶段的 IP 与实际连接 IP 绑定一致，且每跳都会重新走 SSRF 预检。
	headers := http.Header{}
	headers.Set("Content-Type", "application/json")
	resp, err := util.SafeFetch(ctx, cs.endpoint+captchaSiteverifyPath, util.SafeFetchOptions{
		Method:       http.MethodPost,
		Headers:      headers,
		Body:         bytes.NewReader(reqBody),
		Timeout:      5 * time.Second,
		MaxRedirects: 0, // siteverify 不应该有重定向
	})
	if err != nil {
		if _, ok := util.SSRFCode(err); ok {
			return middleware.WrapAppError(http.StatusForbidden, "验证服务地址不允许访问", err)
		}
		return middleware.WrapAppError(http.StatusBadGateway, "验证服务不可用", fmt.Errorf("captcha siteverify: %w", err))
	}
	defer func() {
		_, _ = io.Copy(io.Discard, resp.Body)
		_ = resp.Body.Close()
	}()

	if resp.StatusCode/100 != 2 {
		return middleware.NewAppError(http.StatusBadGateway,
			fmt.Sprintf("验证服务异常 (HTTP %d)", resp.StatusCode))
	}

	// 限制响应体大小，防恶意 captcha 服务返回超大响应耗尽内存
	body, err := io.ReadAll(io.LimitReader(resp.Body, 16*1024))
	if err != nil {
		return middleware.WrapAppError(http.StatusBadGateway, "读取验证响应失败", err)
	}
	var result siteverifyResponse
	if err := json.Unmarshal(body, &result); err != nil {
		return middleware.WrapAppError(http.StatusBadGateway, "验证服务响应异常", fmt.Errorf("captcha decode: %w", err))
	}
	if !result.Success {
		return middleware.NewAppError(http.StatusForbidden, "验证码校验失败")
	}

	// hostname 等值校验：仅当 captcha 服务明确返回 hostname 且本地配置了允许列表时生效。
	// 缺失任一方表示老协议，跳过（fail-open 是向后兼容的必要妥协）。
	if result.Hostname != "" && len(s.allowedHostnames) > 0 {
		got := strings.ToLower(strings.TrimSpace(result.Hostname))
		matched := false
		for _, h := range s.allowedHostnames {
			if got == h {
				matched = true
				break
			}
		}
		if !matched {
			return middleware.NewAppError(http.StatusForbidden, "验证码 token 来源不匹配")
		}
	}

	// challenge_ts 新鲜度校验：同样只在 captcha 服务返回时才做。
	tsRaw := result.ChallengeTS
	if tsRaw == "" {
		tsRaw = result.ChallengeTs2
	}
	if tsRaw != "" {
		if ts, ok := parseChallengeTS(tsRaw); ok {
			if skew := time.Since(ts); skew < -captchaTimestampSkew || skew > captchaTimestampSkew*5 {
				// 允许 60s 未来漂移（时钟偏差）+ 5min 过去（覆盖网络延迟与用户输入时间）
				return middleware.NewAppError(http.StatusForbidden, "验证码已过期，请刷新")
			}
		}
	}

	return nil
}

// parseChallengeTS 支持 ISO8601（reCAPTCHA 风格）与 Unix 秒/毫秒（常见 JSON API 风格）。
func parseChallengeTS(raw string) (time.Time, bool) {
	raw = strings.TrimSpace(raw)
	// ISO8601 / RFC3339
	if t, err := time.Parse(time.RFC3339Nano, raw); err == nil {
		return t, true
	}
	if t, err := time.Parse(time.RFC3339, raw); err == nil {
		return t, true
	}
	// Unix 时间戳
	if n, err := parseInt64(raw); err == nil {
		// 启发式：> 10^12 视为毫秒，否则秒
		if n > 1_000_000_000_000 {
			return time.UnixMilli(n), true
		}
		return time.Unix(n, 0), true
	}
	return time.Time{}, false
}

func parseInt64(s string) (int64, error) {
	// 简单手写避免引入 strconv 之外的开销；保持最小依赖
	var n int64
	if s == "" {
		return 0, errors.New("empty")
	}
	neg := false
	i := 0
	if s[0] == '-' {
		neg = true
		i = 1
	}
	for ; i < len(s); i++ {
		c := s[i]
		if c < '0' || c > '9' {
			return 0, errors.New("non-digit")
		}
		n = n*10 + int64(c-'0')
	}
	if neg {
		n = -n
	}
	return n, nil
}
