// Package handler
// helpers.go 汇总 handler 层多个文件共用的小工具。
package handler

import (
	"strings"
	"time"
)

// timeReplacer 用于 `backup-<stamp>.zip` 这种禁含冒号的文件名场景。
var timeReplacer = strings.NewReplacer(":", "-", ".", "-")

// nowRFCSeconds 返回秒级 RFC3339 时间串（UTC）。
func nowRFCSeconds() string {
	return time.Now().UTC().Format("2006-01-02T15:04:05")
}
