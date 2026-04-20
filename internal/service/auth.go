// Package service 封装领域业务逻辑，不感知 HTTP 层。
// auth.go 对齐 packages/server/src/services/authService.ts。
package service

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"log"
	"net/http"
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

// Login 校验密码后签发 token，并写一条 LoginRecord。
// LoginRecord 不阻塞主流程；失败只记日志。
func (s *AuthService) Login(username, password, ip, userAgent string) (*TokenPair, error) {
	var user model.User
	if err := s.db.Where("username = ?", username).Take(&user).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, middleware.NewAppError(http.StatusUnauthorized, "用户名或密码错误")
		}
		return nil, middleware.WrapAppError(http.StatusInternalServerError, "查询用户失败", err)
	}
	if !user.IsActive {
		return nil, middleware.NewAppError(http.StatusForbidden, "账户已被禁用")
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

// Refresh 校验并轮换 refresh token。复用检测命中时删除该用户的全部 token 并要求重新登录。
func (s *AuthService) Refresh(rawRefresh string) (*TokenPair, error) {
	claims, err := s.jwt.Verify(rawRefresh, util.JWTPurposeRefresh)
	if err != nil {
		return nil, middleware.NewAppError(http.StatusUnauthorized, "Invalid refresh token")
	}

	hashed := util.HashSHA256Hex(rawRefresh)

	var stored model.RefreshToken
	err = s.db.Where("token = ?", hashed).Take(&stored).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		// 复用检测：旧 token 不在 DB 中 → 撤销该用户全部 token
		log.Printf("[auth] refresh token reuse detected, userId=%s", claims.UserID)
		s.db.Where("user_id = ?", claims.UserID).Delete(&model.RefreshToken{})
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

	// 轮换：删旧发新，保留 familyId
	if err := s.db.Delete(&stored).Error; err != nil {
		return nil, middleware.WrapAppError(http.StatusInternalServerError, "轮换失败", err)
	}
	return s.issueTokens(&user, stored.FamilyID)
}

// Logout 撤销传入的 refresh token。即使查不到也静默成功（幂等）。
func (s *AuthService) Logout(rawRefresh string) {
	if rawRefresh == "" {
		return
	}
	s.db.Where("token = ?", util.HashSHA256Hex(rawRefresh)).Delete(&model.RefreshToken{})
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
