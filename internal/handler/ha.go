// ha.go 实现高可用管理端点（/api/v1/admin/ha/*）。
//
// 这组路由刻意不挂在 v1 组下（见 app.go）：切换请求必须能在 replica 上发出
// （replica 侧"升本机为主"只写 Cloudflare TXT，不写本地库），不能被
// RequirePrimary 写闸门拦成 503；同时也避开前端 api.ts 对 NODE_READ_ONLY
// 的自动重试——"切换"这种动作被静默重放是不可接受的。
package handler

import (
	"errors"
	"net/http"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/hor1zon777/m3u8-preview-go/internal/config"
	"github.com/hor1zon777/m3u8-preview-go/internal/dto"
	"github.com/hor1zon777/m3u8-preview-go/internal/ha"
	"github.com/hor1zon777/m3u8-preview-go/internal/litefs"
	"github.com/hor1zon777/m3u8-preview-go/internal/middleware"
	"github.com/hor1zon777/m3u8-preview-go/internal/service"
)

// 手动切换的机器可读错误码，前端据此显示针对性提示。
const (
	// CodeHANotEnabled 未启用租约仲裁（档位 1/2），没有可切换的对象。
	CodeHANotEnabled = "HA_NOT_ENABLED"
	// CodeHAAlreadySwitching 本进程已在退出让位。
	CodeHAAlreadySwitching = "HA_ALREADY_SWITCHING"
	// CodeHAPeerUnreachable 对端探测不可达。
	CodeHAPeerUnreachable = "HA_PEER_UNREACHABLE"
	// CodeHAPeerNotReplica primary 交出领导权时对端未自报 replica。
	CodeHAPeerNotReplica = "HA_PEER_NOT_REPLICA"
	// CodeHAPeerNotPrimary replica 请求升主时对端未自报 primary。
	CodeHAPeerNotPrimary = "HA_PEER_NOT_PRIMARY"
)

// HAHandler 处理高可用管理端点。
type HAHandler struct {
	// agent 延迟读取：ha.Agent 在 main.go 里于 app.Build 之后构造并注入 Deps，
	// 与 /api/health 读取 SubtitleSvc 的闭包是同一手法。nil = 未启用租约仲裁。
	agent func() *ha.Agent
	lfs   *litefs.Provider
	haCfg config.HAConfig
	admin *service.AdminService
	// busyStreams 档位 1/2（无 Agent）时的兜底读数；full 档由 Agent 快照提供。
	busyStreams func() int
}

// NewHAHandler 构造 HAHandler。
func NewHAHandler(agent func() *ha.Agent, lfs *litefs.Provider, haCfg config.HAConfig,
	admin *service.AdminService, busyStreams func() int) *HAHandler {
	return &HAHandler{agent: agent, lfs: lfs, haCfg: haCfg, admin: admin, busyStreams: busyStreams}
}

// Register 挂载路由。
func (h *HAHandler) Register(rg *gin.RouterGroup) {
	rg.GET("/status", h.status)
	rg.POST("/switch", h.requestSwitch)
	rg.POST("/switch/cancel", h.cancelSwitch)
}

// mode 返回部署档位。
func (h *HAHandler) mode() string {
	switch {
	case h.haCfg.LeaseEnabled():
		return "full"
	case h.haCfg.LiteFSEnabled():
		return "role-aware"
	default:
		return "standalone"
	}
}

// status 实现 GET /admin/ha/status。三档均返回 200，前端据 mode 决定渲染
// 状态面板还是配置向导。全部取自缓存，零 Cloudflare/对端外呼。
func (h *HAHandler) status(c *gin.Context) {
	resp := dto.HAStatusResponse{
		Mode:         h.mode(),
		AutoFailback: true,
	}
	if v, err := h.admin.GetSetting("haSetupDismissed"); err == nil {
		resp.SetupDismissed = v == "true"
	}
	if v, err := h.admin.GetSetting("haAutoFailback"); err == nil && v != "" {
		resp.AutoFailback = v != "false"
	}

	if snap := h.agent().StatusSnapshot(); snap != nil {
		resp.Local = dto.HANodeInfo{
			Role:        snap.Role,
			NodeID:      snap.NodeID,
			Preferred:   snap.Preferred,
			TXID:        snap.TXID,
			Draining:    snap.Draining,
			BusyStreams: snap.BusyStreams,
			Epoch:       snap.Epoch,
		}
		if !snap.PeerProbedAt.IsZero() {
			peer := &dto.HAPeerInfo{
				NodeID:              snap.PeerID,
				Reachable:           snap.Peer.Reachable,
				Role:                snap.Peer.Role,
				TXID:                snap.Peer.TXID,
				Draining:            snap.Peer.Draining,
				BusyStreams:         snap.Peer.BusyStreams,
				Version:             snap.Peer.Version,
				ConsecutiveFailures: snap.PeerFailures,
				LastProbeAt:         snap.PeerProbedAt.UTC().Format(time.RFC3339),
			}
			if snap.Peer.Err != nil {
				peer.Error = snap.Peer.Err.Error()
			}
			// 对端自报的 nodeId 比配置值更权威（还能暴露两台配置写反的情况）。
			if snap.Peer.NodeID != "" {
				peer.NodeID = snap.Peer.NodeID
			}
			resp.Peer = peer
		}
		if !snap.LeaseReadAt.IsZero() && snap.Lease.Exists() {
			resp.Lease = &dto.HALeaseInfo{
				Owner:     snap.Lease.Owner,
				Epoch:     snap.Lease.Epoch,
				ExpiresAt: snap.Lease.ExpiresAt.UTC().Format(time.RFC3339),
				State:     snap.Lease.State,
				ReadAt:    snap.LeaseReadAt.UTC().Format(time.RFC3339),
			}
		}
		resp.Switch = dto.HASwitchInfo{
			Phase:     snap.SwitchPhase,
			Manual:    snap.Manual,
			Force:     snap.Force,
			LastError: snap.LastError,
		}
		if !snap.HandoffSince.IsZero() {
			resp.Switch.Since = snap.HandoffSince.UTC().Format(time.RFC3339)
		}
		if !snap.DrainSince.IsZero() {
			resp.Switch.DrainSince = snap.DrainSince.UTC().Format(time.RFC3339)
		}
	} else {
		// 档位 1/2：没有 Agent，本机状态从 LiteFS Provider 拼。
		resp.Local = dto.HANodeInfo{
			Role:        string(h.lfs.Role()),
			NodeID:      h.haCfg.NodeID,
			Preferred:   h.haCfg.Preferred,
			TXID:        h.lfs.TXID(),
			Draining:    h.lfs.Draining(),
			BusyStreams: h.busyStreams(),
		}
		resp.Switch = dto.HASwitchInfo{Phase: ha.PhaseIdle}
	}

	c.JSON(http.StatusOK, dto.OK(resp))
}

// requestSwitch 实现 POST /admin/ha/switch。
//
// 全程异步：这里只做前置校验并置位，实际交接发生在 Agent 之后的 tick（≤10s），
// HTTP 响应远早于进程退出，不存在响应被截断的问题。
func (h *HAHandler) requestSwitch(c *gin.Context) {
	var req dto.HASwitchRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		middleware.AbortWithAppError(c, middleware.NewAppError(http.StatusBadRequest, "mode 只接受 graceful/force"))
		return
	}

	ag := h.agent()
	if ag == nil {
		middleware.AbortWithAppError(c, middleware.NewAppErrorWithCode(
			http.StatusBadRequest, CodeHANotEnabled, "未启用租约仲裁，无法切换；请先完成高可用配置"))
		return
	}

	if err := ag.RequestManualSwitch(req.Mode == "force"); err != nil {
		middleware.AbortWithAppError(c, haSwitchError(err))
		return
	}
	c.JSON(http.StatusOK, dto.OK(gin.H{"accepted": true}))
}

// cancelSwitch 实现 POST /admin/ha/switch/cancel。幂等。
func (h *HAHandler) cancelSwitch(c *gin.Context) {
	ag := h.agent()
	if ag == nil {
		middleware.AbortWithAppError(c, middleware.NewAppErrorWithCode(
			http.StatusBadRequest, CodeHANotEnabled, "未启用租约仲裁"))
		return
	}
	if err := ag.CancelManualSwitch(); err != nil {
		middleware.AbortWithAppError(c, middleware.WrapAppError(http.StatusInternalServerError, "撤销失败", err))
		return
	}
	c.JSON(http.StatusOK, dto.OK(gin.H{"cancelled": true}))
}

// haSwitchError 把 ha 包的前置校验错误映射为带机器可读 code 的 HTTP 错误。
func haSwitchError(err error) *middleware.AppError {
	switch {
	case errors.Is(err, ha.ErrNotEnabled):
		return middleware.NewAppErrorWithCode(http.StatusBadRequest, CodeHANotEnabled, "未启用租约仲裁，无法切换")
	case errors.Is(err, ha.ErrAlreadySwitching):
		return middleware.NewAppErrorWithCode(http.StatusConflict, CodeHAAlreadySwitching, "切换已在进行中")
	case errors.Is(err, ha.ErrPeerUnreachable):
		return middleware.NewAppErrorWithCode(http.StatusConflict, CodeHAPeerUnreachable,
			"对端不可达，无法交接；若对端已宕机，租约过期后备节点会自动接管，无需手动操作")
	case errors.Is(err, ha.ErrPeerNotReplica):
		return middleware.NewAppErrorWithCode(http.StatusConflict, CodeHAPeerNotReplica,
			"对端未自报 replica，可能正在切换或两台配置错乱，拒绝交接")
	case errors.Is(err, ha.ErrPeerNotPrimary):
		return middleware.NewAppErrorWithCode(http.StatusConflict, CodeHAPeerNotPrimary,
			"对端未自报 primary，手动请求无从送达；若对端已宕机，租约过期后本节点会自动接管")
	default:
		return middleware.WrapAppError(http.StatusInternalServerError, "切换请求失败", err)
	}
}
