package util

import (
	"testing"
	"time"

	"github.com/hor1zon777/m3u8-preview-go/internal/config"
)

func makeJWTCfg() *config.JWTConfig {
	return &config.JWTConfig{
		Secret:            "access-secret-at-least-32-chars-xx",
		RefreshSecret:     "refresh-secret-at-least-32-chars-x",
		AccessExpiresIn:   15 * time.Minute,
		RefreshExpiresIn:  7 * 24 * time.Hour,
		Kid:               "v1",
	}
}

func TestJWT_SignVerify_Access(t *testing.T) {
	svc := NewJWTService(makeJWTCfg())
	tok, err := svc.Sign("user-1", "USER", JWTPurposeAccess)
	if err != nil {
		t.Fatalf("sign: %v", err)
	}
	c, err := svc.Verify(tok, JWTPurposeAccess)
	if err != nil {
		t.Fatalf("verify: %v", err)
	}
	if c.UserID != "user-1" || c.Role != "USER" {
		t.Fatalf("wrong claims: %+v", c)
	}
}

func TestJWT_AccessRefreshKeysAreDistinct(t *testing.T) {
	svc := NewJWTService(makeJWTCfg())
	tok, _ := svc.Sign("u", "USER", JWTPurposeAccess)
	// 用 refresh 密钥验签名必须失败
	if _, err := svc.Verify(tok, JWTPurposeRefresh); err == nil {
		t.Fatal("access token should not verify as refresh")
	}
}

func TestJWT_KidRotation(t *testing.T) {
	cfg := makeJWTCfg()
	// 先用 kid=v0 签一个 access token（模拟上一代密钥发放的 token）
	oldCfg := *cfg
	oldCfg.Kid = "v0"
	oldCfg.Secret = "old-access-secret-at-least-32-x"
	oldSvc := NewJWTService(&oldCfg)
	oldTok, err := oldSvc.Sign("u", "ADMIN", JWTPurposeAccess)
	if err != nil {
		t.Fatal(err)
	}

	// 新服务：kid=v1 是主密钥；kid=v0 是上一代
	newCfg := *cfg
	newCfg.KidPrev = "v0"
	newCfg.SecretPrev = oldCfg.Secret
	newSvc := NewJWTService(&newCfg)

	c, err := newSvc.Verify(oldTok, JWTPurposeAccess)
	if err != nil {
		t.Fatalf("should verify old token during rotation: %v", err)
	}
	if c.Role != "ADMIN" {
		t.Fatalf("wrong role: %s", c.Role)
	}
}

func TestJWT_UnknownKidRejected(t *testing.T) {
	cfg := makeJWTCfg()
	// 用未知的 kid=v9 签一个 token
	oddCfg := *cfg
	oddCfg.Kid = "v9"
	oddSvc := NewJWTService(&oddCfg)
	oddTok, _ := oddSvc.Sign("u", "USER", JWTPurposeAccess)

	newSvc := NewJWTService(cfg) // 只配置了 v1，没配置 v9
	if _, err := newSvc.Verify(oddTok, JWTPurposeAccess); err == nil {
		t.Fatal("unknown kid should be rejected")
	}
}
