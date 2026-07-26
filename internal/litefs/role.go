// Package litefs 把 LiteFS 的复制状态轮询成进程内可查询的角色视图。
//
// 背景：主备双节点通过 LiteFS（FUSE 层 SQLite 复制）共享同一份数据库，
// 同一时刻只有一个节点是 primary（可写），另一个是 replica（只读）。
// LiteFS 通过挂载目录下的两个特殊文件暴露状态：
//
//	<dir>/.primary      replica 上存在，内容为当前 primary 的 hostname；
//	                    primary 上不存在。
//	<dir>/<db>-pos      复制位点，格式 "<16 位十六进制 TXID>/<滚动校验和>"。
//
// 本包只做"读状态"，不做任何决策——谁该当 primary 由 internal/ha 的租约仲裁
// 决定，本包只负责如实反映 LiteFS 当前的实际角色。两者的关系是：ha 下指令、
// 容器重启、LiteFS 以新角色挂载、本包观测到变化、写拦截中间件随之放行或拦截。
//
// 未配置 LITEFS_DIR 时（本地 Windows 开发、既有单机部署）整包降级：
// Enabled() 返回 false、Role() 恒为 RolePrimary、AllowWrite() 恒放行，
// 调用方无需写任何 if 分支。
package litefs

import (
	"log"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

// Role 是本节点当前在 LiteFS 集群中的角色。
type Role string

const (
	// RolePrimary 可写节点。全集群同一时刻至多一个。
	RolePrimary Role = "primary"
	// RoleReplica 只读副本。对其执行写事务会被 LiteFS 拒绝。
	RoleReplica Role = "replica"
)

const (
	// primaryMarker 是 LiteFS 在 replica 节点写入的标记文件名。
	primaryMarker = ".primary"

	// pollInterval 角色轮询周期。
	// LiteFS 换主对本进程而言是"容器重启后以新角色启动"，属于秒级事件；
	// 1s 足够灵敏，开销是每秒一次 stat + 一次小文件读，可忽略。
	pollInterval = time.Second
)

// Provider 持有 LiteFS 角色与复制位点的最新快照。
// 零值不可用，必须经 New 构造；并发安全。
type Provider struct {
	// dir 为空表示未启用 LiteFS，所有查询走降级路径。
	dir     string
	posPath string

	mu          sync.RWMutex
	role        Role
	primaryHost string
	txid        string
	// draining 由 internal/ha 在计划内交接（回切）期间置位：
	// 此时本节点在 LiteFS 层仍是 primary（对端还没接手），但必须停止产生新写入，
	// 好让对端把复制位点追平。它与 role 是两个正交的维度，故分开存放。
	draining bool

	subs []chan struct{}

	stopOnce sync.Once
	stop     chan struct{}
	done     chan struct{}
}

// New 构造 Provider。
//
// dir 为 LiteFS 挂载目录（生产为 "/litefs"，留空则禁用）；
// dbPath 为数据库文件的完整路径（用于定位同目录下的 "<db>-pos" 位点文件）。
//
// 构造时立即同步读取一次状态，因此 New 返回后 Role() 就是可信的——
// 避免启动瞬间的写请求撞上"尚未完成首次轮询"的空窗。
func New(dir, dbPath string) *Provider {
	p := &Provider{
		dir:  dir,
		role: RolePrimary, // 降级默认值：未启用 LiteFS 时恒为 primary
		stop: make(chan struct{}),
		done: make(chan struct{}),
	}
	if dir != "" && dbPath != "" {
		p.posPath = dbPath + "-pos"
	}
	if dir != "" {
		p.refresh()
	}
	return p
}

// Enabled 报告是否启用了 LiteFS 角色感知。
func (p *Provider) Enabled() bool { return p != nil && p.dir != "" }

// Start 启动后台轮询。未启用时是空操作，可以无条件调用。
func (p *Provider) Start() {
	if !p.Enabled() {
		close(p.done)
		return
	}
	go p.loop()
}

// Close 停止后台轮询并等待其退出。可重复调用。
func (p *Provider) Close() {
	if p == nil {
		return
	}
	p.stopOnce.Do(func() { close(p.stop) })
	<-p.done
}

func (p *Provider) loop() {
	defer close(p.done)
	t := time.NewTicker(pollInterval)
	defer t.Stop()
	for {
		select {
		case <-p.stop:
			return
		case <-t.C:
			p.refresh()
		}
	}
}

// refresh 读取一次磁盘状态并在角色变化时通知订阅者。
func (p *Provider) refresh() {
	role, host := p.readRole()
	txid := p.readTXID()

	p.mu.Lock()
	changed := role != p.role
	prev := p.role
	p.role = role
	p.primaryHost = host
	p.txid = txid
	subs := append([]chan struct{}(nil), p.subs...)
	p.mu.Unlock()

	if changed {
		log.Printf("[litefs] role changed: %s -> %s (primary=%q txid=%s)", prev, role, host, txid)
		for _, ch := range subs {
			// 非阻塞：订阅者只需要知道"变了"，积压多次通知没有意义。
			select {
			case ch <- struct{}{}:
			default:
			}
		}
	}
}

// readRole 判定当前角色。
//
// 判定依据是 <dir>/.primary 是否存在，而不是去解析 LiteFS 的内部状态：
// 这是 LiteFS 对外承诺的稳定契约，也是官方推荐给应用层的用法。
//
// stat 出现非 NotExist 错误（如 FUSE 尚未挂载完成、EIO）时保守地按 replica 处理：
// 宁可短暂拒绝写入（客户端会收到 503 并重试），也不能在状态不明时放行写入。
func (p *Provider) readRole() (Role, string) {
	marker := filepath.Join(p.dir, primaryMarker)
	b, err := os.ReadFile(marker)
	if err == nil {
		return RoleReplica, strings.TrimSpace(string(b))
	}
	if os.IsNotExist(err) {
		return RolePrimary, ""
	}
	return RoleReplica, ""
}

// readTXID 读取复制位点中的事务 ID（十六进制字符串，如 "00000000000004d2"）。
//
// 位点文件内容形如 "00000000000003e8/a3b2b72f1147c9bc"，斜杠后是滚动校验和。
// 回切判定要比较两节点的 TXID 是否追平，因此这里保留原始十六进制串——
// 它是等宽零填充的，字典序即数值序，直接字符串比较即可，无需解析成整数。
func (p *Provider) readTXID() string {
	if p.posPath == "" {
		return ""
	}
	b, err := os.ReadFile(p.posPath)
	if err != nil {
		return ""
	}
	s := strings.TrimSpace(string(b))
	if i := strings.IndexByte(s, '/'); i >= 0 {
		s = s[:i]
	}
	return s
}

// Role 返回当前角色。未启用 LiteFS 时恒为 RolePrimary。
func (p *Provider) Role() Role {
	if !p.Enabled() {
		return RolePrimary
	}
	p.mu.RLock()
	defer p.mu.RUnlock()
	return p.role
}

// IsPrimary 是 Role() == RolePrimary 的简写。
func (p *Provider) IsPrimary() bool { return p.Role() == RolePrimary }

// PrimaryHost 返回当前 primary 的 hostname；本节点自己是 primary 时返回空串。
func (p *Provider) PrimaryHost() string {
	if !p.Enabled() {
		return ""
	}
	p.mu.RLock()
	defer p.mu.RUnlock()
	return p.primaryHost
}

// TXID 返回本节点的复制位点。未启用或位点文件不可读时返回空串。
func (p *Provider) TXID() string {
	if !p.Enabled() {
		return ""
	}
	p.mu.RLock()
	defer p.mu.RUnlock()
	return p.txid
}

// SetDraining 由 internal/ha 在计划内交接期间调用。
// 置位后 AllowWrite 立即开始拒绝写入，但本节点在 LiteFS 层仍是 primary，
// 以便对端把复制位点追平后再真正易主——这是回切"零数据丢失"的关键一步。
func (p *Provider) SetDraining(v bool) {
	if p == nil {
		return
	}
	p.mu.Lock()
	changed := p.draining != v
	p.draining = v
	p.mu.Unlock()
	if changed {
		log.Printf("[litefs] draining=%v", v)
	}
}

// Draining 报告是否处于计划内交接的停写阶段。
func (p *Provider) Draining() bool {
	if p == nil {
		return false
	}
	p.mu.RLock()
	defer p.mu.RUnlock()
	return p.draining
}

// AllowWrite 报告当前是否可以接受写请求，第二个返回值是拒绝原因（放行时为空）。
//
// 这是写拦截中间件与后台写循环共用的唯一闸门：把"是否可写"的判定收敛到一处，
// 避免各处自行拼 role/draining 条件而出现遗漏。
func (p *Provider) AllowWrite() (bool, string) {
	if !p.Enabled() {
		return true, ""
	}
	p.mu.RLock()
	role, draining := p.role, p.draining
	p.mu.RUnlock()

	switch {
	case draining:
		return false, "节点正在交接领导权"
	case role != RolePrimary:
		return false, "当前节点为只读副本"
	default:
		return true, ""
	}
}

// Subscribe 返回一个角色变更通知通道（容量 1，通知被合并）。
// 调用方只应把它当作"状态变了，去重新读一次"的信号，不携带任何内容。
func (p *Provider) Subscribe() <-chan struct{} {
	ch := make(chan struct{}, 1)
	if !p.Enabled() {
		return ch
	}
	p.mu.Lock()
	p.subs = append(p.subs, ch)
	p.mu.Unlock()
	return ch
}
