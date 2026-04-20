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
