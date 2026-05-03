// Package service 封装领域业务逻辑，不感知 HTTP 层。
// auth.go 对齐 packages/server/src/services/authService.ts。
package service

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"log"
	"net/http"
	"strings"
	"time"

	"golang.org/x/crypto/bcrypt"
	"gorm.io/gorm"

	"github.com/hor1zon777/m3u8-preview-go/internal/config"
	"github.com/hor1zon777/m3u8-preview-go/internal/dto"
	"github.com/hor1zon777/m3u8-preview-go/internal/middleware"
	"github.com/hor1zon777/m3u8-preview-go/internal/model"
	"github.com/hor1zon777/m3u8-preview-go/internal/util"
)

// AuthService 聚合所有登录 / 注册 / token 轮换流程。
type AuthService struct {
	db      *gorm.DB
	jwt     *util.JWTService
	bcrypt  int
	refresh time.Duration
}

// NewAuthService 构造。bcryptCost 通常由 cfg.Bcrypt.SaltRounds 传入（= 12）。
func NewAuthService(db *gorm.DB, jwt *util.JWTService, cfg *config.Config) *AuthService {
	return &AuthService{
		db:      db,
		jwt:     jwt,
		bcrypt:  cfg.Bcrypt.SaltRounds,
		refresh: cfg.JWT.RefreshExpiresIn,
	}
}

// TokenPair 是登录 / 注册 / 刷新的产物。refreshToken 原文只在本次响应返回一次，DB 存的是 SHA-256 hash。
type TokenPair struct {
	User         dto.UserResponse
	AccessToken  string
	RefreshToken string
}

// Register 注册新用户。失败场景：注册开关关闭 / 用户名已存在。
func (s *AuthService) Register(username, password string) (*TokenPair, error) {
	// 注册开关
	var setting model.SystemSetting
	err := s.db.Where("key = ?", model.SettingAllowRegistration).Take(&setting).Error
	if err == nil && setting.Value == "false" {
		return nil, middleware.NewAppError(http.StatusForbidden, "注册功能已关闭")
	}

	var existing model.User
	if err := s.db.Where("username = ?", username).Take(&existing).Error; err == nil {
		return nil, middleware.NewAppError(http.StatusConflict, "用户名已存在")
	} else if !errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, middleware.WrapAppError(http.StatusInternalServerError, "查询用户失败", err)
	}

	hashed, err := bcrypt.GenerateFromPassword([]byte(password), s.bcrypt)
	if err != nil {
		return nil, middleware.WrapAppError(http.StatusInternalServerError, "密码加密失败", err)
	}

	user := model.User{
		Username:     username,
		PasswordHash: string(hashed),
		Role:         model.RoleUser,
		IsActive:     true,
	}
	if err := s.db.Create(&user).Error; err != nil {
		return nil, middleware.WrapAppError(http.StatusInternalServerError, "创建用户失败", err)
	}

	return s.issueTokens(&user, newFamilyID())
}

// dummyBcryptHash 用于拉平"用户不存在"与"密码错误"的时延差，缓解用户名枚举。
// 这是 cost=12 的 bcrypt hash，口令值与正式密码无关，只用于消耗 CPU 时间。
const dummyBcryptHash = "$2a$12$abcdefghijklmnopqrstuuH0Jh0Tn95i6h3nbr8Pxc8T8c4nDqjvK" // 任意一个合法 hash

// Login 校验密码后签发 token，并写一条 LoginRecord。
// LoginRecord 不阻塞主流程；失败只记日志。
// 用户名不存在与密码错误返回相同错误码和消息，并跑一次假 bcrypt 拉平时延；
// 账户禁用也一并映射为 401 以避免泄露账户存在性（仅审计日志记录真实原因）。
func (s *AuthService) Login(username, password, ip, userAgent string) (*TokenPair, error) {
	var user model.User
	err := s.db.Where("username = ?", username).Take(&user).Error
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			// 跑一次假 bcrypt 与真实路径时延对齐，无视结果
			_ = bcrypt.CompareHashAndPassword([]byte(dummyBcryptHash), []byte(password))
			return nil, middleware.NewAppError(http.StatusUnauthorized, "用户名或密码错误")
		}
		return nil, middleware.WrapAppError(http.StatusInternalServerError, "查询用户失败", err)
	}
	if !user.IsActive {
		// 禁用账户也走 bcrypt 并返回 401，避免枚举禁用账号
		_ = bcrypt.CompareHashAndPassword([]byte(user.PasswordHash), []byte(password))
		log.Printf("[auth] login denied for disabled account userId=%s", user.ID)
		return nil, middleware.NewAppError(http.StatusUnauthorized, "用户名或密码错误")
	}
	if err := bcrypt.CompareHashAndPassword([]byte(user.PasswordHash), []byte(password)); err != nil {
		return nil, middleware.NewAppError(http.StatusUnauthorized, "用户名或密码错误")
	}

	tp, err := s.issueTokens(&user, newFamilyID())
	if err != nil {
		return nil, err
	}

	// LoginRecord 最佳努力写入
	s.writeLoginRecord(user.ID, ip, userAgent)
	return tp, nil
}

// Refresh 校验并轮换 refresh token。
// 使用事务 + RowsAffected 检查保证原子，防止并发 refresh 同一 token 都成功（两条合法链路）。
// 复用检测命中时仅撤销同 family 的 token（若查得到 family），避免因客户端双提交踢掉所有设备。
func (s *AuthService) Refresh(rawRefresh string) (*TokenPair, error) {
	claims, err := s.jwt.Verify(rawRefresh, util.JWTPurposeRefresh)
	if err != nil {
		return nil, middleware.NewAppError(http.StatusUnauthorized, "Invalid refresh token")
	}

	hashed := util.HashSHA256Hex(rawRefresh)

	var stored model.RefreshToken
	err = s.db.Where("token = ?", hashed).Take(&stored).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		// 复用检测：token 不在 DB 中说明已被使用过或伪造。
		// 无 family 信息时退化为按 user 撤销（保留原行为，但记录日志便于审计）。
		log.Printf("[auth] refresh token reuse detected, userId=%s", claims.UserID)
		if err := s.db.Where("user_id = ?", claims.UserID).Delete(&model.RefreshToken{}).Error; err != nil {
			log.Printf("[auth] revoke all tokens failed userId=%s: %v", claims.UserID, err)
		}
		return nil, middleware.NewAppError(http.StatusUnauthorized, "Refresh token reuse detected, all sessions revoked")
	}
	if err != nil {
		return nil, middleware.WrapAppError(http.StatusInternalServerError, "查询刷新令牌失败", err)
	}

	if stored.ExpiresAt.Before(time.Now()) {
		_ = s.db.Delete(&stored).Error
		return nil, middleware.NewAppError(http.StatusUnauthorized, "Refresh token expired")
	}

	var user model.User
	if err := s.db.Take(&user, "id = ?", claims.UserID).Error; err != nil {
		return nil, middleware.NewAppError(http.StatusUnauthorized, "User not found or inactive")
	}
	if !user.IsActive {
		return nil, middleware.NewAppError(http.StatusUnauthorized, "User not found or inactive")
	}

	// 原子删除：通过事务中的 RowsAffected 确保同一 token 只被成功消费一次。
	// 并发 refresh 的另一条会拿到 RowsAffected=0，落入 reuse 检测路径。
	var rowsDeleted int64
	err = s.db.Transaction(func(tx *gorm.DB) error {
		res := tx.Where("token = ?", hashed).Delete(&model.RefreshToken{})
		rowsDeleted = res.RowsAffected
		return res.Error
	})
	if err != nil {
		return nil, middleware.WrapAppError(http.StatusInternalServerError, "轮换失败", err)
	}
	if rowsDeleted == 0 {
		// 竞态：另一条请求已消费这张 token。按 reuse 检测处理，但只撤销同 family。
		log.Printf("[auth] refresh token lost race, userId=%s family=%s", claims.UserID, stored.FamilyID)
		if err := s.db.Where("user_id = ? AND family_id = ?", claims.UserID, stored.FamilyID).Delete(&model.RefreshToken{}).Error; err != nil {
			log.Printf("[auth] revoke family tokens failed: %v", err)
		}
		return nil, middleware.NewAppError(http.StatusUnauthorized, "Refresh token already used")
	}
	return s.issueTokens(&user, stored.FamilyID)
}

// Logout 撤销传入的 refresh token。
// DB 错误上报给调用方，避免因 DB 抖动导致"客户端看 200 OK 但服务端未撤销"的会话残留。
func (s *AuthService) Logout(rawRefresh string) error {
	if rawRefresh == "" {
		return nil
	}
	res := s.db.Where("token = ?", util.HashSHA256Hex(rawRefresh)).Delete(&model.RefreshToken{})
	if res.Error != nil {
		log.Printf("[auth] logout delete failed: %v", res.Error)
		return middleware.WrapAppError(http.StatusInternalServerError, "登出失败", res.Error)
	}
	return nil
}

// GetProfile 返回脱敏用户信息。
func (s *AuthService) GetProfile(userID string) (*dto.UserResponse, error) {
	var u model.User
	if err := s.db.Take(&u, "id = ?", userID).Error; err != nil {
		return nil, middleware.NewAppError(http.StatusNotFound, "User not found")
	}
	resp := sanitizeUser(&u)
	return &resp, nil
}

// ChangePassword 校验旧密码 → 写新 hash → 撤销该用户全部 refresh token（强制所有设备重登）。
func (s *AuthService) ChangePassword(userID, oldPassword, newPassword string) error {
	if oldPassword == newPassword {
		return middleware.NewAppError(http.StatusBadRequest, "新密码必须与旧密码不同")
	}
	var u model.User
	if err := s.db.Take(&u, "id = ?", userID).Error; err != nil {
		return middleware.NewAppError(http.StatusNotFound, "用户不存在")
	}
	if err := bcrypt.CompareHashAndPassword([]byte(u.PasswordHash), []byte(oldPassword)); err != nil {
		return middleware.NewAppError(http.StatusUnauthorized, "旧密码错误")
	}
	newHash, err := bcrypt.GenerateFromPassword([]byte(newPassword), s.bcrypt)
	if err != nil {
		return middleware.WrapAppError(http.StatusInternalServerError, "密码加密失败", err)
	}
	return s.db.Transaction(func(tx *gorm.DB) error {
		if err := tx.Model(&model.User{}).Where("id = ?", userID).Update("password_hash", string(newHash)).Error; err != nil {
			return err
		}
		return tx.Where("user_id = ?", userID).Delete(&model.RefreshToken{}).Error
	})
}

// GetRegisterStatus 读 systemSetting 判断是否允许注册。
func (s *AuthService) GetRegisterStatus() bool {
	var setting model.SystemSetting
	err := s.db.Where("key = ?", model.SettingAllowRegistration).Take(&setting).Error
	if err != nil {
		// 未设置时默认允许
		return true
	}
	return setting.Value != "false"
}

// GetSiteName 读 systemSetting 返回站点显示名称；空值或不存在时回退到默认 "M3u8 Preview"。
// 与 migrate.go 的 SettingSiteName 默认值保持一致，避免 admin 清空后前端拿到空字符串。
func (s *AuthService) GetSiteName() string {
	const defaultSiteName = "M3u8 Preview"
	var setting model.SystemSetting
	if err := s.db.Where("key = ?", model.SettingSiteName).Take(&setting).Error; err != nil {
		return defaultSiteName
	}
	if strings.TrimSpace(setting.Value) == "" {
		return defaultSiteName
	}
	return setting.Value
}

// --- 内部辅助 ---

func (s *AuthService) issueTokens(user *model.User, familyID string) (*TokenPair, error) {
	access, err := s.jwt.Sign(user.ID, user.Role, util.JWTPurposeAccess)
	if err != nil {
		return nil, middleware.WrapAppError(http.StatusInternalServerError, "签发 access 失败", err)
	}
	refresh, err := s.jwt.Sign(user.ID, user.Role, util.JWTPurposeRefresh)
	if err != nil {
		return nil, middleware.WrapAppError(http.StatusInternalServerError, "签发 refresh 失败", err)
	}
	rt := model.RefreshToken{
		Token:     util.HashSHA256Hex(refresh),
		FamilyID:  familyID,
		UserID:    user.ID,
		ExpiresAt: time.Now().Add(s.refresh),
	}
	if err := s.db.Create(&rt).Error; err != nil {
		return nil, middleware.WrapAppError(http.StatusInternalServerError, "保存 refresh 失败", err)
	}
	return &TokenPair{
		User:         sanitizeUser(user),
		AccessToken:  access,
		RefreshToken: refresh,
	}, nil
}

func (s *AuthService) writeLoginRecord(userID, ip, rawUA string) {
	info := util.ParseUserAgent(rawUA)
	ipCopy := ip
	uaCopy := rawUA
	rec := model.LoginRecord{
		UserID:    userID,
		IP:        strPtrIfNotEmpty(ipCopy),
		UserAgent: strPtrIfNotEmpty(uaCopy),
		Browser:   info.Browser,
		OS:        info.OS,
		Device:    info.Device,
	}
	if err := s.db.Create(&rec).Error; err != nil {
		log.Printf("[auth] write login record failed: %v", err)
	}
}

func sanitizeUser(u *model.User) dto.UserResponse {
	return dto.UserResponse{
		ID:        u.ID,
		Username:  u.Username,
		Role:      u.Role,
		Avatar:    u.Avatar,
		IsActive:  u.IsActive,
		CreatedAt: util.FormatISO(u.CreatedAt),
		UpdatedAt: util.FormatISO(u.UpdatedAt),
	}
}

func newFamilyID() string {
	var b [16]byte
	_, _ = rand.Read(b[:])
	return hex.EncodeToString(b[:])
}

func strPtrIfNotEmpty(s string) *string {
	if s == "" || s == "unknown" {
		return nil
	}
	return &s
}
