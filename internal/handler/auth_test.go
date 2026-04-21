// Package handler
// auth_test.go 针对加密登录协议的 handler 解密路径做端到端验证：
// 模拟前端（base64url 编码 + ECDH P-256 + HKDF-SHA256 + AES-256-GCM）
// 与 AuthHandler.decryptAuth / unmarshalAndValidate 对齐。
package handler

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/ecdh"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"io"
	"maps"
	"net/http"
	"path/filepath"
	"testing"
	"time"

	"golang.org/x/crypto/hkdf"

	"github.com/hor1zon777/m3u8-preview-go/internal/dto"
	"github.com/hor1zon777/m3u8-preview-go/internal/middleware"
	"github.com/hor1zon777/m3u8-preview-go/internal/util"
)

// buildTestHandler 构造一个只含解密依赖的 AuthHandler —— svc/ticket/cfg 都留 nil，
// 因为 decryptAuth 只使用 ecdh + challenges 两个字段。
func buildTestHandler(t *testing.T) *AuthHandler {
	t.Helper()
	ecdhSvc, err := util.LoadOrGenerateECDH(filepath.Join(t.TempDir(), "ecdh.pem"))
	if err != nil {
		t.Fatalf("load ecdh: %v", err)
	}
	chal := util.NewChallengeStore()
	t.Cleanup(chal.Stop)
	return &AuthHandler{
		ecdh:       ecdhSvc,
		challenges: chal,
	}
}

// encryptAsClient 复刻前端 utils/crypto.ts 的加密步骤。
func encryptAsClient(t *testing.T, h *AuthHandler, aad string, payload map[string]any) (*dto.EncryptedAuthRequest, string) {
	t.Helper()

	// 1. 从 store issue 一个 challenge（模拟前端 GET /auth/challenge）
	challengeID, salt := h.challenges.Issue()

	// 2. 客户端一次性 ECDH 密钥对
	clientPriv, err := ecdh.P256().GenerateKey(rand.Reader)
	if err != nil {
		t.Fatalf("gen client priv: %v", err)
	}

	// 3. ECDH 协商 + HKDF 派生 AES key
	serverPubKey, err := ecdh.P256().NewPublicKey(h.ecdh.PublicKeyRaw())
	if err != nil {
		t.Fatalf("parse server pub: %v", err)
	}
	shared, err := clientPriv.ECDH(serverPubKey)
	if err != nil {
		t.Fatalf("ecdh: %v", err)
	}
	r := hkdf.New(sha256.New, shared, salt, []byte("m3u8preview-auth-v1"))
	aesKey := make([]byte, 32)
	if _, err := io.ReadFull(r, aesKey); err != nil {
		t.Fatalf("hkdf: %v", err)
	}

	// 4. AES-GCM 加密 {payload + ts}
	plaintext, _ := json.Marshal(withTS(payload, time.Now().UnixMilli()))
	block, _ := aes.NewCipher(aesKey)
	gcm, _ := cipher.NewGCM(block)
	iv := make([]byte, 12)
	_, _ = rand.Read(iv)
	ct := gcm.Seal(nil, iv, plaintext, []byte(aad))

	enc := envelope(challengeID, clientPriv.PublicKey().Bytes(), iv, ct)
	return enc, challengeID
}

// envelope 用 base64.RawURLEncoding 打包成 EncryptedAuthRequest。
func envelope(challengeID string, clientPub, iv, ct []byte) *dto.EncryptedAuthRequest {
	return &dto.EncryptedAuthRequest{
		Challenge:  challengeID,
		ClientPub:  base64.RawURLEncoding.EncodeToString(clientPub),
		IV:         base64.RawURLEncoding.EncodeToString(iv),
		Ciphertext: base64.RawURLEncoding.EncodeToString(ct),
	}
}

func withTS(m map[string]any, ts int64) map[string]any {
	out := make(map[string]any, len(m)+1)
	maps.Copy(out, m)
	out["ts"] = ts
	return out
}

func TestAuthHandler_DecryptAuth_HappyPath(t *testing.T) {
	h := buildTestHandler(t)
	enc, _ := encryptAsClient(t, h, aadLogin, map[string]any{
		"username": "alice",
		"password": "Abcdef12",
	})

	pt, appErr := h.decryptAuth(enc, aadLogin)
	if appErr != nil {
		t.Fatalf("decryptAuth failed: %v", appErr)
	}
	var got map[string]any
	if err := json.Unmarshal(pt, &got); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if got["username"] != "alice" || got["password"] != "Abcdef12" {
		t.Fatalf("plaintext mismatch: %+v", got)
	}
}

func TestAuthHandler_DecryptAuth_WrongAAD_Rejects(t *testing.T) {
	h := buildTestHandler(t)
	// 加密时用 login 的 AAD，解密时按 register 校验 —— 必须失败
	enc, _ := encryptAsClient(t, h, aadLogin, map[string]any{"username": "a", "password": "x"})
	if _, err := h.decryptAuth(enc, aadRegister); err == nil {
		t.Fatal("expected failure on AAD mismatch (endpoint-binding broken)")
	}
}

func TestAuthHandler_DecryptAuth_ChallengeIsOneShot(t *testing.T) {
	h := buildTestHandler(t)
	enc, _ := encryptAsClient(t, h, aadLogin, map[string]any{"username": "a", "password": "x"})

	if _, err := h.decryptAuth(enc, aadLogin); err != nil {
		t.Fatalf("first consume failed: %v", err)
	}
	// 用同一批 envelope 再发一次，challenge 已消费，必须被拒
	err := mustReject(h, enc, aadLogin)
	if err == nil {
		t.Fatal("expected replay rejection, got nil")
	}
	if err.Status != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d", err.Status)
	}
}

func TestAuthHandler_DecryptAuth_InvalidBase64_Rejects(t *testing.T) {
	h := buildTestHandler(t)
	enc, _ := encryptAsClient(t, h, aadLogin, map[string]any{"username": "a", "password": "x"})
	enc.IV = "!!!not-base64!!!"
	if _, err := h.decryptAuth(enc, aadLogin); err == nil {
		t.Fatal("expected base64 decode failure")
	}
}

func TestAuthHandler_DecryptAuth_StaleTimestamp_Rejects(t *testing.T) {
	h := buildTestHandler(t)
	// 用 2 分钟前的 ts 加密
	challengeID, salt := h.challenges.Issue()
	clientPriv, _ := ecdh.P256().GenerateKey(rand.Reader)
	serverPubKey, _ := ecdh.P256().NewPublicKey(h.ecdh.PublicKeyRaw())
	shared, _ := clientPriv.ECDH(serverPubKey)
	r := hkdf.New(sha256.New, shared, salt, []byte("m3u8preview-auth-v1"))
	aesKey := make([]byte, 32)
	_, _ = io.ReadFull(r, aesKey)

	oldTS := time.Now().Add(-2 * time.Minute).UnixMilli()
	plaintext, _ := json.Marshal(map[string]any{"username": "a", "password": "x", "ts": oldTS})
	block, _ := aes.NewCipher(aesKey)
	gcm, _ := cipher.NewGCM(block)
	iv := make([]byte, 12)
	_, _ = rand.Read(iv)
	ct := gcm.Seal(nil, iv, plaintext, []byte(aadLogin))
	enc := envelope(challengeID, clientPriv.PublicKey().Bytes(), iv, ct)

	appErr := mustReject(h, enc, aadLogin)
	if appErr == nil || appErr.Message != "请求时间戳超出容许窗口" {
		t.Fatalf("expected ts-window rejection, got %+v", appErr)
	}
}

func TestUnmarshalAndValidate_EnforcesPasswordComplexity(t *testing.T) {
	// 先把自定义 validator 注册到 gin binding engine
	if err := middleware.RegisterCustomValidators(); err != nil {
		t.Fatalf("register validators: %v", err)
	}
	weak, _ := json.Marshal(map[string]any{"username": "alice", "password": "alllower"})
	var req dto.RegisterRequest
	if err := unmarshalAndValidate(weak, &req); err == nil {
		t.Fatal("expected password_complex rejection, got nil")
	}

	strong, _ := json.Marshal(map[string]any{"username": "alice", "password": "Strong12"})
	if err := unmarshalAndValidate(strong, &req); err != nil {
		t.Fatalf("strong password unexpectedly rejected: %v", err)
	}
}

// mustReject 调 decryptAuth 期望返错。返回 *AppError 以便断言 status/message。
func mustReject(h *AuthHandler, enc *dto.EncryptedAuthRequest, aad string) *middleware.AppError {
	_, err := h.decryptAuth(enc, aad)
	return err
}
