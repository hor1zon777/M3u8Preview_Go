package ha

import (
	"strings"
	"testing"
	"time"

	"github.com/hor1zon777/m3u8-preview-go/internal/config"
	"github.com/hor1zon777/m3u8-preview-go/internal/litefs"
)

// TestSafetyInequality 守住整套方案的安全边界。
//
// demoteAfter < leaseTTL < leaseTTL+claimGuard 的含义是"旧主最迟降级的时刻"
// 严格早于"新主最早夺取的时刻"。任何人调整这四个常量而破坏这个关系，
// 都会让集群在故障切换瞬间出现双主并静默丢写——所以用测试钉死。
func TestSafetyInequality(t *testing.T) {
	if demoteAfter >= leaseTTL {
		t.Fatalf("自降级死线 %v 必须严格早于租约 TTL %v，否则旧主可能在租约失效后仍在写入", demoteAfter, leaseTTL)
	}
	if claimGuard <= 0 {
		t.Fatalf("夺取保护期必须为正，当前 %v", claimGuard)
	}
	if margin := (leaseTTL + claimGuard) - demoteAfter; margin < 20*time.Second {
		t.Fatalf("旧主降级与新主夺取之间的安全余量只有 %v，过小；应至少 20s 以吸收 API 延迟抖动", margin)
	}
	// 续租必须足够频繁，才能在 demoteAfter 窗口内留出多次重试机会。
	if renewInterval*3 > demoteAfter {
		t.Fatalf("续租周期 %v 相对自降级死线 %v 过长，网络抖动一次就会触发降级", renewInterval, demoteAfter)
	}
	// 探测判定宕机所需时间必须短于夺取保护期之后的窗口，否则备节点永远来不及确认对端已死。
	if probeFailureThreshold*pollInterval > leaseTTL {
		t.Fatalf("对端宕机判定需要 %v，超过租约 TTL %v", probeFailureThreshold*pollInterval, leaseTTL)
	}
}

func TestLeaseRoundTrip(t *testing.T) {
	exp := time.Unix(1753500000, 0)
	in := Lease{Owner: "node-a", Epoch: 12, ExpiresAt: exp, State: StateDraining}

	got, err := ParseLease(in.String())
	if err != nil {
		t.Fatalf("ParseLease: %v", err)
	}
	if got.Owner != "node-a" || got.Epoch != 12 || !got.ExpiresAt.Equal(exp) || got.State != StateDraining {
		t.Fatalf("往返不一致: %+v", got)
	}
}

func TestParseLeaseEmptyIsNotAnError(t *testing.T) {
	// 记录不存在是首次部署的正常状态，不能当成错误——否则 HA 在引导阶段就卡死。
	l, err := ParseLease("")
	if err != nil {
		t.Fatalf("空记录不应报错: %v", err)
	}
	if l.Exists() {
		t.Fatalf("空记录不应视为存在: %+v", l)
	}
}

func TestParseLeaseStripsQuotes(t *testing.T) {
	// Cloudflare 读回 TXT 时可能带成对双引号，取决于记录创建方式。
	l, err := ParseLease(`"v=1;owner=node-b;epoch=3;exp=1753500000;state=active"`)
	if err != nil {
		t.Fatalf("ParseLease: %v", err)
	}
	if l.Owner != "node-b" || l.Epoch != 3 {
		t.Fatalf("带引号内容解析错误: %+v", l)
	}
}

func TestParseLeaseRejectsFutureVersion(t *testing.T) {
	if _, err := ParseLease("v=99;owner=node-a"); err == nil {
		t.Fatal("未来版本的记录应当报错，避免用旧代码误解新格式")
	}
}

func TestLeaseValidity(t *testing.T) {
	exp := time.Unix(1000, 0)
	l := Lease{Owner: "node-a", Epoch: 1, ExpiresAt: exp}

	if !l.Valid(exp.Add(-time.Second)) {
		t.Fatal("到期前应有效")
	}
	if l.Valid(exp) {
		t.Fatal("到期时刻应视为失效（边界取闭区间会让两端各自解读不同）")
	}
	if got := l.ExpiredFor(exp.Add(30 * time.Second)); got != 30*time.Second {
		t.Fatalf("ExpiredFor = %v, 期望 30s", got)
	}
	if got := l.ExpiredFor(exp.Add(-time.Second)); got != 0 {
		t.Fatalf("未过期时 ExpiredFor 应为 0，得到 %v", got)
	}

	var zero Lease
	if zero.Valid(time.Unix(0, 0)) {
		t.Fatal("不存在的租约永远无效")
	}
}

func TestHandoffRoundTrip(t *testing.T) {
	in := Handoff{Want: "node-a", TXID: "00000000000004d2"}
	got, err := ParseHandoff(in.String())
	if err != nil {
		t.Fatalf("ParseHandoff: %v", err)
	}
	if got != in {
		t.Fatalf("往返不一致: %+v", got)
	}

	// 空请求要能被序列化用于"清空"——Cloudflare 不接受空的 TXT content。
	empty, err := ParseHandoff(Handoff{}.String())
	if err != nil {
		t.Fatalf("ParseHandoff(empty): %v", err)
	}
	if empty.Exists() {
		t.Fatalf("清空后的记录不应视为存在: %+v", empty)
	}
}

func TestTXIDComparisonIsLexicographic(t *testing.T) {
	// 追平判定直接比较十六进制字符串，前提是 LiteFS 输出等宽零填充。
	// 这个测试固化该假设：一旦位点格式变化，回切判定会立刻失灵。
	older, newer := "00000000000003e8", "00000000000004d2"
	if !(older < newer) {
		t.Fatal("等宽十六进制位点的字典序必须与数值序一致")
	}
}

func TestResolutionEnvFile(t *testing.T) {
	r := Resolution{
		Role:       litefs.RolePrimary,
		PrimaryURL: "https://node-a.internal:20203",
		SelfHost:   "node-a",
		Epoch:      7,
		Reason:     "测试",
	}
	out := r.EnvFile()
	for _, want := range []string{
		"LITEFS_CANDIDATE=true",
		"LITEFS_SELF_HOST=node-a",
		"LITEFS_PRIMARY_URL=https://node-a.internal:20203",
		"HA_RESOLVED_ROLE=primary",
	} {
		if !strings.Contains(out, want) {
			t.Fatalf("env 文件缺少 %q:\n%s", want, out)
		}
	}

	r.Role = litefs.RoleReplica
	if !strings.Contains(r.EnvFile(), "LITEFS_CANDIDATE=false") {
		t.Fatal("replica 必须输出 candidate=false，否则重启后会变成第二个主")
	}
}

func TestResolveForceRoleBypassesArbitration(t *testing.T) {
	// 逃生舱：仲裁本身出问题时，运维必须能用一个环境变量把角色钉死。
	cfg := config.HAConfig{
		LiteFSDir:        "/litefs",
		NodeID:           "node-a",
		SelfAdvertiseURL: "https://node-a.internal:20203",
		PeerAdvertiseURL: "https://node-b.internal:20203",
		ForceRole:        "replica",
	}
	got := Resolve(t.Context(), cfg)
	if got.Role != litefs.RoleReplica {
		t.Fatalf("HA_FORCE_ROLE 未生效，得到 %s", got.Role)
	}
	if got.PrimaryURL != cfg.PeerAdvertiseURL {
		t.Fatalf("replica 的 advertise-url 应指向对端，得到 %s", got.PrimaryURL)
	}
}

func TestResolveWithoutLeaseConfigStartsPrimary(t *testing.T) {
	// 档位 2：启用了 LiteFS 但没配租约仲裁（手工验证复制的过渡期），
	// 行为应与单机部署一致，不能因为缺少仲裁者就拒绝提供写服务。
	cfg := config.HAConfig{LiteFSDir: "/litefs", NodeID: "solo"}
	if got := Resolve(t.Context(), cfg); got.Role != litefs.RolePrimary {
		t.Fatalf("未配置租约仲裁时应起为 primary，得到 %s", got.Role)
	}
}
