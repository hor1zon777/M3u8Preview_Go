// Package util
// ecdh.go 实现登录载荷加密的服务端解密工具。
//
// 协议概览（与前端 web/client/src/utils/crypto.ts 对齐）：
//  1. 客户端 GET /auth/challenge 拿到：服务端长寿 ECDH P-256 公钥 raw（65B uncompressed）
//     + 32B 随机 challenge + ttl（60s）。challenge 单次消费，防重放。
//  2. 客户端生成临时 ECDH P-256 密钥对 → 与服务端公钥协商得到 32B 共享密钥（ss）。
//  3. HKDF-SHA256(ss, salt=challenge, info="m3u8preview-auth-v1") → 32B AES-256 key。
//  4. AES-GCM 加密明文（{password, ts}），IV=12B 随机，AAD=username。
//  5. POST {username, clientPub(raw 65B b64), iv(b64), ct(b64 含 16B tag), challenge(b64)}。
//
// 安全性质：
//   - 每次登录的 AES 密钥都不同（前向安全：即使服务端私钥未来泄露，已录制密文仍无法解）。
//   - challenge 单次消费 → 录制整份密文重放返 400。
//   - AAD=username → 把 A 的密文改用户名发给 B 会 GCM tag 校验失败。
//   - ts 窗口 60s 双保险。
//
// 不做的事：
//   - 不校验客户端公钥在曲线上的归属（Go 标准库 NewPublicKey 内部已校验）。
//   - 不防御客户端 JS 被逆向（此为 T1 协议层；T2 WASM/混淆为另一层）。
package util

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/ecdh"
	"crypto/rand"
	"crypto/sha256"
	"encoding/pem"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"

	"golang.org/x/crypto/hkdf"
)

// ecdhPEMType 自定义 PEM 块类型。故意不用 "EC PRIVATE KEY" 以避免与标准 SEC1 格式混淆：
// 这里存的是 crypto/ecdh 包的 raw 32B 私钥，不是 x509.MarshalECPrivateKey 的 DER。
const ecdhPEMType = "M3U8PREVIEW ECDH P-256 PRIVATE KEY"

// hkdfInfo 作为 HKDF 的 info 字节串域分离。
// 若未来新增别的加密用途（如 SSE ticket 加密），换 info 即可派生不同密钥。
var hkdfInfo = []byte("m3u8preview-auth-v1")

// ECDHService 是服务端长寿 ECDH 密钥对的持有者。
// 单实例跨请求复用；加载/生成只在启动时发生一次。
type ECDHService struct {
	priv    *ecdh.PrivateKey
	pubRaw  []byte // 65B uncompressed (0x04 | X | Y)，用于下发给客户端
}

// LoadOrGenerateECDH 从 path 加载 ECDH 私钥；不存在则生成新的并写入 path（权限 0600）。
// 目录不存在时自动创建（0700）。
// 文件写失败会 fatal 一样返回错误，调用方（MustLoad / main）负责决定是否中断启动。
func LoadOrGenerateECDH(path string) (*ECDHService, error) {
	if path == "" {
		return nil, errors.New("ecdh: private key path is empty")
	}

	if raw, err := os.ReadFile(path); err == nil {
		priv, perr := parseECDHPEM(raw)
		if perr != nil {
			return nil, fmt.Errorf("ecdh: parse existing key %s: %w", path, perr)
		}
		return newECDHService(priv), nil
	} else if !errors.Is(err, os.ErrNotExist) {
		return nil, fmt.Errorf("ecdh: read key %s: %w", path, err)
	}

	// 生成新密钥
	priv, err := ecdh.P256().GenerateKey(rand.Reader)
	if err != nil {
		return nil, fmt.Errorf("ecdh: generate key: %w", err)
	}

	// 确保目录存在；0700 避免 group/other 读
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return nil, fmt.Errorf("ecdh: mkdir %s: %w", filepath.Dir(path), err)
	}

	pemBytes := encodeECDHPEM(priv)
	// 写临时文件 + rename，避免部分写导致下次启动解析失败
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, pemBytes, 0o600); err != nil {
		return nil, fmt.Errorf("ecdh: write tmp key: %w", err)
	}
	if err := os.Rename(tmp, path); err != nil {
		_ = os.Remove(tmp)
		return nil, fmt.Errorf("ecdh: rename key: %w", err)
	}
	return newECDHService(priv), nil
}

// PublicKeyRaw 返回服务端公钥的 65B uncompressed 格式（前端 importKey('raw') 直接吃）。
// 返回切片是拷贝，调用方可安全持有/修改。
func (s *ECDHService) PublicKeyRaw() []byte {
	out := make([]byte, len(s.pubRaw))
	copy(out, s.pubRaw)
	return out
}

// DecryptAuthPayload 按协议解密 AES-GCM 密文并返回明文字节。
// 入参全部是原始字节（调用方负责 base64 解码）。
//
// 失败场景返回统一 error，调用方应映射为 400 并避免回显具体原因：
//   - clientPubRaw 长度非 65 / 非 P-256 点
//   - iv 长度非 12
//   - ct 长度 < 16（tag 大小）
//   - GCM 校验失败（篡改 / AAD 错 / 密钥错）
func (s *ECDHService) DecryptAuthPayload(clientPubRaw, iv, ct, aad, salt []byte) ([]byte, error) {
	if len(clientPubRaw) != 65 {
		return nil, fmt.Errorf("ecdh: client pub length=%d, want 65", len(clientPubRaw))
	}
	if len(iv) != 12 {
		return nil, fmt.Errorf("ecdh: iv length=%d, want 12", len(iv))
	}
	if len(ct) < 16 {
		return nil, fmt.Errorf("ecdh: ct length=%d, too short", len(ct))
	}
	if len(salt) == 0 {
		return nil, errors.New("ecdh: empty hkdf salt (challenge)")
	}

	clientPub, err := ecdh.P256().NewPublicKey(clientPubRaw)
	if err != nil {
		return nil, fmt.Errorf("ecdh: invalid client pub: %w", err)
	}
	shared, err := s.priv.ECDH(clientPub)
	if err != nil {
		return nil, fmt.Errorf("ecdh: shared secret: %w", err)
	}

	aesKey, err := deriveAESKey(shared, salt)
	if err != nil {
		return nil, err
	}

	block, err := aes.NewCipher(aesKey)
	if err != nil {
		return nil, fmt.Errorf("ecdh: aes cipher: %w", err)
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return nil, fmt.Errorf("ecdh: gcm: %w", err)
	}

	plaintext, err := gcm.Open(nil, iv, ct, aad)
	if err != nil {
		return nil, fmt.Errorf("ecdh: gcm open: %w", err)
	}
	return plaintext, nil
}

// newECDHService 构造 service 并预计算公钥 raw。
func newECDHService(priv *ecdh.PrivateKey) *ECDHService {
	return &ECDHService{
		priv:   priv,
		pubRaw: priv.PublicKey().Bytes(), // P-256 返回 65B uncompressed
	}
}

// encodeECDHPEM 把 32B raw 私钥包装成自定义 PEM 块。
func encodeECDHPEM(priv *ecdh.PrivateKey) []byte {
	return pem.EncodeToMemory(&pem.Block{
		Type:  ecdhPEMType,
		Bytes: priv.Bytes(), // P-256 私钥 = 32B raw
	})
}

// parseECDHPEM 解析 PEM 为 ECDH 私钥。
func parseECDHPEM(raw []byte) (*ecdh.PrivateKey, error) {
	block, _ := pem.Decode(raw)
	if block == nil {
		return nil, errors.New("ecdh: pem decode failed")
	}
	if block.Type != ecdhPEMType {
		return nil, fmt.Errorf("ecdh: unexpected pem type %q", block.Type)
	}
	return ecdh.P256().NewPrivateKey(block.Bytes)
}

// deriveAESKey 用 HKDF-SHA256 从 ECDH 共享密钥派生 32B AES-256 key。
func deriveAESKey(sharedSecret, salt []byte) ([]byte, error) {
	r := hkdf.New(sha256.New, sharedSecret, salt, hkdfInfo)
	out := make([]byte, 32)
	if _, err := io.ReadFull(r, out); err != nil {
		return nil, fmt.Errorf("ecdh: hkdf: %w", err)
	}
	return out, nil
}
