// Package handler
// auth.go 对接 /api/v1/auth/* 路由，协调 DTO 绑定、service 调用与 Cookie 管理。
// 对齐 packages/server/src/controllers/authController.ts。
package handler

import (
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net/http"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/gin-gonic/gin/binding"
	"github.com/go-playground/validator/v10"

	"github.com/hor1zon777/m3u8-preview-go/internal/config"
	"github.com/hor1zon777/m3u8-preview-go/internal/dto"
	"github.com/hor1zon777/m3u8-preview-go/internal/middleware"
	"github.com/hor1zon777/m3u8-preview-go/internal/service"
	"github.com/hor1zon777/m3u8-preview-go/internal/util"
)

// AuthHandler 汇总 auth 端点。
type AuthHandler struct {
	svc        *service.AuthService
	ticket     *util.SSETicketStore
	cfg        *config.Config
	ecdh       *util.ECDHService
	challenges *util.ChallengeStore
}

// NewAuthHandler 构造。
// ecdh / challenges 用于加密登录协议：前端先拉 challenge，再用 ECDH+HKDF+AES-GCM 提交密文。
func NewAuthHandler(
	svc *service.AuthService,
	ticket *util.SSETicketStore,
	cfg *config.Config,
	ecdhSvc *util.ECDHService,
	challenges *util.ChallengeStore,
) *AuthHandler {
	return &AuthHandler{
		svc:        svc,
		ticket:     ticket,
		cfg:        cfg,
		ecdh:       ecdhSvc,
		challenges: challenges,
	}
}

// Register 注入 Gin 路由。
// authLimiter 已在上游 group.Use 注入；这里不再重复。
func (h *AuthHandler) Register(rg *gin.RouterGroup) {
	rg.POST("/challenge", h.challenge)
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

// challenge 签发一次性加密挑战，绑定设备指纹。
func (h *AuthHandler) challenge(c *gin.Context) {
	var req dto.ChallengeRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		middleware.AbortWithAppError(c, bindErrorToAppError(err))
		return
	}
	id, _ := h.challenges.Issue(req.Fingerprint)
	c.JSON(http.StatusOK, dto.OK(dto.ChallengeResponse{
		ServerPub: base64.RawURLEncoding.EncodeToString(h.ecdh.PublicKeyRaw()),
		Challenge: id,
		TTL:       h.challenges.TTLSeconds(),
	}))
}

func (h *AuthHandler) register(c *gin.Context) {
	var enc dto.EncryptedAuthRequest
	if err := c.ShouldBindJSON(&enc); err != nil {
		middleware.AbortWithAppError(c, bindErrorToAppError(err))
		return
	}
	plaintext, err := h.decryptAuth(&enc, aadRegister)
	if err != nil {
		middleware.AbortWithAppError(c, err)
		return
	}
	var req dto.RegisterRequest
	if err := unmarshalAndValidate(plaintext, &req); err != nil {
		middleware.AbortWithAppError(c, err)
		return
	}
	tp, err2 := h.svc.Register(req.Username, req.Password)
	if err2 != nil {
		_ = c.Error(err2)
		return
	}
	h.setRefreshCookie(c, tp.RefreshToken)
	c.JSON(http.StatusOK, dto.OK(dto.AuthResponse{User: tp.User, AccessToken: tp.AccessToken}))
}

func (h *AuthHandler) login(c *gin.Context) {
	var enc dto.EncryptedAuthRequest
	if err := c.ShouldBindJSON(&enc); err != nil {
		middleware.AbortWithAppError(c, bindErrorToAppError(err))
		return
	}
	plaintext, err := h.decryptAuth(&enc, aadLogin)
	if err != nil {
		middleware.AbortWithAppError(c, err)
		return
	}
	var req dto.LoginRequest
	if err := unmarshalAndValidate(plaintext, &req); err != nil {
		middleware.AbortWithAppError(c, err)
		return
	}
	ip := util.GetClientIP(c, h.cfg.TrustCDN)
	ua := c.GetHeader("User-Agent")
	tp, err2 := h.svc.Login(req.Username, req.Password, ip, ua)
	if err2 != nil {
		_ = c.Error(err2)
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
	var enc dto.EncryptedAuthRequest
	if err := c.ShouldBindJSON(&enc); err != nil {
		middleware.AbortWithAppError(c, bindErrorToAppError(err))
		return
	}
	plaintext, err := h.decryptAuth(&enc, aadChangePassword)
	if err != nil {
		middleware.AbortWithAppError(c, err)
		return
	}
	var req dto.ChangePasswordRequest
	if err := unmarshalAndValidate(plaintext, &req); err != nil {
		middleware.AbortWithAppError(c, err)
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

// --- 加密登录协议 helpers ---

// AAD 端点绑定常量。加入 AES-GCM AAD 后，把 login 的密文改投给 register/change-password 会 GCM 校验失败。
const (
	aadLogin          = "auth:login:v1"
	aadRegister       = "auth:register:v1"
	aadChangePassword = "auth:change-password:v1"
)

// encryptedTSWindow 明文内 ts 字段允许的时钟漂移。超出窗口即便 challenge 尚未消费也视为重放尝试。
const encryptedTSWindow = 60 * time.Second

// encryptedPayloadEnvelope 是前端 JSON 明文的公共字段。各端点的业务字段用 RawMessage 透传。
// 分两步 unmarshal：先读 ts，再 unmarshal 到具体 DTO。
type encryptedPayloadEnvelope struct {
	TS int64 `json:"ts"`
}

// decryptAuth 按协议解密 EncryptedAuthRequest，返回明文字节。
// 失败统一返 400 "请求无效"，不回显内部原因（防止 oracle）。
// 已记入 c.Error(原 err)，ErrorHandler 中间件会日志化。
func (h *AuthHandler) decryptAuth(enc *dto.EncryptedAuthRequest, aad string) (plaintext []byte, appErr *middleware.AppError) {
	clientPub, err := base64.RawURLEncoding.DecodeString(enc.ClientPub)
	if err != nil {
		return nil, middleware.NewAppError(http.StatusBadRequest, "请求无效")
	}
	iv, err := base64.RawURLEncoding.DecodeString(enc.IV)
	if err != nil {
		return nil, middleware.NewAppError(http.StatusBadRequest, "请求无效")
	}
	ct, err := base64.RawURLEncoding.DecodeString(enc.Ciphertext)
	if err != nil {
		return nil, middleware.NewAppError(http.StatusBadRequest, "请求无效")
	}

	// 消费 challenge（单次 + TTL 60s）+ 取出绑定的设备指纹。
	salt, fp, ok := h.challenges.Consume(enc.Challenge)
	if !ok {
		return nil, middleware.NewAppError(http.StatusBadRequest, "挑战已过期或无效")
	}

	// 混合 salt：SHA256(challengeSalt || fingerprint)，与 Rust WASM blend_salt 对齐。
	fpBytes, err := hex.DecodeString(fp)
	if err != nil {
		return nil, middleware.NewAppError(http.StatusBadRequest, "请求无效")
	}
	blendedSalt := util.BlendSalt(salt, fpBytes)

	pt, err := h.ecdh.DecryptAuthPayload(clientPub, iv, ct, []byte(aad), blendedSalt)
	if err != nil {
		return nil, middleware.NewAppError(http.StatusBadRequest, "请求无效")
	}

	// 校验时间戳窗口作为双保险（challenge 已是一次性，这里主要挡客户端时钟飘移 + 便于日志分析）。
	var env encryptedPayloadEnvelope
	if err := json.Unmarshal(pt, &env); err != nil {
		return nil, middleware.NewAppError(http.StatusBadRequest, "请求无效")
	}
	if env.TS == 0 {
		return nil, middleware.NewAppError(http.StatusBadRequest, "请求无效")
	}
	drift := time.Since(time.UnixMilli(env.TS))
	if drift < -encryptedTSWindow || drift > encryptedTSWindow {
		return nil, middleware.NewAppError(http.StatusBadRequest, "请求时间戳超出容许窗口")
	}
	return pt, nil
}

// unmarshalAndValidate 把明文 JSON 解入 dst 并跑 validator binding tag。
// dst 应为 *RegisterRequest / *LoginRequest / *ChangePasswordRequest 之一。
func unmarshalAndValidate(plaintext []byte, dst any) *middleware.AppError {
	if err := json.Unmarshal(plaintext, dst); err != nil {
		return middleware.NewAppError(http.StatusBadRequest, "请求无效")
	}
	if err := binding.Validator.ValidateStruct(dst); err != nil {
		return bindErrorToAppError(err)
	}
	return nil
}
