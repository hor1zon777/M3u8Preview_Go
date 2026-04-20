// Package middleware
// auth.go 对齐 packages/server/src/middleware/auth.ts：
//   - Authenticate：强制 Bearer token，支持 ?ticket=xxx 一次性 SSE ticket 分支
//   - OptionalAuth：无 token 放行、有无效 token 记 warn 后放行
//   - RequireRole：在 Authenticate 之后叠加的角色校验
package middleware

import (
	"net/http"

	"github.com/gin-gonic/gin"

	"github.com/hor1zon777/m3u8-preview-go/internal/util"
)

// context keys — 避免 handler 直接访问字符串字面量。
const (
	ContextKeyUserID = "userId"
	ContextKeyRole   = "role"
)

// AuthDeps 打包认证中间件需要的依赖。
type AuthDeps struct {
	JWT    *util.JWTService
	Ticket *util.SSETicketStore
}

// Authenticate 构造强制认证中间件。
// 优先级：?ticket=xxx（仅 SSE）→ Authorization: Bearer <token>
func Authenticate(d *AuthDeps) gin.HandlerFunc {
	return func(c *gin.Context) {
		// SSE ticket 分支
		if ticket := c.Query("ticket"); ticket != "" {
			claim, ok := d.Ticket.Consume(ticket)
			if !ok {
				AbortWithAppError(c, NewAppError(http.StatusUnauthorized, "Invalid or expired ticket"))
				return
			}
			c.Set(ContextKeyUserID, claim.UserID)
			c.Set(ContextKeyRole, claim.Role)
			c.Next()
			return
		}

		token, ok := bearerToken(c)
		if !ok {
			AbortWithAppError(c, NewAppError(http.StatusUnauthorized, "Authentication required"))
			return
		}
		claims, err := d.JWT.Verify(token, util.JWTPurposeAccess)
		if err != nil || claims == nil || claims.UserID == "" {
			AbortWithAppError(c, NewAppError(http.StatusUnauthorized, "Invalid or expired token"))
			return
		}
		c.Set(ContextKeyUserID, claims.UserID)
		c.Set(ContextKeyRole, claims.Role)
		c.Next()
	}
}

// OptionalAuth 尝试解析 token；失败静默放行。
// 用于 /media/:id/views（允许匿名但识别登录用户）、/playlists/:id/items 等混合场景。
func OptionalAuth(d *AuthDeps) gin.HandlerFunc {
	return func(c *gin.Context) {
		token, ok := bearerToken(c)
		if !ok {
			c.Next()
			return
		}
		claims, err := d.JWT.Verify(token, util.JWTPurposeAccess)
		if err == nil && claims != nil && claims.UserID != "" {
			c.Set(ContextKeyUserID, claims.UserID)
			c.Set(ContextKeyRole, claims.Role)
		}
		c.Next()
	}
}

// RequireRole 在 Authenticate 之后使用，校验角色是否在允许列表中。
func RequireRole(roles ...string) gin.HandlerFunc {
	return func(c *gin.Context) {
		uid, _ := c.Get(ContextKeyUserID)
		if uid == nil {
			AbortWithAppError(c, NewAppError(http.StatusUnauthorized, "Authentication required"))
			return
		}
		role, _ := c.Get(ContextKeyRole)
		roleStr, _ := role.(string)
		for _, r := range roles {
			if r == roleStr {
				c.Next()
				return
			}
		}
		AbortWithAppError(c, NewAppError(http.StatusForbidden, "Insufficient permissions"))
	}
}

// CurrentUserID 是 handler 里取当前用户 ID 的语义化 helper。
func CurrentUserID(c *gin.Context) string {
	v, ok := c.Get(ContextKeyUserID)
	if !ok {
		return ""
	}
	s, _ := v.(string)
	return s
}

// CurrentRole 同上，取角色。
func CurrentRole(c *gin.Context) string {
	v, ok := c.Get(ContextKeyRole)
	if !ok {
		return ""
	}
	s, _ := v.(string)
	return s
}

func bearerToken(c *gin.Context) (string, bool) {
	h := c.GetHeader("Authorization")
	const prefix = "Bearer "
	if len(h) <= len(prefix) || h[:len(prefix)] != prefix {
		return "", false
	}
	return h[len(prefix):], true
}
