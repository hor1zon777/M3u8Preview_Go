// Package util 汇总无状态的通用工具函数。
// pagination.go 对齐 packages/server/src/utils/pagination.ts：
// 把前端传入的 page/limit 钳制到安全范围，防止 OOM 与大 OFFSET DoS。
package util

// MaxSafePage 限制分页深度上界。
// 不加此上限时 `?page=99999999&limit=100` 会让 SQLite 跳过 ~10^10 行，
// 触发全表扫描并长时间占用 `MaxOpenConns=1` 的唯一写连接 → 全站慢速。
// 10000 页在 limit=100 时对应 100 万行翻页深度，已覆盖真实业务场景；
// 超深需求应改用 keyset / cursor 分页。
const MaxSafePage = 10000

// SafePagination 保证 page>=1；limit 在 [1, maxLimit] 内；page 不超过 MaxSafePage；非正整数降级为默认 (1, 20)。
func SafePagination(page, limit, maxLimit int) (int, int) {
	if maxLimit <= 0 {
		maxLimit = 100
	}
	if page < 1 {
		page = 1
	}
	if page > MaxSafePage {
		page = MaxSafePage
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
