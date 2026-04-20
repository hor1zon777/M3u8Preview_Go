// Package util
// jwt.go 对齐 packages/server/src/utils/verifyJwt.ts 与 services/authService.ts 的签/验。
//
// 关键点：
//   - HS256 + HMAC 密钥；header 必带 kid；purpose ∈ {access, refresh} 各用独立密钥
//   - 支持 kid 路由：token.kid == cfg.Kid → 主密钥；== cfg.KidPrev → 上一代密钥；其他拒绝
//   - Claims: { userId, role, iat, exp }
package util

import (
	"errors"
	"fmt"
	"time"

	"github.com/golang-jwt/jwt/v5"

	"github.com/hor1zon777/m3u8-preview-go/internal/config"
)

// JWTPurpose 区分 access / refresh 两套密钥。
type JWTPurpose string

const (
	JWTPurposeAccess  JWTPurpose = "access"
	JWTPurposeRefresh JWTPurpose = "refresh"
)

// TokenClaims 是全部业务 token 的 payload。与 TS TokenPayload 对齐。
type TokenClaims struct {
	UserID string `json:"userId"`
	Role   string `json:"role"`
	jwt.RegisteredClaims
}

// JWTService 封装签发与验证能力。
type JWTService struct {
	cfg *config.JWTConfig
}

// NewJWTService 绑定配置。通常在 main/app 组装时注入。
func NewJWTService(cfg *config.JWTConfig) *JWTService {
	return &JWTService{cfg: cfg}
}

// Sign 生成 JWT。purpose 决定用 access 还是 refresh 密钥。
func (s *JWTService) Sign(userID, role string, purpose JWTPurpose) (string, error) {
	secret, _, _, err := s.selectKey(s.cfg.Kid, purpose)
	if err != nil {
		return "", err
	}
	ttl := s.ttl(purpose)

	now := time.Now()
	claims := TokenClaims{
		UserID: userID,
		Role:   role,
		RegisteredClaims: jwt.RegisteredClaims{
			IssuedAt:  jwt.NewNumericDate(now),
			ExpiresAt: jwt.NewNumericDate(now.Add(ttl)),
		},
	}
	tok := jwt.NewWithClaims(jwt.SigningMethodHS256, claims)
	tok.Header["kid"] = s.cfg.Kid
	return tok.SignedString([]byte(secret))
}

// Verify 校验 token 有效性，返回 claims。
// 根据 token header.kid 路由到对应密钥；算法强制 HS256；未知 kid 直接拒绝。
func (s *JWTService) Verify(tokenStr string, purpose JWTPurpose) (*TokenClaims, error) {
	parser := jwt.NewParser(jwt.WithValidMethods([]string{jwt.SigningMethodHS256.Alg()}))
	claims := &TokenClaims{}
	_, err := parser.ParseWithClaims(tokenStr, claims, func(t *jwt.Token) (interface{}, error) {
		kid, _ := t.Header["kid"].(string)
		secret, _, _, err := s.selectKey(kid, purpose)
		if err != nil {
			return nil, err
		}
		return []byte(secret), nil
	})
	if err != nil {
		return nil, err
	}
	return claims, nil
}

// selectKey 根据 kid + purpose 选择 HMAC 密钥。
// kid 为空 或 kid == cfg.Kid → 主密钥；kid == cfg.KidPrev 且配置了上一代密钥 → 上一代；其他拒绝。
func (s *JWTService) selectKey(kid string, purpose JWTPurpose) (secret, chosenKid string, isPrev bool, err error) {
	current := s.cfg.Secret
	prev := s.cfg.SecretPrev
	if purpose == JWTPurposeRefresh {
		current = s.cfg.RefreshSecret
		prev = s.cfg.RefreshSecretPrev
	}

	if kid == "" || kid == s.cfg.Kid {
		if current == "" {
			return "", "", false, fmt.Errorf("jwt: current secret for %s is empty", purpose)
		}
		return current, s.cfg.Kid, false, nil
	}
	if s.cfg.KidPrev != "" && kid == s.cfg.KidPrev && prev != "" {
		return prev, s.cfg.KidPrev, true, nil
	}
	return "", "", false, errors.New("jwt: unknown kid")
}

func (s *JWTService) ttl(purpose JWTPurpose) time.Duration {
	if purpose == JWTPurposeRefresh {
		return s.cfg.RefreshExpiresIn
	}
	return s.cfg.AccessExpiresIn
}
