// Package util
// ssrf.go 对齐 packages/server/src/utils/ssrfGuard.ts。
//
// 防护策略：
//   1. hostname 层拦截：localhost/0.0.0.0、.local/.internal/.localhost 后缀
//   2. IP 字面量拦截：v4 覆盖 0/127/10/172.16-31/192.168/169.254/100.64-127/≥224；
//                     v6 覆盖 ::1/:://fe80-feb0/fc/fd/100::/2001:db8/2002/2001:0/::ffff:v4/::v4
//   3. DNS 解析拦截：v4+v6 全部地址均不在上述私有段
//   4. safeFetch：redirect=manual + 每跳重验 DNS，防止上游 302 到内网绕过
package util

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// SSRFError 供调用方判断是否属于 SSRF 预检失败，便于返回 400/403。
type SSRFError struct {
	Code    int
	Message string
}

func (e *SSRFError) Error() string { return e.Message }

// newSSRF 快速构造 SSRFError。
func newSSRF(code int, msg string) *SSRFError { return &SSRFError{Code: code, Message: msg} }

// IsPrivateHostname 仅做字符串 / IP 字面量检查，不做 DNS。
func IsPrivateHostname(host string) bool {
	clean := strings.TrimSpace(host)
	clean = strings.TrimPrefix(clean, "[")
	clean = strings.TrimSuffix(clean, "]")
	low := strings.ToLower(clean)

	switch low {
	case "localhost", "127.0.0.1", "0.0.0.0", "::1", "::ffff:127.0.0.1":
		return true
	}
	if strings.HasSuffix(low, ".local") ||
		strings.HasSuffix(low, ".internal") ||
		strings.HasSuffix(low, ".localhost") {
		return true
	}
	if ip := net.ParseIP(low); ip != nil {
		return isPrivateIP(ip)
	}
	return false
}

// isPrivateIP 对任意 IP（v4 或 v6）判断是否属于私有 / 保留段。
func isPrivateIP(ip net.IP) bool {
	if ip == nil {
		return false
	}
	if ip4 := ip.To4(); ip4 != nil {
		return isPrivateV4(ip4)
	}
	return isPrivateV6(ip)
}

func isPrivateV4(ip4 net.IP) bool {
	b := [4]byte{ip4[0], ip4[1], ip4[2], ip4[3]}
	switch {
	case b[0] == 0, b[0] == 127, b[0] == 10:
		return true
	case b[0] == 172 && b[1] >= 16 && b[1] <= 31:
		return true
	case b[0] == 192 && b[1] == 168:
		return true
	case b[0] == 169 && b[1] == 254:
		return true
	case b[0] == 100 && b[1] >= 64 && b[1] <= 127: // CGNAT
		return true
	case b[0] >= 224: // multicast + reserved
		return true
	}
	return false
}

func isPrivateV6(ip net.IP) bool {
	// loopback / unspecified
	if ip.Equal(net.IPv6loopback) || ip.Equal(net.IPv6unspecified) {
		return true
	}
	// Unique Local (fc00::/7) + Link-local (fe80::/10)
	if len(ip) == 16 {
		if ip[0] == 0xfc || ip[0] == 0xfd {
			return true
		}
		// fe80::/10: 前 10 bit 为 1111 1110 10
		if ip[0] == 0xfe && (ip[1]&0xc0) == 0x80 {
			return true
		}
		// 100::/64 discard-only
		if ip[0] == 0x01 && ip[1] == 0x00 && allZero(ip[2:8]) {
			return true
		}
		// 2001:db8::/32 documentation
		if ip[0] == 0x20 && ip[1] == 0x01 && ip[2] == 0x0d && ip[3] == 0xb8 {
			return true
		}
		// 2002::/16 6to4
		if ip[0] == 0x20 && ip[1] == 0x02 {
			return true
		}
		// 2001:0::/32 teredo
		if ip[0] == 0x20 && ip[1] == 0x01 && ip[2] == 0 && ip[3] == 0 {
			return true
		}
		// IPv4-mapped ::ffff:v4
		if allZero(ip[:10]) && ip[10] == 0xff && ip[11] == 0xff {
			return isPrivateV4(ip[12:16])
		}
		// IPv4-compatible ::v4（已废弃但仍需拦截）
		if allZero(ip[:12]) {
			return isPrivateV4(ip[12:16])
		}
	}
	return false
}

func allZero(b []byte) bool {
	for _, x := range b {
		if x != 0 {
			return false
		}
	}
	return true
}

// ValidateResolvedIP 对 host 进行 DNS 解析，任一地址处于私有段即拒绝。
// 注意：与旧版不同，DNS 查询失败现在视为 SSRF 风险（fail-closed），而不是放行。
// 调用方应把此返回的错误直接冒泡给客户端，避免"DNS 抖动 → 校验层失效 → 实际连接打到内网"的绕过。
func ValidateResolvedIP(ctx context.Context, host string) error {
	_, err := lookupSafeIP(ctx, host)
	return err
}

// lookupSafeIP 解析 host 并返回第一个非私有 IP。
// - DNS 查询失败：返回 SSRFError（fail-closed）
// - 任一返回地址为私有段：返回 SSRFError
// - 无任何可用地址：返回 SSRFError
// 此函数既用于预校验，也作为 SafeFetch 里 DialContext 的 IP 来源，确保"校验 IP = 连接 IP"。
func lookupSafeIP(ctx context.Context, host string) (net.IP, error) {
	addrs, err := net.DefaultResolver.LookupIPAddr(ctx, host)
	if err != nil {
		return nil, newSSRF(http.StatusBadGateway, "DNS 解析失败")
	}
	var firstSafe net.IP
	for _, a := range addrs {
		if isPrivateIP(a.IP) {
			return nil, newSSRF(http.StatusForbidden, "不允许访问内网地址")
		}
		if firstSafe == nil {
			firstSafe = a.IP
		}
	}
	if firstSafe == nil {
		return nil, newSSRF(http.StatusBadGateway, "DNS 无可用地址")
	}
	return firstSafe, nil
}

// AssertSafeURL 校验协议 + hostname + DNS。
func AssertSafeURL(ctx context.Context, raw string) (*url.URL, error) {
	u, err := url.Parse(raw)
	if err != nil || u.Scheme == "" || u.Host == "" {
		return nil, newSSRF(http.StatusBadRequest, "无效的 URL")
	}
	if u.Scheme != "http" && u.Scheme != "https" {
		return nil, newSSRF(http.StatusBadRequest, "仅支持 HTTP/HTTPS 协议")
	}
	host := u.Hostname()
	// IP 字面量不必走 DNS
	if ip := net.ParseIP(host); ip != nil {
		if isPrivateIP(ip) {
			return nil, newSSRF(http.StatusForbidden, "不允许访问内网地址")
		}
		return u, nil
	}
	if IsPrivateHostname(host) {
		return nil, newSSRF(http.StatusForbidden, "不允许访问内网地址")
	}
	if err := ValidateResolvedIP(ctx, host); err != nil {
		return nil, err
	}
	return u, nil
}

// SafeFetchOptions 控制 SafeFetch 行为。
type SafeFetchOptions struct {
	MaxRedirects int
	Headers      http.Header
	Method       string
	Body         io.Reader
	Timeout      time.Duration
	Client       *http.Client // 用于测试注入；生产不传
}

// buildPinnedClient 构造一个把 DialContext 固定到 pinnedIP 的 HTTP client。
// 核心作用：消除 DNS Rebinding —— 校验时解析的 IP 与实际拨号 IP 保持一致。
// SNI/Host 头仍然使用原 hostname（由 net/http 根据 req.URL.Host 自动设置），不影响 TLS 验证。
func buildPinnedClient(pinnedIP net.IP, timeout time.Duration) *http.Client {
	dialer := &net.Dialer{Timeout: 10 * time.Second, KeepAlive: 30 * time.Second}
	tr := &http.Transport{
		DialContext: func(ctx context.Context, network, addr string) (net.Conn, error) {
			_, port, err := net.SplitHostPort(addr)
			if err != nil {
				return nil, err
			}
			return dialer.DialContext(ctx, network, net.JoinHostPort(pinnedIP.String(), port))
		},
		TLSHandshakeTimeout:   10 * time.Second,
		ResponseHeaderTimeout: timeout,
		ExpectContinueTimeout: 1 * time.Second,
		DisableKeepAlives:     true, // 防止跨 URL 复用导致 IP 绑定错乱
	}
	return &http.Client{
		Transport: tr,
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			return http.ErrUseLastResponse
		},
		Timeout: timeout,
	}
}

// SafeFetch 手动处理重定向；关键抗 DNS Rebinding：
// 1. 每一跳都用 lookupSafeIP 解析当前 host 得到 pinnedIP
// 2. 为该跳构造一次性 Transport.DialContext → 所有连接都走 pinnedIP
// 这样"校验阶段的 DNS 结果"与"实际连接的 IP"绑定在同一个解析里，
// 无论底层 http 是否再次查询 DNS 都会被 DialContext 替换为已校验 IP。
func SafeFetch(ctx context.Context, raw string, opts SafeFetchOptions) (*http.Response, error) {
	if opts.MaxRedirects <= 0 {
		opts.MaxRedirects = 3
	}
	if opts.Method == "" {
		opts.Method = http.MethodGet
	}
	if opts.Timeout <= 0 {
		opts.Timeout = 30 * time.Second
	}

	currentURL, err := AssertSafeURL(ctx, raw)
	if err != nil {
		return nil, err
	}

	for hop := 0; hop <= opts.MaxRedirects; hop++ {
		host := currentURL.Hostname()
		var pinnedIP net.IP
		if ip := net.ParseIP(host); ip != nil {
			// IP 字面量：AssertSafeURL/AssertSafeURL-on-redirect 已校验，不会是私有 IP
			pinnedIP = ip
		} else {
			pinnedIP, err = lookupSafeIP(ctx, host)
			if err != nil {
				return nil, err
			}
		}

		client := opts.Client
		if client == nil {
			client = buildPinnedClient(pinnedIP, opts.Timeout)
		}

		req, rerr := http.NewRequestWithContext(ctx, opts.Method, currentURL.String(), opts.Body)
		if rerr != nil {
			return nil, rerr
		}
		for k, vs := range opts.Headers {
			for _, v := range vs {
				req.Header.Add(k, v)
			}
		}
		resp, rerr := client.Do(req)
		if rerr != nil {
			return nil, rerr
		}

		if resp.StatusCode >= 300 && resp.StatusCode < 400 {
			loc := resp.Header.Get("Location")
			if loc == "" {
				return resp, nil
			}
			_ = resp.Body.Close()
			if hop >= opts.MaxRedirects {
				return nil, newSSRF(http.StatusBadGateway, "重定向次数过多")
			}
			next, perr := currentURL.Parse(loc)
			if perr != nil {
				return nil, newSSRF(http.StatusBadGateway, "重定向 Location 无效")
			}
			safe, verr := AssertSafeURL(ctx, next.String())
			if verr != nil {
				return nil, verr
			}
			currentURL = safe
			opts.Body = nil
			continue
		}
		return resp, nil
	}
	return nil, fmt.Errorf("safe fetch: unreachable")
}

// 让 errors.As 能匹配 SSRFError
var _ error = (*SSRFError)(nil)

// Unwrap 让 SSRFError 可作为普通 error。
func (e *SSRFError) Unwrap() error { return nil }

// 把 SSRFError 的 Code 暴露给中间件映射成 HTTP 状态。
func SSRFCode(err error) (int, bool) {
	var se *SSRFError
	if errors.As(err, &se) {
		return se.Code, true
	}
	return 0, false
}
