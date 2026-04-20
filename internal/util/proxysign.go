// Package util
// proxysign.go 对齐 packages/server/src/utils/proxySign.ts。
//
// HMAC-SHA256(PROXY_SECRET, url + "\n" + expires + "\n" + userId)
// 校验用 hmac.Equal 等长时间比较；expires 必须未过期。
package util

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"strconv"
	"time"
)

// ProxySigner 封装 HMAC 签名 / 校验。
type ProxySigner struct {
	secret []byte
	ttl    time.Duration
}

// NewProxySigner 构造签名器。ttl 为签名有效期。
func NewProxySigner(secret string, ttl time.Duration) *ProxySigner {
	return &ProxySigner{secret: []byte(secret), ttl: ttl}
}

// Sign 返回要拼到代理 URL 尾部的 "&expires=T&sig=S" 片段。
// userId 必须非空（调用方保证）。
func (p *ProxySigner) Sign(rawURL, userID string) string {
	expires := time.Now().Add(p.ttl).Unix()
	sig := p.compute(rawURL, strconv.FormatInt(expires, 10), userID)
	return fmt.Sprintf("&expires=%d&sig=%s", expires, sig)
}

// Verify 校验 expires 未过期 + 签名匹配 + 绑定 userId。
// sig 与 expected 用 hmac.Equal 比较避免时序泄漏。
func (p *ProxySigner) Verify(rawURL, expires, sig, userID string) bool {
	expiresNum, err := strconv.ParseInt(expires, 10, 64)
	if err != nil {
		return false
	}
	if expiresNum < time.Now().Unix() {
		return false
	}
	expected := p.compute(rawURL, expires, userID)
	// hmac.Equal 要求等长，所以即使输入短也不会 panic
	return hmac.Equal([]byte(sig), []byte(expected))
}

func (p *ProxySigner) compute(rawURL, expires, userID string) string {
	h := hmac.New(sha256.New, p.secret)
	h.Write([]byte(rawURL))
	h.Write([]byte{'\n'})
	h.Write([]byte(expires))
	h.Write([]byte{'\n'})
	h.Write([]byte(userID))
	return hex.EncodeToString(h.Sum(nil))
}
