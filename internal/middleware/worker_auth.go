// Package middleware
// worker_auth.go 提供远程字幕 worker 的 Bearer token 鉴权。
//
// 与 user JWT 完全独立：
//   - JWT 是短期 access token（几小时），人类登录用
//   - worker token 是长期凭证（admin 显式生成 / 吊销），机器对机器调用用
//   - 两者中间件互不干扰；worker 端点路由组单独挂 RequireWorkerAuth
//
// Token 格式：`mwt_<32 chars [a-z2-7]>`，bcrypt cost=12 存 DB（参考 user password 风格）。
// 明文仅在 admin 创建时返回一次；后续通过 TokenPrefix（明文前 12 位）做候选检索。
//
// 性能：
//   - bcrypt 比对 cost=12 ≈ 200ms。worker 5s 轮询时若每次都 bcrypt 会浪费 CPU。
//   - tokenAuthCache 用 sync.Map 缓存 (plaintext sha256) → tokenID（命中后不再 bcrypt），
//     5 分钟内 worker 反复 claim 都是 O(1)；token 吊销时通过 InvalidateWorkerTokenCache 清除。
package middleware

import (
	"crypto/sha256"
	"encoding/hex"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/gin-gonic/gin"
	"golang.org/x/crypto/bcrypt"
	"gorm.io/gorm"

	"github.com/hor1zon777/m3u8-preview-go/internal/model"
)

// context keys 给后续 handler 取信息用。
const (
	ContextKeyWorkerTokenID = "workerTokenId"
)

// WorkerTokenPrefix 由前缀检索 token 时的 SQL LIKE 长度。
//
// 明文格式 "mwt_<32>"；前 12 位 = "mwt_" + 8 位随机，已具备很高区分度，
// 索引扫描代价 O(候选数)；后 24 位由 bcrypt 校验。
const WorkerTokenPrefix = 12

// 内存缓存：plaintext sha256 → tokenID + 缓存到期时间。
// 不缓存 bcrypt 失败结果，避免攻击者通过缓存推断 token 存在性。
type tokenCacheEntry struct {
	tokenID  string
	expireAt time.Time
}

var (
	tokenAuthCache   sync.Map
	tokenCacheTTL    = 5 * time.Minute
	dummyTokenHash   = "$2a$12$abcdefghijklmnopqrstuvwxyz0123456789ABCDEFGHIJKLMNOPQR" // 假 hash 用于时序攻击防御
)

// RequireWorkerAuth 校验 Authorization: Bearer mwt_xxx。
//
// 流程：
//  1. 从 header 解析 Bearer token（同 user 路径的 bearerToken）
//  2. 检查格式（mwt_ 前缀 + 长度）；不合法直接 401
//  3. 命中缓存 → 续期 → set context → next
//  4. 按前 WorkerTokenPrefix 字符查 DB（subtitle_worker_tokens.token_prefix）
//  5. 对每个候选 bcrypt 比对，命中且未吊销则缓存 + 异步刷 LastUsedAt
//
// 时序攻击防御：候选为空时跑一次假 bcrypt（dummy hash），与真实路径时延对齐。
func RequireWorkerAuth(db *gorm.DB) gin.HandlerFunc {
	return func(c *gin.Context) {
		token, ok := bearerToken(c)
		if !ok {
			AbortWithAppError(c, NewAppError(http.StatusUnauthorized, "Worker authentication required"))
			return
		}
		if !looksLikeWorkerToken(token) {
			AbortWithAppError(c, NewAppError(http.StatusUnauthorized, "Invalid worker token format"))
			return
		}

		key := tokenCacheKey(token)
		if entry, hit := loadTokenCacheEntry(key); hit {
			c.Set(ContextKeyWorkerTokenID, entry.tokenID)
			c.Next()
			return
		}

		prefix := token[:WorkerTokenPrefix]
		var candidates []model.SubtitleWorkerToken
		if err := db.Where("token_prefix = ? AND revoked_at IS NULL", prefix).Find(&candidates).Error; err != nil {
			// DB 错误：不暴露内部细节，返回 401
			AbortWithAppError(c, NewAppError(http.StatusUnauthorized, "Authentication failed"))
			return
		}

		if len(candidates) == 0 {
			// 没有候选也跑一次假 bcrypt 平时延
			_ = bcrypt.CompareHashAndPassword([]byte(dummyTokenHash), []byte(token))
			AbortWithAppError(c, NewAppError(http.StatusUnauthorized, "Invalid worker token"))
			return
		}

		var matched *model.SubtitleWorkerToken
		for i := range candidates {
			if err := bcrypt.CompareHashAndPassword([]byte(candidates[i].TokenHash), []byte(token)); err == nil {
				matched = &candidates[i]
				break
			}
		}
		if matched == nil {
			AbortWithAppError(c, NewAppError(http.StatusUnauthorized, "Invalid worker token"))
			return
		}

		// 命中：写缓存 + 异步刷 LastUsedAt（不阻塞请求）
		storeTokenCacheEntry(key, matched.ID)
		go updateTokenLastUsedAt(db, matched.ID)

		c.Set(ContextKeyWorkerTokenID, matched.ID)
		c.Next()
	}
}

// CurrentWorkerTokenID 给 handler 用的语义化 helper。
func CurrentWorkerTokenID(c *gin.Context) string {
	v, ok := c.Get(ContextKeyWorkerTokenID)
	if !ok {
		return ""
	}
	s, _ := v.(string)
	return s
}

// InvalidateWorkerTokenCache admin 吊销 / 删除 token 时调用。
// 对于已知明文 token 可以直接 Delete；不知明文时可调用 PurgeWorkerTokenCache 清空整个缓存（重启级影响很小，token 通常少）。
func InvalidateWorkerTokenCache(plaintext string) {
	tokenAuthCache.Delete(tokenCacheKey(plaintext))
}

// PurgeWorkerTokenCache 清空整个 worker token 缓存。admin 吊销 token 后调用，
// 确保已被吊销的 token 不会因为缓存还能继续访问几分钟。
func PurgeWorkerTokenCache() {
	tokenAuthCache.Range(func(k, _ any) bool {
		tokenAuthCache.Delete(k)
		return true
	})
}

// looksLikeWorkerToken 快速排除明显畸形输入，避免毫无意义地查 DB / 跑 bcrypt。
//
// 合法格式：mwt_ 前缀 + 32 位字符。允许 [a-zA-Z0-9_-] 即可（实际生成是 base32 / base62）。
func looksLikeWorkerToken(s string) bool {
	if !strings.HasPrefix(s, "mwt_") {
		return false
	}
	if len(s) < WorkerTokenPrefix+8 || len(s) > 64 {
		return false
	}
	for _, r := range s {
		switch {
		case r >= 'a' && r <= 'z',
			r >= 'A' && r <= 'Z',
			r >= '0' && r <= '9',
			r == '_', r == '-':
		default:
			return false
		}
	}
	return true
}

func tokenCacheKey(plaintext string) string {
	sum := sha256.Sum256([]byte(plaintext))
	return hex.EncodeToString(sum[:])
}

func loadTokenCacheEntry(key string) (tokenCacheEntry, bool) {
	v, ok := tokenAuthCache.Load(key)
	if !ok {
		return tokenCacheEntry{}, false
	}
	entry := v.(tokenCacheEntry)
	if time.Now().After(entry.expireAt) {
		tokenAuthCache.Delete(key)
		return tokenCacheEntry{}, false
	}
	return entry, true
}

func storeTokenCacheEntry(key, tokenID string) {
	tokenAuthCache.Store(key, tokenCacheEntry{
		tokenID:  tokenID,
		expireAt: time.Now().Add(tokenCacheTTL),
	})
}

func updateTokenLastUsedAt(db *gorm.DB, tokenID string) {
	now := time.Now()
	// 失败仅打日志（其实这里没 logger，所以静默忽略）；不阻塞主流程
	_ = db.Model(&model.SubtitleWorkerToken{}).
		Where("id = ?", tokenID).
		Update("last_used_at", &now).Error
}
