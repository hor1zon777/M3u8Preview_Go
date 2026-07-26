// manual.go 实现管理员手动主/备切换。
//
// 与自动仲裁复用同一套交接协议，只是请求来源不同：
//
//	primary 侧"交出领导权"  —— 纯进程内标志（manualHandoff），不写任何 TXT。
//	  TXT handoff 记录存在的唯一理由是副本没有别的通道通知主节点；
//	  管理请求直接敲在 owner 上时无需绕行 Cloudflare，单写入者纪律零扰动。
//
//	replica 侧"升本机为主" —— 复用 _ha-handoff 挑战者协议（跳过 Preferred
//	  与追平观察期检查），记录的写入统一收口在 tick 循环，避免跨 goroutine 写竞态。
//
// force 只跳过"等 audio 流结束"阶段；停写 → 对端追平 → 交接的零数据丢失
// 流程不受影响，drainTimeout 仍然兜底。
package ha

import (
	"context"
	"errors"
	"fmt"
	"log"
	"time"
)

// 手动切换的前置校验错误，由 handler 映射为对应的 HTTP 错误码。
var (
	// ErrNotEnabled 未启用租约仲裁（档位 1/2），没有可切换的对象。
	ErrNotEnabled = errors.New("未启用租约仲裁")
	// ErrAlreadySwitching 本进程已在退出让位，不接受新请求。
	ErrAlreadySwitching = errors.New("切换已在进行中")
	// ErrPeerUnreachable 对端探测不可达。
	ErrPeerUnreachable = errors.New("对端不可达")
	// ErrPeerNotReplica primary 侧交出领导权时，对端必须明确自报 replica。
	ErrPeerNotReplica = errors.New("对端未自报 replica")
	// ErrPeerNotPrimary replica 侧请求升主时，对端必须明确自报 primary——
	// 对端已宕机时正确路径是等自动夺取（tryClaim），手动请求无从送达。
	ErrPeerNotPrimary = errors.New("对端未自报 primary")
)

// RequestManualSwitch 发起手动切换，语义随本机角色自适应：
// primary = 把领导权交给对端；replica = 请求把本机升为主。
//
// 全程异步：这里只做前置校验并置位标志，实际动作发生在之后的 tick（≤10s）。
// 已有平滑切换在进行时再次以 force=true 调用 = 升级为强制（不报错）；
// force 只升不降，要降级需先撤销再重新发起。
func (a *Agent) RequestManualSwitch(force bool) error {
	if a == nil {
		return ErrNotEnabled
	}
	if a.isSwitching() {
		return ErrAlreadySwitching
	}
	peer, _ := a.probe.Last()

	if a.lfs.IsPrimary() {
		if !peer.Reachable {
			return ErrPeerUnreachable
		}
		if !peer.IsReplica() {
			return ErrPeerNotReplica
		}
		a.mu.Lock()
		a.manualHandoff = true
		if force {
			a.manualForce = true
		}
		a.lastCancelReason = ""
		a.lastCancelAt = time.Time{}
		a.mu.Unlock()
		log.Printf("[ha] 管理员发起手动切换: 交出领导权给 %s（force=%v）", a.cfg.PeerID, force)
		return nil
	}

	if !peer.Reachable {
		return ErrPeerUnreachable
	}
	if !peer.IsPrimary() {
		return ErrPeerNotPrimary
	}
	a.mu.Lock()
	a.manualClaim = true
	a.manualClaimCleanup = false
	if force {
		a.manualForce = true
	}
	// 清掉节流时间戳，让下一个 tick 立即写出（或以 force 重写）交还请求。
	a.lastHandoffWriteAt = time.Time{}
	a.lastCancelReason = ""
	a.lastCancelAt = time.Time{}
	a.mu.Unlock()
	log.Printf("[ha] 管理员发起手动切换: 请求 %s 交还领导权（force=%v）", a.cfg.PeerID, force)
	return nil
}

// CancelManualSwitch 撤销进行中的手动切换。幂等：没有进行中的请求也返回成功。
//
// primary 侧：清标志并恢复写入（若已停写）。
// replica 侧：清标志，并交由 tick 循环写空记录清场（runManualClaimCleanup）。
func (a *Agent) CancelManualSwitch() error {
	if a == nil {
		return ErrNotEnabled
	}
	a.mu.Lock()
	hadHandoff := a.manualHandoff
	hadClaim := a.manualClaim
	a.manualHandoff = false
	a.manualClaim = false
	a.manualForce = false
	if hadClaim {
		a.manualClaimCleanup = true
	}
	a.mu.Unlock()

	if hadHandoff {
		a.pauseHandoff("管理员撤销手动切换")
	}
	if hadHandoff || hadClaim {
		log.Printf("[ha] 管理员撤销手动切换")
	}
	return nil
}

// SetAutoFailbackGate 注入自动回切闸门（通常查 system_settings.haAutoFailback）。
// 必须在 Start 之前调用一次；nil 闸门视为恒 true。
func (a *Agent) SetAutoFailbackGate(f func() bool) {
	if a == nil {
		return
	}
	a.mu.Lock()
	a.autoFailback = f
	a.mu.Unlock()
}

// SetPreferredYieldHook 注入"preferred 节点让位"钩子（通常把
// system_settings.haAutoFailback 写成 false）。必须在 Start 之前调用一次。
func (a *Agent) SetPreferredYieldHook(f func() error) {
	if a == nil {
		return
	}
	a.mu.Lock()
	a.onPreferredYield = f
	a.mu.Unlock()
}

// manualHandoffRequested 报告 primary 侧是否有待处理的手动交接。
func (a *Agent) manualHandoffRequested() bool {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.manualHandoff
}

// progressManualHandoff 推进 primary 侧的手动交接。
//
// 与 TXT 路径的差别只在对端异常时的处置：单次探测失败视为抖动，暂停进度等下个
// tick 重试；连续失败达到宕机阈值或对端不再自报 replica 时才彻底中止——
// 手动切换是一次明确的操作，悄悄挂起几小时后突然执行比报告失败更危险。
func (a *Agent) progressManualHandoff(ctx context.Context, peer PeerStatus, now time.Time) {
	if !peer.Reachable {
		if a.probe.Down() {
			a.cancelHandoff("手动切换中止: 对端连续探测失败")
		} else {
			a.pauseHandoff("对端探测失败，暂缓手动交接")
		}
		return
	}
	if !peer.IsReplica() {
		a.cancelHandoff(fmt.Sprintf("手动切换中止: 对端角色为 %q 而非 replica", peer.Role))
		return
	}
	a.mu.Lock()
	force := a.manualForce
	a.mu.Unlock()
	a.progressHandoff(ctx, peer, now, force)
}

// maybeRequestManualTakeover 推进 replica 侧的手动升主：周期性写 _ha-handoff
// 请求对端交还。返回 true 表示手动请求处于活跃状态（本 tick 不再走自动回切，
// 避免同一节点对同一条记录写出两种内容）。
func (a *Agent) maybeRequestManualTakeover(ctx context.Context, lease Lease, now time.Time) bool {
	a.mu.Lock()
	claim := a.manualClaim
	force := a.manualForce
	a.mu.Unlock()
	if !claim {
		return false
	}

	// 只有对端仍是合法主时"请求交还"才有意义。租约过期由 tryClaim（前置执行）
	// 接管，租约指向本节点由 tickReplica 的升主分支收尾——这里只等待。
	if !lease.Valid(now) || lease.Owner != a.cfg.PeerID {
		return true
	}

	a.mu.Lock()
	due := a.lastHandoffWriteAt.IsZero() || now.Sub(a.lastHandoffWriteAt) >= handoffRefreshInterval
	if due {
		a.lastHandoffWriteAt = now
	}
	a.mu.Unlock()
	if !due {
		return true
	}

	self := a.lfs.TXID()
	req := Handoff{Want: a.cfg.NodeID, TXID: self, Force: force}
	if err := a.cf.PutTXT(ctx, a.cfg.CFHandoffRecord, req.String()); err != nil {
		log.Printf("[ha] 写手动交还请求失败: %v", err)
		return true
	}
	log.Printf("[ha] 已向 %s 发出手动交还领导权请求（txid=%s force=%v）", a.cfg.PeerID, self, force)
	return true
}

// runManualClaimCleanup 在撤销手动升主后写空记录清场。
// 放在 tick 循环里执行是刻意的：本节点对 _ha-handoff 的所有写入都收口在
// 同一个 goroutine，从结构上排除写竞态。返回 true 表示本 tick 已被占用。
func (a *Agent) runManualClaimCleanup(ctx context.Context) bool {
	a.mu.Lock()
	cleanup := a.manualClaimCleanup
	a.mu.Unlock()
	if !cleanup {
		return false
	}
	if err := a.cf.PutTXT(ctx, a.cfg.CFHandoffRecord, Handoff{}.String()); err != nil {
		log.Printf("[ha] 撤销手动升主清场失败（下个周期重试）: %v", err)
		return true
	}
	a.mu.Lock()
	a.manualClaimCleanup = false
	a.mu.Unlock()
	log.Printf("[ha] 已清空手动升主请求记录")
	return true
}
