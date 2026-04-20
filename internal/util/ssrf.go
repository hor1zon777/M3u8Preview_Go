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
// 纯 v4 / 纯 v6 域名其中一边 ENOTFOUND 是正常的，不视为错误。
func ValidateResolvedIP(ctx context.Context, host string) error {
	// net.DefaultResolver.LookupIPAddr 同时返回 v4 + v6
	addrs, err := net.DefaultResolver.LookupIPAddr(ctx, host)
	if err != nil {
		// DNS 查不到让上层 http 层去报错；这里不当 SSRF 处理
		return nil
	}
	for _, a := range addrs {
		if isPrivateIP(a.IP) {
			return newSSRF(http.StatusForbidden, "不允许访问内网地址")
		}
	}
	return nil
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
	if IsPrivateHostname(u.Hostname()) {
		return nil, newSSRF(http.StatusForbidden, "不允许访问内网地址")
	}
	if err := ValidateResolvedIP(ctx, u.Hostname()); err != nil {
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
	Client       *http.Client // 用于测试注入
}

// SafeFetch 手动处理重定向，每一跳都做 AssertSafeURL，防止上游 302 到内网。
// 返回最终 Response（调用方负责 Close Body）。
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
	client := opts.Client
	if client == nil {
		client = &http.Client{
			// 禁止自动跟随；本函数自己处理
			CheckRedirect: func(req *http.Request, via []*http.Request) error {
				return http.ErrUseLastResponse
			},
			Timeout: opts.Timeout,
		}
	}

	currentURL, err := AssertSafeURL(ctx, raw)
	if err != nil {
		return nil, err
	}

	for hop := 0; hop <= opts.MaxRedirects; hop++ {
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
				// 异常 3xx 无 Location，原样返回
				return resp, nil
			}
			// 关闭中间响应的 body，避免连接泄漏
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
			// body 流只能读一次：重定向场景不太可能带 body（GET），这里简单置 nil
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
