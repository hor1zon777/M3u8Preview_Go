// Package dto
// auth.go 汇总 auth 相关请求/响应 DTO，对齐 shared/validation.ts 的 Zod schema。
// 用 go-playground/validator 的 binding tag 做大部分校验；密码复杂度与新旧不同等交叉规则用 struct-level 校验。
package dto

// RegisterRequest POST /auth/register
// username: 3-50 长度，仅 alnum + 下划线；password: 8-72 长度 + 大/小/数字（复杂度由 validator 注册）
type RegisterRequest struct {
	Username string `json:"username" binding:"required,min=3,max=50,username_chars"`
	Password string `json:"password" binding:"required,min=8,max=72,password_complex"`
}

// LoginRequest POST /auth/login
type LoginRequest struct {
	Username string `json:"username" binding:"required"`
	Password string `json:"password" binding:"required"`
}

// ChangePasswordRequest POST /auth/change-password
// 新旧密码必须不同（在 service 层额外校验）
type ChangePasswordRequest struct {
	OldPassword string `json:"oldPassword" binding:"required"`
	NewPassword string `json:"newPassword" binding:"required,min=8,max=72,password_complex"`
}

// UserResponse 是脱敏后的用户信息，用于登录/注册/me 响应。
// 对齐 shared/types.ts User interface；createdAt/updatedAt 格式化为 ISO8601 毫秒。
type UserResponse struct {
	ID        string  `json:"id"`
	Username  string  `json:"username"`
	Role      string  `json:"role"`
	Avatar    *string `json:"avatar,omitempty"`
	IsActive  bool    `json:"isActive"`
	CreatedAt string  `json:"createdAt"`
	UpdatedAt string  `json:"updatedAt"`
}

// AuthResponse 对齐 shared/types.ts AuthResponse。
// 注意：refreshToken 不放响应 body；通过 Set-Cookie 下发（对齐 TS 版）。
type AuthResponse struct {
	User        UserResponse `json:"user"`
	AccessToken string       `json:"accessToken"`
}

// RegisterStatusResponse GET /auth/register-status
type RegisterStatusResponse struct {
	AllowRegistration bool `json:"allowRegistration"`
}

// SSETicketResponse POST /auth/sse-ticket
type SSETicketResponse struct {
	Ticket string `json:"ticket"`
}

// --- 加密登录协议 DTO（与前端 web/client/src/utils/crypto.ts 对齐）---

// ChallengeResponse GET /auth/challenge
// serverPub: 服务端长寿 ECDH P-256 公钥 raw(65B uncompressed)，base64url 无 padding。
// challenge: 一次性 salt 标识（也是 HKDF salt 的 base64url 编码），前端原样回传。
// ttl: challenge 过期秒数，前端决定是否刷新。
type ChallengeResponse struct {
	ServerPub string `json:"serverPub"`
	Challenge string `json:"challenge"`
	TTL       int    `json:"ttl"`
}

// EncryptedAuthRequest 是 login/register/change-password 的统一传输壳。
// 内部明文格式由具体端点决定：
//   - login:      {"username":"...","password":"..."}
//   - register:   {"username":"...","password":"..."}
//   - changePwd:  {"oldPassword":"...","newPassword":"..."}
//
// 所有二进制字段均 base64url 无 padding 编码。
// AES-GCM 的 AAD 在 handler 层按端点名绑定（"login"/"register"/"change-password"），
// 防止攻击者把 login 的密文原样当 change-password 提交。
type EncryptedAuthRequest struct {
	Challenge  string `json:"challenge" binding:"required"`
	ClientPub  string `json:"clientPub" binding:"required"`
	IV         string `json:"iv"        binding:"required"`
	Ciphertext string `json:"ct"        binding:"required"`
}
