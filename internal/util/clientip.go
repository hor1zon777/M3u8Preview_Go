// Package util
// clientip.go 对齐 packages/server/src/utils/getClientIp.ts。
//
// 取真实客户端 IP 的信任模型：
//  1. TrustCDN=false（默认 false 的场景）：
//     client -> nginx -> Go；nginx 用 $remote_addr 覆盖 X-Forwarded-For。
//     Gin SetTrustedProxies(["127.0.0.1", "::1"]) 后 c.ClientIP() 即真实 IP。
//     此模式下必须忽略 CF-Connecting-IP / True-Client-IP（客户端可伪造）。
//  2. TrustCDN=true：
//     client -> CDN -> nginx -> Go；CDN 在 CF-Connecting-IP / True-Client-IP 写真实 IP。
//     只在站点确实在 CDN 后启用，否则攻击者可伪造这些头旁路限流。
package util

import (
	"regexp"

	"github.com/gin-gonic/gin"
)

var ipv4MappedV6 = regexp.MustCompile(`^::ffff:(\d+\.\d+\.\d+\.\d+)$`)

// GetClientIP 返回规范化的客户端 IP，取不到时返回 "unknown"。
func GetClientIP(c *gin.Context, trustCDN bool) string {
	if trustCDN {
		if cf := c.GetHeader("CF-Connecting-IP"); validCDNIP(cf) {
			return normalizeIP(cf)
		}
		if tc := c.GetHeader("True-Client-IP"); validCDNIP(tc) {
			return normalizeIP(tc)
		}
	}

	// Gin 已根据 SetTrustedProxies 处理了 X-Forwarded-For，直接用
	ip := c.ClientIP()
	if ip == "" {
		return "unknown"
	}
	return normalizeIP(ip)
}

// normalizeIP 把 IPv4-mapped IPv6（::ffff:1.2.3.4）简化为 v4 形式。
func normalizeIP(ip string) string {
	if m := ipv4MappedV6.FindStringSubmatch(ip); m != nil {
		return m[1]
	}
	return ip
}

// validCDNIP 过滤 CDN 头里明显错误的值（本地回环等）。
func validCDNIP(ip string) bool {
	if ip == "" || ip == "unknown" {
		return false
	}
	switch ip {
	case "127.0.0.1", "::1", "::ffff:127.0.0.1", "localhost", "0.0.0.0":
		return false
	}
	return true
}
