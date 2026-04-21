package util

import (
	"bytes"
	"crypto/aes"
	"crypto/cipher"
	"crypto/ecdh"
	"crypto/rand"
	"os"
	"path/filepath"
	"testing"
)

// encryptForTest 在测试里扮演客户端：生成临时密钥、ECDH、HKDF、AES-GCM 加密。
// 生产代码不用这个；仅验证 ECDHService.DecryptAuthPayload 的端到端正确性。
func encryptForTest(t *testing.T, serverPub []byte, plaintext, aad, salt []byte) (clientPubRaw, iv, ct []byte) {
	t.Helper()

	clientPriv, err := ecdh.P256().GenerateKey(rand.Reader)
	if err != nil {
		t.Fatalf("gen client priv: %v", err)
	}
	serverPubKey, err := ecdh.P256().NewPublicKey(serverPub)
	if err != nil {
		t.Fatalf("parse server pub: %v", err)
	}
	shared, err := clientPriv.ECDH(serverPubKey)
	if err != nil {
		t.Fatalf("client ecdh: %v", err)
	}

	aesKey, err := deriveAESKey(shared, salt)
	if err != nil {
		t.Fatalf("hkdf: %v", err)
	}
	block, err := aes.NewCipher(aesKey)
	if err != nil {
		t.Fatalf("aes cipher: %v", err)
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		t.Fatalf("gcm: %v", err)
	}
	iv = make([]byte, 12)
	if _, err := rand.Read(iv); err != nil {
		t.Fatalf("rand iv: %v", err)
	}
	ct = gcm.Seal(nil, iv, plaintext, aad)
	return clientPriv.PublicKey().Bytes(), iv, ct
}

func TestECDH_LoadOrGenerate_CreatesFile(t *testing.T) {
	dir := t.TempDir()
	keyPath := filepath.Join(dir, "ecdh.pem")

	svc1, err := LoadOrGenerateECDH(keyPath)
	if err != nil {
		t.Fatalf("first load: %v", err)
	}
	if _, err := os.Stat(keyPath); err != nil {
		t.Fatalf("key file not written: %v", err)
	}

	// 第二次加载应拿到相同公钥（证明是加载而非重新生成）
	svc2, err := LoadOrGenerateECDH(keyPath)
	if err != nil {
		t.Fatalf("second load: %v", err)
	}
	if !bytes.Equal(svc1.PublicKeyRaw(), svc2.PublicKeyRaw()) {
		t.Fatal("pub mismatch between first and second load")
	}
}

func TestECDH_LoadOrGenerate_RejectsMalformed(t *testing.T) {
	dir := t.TempDir()
	keyPath := filepath.Join(dir, "ecdh.pem")
	if err := os.WriteFile(keyPath, []byte("not a pem"), 0o600); err != nil {
		t.Fatalf("write bad file: %v", err)
	}
	if _, err := LoadOrGenerateECDH(keyPath); err == nil {
		t.Fatal("expected error on malformed pem")
	}
}

func TestECDH_DecryptAuthPayload_EndToEnd(t *testing.T) {
	svc, err := LoadOrGenerateECDH(filepath.Join(t.TempDir(), "k.pem"))
	if err != nil {
		t.Fatalf("load svc: %v", err)
	}

	salt := make([]byte, 32)
	_, _ = rand.Read(salt)
	plaintext := []byte(`{"password":"Abcdef12","ts":1700000000000}`)
	aad := []byte("alice")

	clientPub, iv, ct := encryptForTest(t, svc.PublicKeyRaw(), plaintext, aad, salt)

	got, err := svc.DecryptAuthPayload(clientPub, iv, ct, aad, salt)
	if err != nil {
		t.Fatalf("decrypt: %v", err)
	}
	if !bytes.Equal(got, plaintext) {
		t.Fatalf("plaintext mismatch:\n got=%s\nwant=%s", got, plaintext)
	}
}

func TestECDH_DecryptAuthPayload_FailureCases(t *testing.T) {
	svc, err := LoadOrGenerateECDH(filepath.Join(t.TempDir(), "k.pem"))
	if err != nil {
		t.Fatalf("load svc: %v", err)
	}
	salt := make([]byte, 32)
	_, _ = rand.Read(salt)
	plaintext := []byte(`{"password":"x","ts":1}`)
	aad := []byte("alice")
	clientPub, iv, ct := encryptForTest(t, svc.PublicKeyRaw(), plaintext, aad, salt)

	otherSalt := make([]byte, 32)
	_, _ = rand.Read(otherSalt)

	cases := []struct {
		name      string
		clientPub []byte
		iv        []byte
		ct        []byte
		aad       []byte
		salt      []byte
	}{
		{"wrong aad (username tampered)", clientPub, iv, ct, []byte("bob"), salt},
		{"wrong salt (different challenge)", clientPub, iv, ct, aad, otherSalt},
		{"tampered ciphertext", clientPub, iv, flipByte(ct, 0), aad, salt},
		{"bad iv length", clientPub, iv[:11], ct, aad, salt},
		{"bad pub length", clientPub[:64], iv, ct, aad, salt},
		{"empty salt", clientPub, iv, ct, aad, nil},
		{"too short ct", clientPub, iv, ct[:10], aad, salt},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := svc.DecryptAuthPayload(tc.clientPub, tc.iv, tc.ct, tc.aad, tc.salt); err == nil {
				t.Fatal("expected error, got nil")
			}
		})
	}
}

// flipByte 返回拷贝后的切片，把第 i 个字节与 0xff 异或。
func flipByte(b []byte, i int) []byte {
	out := make([]byte, len(b))
	copy(out, b)
	out[i] ^= 0xff
	return out
}

func TestECDH_HKDF_Deterministic(t *testing.T) {
	ss := bytes.Repeat([]byte{0xab}, 32)
	salt := bytes.Repeat([]byte{0x01}, 32)
	k1, err := deriveAESKey(ss, salt)
	if err != nil {
		t.Fatalf("hkdf: %v", err)
	}
	k2, err := deriveAESKey(ss, salt)
	if err != nil {
		t.Fatalf("hkdf: %v", err)
	}
	if !bytes.Equal(k1, k2) {
		t.Fatal("hkdf not deterministic for same input")
	}
	if len(k1) != 32 {
		t.Fatalf("aes key length=%d, want 32", len(k1))
	}
}
