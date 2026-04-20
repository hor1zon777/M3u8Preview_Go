// Package util
// uaparser.go 对齐 ua-parser-js 在 loginRecordService 的用法：
// 把 User-Agent 解析成 browser / os / device 三个可空字符串，device 为空时返回 "Desktop"。
package util

import (
	"strings"

	ua "github.com/mileusna/useragent"
)

// UAInfo 是解析结果。字段为 nil 表示未能识别（与 TS 版 null 语义一致）。
type UAInfo struct {
	Browser *string
	OS      *string
	Device  *string
}

// ParseUserAgent 解析 UA 字符串。空字符串直接返回全 nil。
// browser / os 格式 "Name Version"（去掉尾空格）；device 始终非 nil（"Desktop" 兜底）。
func ParseUserAgent(raw string) UAInfo {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return UAInfo{}
	}
	p := ua.Parse(raw)

	var info UAInfo
	if p.Name != "" {
		s := strings.TrimSpace(p.Name + " " + p.Version)
		info.Browser = &s
	}
	if p.OS != "" {
		s := strings.TrimSpace(p.OS + " " + p.OSVersion)
		info.OS = &s
	}
	// mileusna/useragent 的 DeviceType 是 "mobile"/"tablet"/""，桌面端为空字符串
	dev := p.Device
	if dev == "" {
		if p.Mobile {
			dev = "mobile"
		} else if p.Tablet {
			dev = "tablet"
		} else {
			dev = "Desktop"
		}
	}
	info.Device = &dev
	return info
}
