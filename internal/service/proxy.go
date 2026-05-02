// Package service
// proxy.go 对齐 packages/server/src/routes/proxyRoutes.ts：
//   - /proxy/sign：校验 URL + 扩展名白名单 + media 存在 + HMAC 签名返回代理入口
//   - /proxy/m3u8：验签 + SSRF + 域名白名单 + 流式转发 + m3u8 重写 + 特殊 Referer
package service

import (
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"gorm.io/gorm"

	"github.com/hor1zon777/m3u8-preview-go/internal/middleware"
	"github.com/hor1zon777/m3u8-preview-go/internal/model"
)

// defaultAllowedExtensions 与 TS DEFAULT_ALLOWED_EXTENSIONS 保持一致。
const defaultAllowedExtensions = ".m3u8,.ts,.m4s,.mp4,.aac,.key,.jpg,.jpeg,.png,.webp"

// ProxyService 负责所有 proxy 相关的 DB 查询 + 缓存管理。
type ProxyService struct {
	db *gorm.DB

	extMu       sync.RWMutex
	extSet      map[string]struct{}
	extExpires  time.Time
	extTTL      time.Duration
}

// NewProxyService 构造。
func NewProxyService(db *gorm.DB) *ProxyService {
	return &ProxyService{
		db:     db,
		extTTL: 30 * time.Second,
	}
}

// AllowedExtensions 读取当前允许代理的扩展名集合（带 30s 缓存）。
func (s *ProxyService) AllowedExtensions() map[string]struct{} {
	s.extMu.RLock()
	if s.extSet != nil && time.Now().Before(s.extExpires) {
		cached := s.extSet
		s.extMu.RUnlock()
		return cached
	}
	s.extMu.RUnlock()

	raw := defaultAllowedExtensions
	var setting model.SystemSetting
	if err := s.db.Where("key = ?", "proxyAllowedExtensions").Take(&setting).Error; err == nil {
		if strings.TrimSpace(setting.Value) != "" {
			raw = setting.Value
		}
	}

	set := make(map[string]struct{})
	for _, ext := range strings.Split(raw, ",") {
		e := strings.ToLower(strings.TrimSpace(ext))
		if e == "" {
			continue
		}
		if !strings.HasPrefix(e, ".") {
			e = "." + e
		}
		set[e] = struct{}{}
	}

	s.extMu.Lock()
	s.extSet = set
	s.extExpires = time.Now().Add(s.extTTL)
	s.extMu.Unlock()
	return set
}

// InvalidateExtensionsCache 管理员改 proxyAllowedExtensions 后必须调用。
func (s *ProxyService) InvalidateExtensionsCache() {
	s.extMu.Lock()
	s.extSet = nil
	s.extExpires = time.Time{}
	s.extMu.Unlock()
}

// ValidateMediaExists 校验 (targetURL, userID) 对应某条 ACTIVE media。
// TS 版用了两级缓存；此处简化为直查 + 前缀匹配兜底。
func (s *ProxyService) ValidateMediaExists(u *url.URL, userID string) error {
	full := u.String()
	var found int64
	if err := s.db.Model(&model.Media{}).
		Where("m3u8_url = ? AND status = ?", full, model.MediaStatusActive).
		Limit(1).
		Count(&found).Error; err != nil {
		return middleware.WrapAppError(http.StatusInternalServerError, "查询失败", err)
	}
	if found > 0 {
		return nil
	}
	prefix := fmt.Sprintf("%s://%s%s", u.Scheme, u.Host, u.Path)
	if err := s.db.Model(&model.Media{}).
		Where("m3u8_url LIKE ? AND status = ?", prefix+"%", model.MediaStatusActive).
		Limit(1).
		Count(&found).Error; err != nil {
		return middleware.WrapAppError(http.StatusInternalServerError, "查询失败", err)
	}
	if found == 0 {
		return middleware.NewAppError(http.StatusForbidden, "未找到对应的媒体记录，拒绝代理")
	}
	return nil
}

// ValidateSegmentDomain 校验该 host 是否有任何 ACTIVE media 的 m3u8Url 以它为前缀。
func (s *ProxyService) ValidateSegmentDomain(u *url.URL) error {
	prefix := fmt.Sprintf("%s://%s", u.Scheme, u.Host)
	var found int64
	if err := s.db.Model(&model.Media{}).
		Where("m3u8_url LIKE ? AND status = ?", prefix+"%", model.MediaStatusActive).
		Limit(1).
		Count(&found).Error; err != nil {
		return middleware.WrapAppError(http.StatusInternalServerError, "查询失败", err)
	}
	if found == 0 {
		return middleware.NewAppError(http.StatusForbidden, "未找到对应的媒体记录，拒绝代理")
	}
	return nil
}

// PathExtension 提取 URL 路径最后一段的扩展名（包含前导 "."，小写）。
func PathExtension(u *url.URL) string {
	last := u.Path
	if idx := strings.LastIndex(last, "/"); idx >= 0 {
		last = last[idx+1:]
	}
	if qs := strings.Index(last, "?"); qs >= 0 {
		last = last[:qs]
	}
	dot := strings.LastIndex(last, ".")
	if dot < 0 {
		return ""
	}
	return strings.ToLower(last[dot:])
}

// IsSurritHostname 判断是否 surrit.com 子域（需要特殊 Referer）。
func IsSurritHostname(host string) bool {
	host = strings.ToLower(host)
	return host == "surrit.com" || strings.HasSuffix(host, ".surrit.com")
}

// HeadersForM3u8URL 根据 m3u8 URL 的域名返回下载时应携带的 HTTP 头。
// 与代理端点 (ProxyHandler.m3u8) 注入的逻辑保持一致，确保 worker 直连源站
// 也能拿到与服务端代理一样的鉴权效果。
//
// 当前规则：
//   - User-Agent: 模拟桌面浏览器（绕过最简单的 UA 校验）
//   - Referer:    surrit.* 域名要求 https://missav.ws；其它域名留给上游/代理或将来扩展
func HeadersForM3u8URL(rawURL string) map[string]string {
	headers := map[string]string{
		"User-Agent": "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/120.0.0.0 Safari/537.36",
		"Accept":     "*/*",
	}
	if rawURL == "" {
		return headers
	}
	// 只取 host，URL 解析失败也不报错（headers 至少包含 UA）
	if idx := strings.Index(rawURL, "://"); idx > 0 {
		rest := rawURL[idx+3:]
		host := rest
		if slash := strings.Index(rest, "/"); slash > 0 {
			host = rest[:slash]
		}
		if at := strings.Index(host, "@"); at >= 0 {
			host = host[at+1:]
		}
		if colon := strings.Index(host, ":"); colon > 0 {
			host = host[:colon]
		}
		if IsSurritHostname(host) {
			headers["Referer"] = "https://missav.ws"
		}
	}
	return headers
}

// IsM3u8ContentType 判断 content-type 是否属于 m3u8 族。
func IsM3u8ContentType(ct string) bool {
	low := strings.ToLower(ct)
	return strings.Contains(low, "mpegurl") ||
		strings.Contains(low, "vnd.apple.mpegurl") ||
		low == "audio/mpegurl"
}
