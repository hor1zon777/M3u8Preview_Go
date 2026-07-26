// ha.go 定义高可用管理 API（/api/v1/admin/ha/*）的请求与响应结构。
// 字段命名对齐 web/shared/src/types/index.ts 中的 HaStatus 等前端类型。
package dto

// HAStatusResponse 是 GET /admin/ha/status 的响应。
type HAStatusResponse struct {
	// Mode 部署档位："standalone"（单机）/ "role-aware"（仅角色感知）/ "full"（完整 HA）。
	Mode string `json:"mode"`
	// SetupDismissed 管理员是否已忽略首次部署引导（system_settings.haSetupDismissed）。
	SetupDismissed bool `json:"setupDismissed"`
	// AutoFailback 自动回切闸门当前值（system_settings.haAutoFailback，缺省 true）。
	AutoFailback bool `json:"autoFailback"`
	// Local 本机状态（三档均有）。
	Local HANodeInfo `json:"local"`
	// Peer 对端探测缓存；仅 full 档且已探测过时非空。
	Peer *HAPeerInfo `json:"peer,omitempty"`
	// Lease 租约缓存；仅 full 档且成功读到过时非空。
	Lease *HALeaseInfo `json:"lease,omitempty"`
	// Switch 交接进度。
	Switch HASwitchInfo `json:"switch"`
}

// HANodeInfo 本机节点状态。
type HANodeInfo struct {
	Role        string `json:"role"`
	NodeID      string `json:"nodeId,omitempty"`
	Preferred   bool   `json:"preferred"`
	TXID        string `json:"txid,omitempty"`
	Draining    bool   `json:"draining"`
	BusyStreams int    `json:"busyStreams"`
	Epoch       int64  `json:"epoch"`
}

// HAPeerInfo 对端探测缓存（零额外外呼，来自 Agent 内部 Prober）。
type HAPeerInfo struct {
	NodeID              string `json:"nodeId,omitempty"`
	Reachable           bool   `json:"reachable"`
	Role                string `json:"role,omitempty"`
	TXID                string `json:"txid,omitempty"`
	Draining            bool   `json:"draining"`
	BusyStreams         int    `json:"busyStreams"`
	ConsecutiveFailures int    `json:"consecutiveFailures"`
	// LastProbeAt RFC3339；空串表示还没探测过。
	LastProbeAt string `json:"lastProbeAt,omitempty"`
	// Error 最近一次探测失败原因。
	Error string `json:"error,omitempty"`
}

// HALeaseInfo 租约缓存。
type HALeaseInfo struct {
	Owner string `json:"owner"`
	Epoch int64  `json:"epoch"`
	// ExpiresAt RFC3339（Cloudflare 服务端时钟坐标系）。
	ExpiresAt string `json:"expiresAt"`
	State     string `json:"state"`
	// ReadAt 本机最近一次成功读到租约的时刻（RFC3339），供前端显示数据新鲜度。
	ReadAt string `json:"readAt"`
}

// HASwitchInfo 交接进度。
type HASwitchInfo struct {
	// Phase 见 internal/ha/status.go 的 Phase* 常量。
	Phase string `json:"phase"`
	// Manual 当前交接是否由管理员手动发起。
	Manual bool `json:"manual"`
	// Force 是否强制模式（跳过等 audio 流结束）。
	Force bool `json:"force"`
	// Since 收到/发起交接请求的时刻（RFC3339）。
	Since string `json:"since,omitempty"`
	// DrainSince 进入停写的时刻（RFC3339）。
	DrainSince string `json:"drainSince,omitempty"`
	// LastError 仅 phase=aborted 时非空，最近一次手动切换被中止的原因。
	LastError string `json:"lastError,omitempty"`
}

// HASwitchRequest 是 POST /admin/ha/switch 的请求体。
type HASwitchRequest struct {
	// Mode "graceful"（平滑交接：等 audio 流结束）或 "force"（跳过等流直接停写交接）。
	Mode string `json:"mode" binding:"required,oneof=graceful force"`
}
