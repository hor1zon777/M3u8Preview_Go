// Package handler
// auth.go 对接 /api/v1/auth/* 路由，协调 DTO 绑定、service 调用与 Cookie 管理。
// 对齐 packages/server/src/controllers/authController.ts。
package handler

import (
	"errors"
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/go-playground/validator/v10"

	"github.com/hor1zon777/m3u8-preview-go/internal/config"
	"github.com/hor1zon777/m3u8-preview-go/internal/dto"
	"github.com/hor1zon777/m3u8-preview-go/internal/middleware"
	"github.com/hor1zon777/m3u8-preview-go/internal/service"
	"github.com/hor1zon777/m3u8-preview-go/internal/util"
)

// AuthHandler 汇总 auth 端点。
type AuthHandler struct {
	svc     *service.AuthService
	ticket  *util.SSETicketStore
	cfg     *config.Config
}

// NewAuthHandler 构造。
func NewAuthHandler(svc *service.AuthService, ticket *util.SSETicketStore, cfg *config.Config) *AuthHandler {
	return &AuthHandler{svc: svc, ticket: ticket, cfg: cfg}
}

// Register 注入 Gin 路由。
// authLimiter 已在上游 group.Use 注入；这里不再重复。
func (h *AuthHandler) Register(rg *gin.RouterGroup) {
	rg.POST("/register", h.register)
	rg.POST("/login", h.login)
	rg.POST("/refresh", h.refresh)
	rg.POST("/logout", h.logout)
	rg.GET("/register-status", h.registerStatus)
}

// RegisterAuthed 挂需要登录的端点（调用方在 Use(Authenticate) 之后传入此 group）。
func (h *AuthHandler) RegisterAuthed(rg *gin.RouterGroup) {
	rg.GET("/me", h.me)
	rg.POST("/change-password", h.changePassword)
	rg.POST("/sse-ticket", h.sseTicket)
}

// --- handlers ---

func (h *AuthHandler) register(c *gin.Context) {
	var req dto.RegisterRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		middleware.AbortWithAppError(c, bindErrorToAppError(err))
		return
	}
	tp, err := h.svc.Register(req.Username, req.Password)
	if err != nil {
		_ = c.Error(err)
		return
	}
	h.setRefreshCookie(c, tp.RefreshToken)
	c.JSON(http.StatusOK, dto.OK(dto.AuthResponse{User: tp.User, AccessToken: tp.AccessToken}))
}

func (h *AuthHandler) login(c *gin.Context) {
	var req dto.LoginRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		middleware.AbortWithAppError(c, bindErrorToAppError(err))
		return
	}
	ip := util.GetClientIP(c, h.cfg.TrustCDN)
	ua := c.GetHeader("User-Agent")
	tp, err := h.svc.Login(req.Username, req.Password, ip, ua)
	if err != nil {
		_ = c.Error(err)
		return
	}
	h.setRefreshCookie(c, tp.RefreshToken)
	c.JSON(http.StatusOK, dto.OK(dto.AuthResponse{User: tp.User, AccessToken: tp.AccessToken}))
}

func (h *AuthHandler) refresh(c *gin.Context) {
	raw := refreshCookieValue(c)
	if raw == "" {
		middleware.AbortWithAppError(c, middleware.NewAppError(http.StatusUnauthorized, "缺少刷新令牌"))
		return
	}
	tp, err := h.svc.Refresh(raw)
	if err != nil {
		// refresh 失败时清 Cookie，避免前端反复重试
		h.clearRefreshCookie(c)
		_ = c.Error(err)
		return
	}
	h.setRefreshCookie(c, tp.RefreshToken)
	c.JSON(http.StatusOK, dto.OK(dto.AuthResponse{User: tp.User, AccessToken: tp.AccessToken}))
}

func (h *AuthHandler) logout(c *gin.Context) {
	raw := refreshCookieValue(c)
	// 不论 DB 是否删除成功都清 cookie（从客户端角度已登出）；但服务端错误需记录。
	if err := h.svc.Logout(raw); err != nil {
		// 记录但不阻塞 cookie 清理；用 _ = c.Error(err) 让 error middleware 感知到
		_ = c.Error(err)
	}
	h.clearRefreshCookie(c)
	c.JSON(http.StatusOK, dto.OK(gin.H{"message": "logged out"}))
}

func (h *AuthHandler) registerStatus(c *gin.Context) {
	allow := h.svc.GetRegisterStatus()
	c.JSON(http.StatusOK, dto.OK(dto.RegisterStatusResponse{AllowRegistration: allow}))
}

func (h *AuthHandler) me(c *gin.Context) {
	uid := middleware.CurrentUserID(c)
	u, err := h.svc.GetProfile(uid)
	if err != nil {
		_ = c.Error(err)
		return
	}
	c.JSON(http.StatusOK, dto.OK(u))
}

func (h *AuthHandler) changePassword(c *gin.Context) {
	var req dto.ChangePasswordRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		middleware.AbortWithAppError(c, bindErrorToAppError(err))
		return
	}
	uid := middleware.CurrentUserID(c)
	if err := h.svc.ChangePassword(uid, req.OldPassword, req.NewPassword); err != nil {
		_ = c.Error(err)
		return
	}
	h.clearRefreshCookie(c)
	c.JSON(http.StatusOK, dto.OK(gin.H{"message": "password changed"}))
}

func (h *AuthHandler) sseTicket(c *gin.Context) {
	uid := middleware.CurrentUserID(c)
	role := middleware.CurrentRole(c)
	ticket := h.ticket.Issue(uid, role)
	c.JSON(http.StatusOK, dto.OK(dto.SSETicketResponse{Ticket: ticket}))
}

// --- Cookie helpers ---

const refreshCookieName = "refreshToken"

func (h *AuthHandler) setRefreshCookie(c *gin.Context, token string) {
	maxAge := int(h.cfg.JWT.RefreshExpiresIn.Seconds())
	c.SetSameSite(http.SameSiteLaxMode)
	c.SetCookie(refreshCookieName, token, maxAge, "/", "", h.cfg.CookieSecure, true)
}

func (h *AuthHandler) clearRefreshCookie(c *gin.Context) {
	c.SetSameSite(http.SameSiteLaxMode)
	c.SetCookie(refreshCookieName, "", -1, "/", "", h.cfg.CookieSecure, true)
}

func refreshCookieValue(c *gin.Context) string {
	v, err := c.Cookie(refreshCookieName)
	if err != nil {
		return ""
	}
	return v
}

// bindErrorToAppError 把 validator 错误映射成 400 AppError，错误消息保留字段名便于前端解析。
func bindErrorToAppError(err error) *middleware.AppError {
	var verrs validator.ValidationErrors
	if errors.As(err, &verrs) && len(verrs) > 0 {
		fe := verrs[0]
		return middleware.NewAppError(http.StatusBadRequest,
			fe.Field()+": "+fe.Tag())
	}
	return middleware.WrapAppError(http.StatusBadRequest, "请求体无效", err)
}
