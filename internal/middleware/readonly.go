// readonly.go 实现主备高可用下的"只读副本写拦截"。
//
// 在 LiteFS 主备架构里，同一时刻只有 primary 节点可写；replica 上的写事务会被
// LiteFS 直接拒绝，暴露给调用方的是底层 SQLite 错误，既难看又难以自动处理。
// 本中间件把这件事提前到路由层：在 replica（或计划内交接的停写阶段）收到写请求时，
// 立刻返回 503 + Retry-After + 机器可读 code，让前端能提示"服务切换中"、
// 让 worker 能识别出"该换节点了"。
package middleware

import (
	"net/http"
	"strconv"

	"github.com/gin-gonic/gin"
)

// CodeNodeReadOnly 是写拦截的机器可读错误码。
// 前端据此显示切换提示并自动重试；worker 据此重新探测哪个节点是 primary。
const CodeNodeReadOnly = "NODE_READ_ONLY"

// readOnlyRetryAfterSec 是回给客户端的 Retry-After（秒）。
//
// 取 5 秒而非按最坏情况取几十秒：故障切换期间 DNS 也会随之移动，客户端下一次
// 重试通常已经落到新的 primary 上；退避太久反而拉长用户可感知的中断。
// 真正的兜底是客户端自己的重试上限，不是这个值。
const readOnlyRetryAfterSec = 5

// WriteGate 是写闸门的最小接口，由 internal/litefs.Provider 实现。
//
// 定义在消费侧而非提供侧，是为了让 middleware 包不依赖 litefs 包——
// 单元测试可以塞一个两行的假实现，不需要真的挂载 FUSE。
type WriteGate interface {
	// AllowWrite 返回是否放行写入，以及拒绝时的中文原因。
	AllowWrite() (bool, string)
}

// RequirePrimary 拦截 replica 上的写请求。
//
// 判定只看 HTTP 方法：GET / HEAD / OPTIONS 一律放行（读请求在 replica 上是安全的，
// 数据只是可能略微滞后），其余方法在闸门关闭时返回 503。
//
// 这里刻意不去逐个路由标注"是否写"——方法语义是全站已经遵守的约定（RESTful 写操作
// 都用 POST/PUT/PATCH/DELETE），而万一有漏网的 GET 写操作，LiteFS 自身的只读保护
// 仍是最后一道防线，不会造成数据不一致。
//
// gate 为 nil 时中间件退化为空操作，方便单机部署与测试直接跳过。
func RequirePrimary(gate WriteGate) gin.HandlerFunc {
	if gate == nil {
		return func(c *gin.Context) { c.Next() }
	}
	return func(c *gin.Context) {
		switch c.Request.Method {
		case http.MethodGet, http.MethodHead, http.MethodOptions:
			c.Next()
			return
		}

		if ok, reason := gate.AllowWrite(); !ok {
			c.Header("Retry-After", strconv.Itoa(readOnlyRetryAfterSec))
			AbortWithAppError(c, NewAppErrorWithCode(
				http.StatusServiceUnavailable,
				CodeNodeReadOnly,
				reason+"，请稍后重试",
			))
			return
		}
		c.Next()
	}
}
