// Package util
// timefmt.go 统一把 time.Time 序列化为 JavaScript Date#toISOString 兼容格式（毫秒精度）。
// 前端依赖这种格式解析；Go 默认 RFC3339Nano（纳秒）会让某些测试断言失败。
package util

import "time"

// ISOTime 是一个 time.Time 的 JSON 包装，MarshalJSON 输出 "2006-01-02T15:04:05.000Z"。
type ISOTime time.Time

// ISOFormat 是 JavaScript 侧 new Date().toISOString() 的格式。
const ISOFormat = "2006-01-02T15:04:05.000Z07:00"

// MarshalJSON 输出毫秒精度的 UTC ISO8601 字符串，与 JS 对齐。
func (t ISOTime) MarshalJSON() ([]byte, error) {
	s := time.Time(t).UTC().Format(`"2006-01-02T15:04:05.000Z"`)
	return []byte(s), nil
}

// FormatISO 把 time.Time 转成 JS 等价的 ISO 字符串。用于不愿意改类型时的手动转换。
func FormatISO(t time.Time) string {
	return t.UTC().Format("2006-01-02T15:04:05.000Z")
}

// NowISO 返回当前时刻的 ISO 字符串。
func NowISO() string {
	return FormatISO(time.Now())
}
