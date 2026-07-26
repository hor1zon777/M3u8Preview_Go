// resolve.go 实现开机角色决议——在 LiteFS 挂载之前决定本次以什么角色启动。
//
// 为什么不能直接沿用上次的角色：旧主崩溃期间领导权可能已被切走，它回归时若
// 读着本地文件说"我是主"就直接起为主，集群就有了两个主。因此每次开机都必须
// 重新向 Cloudflare（租约）与对端（直连探测）求证。
//
// 本文件是"谁是主"的唯一判定入口：运行期状态机（agent.go）只负责判断"该换了"
// 并触发进程退出，换成什么角色一律由重启后的本决议说了算。
package ha

import (
	"context"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/hor1zon777/m3u8-preview-go/internal/config"
	"github.com/hor1zon777/m3u8-preview-go/internal/litefs"
)

// bootstrapWait 首次部署时，非首选节点在创建租约前的等让时长。
// 让首选节点先建，避免两台同时创建同一条记录。
const bootstrapWait = 30 * time.Second

// Resolution 是开机角色决议结果。
type Resolution struct {
	// Role 本次启动应当采用的角色。
	Role litefs.Role
	// PrimaryURL 当前 primary 的 LiteFS API 地址，写进 litefs.yml 的 advertise-url。
	PrimaryURL string
	// SelfHost 本节点在 LiteFS 集群里的 hostname。
	SelfHost string
	// Epoch 决议时观察到的租约世代号。
	Epoch int64
	// Reason 决议依据，写进 env 文件与日志便于事后复盘。
	Reason string
}

// IsPrimary 报告决议结果是否为 primary。
func (r Resolution) IsPrimary() bool { return r.Role == litefs.RolePrimary }

// EnvFile 渲染成 shell 可 source 的 KEY=value 文本，供 docker-entrypoint.sh 使用。
func (r Resolution) EnvFile() string {
	var b strings.Builder
	b.WriteString("# 由 `server ha-resolve-role` 自动生成，每次容器启动都会重写。\n")
	fmt.Fprintf(&b, "# 决议依据: %s\n", r.Reason)
	fmt.Fprintf(&b, "LITEFS_CANDIDATE=%t\n", r.IsPrimary())
	fmt.Fprintf(&b, "LITEFS_SELF_HOST=%s\n", r.SelfHost)
	fmt.Fprintf(&b, "LITEFS_PRIMARY_URL=%s\n", r.PrimaryURL)
	fmt.Fprintf(&b, "HA_RESOLVED_ROLE=%s\n", r.Role)
	fmt.Fprintf(&b, "HA_RESOLVED_EPOCH=%d\n", r.Epoch)
	return b.String()
}

// WriteRoleEnv 原子写入角色 env 文件。
//
// 先写临时文件再 rename：entrypoint 正在 source 这个文件时若读到写了一半的内容，
// litefs 会带着空的 candidate 启动，后果是角色错乱。
func WriteRoleEnv(path string, r Resolution) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return fmt.Errorf("ha: 创建角色文件目录: %w", err)
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, []byte(r.EnvFile()), 0o644); err != nil {
		return fmt.Errorf("ha: 写角色文件: %w", err)
	}
	if err := os.Rename(tmp, path); err != nil {
		return fmt.Errorf("ha: 提交角色文件: %w", err)
	}
	return nil
}

// Resolve 执行开机角色决议。
//
// 任何情况下都返回一个可用的结果，不返回 error——决议失败时退回最保守的
// replica（只读），让节点仍能提供读服务并在后台等待恢复，而不是拒绝启动。
func Resolve(ctx context.Context, cfg config.HAConfig) Resolution {
	self := cfg.NodeID
	if self == "" {
		self, _ = os.Hostname()
	}
	asPrimary := func(epoch int64, reason string, args ...any) Resolution {
		return Resolution{
			Role: litefs.RolePrimary, PrimaryURL: cfg.SelfAdvertiseURL, SelfHost: self,
			Epoch: epoch, Reason: fmt.Sprintf(reason, args...),
		}
	}
	asReplica := func(epoch int64, reason string, args ...any) Resolution {
		return Resolution{
			Role: litefs.RoleReplica, PrimaryURL: cfg.PeerAdvertiseURL, SelfHost: self,
			Epoch: epoch, Reason: fmt.Sprintf(reason, args...),
		}
	}

	// 逃生舱：人工指定角色，跳过全部仲裁。排障、灰度、以及仲裁本身出问题时用。
	switch cfg.ForceRole {
	case string(litefs.RolePrimary):
		return asPrimary(0, "HA_FORCE_ROLE=primary 人工指定")
	case string(litefs.RoleReplica):
		return asReplica(0, "HA_FORCE_ROLE=replica 人工指定")
	}

	// 档位 2：启用了 LiteFS 但没配租约仲裁（手工验证复制的过渡期）。
	// 此时没有仲裁者，按单节点处理起为 primary；两台并行验证时应显式用 HA_FORCE_ROLE 指定。
	if !cfg.LeaseEnabled() {
		return asPrimary(0, "未启用租约仲裁，按单节点起为 primary")
	}

	cf := NewCFClient(cfg.CFAPIToken, cfg.CFZoneID)
	prober, err := NewProber(cfg.PeerBaseURL, cfg.PeerCAFile)
	if err != nil {
		log.Printf("[ha] 构造对端探测器失败: %v", err)
		return asReplica(0, "对端探测器不可用，保守起为只读副本")
	}

	lease, cfErr := readLeaseVia(ctx, cf, cfg.CFLeaseRecord)
	if cfErr != nil {
		log.Printf("[ha] 开机读取租约失败: %v", cfErr)
		return resolveWithoutCloudflare(ctx, cfg, prober, asPrimary, asReplica)
	}

	now := cf.Now()

	// 租约仍然有效：直接服从，这是最常见也最明确的一条路径。
	if lease.Valid(now) {
		if lease.Owner == cfg.NodeID {
			return asPrimary(lease.Epoch, "租约有效且属于本节点（epoch=%d）", lease.Epoch)
		}
		return asReplica(lease.Epoch, "租约由 %s 持有且仍有效（epoch=%d）", lease.Owner, lease.Epoch)
	}

	// 租约过期或不存在：先看对端是不是已经在服务。
	peer := prober.Probe(ctx)
	if peer.IsPrimary() {
		return asReplica(lease.Epoch, "租约已过期但对端正在以 primary 服务，让位")
	}

	// 首次部署：让首选节点先创建记录，非首选等一轮再重读。
	if !lease.Exists() && !cfg.Preferred {
		log.Printf("[ha] 租约记录不存在，等待 %v 让 %s 先创建", bootstrapWait, cfg.PeerID)
		select {
		case <-ctx.Done():
		case <-time.After(bootstrapWait):
		}
		if l2, err := readLeaseVia(ctx, cf, cfg.CFLeaseRecord); err == nil && l2.Valid(cf.Now()) {
			if l2.Owner == cfg.NodeID {
				return asPrimary(l2.Epoch, "等待后租约已指向本节点（epoch=%d）", l2.Epoch)
			}
			return asReplica(l2.Epoch, "等待后租约由 %s 创建（epoch=%d）", l2.Owner, l2.Epoch)
		}
	}

	// 夺取：写租约 + 回读校验 + 切 DNS。
	epoch := lease.Epoch + 1
	next := Lease{Owner: cfg.NodeID, Epoch: epoch, ExpiresAt: now.Add(leaseTTL), State: StateActive}
	if err := cf.PutTXT(ctx, cfg.CFLeaseRecord, next.String()); err != nil {
		log.Printf("[ha] 开机夺取租约失败: %v", err)
		return asReplica(lease.Epoch, "无法写入租约，保守起为只读副本")
	}
	if got, err := readLeaseVia(ctx, cf, cfg.CFLeaseRecord); err != nil || got.Owner != cfg.NodeID {
		log.Printf("[ha] 开机夺取后回读校验失败（owner=%q err=%v）", got.Owner, err)
		return asReplica(lease.Epoch, "夺取租约后回读校验未通过，保守起为只读副本")
	}
	if _, err := cf.EnsureA(ctx, cfg.CFRecordName, cfg.SelfPublicIP); err != nil {
		log.Printf("[ha] 开机切换 A 记录失败（升主后会自行校正）: %v", err)
	}
	return asPrimary(epoch, "租约过期且对端未在服务，夺取领导权（epoch=%d）", epoch)
}

// resolveWithoutCloudflare 处理"开机时 Cloudflare 不可达"的兜底路径。
//
// 此时唯一可用的证据是对端直连探测：
//   - 对端自称 primary → 让位（明确证据，安全）
//   - 对端自称 replica 且本节点是首选主 → 升主。这条路径没有仲裁者，
//     靠"只有首选节点允许走它"来保证任何时刻至多一个节点可能自升主，
//     从结构上排除两台同时升主的竞态
//   - 其余情况（对端不可达，或对端是 replica 但本节点非首选）→ 只读
//
// 最后一档是刻意的保守取舍：Cloudflare 与对端同时不可达属于双重故障，
// 宁可短暂只读，也不冒双主导致 LiteFS 分叉丢写的风险。任一方恢复后，
// 运行期状态机会在下一轮把角色纠正过来。
func resolveWithoutCloudflare(
	ctx context.Context,
	cfg config.HAConfig,
	prober *Prober,
	asPrimary, asReplica func(int64, string, ...any) Resolution,
) Resolution {
	peer := prober.Probe(ctx)
	switch {
	case peer.IsPrimary():
		return asReplica(0, "Cloudflare 不可达，但对端正在以 primary 服务，让位")
	case peer.IsReplica() && cfg.Preferred:
		return asPrimary(0, "Cloudflare 不可达，对端确认为 replica，首选节点自升主")
	default:
		return asReplica(0, "Cloudflare 与对端均无法确认领导权归属，保守起为只读副本")
	}
}

func readLeaseVia(ctx context.Context, cf *CFClient, name string) (Lease, error) {
	raw, err := cf.GetTXT(ctx, name)
	if err != nil {
		return Lease{}, err
	}
	return ParseLease(raw)
}
