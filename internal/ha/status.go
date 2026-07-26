// status.go 把 Agent 的内部状态聚合成一份快照，供管理面板的状态 API 展示。
// 全部取自缓存（探测结果、最近一次读到的租约），零额外外呼。
package ha

import "time"

// 交接进度阶段，供前端渲染步骤条。
const (
	// PhaseIdle 无交接进行中。
	PhaseIdle = "idle"
	// PhaseRequested 请求已置位，尚未进入实质阶段（等对端受理 / 等下个 tick）。
	PhaseRequested = "requested"
	// PhaseWaitingStreams 等待 audio 桥接流结束。
	PhaseWaitingStreams = "waiting-streams"
	// PhaseDraining 停写中，等待对端追平复制位点。
	PhaseDraining = "draining"
	// PhaseSwitching 已发起进程退出，容器即将以新角色重启。
	PhaseSwitching = "switching"
	// PhaseAborted 最近一次手动切换被中止（原因见 LastError）。
	PhaseAborted = "aborted"
)

// abortedVisibleFor 中止状态在快照中保留多久：太短前端轮询会错过，
// 太长会让一次旧失败一直挂在界面上吓人。
const abortedVisibleFor = 5 * time.Minute

// StatusSnapshot 是 Agent 状态的一致性快照。
type StatusSnapshot struct {
	// 本机身份与角色。
	NodeID      string
	PeerID      string
	Preferred   bool
	Role        string
	TXID        string
	Draining    bool
	Epoch       int64
	BusyStreams int

	// 对端探测缓存。PeerProbedAt 为零值表示还没探测过。
	Peer         PeerStatus
	PeerFailures int
	PeerProbedAt time.Time

	// 租约缓存。LeaseReadAt 为零值表示还没成功读到过。
	Lease       Lease
	LeaseReadAt time.Time

	// 交接进度。
	SwitchPhase  string
	Manual       bool
	Force        bool
	HandoffSince time.Time
	DrainSince   time.Time
	// LastError 仅在 SwitchPhase == PhaseAborted 时非空。
	LastError string
}

// StatusSnapshot 聚合当前状态。nil Agent（未启用租约仲裁）返回 nil。
func (a *Agent) StatusSnapshot() *StatusSnapshot {
	if a == nil {
		return nil
	}
	peer, failures, probedAt := a.probe.Snapshot()

	a.mu.Lock()
	lease, leaseAt := a.lastLease, a.lastLeaseAt
	manualHandoff, manualClaim, force := a.manualHandoff, a.manualClaim, a.manualForce
	handoffSince, drainSince := a.handoffSince, a.drainSince
	switching := a.switching
	cancelReason, cancelAt := a.lastCancelReason, a.lastCancelAt
	epoch := a.epoch
	a.mu.Unlock()

	role := "replica"
	if a.lfs.IsPrimary() {
		role = "primary"
	}

	s := &StatusSnapshot{
		NodeID:      a.cfg.NodeID,
		PeerID:      a.cfg.PeerID,
		Preferred:   a.cfg.Preferred,
		Role:        role,
		TXID:        a.lfs.TXID(),
		Draining:    a.lfs.Draining(),
		Epoch:       epoch,
		BusyStreams: a.currentBusyStreams(),

		Peer:         peer,
		PeerFailures: failures,
		PeerProbedAt: probedAt,

		Lease:       lease,
		LeaseReadAt: leaseAt,

		Manual:       manualHandoff || manualClaim,
		Force:        force,
		HandoffSince: handoffSince,
		DrainSince:   drainSince,
	}

	switch {
	case switching:
		s.SwitchPhase = PhaseSwitching
	case !drainSince.IsZero():
		s.SwitchPhase = PhaseDraining
	case !handoffSince.IsZero():
		s.SwitchPhase = PhaseWaitingStreams
	case manualClaim && peer.Reachable && peer.Draining:
		// 本机是挑战者：对端已进入停写，说明请求已被受理。
		s.SwitchPhase = PhaseDraining
	case manualHandoff || manualClaim:
		s.SwitchPhase = PhaseRequested
	case cancelReason != "" && time.Since(cancelAt) < abortedVisibleFor:
		s.SwitchPhase = PhaseAborted
		s.LastError = cancelReason
	default:
		s.SwitchPhase = PhaseIdle
	}
	return s
}

// cacheLease 缓存最近一次成功读到的租约（tickPrimary / tickReplica 调用）。
func (a *Agent) cacheLease(l Lease) {
	a.mu.Lock()
	a.lastLease = l
	a.lastLeaseAt = time.Now()
	a.mu.Unlock()
}
