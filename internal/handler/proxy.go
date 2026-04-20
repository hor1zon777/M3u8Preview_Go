// Package handler
// proxy.go 对接 /api/v1/proxy/*：sign + m3u8 代理。
package handler

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/hor1zon777/m3u8-preview-go/internal/dto"
	"github.com/hor1zon777/m3u8-preview-go/internal/middleware"
	"github.com/hor1zon777/m3u8-preview-go/internal/service"
	"github.com/hor1zon777/m3u8-preview-go/internal/util"
)

// ProxyHandler 汇总 proxy 端点。
type ProxyHandler struct {
	svc    *service.ProxyService
	signer *util.ProxySigner
}

// NewProxyHandler 构造。
func NewProxyHandler(svc *service.ProxyService, signer *util.ProxySigner) *ProxyHandler {
	return &ProxyHandler{svc: svc, signer: signer}
}

// RegisterSign 挂 /proxy/sign（Authenticate + signLimiter 由上游注入）。
func (h *ProxyHandler) RegisterSign(rg *gin.RouterGroup) {
	rg.GET("/sign", h.sign)
}

// RegisterM3U8 挂 /proxy/m3u8（Authenticate + proxyLimiter 由上游注入）。
func (h *ProxyHandler) RegisterM3U8(rg *gin.RouterGroup) {
	rg.GET("/m3u8", h.m3u8)
}

// Range 值白名单：只允许 bytes=<n>-<n?>
var rangeRegexp = regexp.MustCompile(`^bytes=\d+-\d*$`)

const (
	connectTimeout = 15 * time.Second
	totalTimeout   = 120 * time.Second
)

// sign 产生带 HMAC 的代理入口 URL。
func (h *ProxyHandler) sign(c *gin.Context) {
	rawURL := c.Query("url")
	if rawURL == "" {
		middleware.AbortWithAppError(c, middleware.NewAppError(http.StatusBadRequest, "缺少 url 参数"))
		return
	}
	userID := middleware.CurrentUserID(c)
	if userID == "" {
		middleware.AbortWithAppError(c, middleware.NewAppError(http.StatusUnauthorized, "Authentication required"))
		return
	}

	target, err := h.validateProxyURL(c, rawURL)
	if err != nil {
		_ = c.Error(err)
		return
	}
	if !strings.HasSuffix(strings.ToLower(target.Path), ".m3u8") {
		middleware.AbortWithAppError(c, middleware.NewAppError(http.StatusBadRequest, "签名端点仅支持 m3u8 URL"))
		return
	}
	if err := h.svc.ValidateMediaExists(target, userID); err != nil {
		_ = c.Error(err)
		return
	}

	signed := h.signer.Sign(rawURL, userID)
	proxyURL := "/api/v1/proxy/m3u8?url=" + url.QueryEscape(rawURL) + signed
	c.JSON(http.StatusOK, dto.APIResponse{Success: true, Message: "", Data: gin.H{"proxyUrl": proxyURL}})
}

// m3u8 代理 m3u8/segment。
func (h *ProxyHandler) m3u8(c *gin.Context) {
	rawURL := c.Query("url")
	if rawURL == "" {
		middleware.AbortWithAppError(c, middleware.NewAppError(http.StatusBadRequest, "缺少 url 参数"))
		return
	}
	userID := middleware.CurrentUserID(c)
	if userID == "" {
		middleware.AbortWithAppError(c, middleware.NewAppError(http.StatusUnauthorized, "Authentication required"))
		return
	}

	expires := c.Query("expires")
	sig := c.Query("sig")
	if expires == "" || sig == "" || !h.signer.Verify(rawURL, expires, sig, userID) {
		middleware.AbortWithAppError(c, middleware.NewAppError(http.StatusForbidden, "签名无效或已过期"))
		return
	}

	target, err := h.validateProxyURL(c, rawURL)
	if err != nil {
		_ = c.Error(err)
		return
	}

	isM3U8 := strings.HasSuffix(strings.ToLower(target.Path), ".m3u8")
	if !isM3U8 {
		if err := h.svc.ValidateSegmentDomain(target); err != nil {
			_ = c.Error(err)
			return
		}
	}

	headers := http.Header{}
	headers.Set("User-Agent", "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36")
	headers.Set("Accept", "*/*")
	if service.IsSurritHostname(target.Hostname()) {
		headers.Set("Referer", "https://missav.ws")
	}
	if rawRange := c.GetHeader("Range"); rawRange != "" && rangeRegexp.MatchString(rawRange) {
		headers.Set("Range", rawRange)
	}

	ctx, cancel := context.WithTimeout(c.Request.Context(), totalTimeout)
	defer cancel()

	resp, err := util.SafeFetch(ctx, target.String(), util.SafeFetchOptions{
		MaxRedirects: 3,
		Headers:      headers,
		Method:       http.MethodGet,
		Timeout:      connectTimeout,
	})
	if err != nil {
		if code, ok := util.SSRFCode(err); ok {
			middleware.AbortWithAppError(c, middleware.NewAppError(code, err.Error()))
			return
		}
		if errors.Is(err, context.DeadlineExceeded) || errors.Is(err, context.Canceled) {
			middleware.AbortWithAppError(c, middleware.NewAppError(http.StatusGatewayTimeout, "代理请求超时"))
			return
		}
		middleware.AbortWithAppError(c, middleware.WrapAppError(http.StatusBadGateway, "代理请求失败", err))
		return
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		c.JSON(resp.StatusCode, dto.APIResponse{
			Success: false,
			Error:   "上游服务器返回 " + http.StatusText(resp.StatusCode),
		})
		return
	}

	ct := resp.Header.Get("Content-Type")
	if ct != "" {
		c.Writer.Header().Set("Content-Type", ct)
	}
	if cl := resp.Header.Get("Content-Length"); cl != "" {
		c.Writer.Header().Set("Content-Length", cl)
	}
	status := http.StatusOK
	if cr := resp.Header.Get("Content-Range"); cr != "" {
		c.Writer.Header().Set("Content-Range", cr)
		status = http.StatusPartialContent
	}
	if isM3U8 {
		c.Writer.Header().Set("Cache-Control", "no-store")
	} else {
		c.Writer.Header().Set("Cache-Control", "private, max-age=600")
	}

	if isM3U8 && service.IsM3u8ContentType(ct) {
		body, rerr := io.ReadAll(resp.Body)
		if rerr != nil {
			middleware.AbortWithAppError(c, middleware.WrapAppError(http.StatusBadGateway, "读取 m3u8 失败", rerr))
			return
		}
		rewritten := h.rewriteM3U8(string(body), target, userID)
		c.Writer.Header().Del("Content-Length")
		c.Writer.WriteHeader(status)
		_, _ = c.Writer.Write([]byte(rewritten))
		return
	}

	c.Writer.WriteHeader(status)
	// 流式转发；忽略写入错误（客户端断开是常态）
	_, _ = io.Copy(c.Writer, resp.Body)
}

// validateProxyURL 封装所有 URL 合规检查：协议、SSRF、扩展名白名单。
func (h *ProxyHandler) validateProxyURL(c *gin.Context, raw string) (*url.URL, error) {
	u, err := util.AssertSafeURL(c.Request.Context(), raw)
	if err != nil {
		if code, ok := util.SSRFCode(err); ok {
			return nil, middleware.NewAppError(code, err.Error())
		}
		return nil, middleware.WrapAppError(http.StatusBadRequest, "URL 校验失败", err)
	}
	ext := service.PathExtension(u)
	if ext == "" {
		return nil, middleware.NewAppError(http.StatusBadRequest, "不支持代理此类型的资源")
	}
	allowed := h.svc.AllowedExtensions()
	if _, ok := allowed[ext]; !ok {
		return nil, middleware.NewAppError(http.StatusBadRequest, "不支持代理此类型的资源")
	}
	return u, nil
}

// rewriteM3U8 按行重写内容：# 开头含 URI="..." 只替换 URI，其它非注释整行换成代理 URL。
func (h *ProxyHandler) rewriteM3U8(content string, base *url.URL, userID string) string {
	const proxyPrefix = "/api/v1/proxy/m3u8?url="
	lines := strings.Split(content, "\n")
	for i, line := range lines {
		trimmed := strings.TrimSpace(line)
		if trimmed == "" {
			continue
		}
		if strings.HasPrefix(trimmed, "#") {
			if strings.Contains(trimmed, `URI="`) {
				lines[i] = rewriteURIAttribute(line, base, userID, h.signer, proxyPrefix)
			}
			continue
		}
		abs := resolveAbsURL(base, trimmed)
		lines[i] = proxyPrefix + url.QueryEscape(abs) + h.signer.Sign(abs, userID)
	}
	return strings.Join(lines, "\n")
}

// rewriteURIAttribute 只替换一行内 URI="..." 里的 URL，保留 tag 结构。
func rewriteURIAttribute(line string, base *url.URL, userID string, signer *util.ProxySigner, proxyPrefix string) string {
	re := regexp.MustCompile(`URI="([^"]+)"`)
	return re.ReplaceAllStringFunc(line, func(match string) string {
		sub := re.FindStringSubmatch(match)
		if len(sub) < 2 {
			return match
		}
		abs := resolveAbsURL(base, sub[1])
		return `URI="` + proxyPrefix + url.QueryEscape(abs) + signer.Sign(abs, userID) + `"`
	})
}

// resolveAbsURL 将可能的相对路径解析为绝对 URL。
func resolveAbsURL(base *url.URL, raw string) string {
	if strings.HasPrefix(raw, "http://") || strings.HasPrefix(raw, "https://") {
		return raw
	}
	u, err := base.Parse(raw)
	if err != nil {
		return raw
	}
	return u.String()
}
