// Package util
// hash.go 通用哈希工具。
package util

import (
	"crypto/sha256"
	"encoding/hex"
)

// HashSHA256Hex 返回输入的 SHA-256 十六进制字符串。
// 用于存储 refresh token 的 hash（与 TS authService 的 hashToken 兼容）。
func HashSHA256Hex(s string) string {
	h := sha256.Sum256([]byte(s))
	return hex.EncodeToString(h[:])
}
