// Package handler 存放 Gin HTTP 处理器。每个领域一个文件。
package handler

import (
	"net/http"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/hor1zon777/m3u8-preview-go/internal/version"
)

// NodeStatus 是 /api/health 暴露的节点状态快照。
//
// 这个端点在主备架构里承担三个角色，是全系统唯一的角色裁决信号：
//   - Docker HEALTHCHECK / nginx 探活（沿用旧行为，只看 status 字段）
//   - worker 选节点：只连 role=primary 的那台
//   - 对端直连探测：故障判定的第二信号，以及回切时比较复制位点
//
// 因此它必须无鉴权、无副作用、且在 replica 上同样可用。
type NodeStatus struct {
	// Role 为 "primary" 或 "replica"。未启用 LiteFS 的单机部署恒为 primary。
	Role string
	// NodeID 本节点标识（如 "node-a"）；未配置 HA 时为空。
	NodeID string
	// Epoch 租约世代号，每次易主递增。未启用租约仲裁时为 0。
	Epoch int64
	// TXID LiteFS 复制位点（十六进制）。回切前用它判断副本是否已追平。
	TXID string
	// Draining 是否处于计划内交接的停写阶段。
	Draining bool
	// BusyStreams 正在进行的 audio 桥接流数量；回切要等它归零。
	BusyStreams int
}

// Health 实现 GET /api/health，供 Docker HEALTHCHECK 与 nginx 探活使用。
// 对齐 packages/server/src/app.ts 中的同名端点。
//
// status 字段恒为 "ok"：本端点只表示"进程活着且能响应"，不表示"可写"。
// 是否可写由 role/draining 表达——replica 是健康的，只是不接受写入，
// 若这里对 replica 返回非 200，Docker HEALTHCHECK 会把备节点判为不健康并反复重启，
// 反而破坏了它作为热备的价值。
func Health(status func() NodeStatus) gin.HandlerFunc {
	return func(c *gin.Context) {
		// 版本信息放在这个无鉴权端点上，是为了让登录页页脚、故障排查脚本
		// 都能在不登录的前提下确认线上跑的是哪个版本。
		body := gin.H{
			"status":    "ok",
			"timestamp": time.Now().UTC().Format(time.RFC3339Nano),
			"version":   version.Version,
			"commit":    version.Commit,
			"buildTime": version.BuildTime,
		}
		if status != nil {
			s := status()
			body["role"] = s.Role
			body["draining"] = s.Draining
			body["busyStreams"] = s.BusyStreams
			if s.NodeID != "" {
				body["nodeId"] = s.NodeID
			}
			if s.Epoch > 0 {
				body["epoch"] = s.Epoch
			}
			if s.TXID != "" {
				body["txid"] = s.TXID
			}
		}
		c.JSON(http.StatusOK, body)
	}
}
