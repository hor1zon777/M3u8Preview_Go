// Package middleware
// ratelimit.go 提供基于 IP 的滑动窗口限流器 + 读 DB enableRateLimit 的条件包装。
//
// 对齐 packages/server/src/app.ts 中的 authLimiter / globalLimiter / proxyLimiter 等；
// 对齐 middleware/conditionalRateLimit.ts 的 1s TTL + in-flight 去重（这里用 singleflight）。
package middleware

import (
	"net/http"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/gin-gonic/gin"
	"golang.org/x/sync/singleflight"
	"gorm.io/gorm"

	"github.com/hor1zon777/m3u8-preview-go/internal/model"
)

// ---- 固定窗口限流 ----

type bucket struct {
	count     int
	expiresAt int64 // unix nano
}

// WindowLimiter 固定时间窗口限流（与 express-rate-limit 默认模型一致）。
// 不保证严格滑动窗口，但实现简单、无外部依赖、内存占用稳定。
type WindowLimiter struct {
	max    int
	window time.Duration
	mu     sync.Mutex
	store  map[string]*bucket
}

// NewWindowLimiter 构造 max 请求 / window 秒的限流器。
func NewWindowLimiter(max int, window time.Duration) *WindowLimiter {
	l := &WindowLimiter{
		max:    max,
		window: window,
		store:  make(map[string]*bucket),
	}
	go l.sweep()
	return l
}

// Allow 检查 key 是否还有配额。
func (l *WindowLimiter) Allow(key string) bool {
	now := time.Now().UnixNano()
	l.mu.Lock()
	defer l.mu.Unlock()

	b, ok := l.store[key]
	if !ok || b.expiresAt <= now {
		l.store[key] = &bucket{count: 1, expiresAt: now + l.window.Nanoseconds()}
		return true
	}
	if b.count >= l.max {
		return false
	}
	b.count++
	return true
}

// sweep 周期清理过期桶，防止内存泄漏。
func (l *WindowLimiter) sweep() {
	t := time.NewTicker(l.window)
	defer t.Stop()
	for range t.C {
		now := time.Now().UnixNano()
		l.mu.Lock()
		for k, b := range l.store {
			if b.expiresAt <= now {
				delete(l.store, k)
			}
		}
		l.mu.Unlock()
	}
}

// RateLimit 返回一个 Gin 中间件。
// keyFn 为空时默认按 c.ClientIP() 分桶；传入可用于按 userId / 其他维度分桶。
// message 失败响应文案。
func RateLimit(l *WindowLimiter, message string, keyFn func(*gin.Context) string) gin.HandlerFunc {
	if keyFn == nil {
		keyFn = func(c *gin.Context) string { return c.ClientIP() }
	}
	if message == "" {
		message = "Too many requests, please try again later"
	}
	return func(c *gin.Context) {
		key := keyFn(c)
		if !l.Allow(key) {
			c.Header("Retry-After", "60")
			AbortWithAppError(c, NewAppError(http.StatusTooManyRequests, message))
			return
		}
		c.Next()
	}
}

// ---- conditionalRateLimit：按 DB 设置开关限流 ----

// RateLimitSettingCache 用于读取 enableRateLimit 并提供快速缓存。
// 缓存 TTL 1s + singleflight 合并并发 DB 读。
type RateLimitSettingCache struct {
	db      *gorm.DB
	ttl     time.Duration
	enabled atomic.Bool
	expires atomic.Int64 // unix nano
	group   singleflight.Group
}

// NewRateLimitSettingCache 构造并初始化为默认启用（安全默认：bias toward limiting）。
func NewRateLimitSettingCache(db *gorm.DB) *RateLimitSettingCache {
	c := &RateLimitSettingCache{db: db, ttl: time.Second}
	c.enabled.Store(true)
	return c
}

// Enabled 返回当前是否启用限流（命中缓存直接返回；过期时经 singleflight 刷新）。
func (c *RateLimitSettingCache) Enabled() bool {
	now := time.Now().UnixNano()
	if now < c.expires.Load() {
		return c.enabled.Load()
	}
	// singleflight 合并并发查询
	v, _, _ := c.group.Do("enableRateLimit", func() (interface{}, error) {
		var setting model.SystemSetting
		err := c.db.Where("key = ?", model.SettingEnableRateLimit).Take(&setting).Error
		enabled := true
		if err == nil && strings.EqualFold(setting.Value, "false") {
			enabled = false
		}
		c.enabled.Store(enabled)
		c.expires.Store(time.Now().Add(c.ttl).UnixNano())
		return enabled, nil
	})
	b, _ := v.(bool)
	return b
}

// Invalidate 立即清掉缓存；PUT /admin/settings enableRateLimit 后必须调用。
func (c *RateLimitSettingCache) Invalidate() {
	c.expires.Store(0)
}

// ConditionalRateLimit 把一个普通限流中间件包装成按设置开关启用/跳过。
// bypass 用于 proxy 路由下允许特定 isToggleRequest 直通；其它场景传 nil。
func ConditionalRateLimit(
	cache *RateLimitSettingCache,
	limiter gin.HandlerFunc,
	bypass func(*gin.Context) bool,
) gin.HandlerFunc {
	return func(c *gin.Context) {
		if bypass != nil && bypass(c) {
			c.Next()
			return
		}
		if !cache.Enabled() {
			c.Next()
			return
		}
		limiter(c)
	}
}

// IsRateLimitToggleRequest 识别 "PUT /api/v1/admin/settings body.key=enableRateLimit"。
// 用于在全局限流器上 bypass 该请求，防止管理员因为限流本身无法关限流。
// 注意：这里不读 body（Gin 只允许 body 读一次）——该 bypass 只做粗粒度路径匹配；
// 更严格的 body key 判断放到 handler 内。
func IsRateLimitToggleRequest(c *gin.Context) bool {
	return c.Request.Method == http.MethodPut &&
		c.Request.URL.Path == "/api/v1/admin/settings"
}
