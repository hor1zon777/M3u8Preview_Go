// agent.go 是运行期的领导权状态机。
//
// 决策规则（完整推导见 docs/ha-failover.md §6）：
//
//	备节点升主 —— 必须同时满足 4 条
//	  1. 租约已过期超过 claimGuard
//	  2. 直连探测主连续 probeFailureThreshold 次失败
//	  3. 自己能成功写 Cloudflare API
//	  4. 距上次切换已过 switchCooldown
//
//	主节点维持主 —— 满足其一即可
//	  (a) 续租成功
//	  (b) 续租失败，但对端明确自报 role=replica（无接管发生的正面证据）
//	两条都不成立且超过 demoteAfter → 立即自降级
//
// 这套双信号规则让两类最容易误判的故障都退化为安全状态：
// Cloudflare API 故障时 (b) 成立，主继续服务、备写不了 CF 也不会抢；
// 主备互相断网时租约仍有效，主继续续租、备看到有效租约不抢。
package ha

import (
	"context"
	"fmt"
	"log"
	"sync"
	"time"

	"github.com/hor1zon777/m3u8-preview-go/internal/config"
	"github.com/hor1zon777/m3u8-preview-go/internal/litefs"
)

// 时间参数。
//
// 刻意不做成环境变量：前四个之间存在必须满足的安全不等式
//
//	demoteAfter < leaseTTL < leaseTTL + claimGuard
//
// 含义是"旧主最迟降级的时刻"严格早于"新主最早夺取的时刻"，中间留 30s 余量。
// 一旦被误配置就会出现双主并静默丢数据，因此固定在代码里，并在 New 中断言。
const (
	// renewInterval primary 续租周期。
	renewInterval = 15 * time.Second
	// leaseTTL 每次续租写入的有效期。
	leaseTTL = 60 * time.Second
	// demoteAfter 距上次"确认安全"超过此时长仍无法自证 → 自降级。
	demoteAfter = 45 * time.Second
	// claimGuard 租约过期后，备节点还要再等这么久才允许夺取。
	claimGuard = 15 * time.Second

	// pollInterval 状态机轮询周期（同时也是对端探测周期）。
	pollInterval = 10 * time.Second
	// probeFailureThreshold 连续多少次探测失败判定对端宕机。
	probeFailureThreshold = 3
	// switchCooldown 两次切换之间的最小间隔，防抖动。
	switchCooldown = 120 * time.Second

	// failbackStableFor 回切前，副本需要持续追平复制位点的时长。
	failbackStableFor = 60 * time.Second
	// failbackMaxWait 等待 audio 桥接流结束的上限，超时后强制交接。
	failbackMaxWait = 30 * time.Minute
	// drainTimeout 进入停写后等待对端追平的上限。超时则放弃本次交接并恢复写入，
	// 避免因对端异常而无限期停写——停写比不回切危险得多。
	drainTimeout = 90 * time.Second
	// aRecordCheckInterval A 记录的幂等校验周期（仅 primary 执行）。
	aRecordCheckInterval = 60 * time.Second
	// handoffRefreshInterval 交还请求的重写周期。
	// 它是状态标志而非事件，写一次即可；周期性重写只是为了在对端中止交接
	// （清空记录）后能自动重新发起，因此取一个不浪费 API 配额的低频值。
	handoffRefreshInterval = 60 * time.Second
)

// Agent 是运行期领导权状态机。
type Agent struct {
	cfg   config.HAConfig
	lfs   *litefs.Provider
	cf    *CFClient
	probe *Prober
	// busyStreams 返回正在进行的 audio 桥接流数量，用于回切时避免拦腰截断音频流。
	busyStreams func() int

	// switchCh 通知 main 优雅退出，由容器 restart 策略拉起后以新角色重新挂载。
	// 缓冲 1 且只发一次——退出流程一旦开始就不需要第二个信号。
	switchCh chan string

	stop     chan struct{}
	done     chan struct{}
	stopOnce sync.Once

	mu sync.Mutex
	// epoch 最近观察到的租约世代号。
	epoch int64
	// lastSafeAt 最近一次确认"本节点可以继续当主"的时刻（续租成功或拿到对端是
	// replica 的正面证据）。自降级判定以它为基准。
	lastSafeAt time.Time
	// lastRenewAt 最近一次成功续租的时刻，用于按 renewInterval 节流写请求。
	lastRenewAt time.Time
	// lastSwitchAt 最近一次角色切换时刻，用于冷却期判定。
	lastSwitchAt time.Time
	// lastARecordAt 最近一次校验 A 记录的时刻。
	lastARecordAt time.Time
	// caughtUpSince 副本持续追平主的起始时刻，零值表示尚未追平。
	caughtUpSince time.Time
	// lastHandoffWriteAt 最近一次写交还请求的时刻，用于节流。
	lastHandoffWriteAt time.Time
	// handoffSince 收到交还请求的时刻，用于 failbackMaxWait 计时。
	handoffSince time.Time
	// drainSince 进入停写的时刻，用于 drainTimeout 计时。
	drainSince time.Time
	// switching 已经发起切换，后续 tick 不再重复决策。
	switching bool
}

// New 构造 Agent。cfg.LeaseEnabled() 为 false 时返回 (nil, nil)，调用方按未启用处理。
func New(cfg config.HAConfig, lfs *litefs.Provider, busyStreams func() int) (*Agent, error) {
	if !cfg.LeaseEnabled() {
		return nil, nil
	}
	// 安全不等式断言：违反它会导致双主，宁可启动失败也不能带病运行。
	if !(demoteAfter < leaseTTL && leaseTTL < leaseTTL+claimGuard) {
		return nil, fmt.Errorf("ha: 时间参数违反安全不等式 demoteAfter(%v) < leaseTTL(%v) < leaseTTL+claimGuard(%v)",
			demoteAfter, leaseTTL, leaseTTL+claimGuard)
	}
	prober, err := NewProber(cfg.PeerBaseURL, cfg.PeerCAFile)
	if err != nil {
		return nil, err
	}
	return &Agent{
		cfg:         cfg,
		lfs:         lfs,
		cf:          NewCFClient(cfg.CFAPIToken, cfg.CFZoneID),
		probe:       prober,
		busyStreams: busyStreams,
		switchCh:    make(chan string, 1),
		stop:        make(chan struct{}),
		done:        make(chan struct{}),
	}, nil
}

// SwitchRequested 返回角色切换通知通道。
//
// 收到消息意味着"本进程应当退出，让容器以新角色重新挂载 LiteFS"。
// 切换必须走进程重启，因为 LiteFS 的 static 租约在 litefs.yml 里静态声明
// candidate，换角色只能重新 mount。
func (a *Agent) SwitchRequested() <-chan string {
	if a == nil {
		return nil
	}
	return a.switchCh
}

// Epoch 返回最近观察到的租约世代号，供 /api/health 上报。
func (a *Agent) Epoch() int64 {
	if a == nil {
		return 0
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.epoch
}

// Start 启动状态机。
func (a *Agent) Start() {
	if a == nil {
		return
	}
	log.Printf("[ha] 租约仲裁已启用: node=%s peer=%s preferred=%v lease=%s",
		a.cfg.NodeID, a.cfg.PeerID, a.cfg.Preferred, a.cfg.CFLeaseRecord)
	go a.loop()
}

// Close 停止状态机并等待退出。
func (a *Agent) Close() {
	if a == nil {
		return
	}
	a.stopOnce.Do(func() { close(a.stop) })
	<-a.done
}

func (a *Agent) loop() {
	defer close(a.done)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go func() {
		<-a.stop
		cancel()
	}()

	t := time.NewTicker(pollInterval)
	defer t.Stop()

	// 启动后立刻跑一轮：刚重启完成时应尽快续上租约，而不是先空等一个周期。
	a.tick(ctx)
	for {
		select {
		case <-a.stop:
			return
		case <-t.C:
			a.tick(ctx)
		}
	}
}

func (a *Agent) tick(ctx context.Context) {
	if a.isSwitching() {
		return
	}
	peer := a.probe.Probe(ctx)
	if peer.Reachable && peer.NodeID != "" && peer.NodeID != a.cfg.PeerID {
		log.Printf("[ha] 警告: 对端自报 nodeId=%q 与配置 HA_PEER_ID=%q 不符，请检查两台机器的配置",
			peer.NodeID, a.cfg.PeerID)
	}

	if a.lfs.IsPrimary() {
		a.tickPrimary(ctx, peer)
		return
	}
	a.tickReplica(ctx, peer)
}

// ---- primary 分支 ----

func (a *Agent) tickPrimary(ctx context.Context, peer PeerStatus) {
	// 停写超时兜底必须在读租约之前、且不依赖 Cloudflare 可达：
	// 否则"交接进行到一半时 Cloudflare 挂了"会让本节点一直卡在停写状态，
	// 整个系统不可写——这比没能回切严重得多。
	a.enforceDrainTimeout()

	lease, err := a.readLease(ctx)
	if err == nil {
		now := a.cf.Now()

		// 已被对端接管：租约是权威，立即让位，绝不与新主并存。
		if lease.Exists() && lease.Owner != a.cfg.NodeID && lease.Valid(now) {
			a.requestSwitch("租约已被 %s 持有（epoch=%d），本节点让位", lease.Owner, lease.Epoch)
			return
		}

		if a.renewDue(now) {
			if err := a.renew(ctx, lease, now); err != nil {
				log.Printf("[ha] 续租失败: %v", err)
				a.evaluateDemote(peer)
				return
			}
		}
		a.markSafe()
		a.maintainARecord(ctx, now)
		a.handleHandoffRequest(ctx, peer, now)
		return
	}

	log.Printf("[ha] 读取租约失败: %v", err)
	a.evaluateDemote(peer)
}

// evaluateDemote 在无法与 Cloudflare 通信时决定继续服务还是自降级。
//
// 这里是整套方案里最关键的一处判断：只有拿到"对端明确自报 replica"这个**正面证据**
// 才能继续当主。不能用"没有证据表明对端接管了"来代替——探测失败时，
// 对端可能正在服务，也可能已经死了，两者无法区分，此时继续写入就是赌博。
func (a *Agent) evaluateDemote(peer PeerStatus) {
	if peer.IsReplica() {
		// (b) 成立：Cloudflare 不可达但对端确认没接管 → fail-static，服务不受影响。
		a.markSafe()
		log.Printf("[ha] 无法访问 Cloudflare，但对端自报 replica，维持 primary 继续服务")
		return
	}
	a.mu.Lock()
	last := a.lastSafeAt
	a.mu.Unlock()
	if last.IsZero() {
		// 启动后还没成功过一次，给它一个完整的 demoteAfter 窗口。
		a.markSafe()
		return
	}
	if elapsed := time.Since(last); elapsed > demoteAfter {
		a.requestSwitch("已 %v 无法续租且无法确认对端未接管，自降级为只读", elapsed.Truncate(time.Second))
	}
}

// renewDue 判断是否到了该写续租的时刻（按 renewInterval 节流，避免每个 tick 都写）。
func (a *Agent) renewDue(now time.Time) bool {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.lastRenewAt.IsZero() || now.Sub(a.lastRenewAt) >= renewInterval
}

// renew 续租。
//
// 不做回读校验：_ha-lease 只有当前 owner 会写，PATCH 成功即权威。
// 真正存在并发写风险的是夺取路径（claim），那里才值得多花一次 API 调用回读。
func (a *Agent) renew(ctx context.Context, cur Lease, now time.Time) error {
	epoch := cur.Epoch
	switch {
	case !cur.Exists():
		// 记录被误删或首次引导：重新宣告。
		epoch = 1
	case cur.Owner != a.cfg.NodeID:
		// 走到这里说明该租约已过期（有效的对端租约在上游就让位了），重新宣告一个新世代。
		epoch = cur.Epoch + 1
	}

	state := StateActive
	if a.lfs.Draining() {
		state = StateDraining
	}
	next := Lease{Owner: a.cfg.NodeID, Epoch: epoch, ExpiresAt: now.Add(leaseTTL), State: state}
	if err := a.cf.PutTXT(ctx, a.cfg.CFLeaseRecord, next.String()); err != nil {
		return err
	}

	a.mu.Lock()
	a.lastRenewAt = now
	a.epoch = epoch
	a.mu.Unlock()
	return nil
}

// maintainARecord 幂等地把用户流量的 A 记录指向本节点。
//
// 每 aRecordCheckInterval 校验一次而不是每次续租都查：它同时兜住了故障切换与
// 计划内交接两条路径遗留的不一致，属于自愈机制而非主路径，低频足够。
func (a *Agent) maintainARecord(ctx context.Context, now time.Time) {
	a.mu.Lock()
	due := a.lastARecordAt.IsZero() || now.Sub(a.lastARecordAt) >= aRecordCheckInterval
	if due {
		a.lastARecordAt = now
	}
	a.mu.Unlock()
	if !due {
		return
	}
	changed, err := a.cf.EnsureA(ctx, a.cfg.CFRecordName, a.cfg.SelfPublicIP)
	if err != nil {
		log.Printf("[ha] 校验 A 记录失败: %v", err)
		return
	}
	if changed {
		log.Printf("[ha] 已把 %s 指向本节点 %s", a.cfg.CFRecordName, a.cfg.SelfPublicIP)
	}
}

// ---- 计划内交接（回切）：primary 侧 ----

// handleHandoffRequest 处理对端发来的交还领导权请求。
//
// 交接必须"先降后升"：本节点先停写并等对端追平复制位点，再把租约写给对端。
// 顺序颠倒会出现双主；不等追平则会丢掉本节点在接管期间产生的写入——
// 而"零数据丢失"正是选择真复制库方案的核心收益。
func (a *Agent) handleHandoffRequest(ctx context.Context, peer PeerStatus, now time.Time) {
	h, err := a.readHandoff(ctx)
	if err != nil {
		log.Printf("[ha] 读取交还请求失败: %v", err)
		return
	}

	if !h.Exists() || h.Want != a.cfg.PeerID {
		a.cancelHandoff("交还请求已撤销")
		return
	}
	if !peer.Reachable {
		a.cancelHandoff("对端不可达，暂缓交接")
		return
	}

	a.mu.Lock()
	if a.handoffSince.IsZero() {
		a.handoffSince = now
		log.Printf("[ha] 收到 %s 的交还请求，开始准备交接", h.Want)
	}
	waited := now.Sub(a.handoffSince)
	draining := !a.drainSince.IsZero()
	drainElapsed := time.Duration(0)
	if draining {
		drainElapsed = now.Sub(a.drainSince)
	}
	a.mu.Unlock()

	// 等 audio 桥接流跑完再交接：broker 是纯内存 io.Pipe，强切会把正在传输的
	// 音频流拦腰截断，任务只能重跑。超过上限则不再等，避免长尾任务无限期阻塞回切。
	if !draining {
		if busy := a.currentBusyStreams(); busy > 0 && waited < failbackMaxWait {
			log.Printf("[ha] 交接等待中: 仍有 %d 条 audio 流进行中（已等 %v）", busy, waited.Truncate(time.Second))
			return
		}
		a.beginDrain(now)
		return
	}

	// 停写超时已在 enforceDrainTimeout 里处理；这里只需确认还在停写窗口内。
	if drainElapsed > drainTimeout {
		_ = a.cf.PutTXT(ctx, a.cfg.CFHandoffRecord, Handoff{}.String())
		return
	}

	self := a.lfs.TXID()
	if self == "" || peer.TXID == "" || peer.TXID < self {
		log.Printf("[ha] 交接等待中: 对端复制位点 %s 尚未追平本节点 %s", peer.TXID, self)
		return
	}

	a.completeHandoff(ctx, now)
}

// enforceDrainTimeout 在停写超时后无条件恢复写入。
//
// 独立于 Cloudflare 可达性：交接进行到一半时若 Cloudflare 挂了，主流程会在读租约
// 处提前返回，永远走不到交接逻辑里的超时判断，本节点就会一直停写。
func (a *Agent) enforceDrainTimeout() {
	a.mu.Lock()
	drainSince := a.drainSince
	a.mu.Unlock()
	if drainSince.IsZero() || time.Since(drainSince) <= drainTimeout {
		return
	}
	a.cancelHandoff(fmt.Sprintf("停写超过 %v 仍未完成交接，恢复写入", drainTimeout))
}

// beginDrain 进入停写阶段：停止接受新写入，让对端有机会追平。
func (a *Agent) beginDrain(now time.Time) {
	a.mu.Lock()
	a.drainSince = now
	a.mu.Unlock()
	a.lfs.SetDraining(true)
	log.Printf("[ha] 进入停写阶段，等待对端追平复制位点")
}

// cancelHandoff 撤销交接并恢复写入。
func (a *Agent) cancelHandoff(reason string) {
	a.mu.Lock()
	active := !a.handoffSince.IsZero() || !a.drainSince.IsZero()
	a.handoffSince = time.Time{}
	a.drainSince = time.Time{}
	a.mu.Unlock()
	if !active {
		return
	}
	a.lfs.SetDraining(false)
	log.Printf("[ha] 交接中止: %s", reason)
}

// completeHandoff 执行交接：把租约写给对端、切 DNS、退出让位。
func (a *Agent) completeHandoff(ctx context.Context, now time.Time) {
	a.mu.Lock()
	epoch := a.epoch + 1
	a.mu.Unlock()

	next := Lease{Owner: a.cfg.PeerID, Epoch: epoch, ExpiresAt: now.Add(leaseTTL), State: StateActive}
	if err := a.cf.PutTXT(ctx, a.cfg.CFLeaseRecord, next.String()); err != nil {
		log.Printf("[ha] 交接失败（写租约）: %v", err)
		return
	}
	// 租约已经易主，从这一刻起本节点必须尽快退出，因此后续步骤失败也不回滚。
	if _, err := a.cf.EnsureA(ctx, a.cfg.CFRecordName, a.cfg.PeerPublicIP); err != nil {
		log.Printf("[ha] 交接后切换 A 记录失败（新主会自行校正）: %v", err)
	}
	if err := a.cf.PutTXT(ctx, a.cfg.CFHandoffRecord, Handoff{}.String()); err != nil {
		log.Printf("[ha] 清空交还请求失败: %v", err)
	}
	a.markSwitch(now)
	a.requestSwitch("已把领导权交还给 %s（epoch=%d）", a.cfg.PeerID, epoch)
}

// ---- replica 分支 ----

func (a *Agent) tickReplica(ctx context.Context, peer PeerStatus) {
	lease, err := a.readLease(ctx)
	if err != nil {
		// Cloudflare 不可达时副本什么都不做：它既无法宣告领导权，也无法切 DNS，
		// 贸然升主只会制造一个无人知晓的第二主。
		log.Printf("[ha] 读取租约失败（维持 replica）: %v", err)
		return
	}
	now := a.cf.Now()

	// 租约已指向本节点（多为交接完成）：升主。
	if lease.Owner == a.cfg.NodeID && lease.Valid(now) {
		a.markSwitch(now)
		a.requestSwitch("租约已交予本节点（epoch=%d），升为 primary", lease.Epoch)
		return
	}

	a.mu.Lock()
	a.epoch = lease.Epoch
	a.mu.Unlock()

	if a.tryClaim(ctx, lease, peer, now) {
		return
	}
	a.maybeRequestFailback(ctx, lease, peer, now)
}

// tryClaim 实现"备节点升主"的四条必要条件，全部满足才夺取租约。
func (a *Agent) tryClaim(ctx context.Context, lease Lease, peer PeerStatus, now time.Time) bool {
	// 条件 1：租约过期足够久（含保护期，确保旧主已越过自降级死线）。
	if lease.Exists() && lease.ExpiredFor(now) <= claimGuard {
		return false
	}
	// 条件 2：对端连续探测失败。租约过期但对端还活着，说明它只是连不上
	// Cloudflare（fail-static 场景），不是宕机——此时抢主就是制造双主。
	if !a.probe.Down() {
		if peer.Reachable {
			log.Printf("[ha] 租约已过期但对端仍可达（role=%s），不夺取", peer.Role)
		}
		return false
	}
	// 条件 4：冷却期（条件 3"能写 Cloudflare"由下面的写入本身来验证）。
	if !a.cooldownPassed(now) {
		return false
	}

	// 首次引导时让非优先节点稍后再抢，避免两台同时创建租约记录。
	if !lease.Exists() && !a.cfg.Preferred {
		log.Printf("[ha] 租约记录不存在且本节点非首选，等待 %s 先创建", a.cfg.PeerID)
		return false
	}

	epoch := lease.Epoch + 1
	next := Lease{Owner: a.cfg.NodeID, Epoch: epoch, ExpiresAt: now.Add(leaseTTL), State: StateActive}
	if err := a.cf.PutTXT(ctx, a.cfg.CFLeaseRecord, next.String()); err != nil {
		log.Printf("[ha] 夺取租约失败: %v", err)
		return false
	}

	// 夺取是高风险路径：回读确认没有被对端同时覆盖。
	// 续租路径不做这一步（单写入者，PATCH 成功即权威），这里值得多一次调用。
	got, err := a.readLease(ctx)
	if err != nil || got.Owner != a.cfg.NodeID {
		log.Printf("[ha] 夺取租约后回读校验失败（owner=%q err=%v），放弃本次夺取", got.Owner, err)
		return false
	}

	if _, err := a.cf.EnsureA(ctx, a.cfg.CFRecordName, a.cfg.SelfPublicIP); err != nil {
		log.Printf("[ha] 夺取后切换 A 记录失败（升主后会自行校正）: %v", err)
	}

	a.mu.Lock()
	a.epoch = epoch
	a.mu.Unlock()
	a.markSwitch(now)
	a.requestSwitch("对端宕机且租约已过期 %v，夺取领导权（epoch=%d）",
		lease.ExpiredFor(now).Truncate(time.Second), epoch)
	return true
}

// maybeRequestFailback 在本节点是首选主、且已追平复制位点时，请求交还领导权。
//
// 只写 _ha-handoff 记录、不碰 _ha-lease：租约永远只由当前 owner 写，
// 两条记录各自单写入者是本方案规避 Cloudflare 无 CAS 的核心手段。
func (a *Agent) maybeRequestFailback(ctx context.Context, lease Lease, peer PeerStatus, now time.Time) {
	if !a.cfg.Preferred || !lease.Valid(now) || lease.Owner != a.cfg.PeerID {
		return
	}
	self := a.lfs.TXID()
	caughtUp := peer.Reachable && self != "" && peer.TXID != "" && self >= peer.TXID

	a.mu.Lock()
	if !caughtUp {
		a.caughtUpSince = time.Time{}
		a.mu.Unlock()
		return
	}
	if a.caughtUpSince.IsZero() {
		a.caughtUpSince = now
		log.Printf("[ha] 已追平主节点复制位点（txid=%s），进入回切观察期", self)
	}
	stable := now.Sub(a.caughtUpSince)
	a.mu.Unlock()

	// 观察期要求"持续"追平而非某一瞬间追平：主节点在持续写入时副本会反复
	// 落后又追上，只在瞬时追平就请求交接会让交接反复开始又中止。
	if stable < failbackStableFor {
		return
	}

	// 按 handoffRefreshInterval 节流：交还请求是一个状态标志而非事件，写一次就够。
	// 但仍要周期性重写，以便在对端中止交接（清空记录）后能自动重新发起。
	a.mu.Lock()
	due := a.lastHandoffWriteAt.IsZero() || now.Sub(a.lastHandoffWriteAt) >= handoffRefreshInterval
	if due {
		a.lastHandoffWriteAt = now
	}
	a.mu.Unlock()
	if !due {
		return
	}

	if err := a.cf.PutTXT(ctx, a.cfg.CFHandoffRecord, Handoff{Want: a.cfg.NodeID, TXID: self}.String()); err != nil {
		log.Printf("[ha] 写交还请求失败: %v", err)
		return
	}
	log.Printf("[ha] 已向 %s 发出交还领导权请求（txid=%s）", a.cfg.PeerID, self)
}

// ---- 公共辅助 ----

func (a *Agent) readLease(ctx context.Context) (Lease, error) {
	raw, err := a.cf.GetTXT(ctx, a.cfg.CFLeaseRecord)
	if err != nil {
		return Lease{}, err
	}
	return ParseLease(raw)
}

func (a *Agent) readHandoff(ctx context.Context) (Handoff, error) {
	raw, err := a.cf.GetTXT(ctx, a.cfg.CFHandoffRecord)
	if err != nil {
		return Handoff{}, err
	}
	return ParseHandoff(raw)
}

func (a *Agent) currentBusyStreams() int {
	if a.busyStreams == nil {
		return 0
	}
	return a.busyStreams()
}

func (a *Agent) markSafe() {
	a.mu.Lock()
	a.lastSafeAt = time.Now()
	a.mu.Unlock()
}

func (a *Agent) markSwitch(now time.Time) {
	a.mu.Lock()
	a.lastSwitchAt = now
	a.mu.Unlock()
}

func (a *Agent) cooldownPassed(now time.Time) bool {
	a.mu.Lock()
	last := a.lastSwitchAt
	a.mu.Unlock()
	return last.IsZero() || now.Sub(last) >= switchCooldown
}

func (a *Agent) isSwitching() bool {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.switching
}

// requestSwitch 请求本进程退出，由容器 restart 策略拉起后重新决议角色。
//
// 这里刻意不写角色文件：开机决议（resolve.go）永远重新向 Cloudflare 与对端
// 求证一次，让"谁是主"只有一个判定入口。运行期状态机只负责判断"该换了"，
// 换成什么由重启后的决议说了算——两处各自判定容易出现分歧，而分歧就是双主。
func (a *Agent) requestSwitch(format string, args ...any) {
	a.mu.Lock()
	if a.switching {
		a.mu.Unlock()
		return
	}
	a.switching = true
	a.mu.Unlock()

	reason := fmt.Sprintf(format, args...)
	log.Printf("[ha] 触发角色切换: %s", reason)
	select {
	case a.switchCh <- reason:
	default:
	}
}
