// Package ha 实现主备双节点的领导权仲裁（完整设计见 docs/ha-failover.md）。
//
// 两节点集群的根本困难是：节点无法自己分辨"对端死了"和"我们之间断网了"，
// 贸然自封主库就是脑裂。因此必须有一个外部的、强一致的、双方都能访问的裁决点。
//
// 本方案不引入第三台机器，而是把 Cloudflare DNS 的一条 TXT 记录当作
// **带 TTL 的分布式租约**：谁持有租约谁是 primary。Cloudflare 全球可达、
// 写入串行化，且切换 DNS 本来就要用它——裁决点是白捡的。
//
// 关键的正确性来自"双信号"：租约（经 Cloudflare）+ 对端直连探测。
// 单靠租约会在 Cloudflare API 故障时误判；单靠直连探测会在主备断网时误判。
// 两个信号交叉验证后，这两种情况都退化为安全状态（见 agent.go 的决策规则）。
//
// 本文件负责两条 TXT 记录的编解码。它们职责分离，各自只有一个写入者：
//
//	_ha-lease    只有当前 owner 写   "v=1;owner=node-a;epoch=12;exp=1753500000;state=active"
//	_ha-handoff  只有挑战者写        "v=1;want=node-a;txid=00000000000004d2"
//
// 两节点永不写同一条记录，从结构上消除了写竞态——这正是选用两条记录而非
// 一条大记录的原因（Cloudflare DNS API 没有 compare-and-swap）。
package ha

import (
	"fmt"
	"strconv"
	"strings"
	"time"
)

// recordVersion 是记录格式版本号，便于未来平滑升级字段布局。
const recordVersion = 1

// 租约状态取值。
const (
	// StateActive 正常持有领导权。
	StateActive = "active"
	// StateDraining 计划内交接进行中：owner 仍是本节点，但已停止接受写入，
	// 等待对端追平复制位点。见 docs/ha-failover.md §8。
	StateDraining = "draining"
)

// Lease 是 _ha-lease TXT 记录承载的租约状态。
//
// 零值表示"租约不存在"（首次部署或记录被误删），Valid 会返回 false。
type Lease struct {
	// Owner 当前持有领导权的节点 ID。
	Owner string
	// Epoch 世代号，每次易主递增。用于识别陈旧写入与排查日志。
	Epoch int64
	// ExpiresAt 租约到期时刻。owner 每 15s 续租一次，每次续到 now+60s。
	ExpiresAt time.Time
	// State 见 StateActive / StateDraining。
	State string
}

// Exists 报告记录是否存在（Owner 非空即视为存在）。
func (l Lease) Exists() bool { return l.Owner != "" }

// Valid 报告在给定时刻租约是否仍然有效。
// now 必须传 Cloudflare 服务端时钟（见 CFClient.Now），不能用本机时钟——
// 两台 VPS 的时钟漂移会直接转化为安全边界的误差。
func (l Lease) Valid(now time.Time) bool {
	return l.Exists() && now.Before(l.ExpiresAt)
}

// ExpiredFor 返回租约已过期多久；未过期时返回 0。
func (l Lease) ExpiredFor(now time.Time) time.Duration {
	if !l.Exists() || now.Before(l.ExpiresAt) {
		return 0
	}
	return now.Sub(l.ExpiresAt)
}

// String 序列化为 TXT 记录内容。
func (l Lease) String() string {
	state := l.State
	if state == "" {
		state = StateActive
	}
	return fmt.Sprintf("v=%d;owner=%s;epoch=%d;exp=%d;state=%s",
		recordVersion, l.Owner, l.Epoch, l.ExpiresAt.Unix(), state)
}

// ParseLease 解析 TXT 记录内容。
//
// 空串返回零值且不报错——"记录不存在"是首次部署的正常状态，不是异常。
// 字段缺失同样宽容处理：只有格式明显损坏（版本号非法）才报错，
// 因为一个解析失败就让整个 HA 停摆，比丢一两个字段危险得多。
func ParseLease(s string) (Lease, error) {
	kv, err := parseKV(s)
	if err != nil {
		return Lease{}, err
	}
	if len(kv) == 0 {
		return Lease{}, nil
	}
	l := Lease{
		Owner: kv["owner"],
		State: kv["state"],
	}
	if l.State == "" {
		l.State = StateActive
	}
	if v := kv["epoch"]; v != "" {
		l.Epoch, _ = strconv.ParseInt(v, 10, 64)
	}
	if v := kv["exp"]; v != "" {
		if sec, e := strconv.ParseInt(v, 10, 64); e == nil {
			l.ExpiresAt = time.Unix(sec, 0)
		}
	}
	return l, nil
}

// Handoff 是 _ha-handoff TXT 记录：高优先级节点在恢复并追平后，
// 用它向当前 owner 请求交还领导权。
type Handoff struct {
	// Want 请求接管的节点 ID。
	Want string
	// TXID 挑战者当前的 LiteFS 复制位点，供 owner 判断它是否已追平。
	TXID string
	// Force 请求 owner 跳过"等 audio 流结束"直接进入停写交接（管理员手动强制切换）。
	// 只影响等待阶段：drain / 追平 / 交接的零数据丢失流程不受它影响。
	Force bool
}

// Exists 报告是否存在有效的交还请求。
func (h Handoff) Exists() bool { return h.Want != "" }

// String 序列化为 TXT 记录内容。空请求序列化为 "v=1;want=;txid="，
// 用于清空记录（Cloudflare 的 TXT content 不允许为空串）。
// force 仅在为 true 时输出：旧版本节点解析时忽略未知键，自然退化为平滑交接。
func (h Handoff) String() string {
	s := fmt.Sprintf("v=%d;want=%s;txid=%s", recordVersion, h.Want, h.TXID)
	if h.Force {
		s += ";force=1"
	}
	return s
}

// ParseHandoff 解析交还请求记录。空串返回零值。
func ParseHandoff(s string) (Handoff, error) {
	kv, err := parseKV(s)
	if err != nil {
		return Handoff{}, err
	}
	return Handoff{Want: kv["want"], TXID: kv["txid"], Force: kv["force"] == "1"}, nil
}

// parseKV 解析 "k=v;k=v" 形式的记录内容。
//
// 会剥掉 Cloudflare 在 TXT content 外可能附带的成对双引号——
// 读回来的值有时带引号有时不带，取决于记录的创建方式，这里统一归一化。
func parseKV(s string) (map[string]string, error) {
	s = strings.TrimSpace(s)
	if len(s) >= 2 && s[0] == '"' && s[len(s)-1] == '"' {
		s = s[1 : len(s)-1]
	}
	if s == "" {
		return nil, nil
	}
	out := make(map[string]string, 5)
	for _, part := range strings.Split(s, ";") {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		k, v, ok := strings.Cut(part, "=")
		if !ok {
			continue
		}
		out[strings.TrimSpace(k)] = strings.TrimSpace(v)
	}
	if v, ok := out["v"]; ok {
		n, err := strconv.Atoi(v)
		if err != nil || n <= 0 || n > recordVersion {
			return nil, fmt.Errorf("ha: 不支持的记录版本 %q", v)
		}
	}
	return out, nil
}
