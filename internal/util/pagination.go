// Package util 汇总无状态的通用工具函数。
// pagination.go 对齐 packages/server/src/utils/pagination.ts：
// 把前端传入的 page/limit 钳制到安全范围，防止 OOM。
package util

// SafePagination 保证 page>=1；limit 在 [1, maxLimit] 内；非正整数降级为默认 (1, 20)。
func SafePagination(page, limit, maxLimit int) (int, int) {
	if maxLimit <= 0 {
		maxLimit = 100
	}
	if page < 1 {
		page = 1
	}
	if limit < 1 {
		limit = 20
	}
	if limit > maxLimit {
		limit = maxLimit
	}
	return page, limit
}

// Offset 计算分页 SQL OFFSET。
func Offset(page, limit int) int {
	return (page - 1) * limit
}
