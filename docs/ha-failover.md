# 主备双节点高可用（HA Failover）设计方案

> 状态：设计定稿，待实施
> 适用范围：本仓库（server）+ audio worker 仓库 + GPU ASR worker 仓库 + Cloudflare 配置

---

## 1. 目标与约束

### 1.1 功能目标

1. 主/备节点分别部署在两台不同 VPS，用户连接地址默认落到**主节点**。
2. 主节点挂掉后**自动切换**到备用节点。
3. 备用节点也挂掉时**不切换**，等待——**哪个节点先上线就切到哪个**。
4. 已切到备节点后，主节点恢复则**自动切回主节点**。

### 1.2 硬约束

| 约束 | 来源 |
|---|---|
| **只有主/备两台 VPS**，不接受第三台仲裁机 | 用户明确要求 |
| **不引入 Docker 之外的改动**（不装 systemd 单元、不装宿主软件） | 用户明确要求 |
| **Cloudflare 只负责切换 DNS**，不跑 Worker 做仲裁 | 用户明确要求 |
| 主/备之间可以借 Cloudflare 通信 | 用户允许 |
| 数据层要做**真复制库**（方案 C），不接受定时备份/单向快照 | 用户明确要求 |

### 1.3 现状事实（决定选型的关键）

- 全部 DB 访问走 **GORM + glebarez/sqlite**（`internal/db/conn.go:57`），存在事务与 SAVEPOINT（`internal/service/import.go:263`）。
- 服务端是**单机状态**：SQLite 本地库 + 内存 broker（`io.Pipe`）+ 内存 long-poll 通道。
- 浏览器前端**不持有服务端地址**，全部走相对路径 `/api/v1`（`web/client/src/services/api.ts:5`），落到哪台机器由 DNS 决定。
- worker 是**纯 pull 模型 HTTP 客户端**（claim long-poll + heartbeat + audio-stream），连接地址在 worker 侧仓库配置，目前是单一 URL。
- **VTT 是磁盘文件而非 DB 记录**（`vtt_path` 相对路径，见 `internal/model/subtitle.go:7`）。
- 部署形态：单容器 alpine + `docker-entrypoint.sh`（root → su-exec appuser 降权）+ 独立 nginx 容器，均为 `network_mode: host`。

---

## 2. 选型结论

| 候选 | 结论 | 理由 |
|---|---|---|
| **LiteFS** | ✅ **采用** | FUSE 层拦截 SQLite 事务做复制，对应用**完全透明**：GORM、glebarez 驱动、事务、SAVEPOINT 全部原样工作，业务代码近乎零改动 |
| rqlite | ❌ 否决 | HTTP 语句接口、无 GORM dialector、不支持交互式事务 → 等于重写整个 service 层；且 Raft 同步共识让每次写入都付一次跨区 RTT |
| Consul 租约 | ❌ 否决 | 两节点无法组成安全 quorum（2 节点 quorum=2，挂一台即失去 leader 选举），必须第三台机器——用户不接受 |
| marmot（CRDT 双活） | ❌ 否决 | 复制通道（嵌入式 NATS JetStream）在两节点下同样有 quorum 问题，且成熟度不足 |
| Litestream | ❌ 否决 | 单节点灾备工具，不支持实时复制到活节点，不支持自动 failover |

### 2.1 LiteFS 关键特性（官方文档确认）

- 以 FUSE passthrough 文件系统拦截 SQLite 页写入，转成 LTX 事务文件并流式复制。
- **官方部署模型就是「跑在容器内，与应用同容器」**，且推荐 LiteFS 以 root 运行、应用用 `su` 降权——与本仓库现有的 `root entrypoint → su-exec appuser` 模式天然吻合。
- 异步复制：写入不等对端确认 → 写延迟不受主备跨区 RTT 影响；代价是主猝死时最后不到一秒的未复制写入可能丢失。
- **分叉自愈**：滚动校验和会检测到旧主带着未复制写入回归造成的分叉，落后方自动从新 primary 全量快照重建，无需人工合并。
- FUSE 写入吞吐上限约 **100 事务/秒**。
- 挂载目录**不支持子目录**，DB 必须放在挂载根。
- 存量库必须用 `litefs import` 导入，**不能用 `cp`**（官方明确警告会损坏）。
- 节点间 API 端口默认 20202，**自身无任何认证**，官方标注 "MUST NOT be publicly accessible"。

---

## 3. 核心思路：把 Cloudflare DNS 记录当作分布式租约

两节点无法自己分辨「对端死了」和「我们之间断网了」——这是 2 节点集群的根本困难，贸然自封主库即脑裂。因此必须有一个**外部、强一致、双方都能访问**的裁决点。

**Cloudflare DNS API 本身就是这个裁决点**：全球可达、写入串行化、你本来就要用它切 DNS。把一条 TXT 记录当作**带 TTL 的租约**，谁持有租约谁是 primary——问题闭合，既不需要 Worker，也不需要第三台机器。

判定逻辑跑在两个节点自己的 Go 进程里；Cloudflare 只是被动的存储与 DNS 执行器。

---

## 4. 架构总览

```
                    ┌────────────────────────────────────────┐
                    │  Cloudflare（无 Worker，仅 DNS + API）    │
                    │  · _ha-lease   TXT  ← 租约（真实状态）     │
                    │  · _ha-handoff TXT  ← 回切请求            │
                    │  · <domain>    A    ← 用户流量指向         │
                    └────────▲───────────────────▲─────────────┘
                    CF API 读写                CF API 读写
        ┌────────────────────┴──┐            ┌──┴────────────────────┐
        │ 主 VPS                 │            │ 备 VPS                 │
        │ ┌───────────────────┐ │  LTX 复制   │ ┌───────────────────┐ │
        │ │ litefs mount(root) │◀┼───────────┼▶│ litefs mount(root) │ │
        │ │  └ su-exec appuser │ │  (TLS+ACL) │ │  └ su-exec appuser │ │
        │ │      └ server      │ │            │ │      └ server      │ │
        │ │         └ HA 协程   │◀┼───直连探测──┼▶│         └ HA 协程   │ │
        │ └───────────────────┘ │            │ └───────────────────┘ │
        │ nginx 容器             │            │ nginx 容器             │
        └───────────────────────┘            └───────────────────────┘
              ▲                                        ▲
              └──── 用户（CF DNS 指向 primary）           │
              └──── worker（直连两地址，跟随 role=primary）─┘
```

**流量原则**：DNS 只指向 primary，**replica 不对用户服务**。原因是 SSE、audio broker（内存 `io.Pipe`）、long-poll 通道都是节点内存态，读写分流会导致状态分裂。目标是 HA 而非读扩容。

---

## 5. 租约设计

### 5.1 两条 TXT 记录（职责分离，杜绝并发覆盖）

```
_ha-lease.<domain>    TXT  "v=1;owner=node-a;epoch=12;exp=1753500000;state=active"
_ha-handoff.<domain>  TXT  "v=1;want=node-a;txid=00000000000004d2"
```

| 记录 | 唯一写入者 | 用途 |
|---|---|---|
| `_ha-lease` | 当前 owner | 每 15s 续租，`exp = now + 60` |
| `_ha-handoff` | 挑战者（高优先级节点） | 回切请求 |

两节点**永不写同一条记录**，从结构上消除写竞态。

字段说明：

- `owner`：节点 ID（`node-a` / `node-b`）
- `epoch`：单调递增，每次易主 +1，用于识别陈旧状态
- `exp`：租约到期 Unix 秒
- `state`：`active` | `draining`
- `want` / `txid`：挑战者 ID 与其当前复制位点

### 5.2 时钟：用 CF API 响应的 `Date` 头

每次调用 CF API 都会返回 `Date` 响应头。**以它作为两节点的共同时钟**，彻底绕开 VPS 之间时钟漂移问题，不依赖 NTP 精度。

### 5.3 时间参数与安全边界

| 参数 | 值 | 说明 |
|---|---|---|
| 续租周期 | 15s | primary 写 `_ha-lease` |
| 租约 TTL | 60s | `exp = now + 60` |
| **主自降级死线** | **45s** | 距上次成功续租超 45s 且无法自证安全 → 立即降级 |
| **备夺取保护期** | **exp + 15s** | 即 `last_renew + 75s` 之后才允许接管 |
| 对端直连探测 | 10s 一次，连续 3 次失败判定 down | |
| 租约轮询 | 10s | 两节点均轮询 |
| 切换冷却 | 120s | 防抖动 |

**安全边界**：主最迟在 `renew+45s` 降级，备最早在 `renew+75s` 接管，中间保留 **30s 安全余量**。

### 5.4 CF API 用量

限速为 **1200 次 / 5 分钟**，按 **user** 计且所有 token 共享。本方案用量：

| 操作 | 频率 | 5 分钟用量 |
|---|---|---|
| primary 续租（写） | 15s | 20 |
| 两节点轮询租约（读） | 各 10s | 60 |
| A 记录校验（读，仅 primary） | 60s | 5 |
| **合计** | | **≈ 85** |

余量充足。⚠️ 注意：同账号下其它自动化（certbot DNS-01、外部 DDNS 等）共享同一配额。

---

## 6. 决策规则（方案的心脏）

### 6.1 备节点升主 —— 必须**同时**满足 4 条

1. 从 CF 读到租约 `exp` 已过期**超过 15s**
2. 直连探测主的 `/api/health` **连续 3 次失败**（≥30s）
3. **自己能成功写 CF API**（写不了就既无法宣告也无法切 DNS，升主没有意义）
4. 距上次切换已过 120s 冷却期

### 6.2 主节点维持主 —— 满足**其一**即可

- **(a)** 续租成功（写入 CF 后回读确认 `owner=自己`）
- **(b)** 续租失败，但**直连探测备成功且备明确报告 `role=replica`**（有直接证据证明未发生接管）

两条都不成立 → **立即自降级**（写角色文件 + 退出 → 容器重启为 replica）。

### 6.3 全场景真值表

| 场景 | 主的行为 | 备的行为 | 结果 |
|---|---|---|---|
| 一切正常 | 续租成功 | 见租约有效，静默 | 正常 |
| 主进程/机器挂掉 | — | 租约过期 + 直连失败 + 能写 CF → 接管 | ✅ 切备（~45–60s） |
| **主备互相断网，双方都能连 CF** | (a) 成立，继续服务 | 租约有效 → 不接管 | ✅ **不误切**（复制中断，需告警） |
| **CF API 故障，主备互通** | (b) 成立，继续服务 | 写不了 CF → 不接管 | ✅ **fail-static，服务零影响** |
| 主完全孤立（连备也不通） | (a)(b) 均不成立 → 降级只读 | 租约过期 + 直连失败 → 接管 | ✅ 切备，**无双主** |
| 主备全挂 | — | — | ✅ **先起来的那台**接管 |
| 备服务中，主恢复 | 开机见 owner=备 → 起为 replica 并同步 | 见 handoff 请求 → 有序交还 | ✅ 自动回切 |

> 「CF API 故障」与「主备互相断网」这两行，正是单信号方案会判错的地方。**双信号规则**（租约 + 直连探测）把它们都变成了安全状态。

### 6.4 防脑裂三条铁律

1. **replica 永不自封 primary** —— static 租约下 `candidate: false` 的节点在结构上没有升主能力。
2. **开机必须重新确权，不信本地角色文件** —— 见 §7。
3. **主失去租约必须自降级（fencing）** —— 这是租约式领导权的标准要求。

万一仍发生瞬时双主：LiteFS 滚动校验和会检测分叉，落后方自动从新 primary 快照重建，旧主未复制的最后不到一秒写入被丢弃。对心跳/任务状态类数据可接受（stale recovery 会重派任务）。

---

## 7. 开机定角色

**绝不信任本地角色文件**——否则旧主被切走后回归会变成第二个主。

entrypoint 在 litefs 挂载**之前**调用 `/app/server ha-resolve-role`（Go 子命令，复用现有 config 与 HTTP 客户端，避免引入 `jq` 之类 shell 依赖）：

```
0. HA_FORCE_ROLE 已设置 → 直接采用（人工接管逃生舱，跳过全部仲裁）
1. 读 CF 租约
   ├─ 有效且 owner=自己  → primary
   └─ 有效且 owner=对端  → replica
2. 租约过期或不存在 → 直连探测对端 /api/health
   ├─ 对端是 primary   → 自己当 replica（它可能正处于 fail-static 状态）
   └─ 对端不可达       → 按 §6.1 规则夺取租约（写入后回读校验）→ primary
3. CF 不可达 → 只剩对端探测这一个证据源
   ├─ 对端自称 primary            → replica（明确证据，让位）
   ├─ 对端自称 replica 且本节点首选 → primary（见下）
   └─ 其余（对端也不可达）         → replica（只读）
```

第 3 条中间那一支需要解释：Cloudflare 不可达时没有仲裁者，为什么允许自升主？

因为**"对端明确自称 replica"是正面证据**——它直接排除了"对端已接管"这一唯一的危险情形。而"两台同时走这条路径"的竞态，则由 `HA_PREFERRED` 从结构上排除：只有被标记为首选的那一台被允许走它，另一台无论看到什么都只能当 replica。任何时刻至多一个节点可能通过这条路径升主，因此不存在并发。

最后一支是**刻意的保守取舍**：Cloudflare 与对端同时不可达属于双重故障，宁可短暂只读，也不冒双主导致 LiteFS 分叉丢写的风险。任一方恢复后运行期状态机会在下一轮纠正角色。

决议结果写成 shell 可 `source` 的 env 文件（默认 `/data/litefs-role`，原子 rename 写入），供 `litefs.yml` 的 `${}` 展开：

```sh
LITEFS_CANDIDATE=true
LITEFS_SELF_HOST=node-a
LITEFS_PRIMARY_URL=https://node-a.internal:20203
```

### 7.1 首次部署引导

`_ha-lease` 记录不存在时：首选节点（`HA_PREFERRED=true`）立即创建并宣告自己为 owner；非首选节点**先等 30s 再重读**，仅当仍不存在才创建。避免同时创建。

### 7.2 唯一判定入口

运行期状态机（`internal/ha/agent.go`）只负责判断"该换角色了"并触发进程退出，**换成什么角色一律由重启后的本决议说了算**。两处各自判定容易出现分歧，而分歧就是双主。

---

## 8. 自动回切（Failback）

"主"那台设 `HA_PREFERRED=true`，只有它会发起回切。全程由两节点自己完成，无外部协调者。

```
1. 主作为 replica 运行，健康且持续 60s 追平备的 TXID
     → 写 _ha-handoff: want=node-a;txid=<自己的位点>

2. 备轮询看到 handoff 请求 → 校验条件：
     · 主可达
     · 当前无进行中的 audio 桥接流（内存 io.Pipe 强切会断）
   条件不满足则等待，上限 30 分钟后强制执行

3. 备进入 draining：立即对写请求返 503，停止产生新写

4. 备等待主 TXID 完全追平（读主 /api/health 的 txid）
   停写超过 90s 仍未追平 → 放弃本次交接并恢复写入
   （停写状态下整个系统不可写，比"没能回切"严重得多）

5. 备写 _ha-lease: owner=node-a, epoch+1
   同时把 A 记录改指向主，清空 _ha-handoff

6. 备退出 → 重启后决议为 replica

7. 主轮询看到 owner=自己 → 退出 → 重启后决议为 primary
```

> "持续追平"而非"瞬时追平"是必要的：主节点在持续写入时副本会反复落后又追上，
> 只在某一瞬间追平就发起交接，会让交接反复开始又中止。

**先降后升，顺序不可颠倒**（反过来会出现双主）。空窗期 20–40s：写请求 503，**读全程可用**。

**零数据丢失** —— 备接管期间的所有写入在第 4 步完整回流到主。这正是选择方案 C（真复制库）的核心收益。

### 8.1 A 记录维护规则

**持有租约并以 primary 运行的节点，在每个续租周期检查 A 记录是否指向自己，不一致就 PATCH**（幂等操作，同时覆盖故障切换与回切两条路径）。

---

## 9. 节点间通道

LiteFS 的 20202 端口无认证且官方要求不可公网可达，必须加固。

### 9.1 默认方案：直连（推荐）

```yaml
# docker-compose.yml
    extra_hosts:
      - "peer.internal:<对端公网IP>"
```

- litefs 的 `http.addr` 绑 `127.0.0.1:20202`
- 现有 nginx 容器加一个 server 块（`nginx-litefs.conf`）：
  - 监听 20203、TLS（自签证书即可）
  - `allow <对端IP>; deny all;`
  - `proxy_pass http://127.0.0.1:20202`，关缓冲、超时 3600s、`client_max_body_size 0`
- `advertise-url: https://<对端主机名>:20203`
- 对端的自签 CA 由 entrypoint 装进容器信任链（`HA_PEER_CA_FILE` → `update-ca-certificates`）

**没有共享密钥**：LiteFS 客户端不支持自定义认证头，加了只会让复制握手失败。保护手段是 TLS + 源 IP 白名单——攻击者需同时伪造对端源 IP 并完成 TCP 握手才能触达。装 CA 而不是关闭证书校验，是因为这条通道同时也是"对端是否存活"的判定依据，被中间人劫持等于把切主决策交给攻击者。

用 `extra_hosts` 而非 DNS 记录：既能用主机名做 TLS 证书校验，又**不把源站 IP 暴露到公开 DNS**（避免抵消主域名橙云的 DDoS 防护）。全部是 compose 与 nginx.conf 字段，不出 Docker。

### 9.2 备选方案：走 CF 橙云

若两台 VPS 直连质量差或链路被干扰，给每节点建橙云子域 `node-a.<domain>`：

- ✅ 隐藏源站 IP、免费 TLS、借 CF 骨干传输
- ✅ **100MB 限制不适用**：CF 的 100MB 上限只针对**请求体**，**响应体不限制**；LiteFS 的大数据（LTX 流 / 全量快照）是 replica 发小请求、primary 流式**响应**回传，正好落在不受限的方向
- ⚠️ 空闲超 100s 的长连接可能被 524 中断；LiteFS 会自动重连，功能不受影响但日志变噪
- ⚠️ 大流量长期过 CF 代理属 ToS 灰区

**决策规则**：直连 RTT / 丢包正常 → 用 `extra_hosts`；直连不可靠 → 换橙云。

---

## 10. Docker 内的具体形态

### 10.1 Dockerfile（新增 3 行）

```dockerfile
COPY --from=flyio/litefs:0.5 /usr/local/bin/litefs /usr/local/bin/litefs
RUN apk add --no-cache fuse3 \
 && echo "user_allow_other" >> /etc/fuse.conf
```

`user_allow_other` **必需**：litefs 以 root 挂载 FUSE，而 server 以 appuser 运行，默认 FUSE 会拒绝非挂载用户访问。

### 10.2 进程模型

```
entrypoint (root)
  ├─ 目录兜底 + dist 同步 + 权限修正（现有逻辑保留）
  ├─ /app/server ha-resolve-role  →  写 /data/litefs-role，export 环境变量
  └─ exec litefs mount
       └─ su-exec appuser:appgroup /app/server
```

### 10.3 `/etc/litefs.yml`

```yaml
fuse:
  dir: "/litefs"
  allow-other: true          # app 以 appuser 访问 root 挂载的 FUSE，必须开

data:
  dir: "/var/lib/litefs"
  retention: "1h"            # 默认 10m；调长以便短时下线的节点走增量而非全量快照

exit-on-error: false          # 对端未就绪时不自杀，持续重试

http:
  addr: "127.0.0.1:20202"     # 绝不直接暴露公网

lease:
  type: "static"
  candidate: ${LITEFS_CANDIDATE}            # true = 本机是 primary
  hostname: "${LITEFS_SELF_HOST}"
  advertise-url: "${LITEFS_PRIMARY_URL}"    # 两边都填「当前 primary」的地址

exec:
  - cmd: "su-exec appuser:appgroup /app/server"
```

`DATABASE_URL` → `file:/litefs/m3u8preview.db`（LiteFS 不支持子目录）。`/data` 继续 bind mount，存 `ecdh.pem` 与角色文件。

### 10.4 docker-compose.ha.yml（叠加层）

HA 配置放在**独立的 override 文件**里，基础 `docker-compose.yml` 保持单机语义不变——同一份镜像同时服务单机与主备两种部署，不叠加就完全不受影响。

```
docker compose -f docker-compose.yml -f docker-compose.ha.yml up -d
```

叠加层提供：`devices: /dev/fuse`、`cap_add: SYS_ADMIN`、`security_opt: apparmor:unconfined`、`extra_hosts` 映射对端主机名、`./litefs:/var/lib/litefs` 卷、`./certs` 证书卷、全部 HA 环境变量，以及 nginx 侧的节点通道模板与端口。

官方省事写法是 `privileged: true`，上面是缩小权限后的等价配置。

### 10.5 运行 server 的用户

LiteFS 官方推荐以 root 挂载、用 `su` 把应用降权，但它**没有提供 FUSE 的 uid/gid 映射选项**，挂载点里的文件由 root 呈现。appuser 能否在 `/litefs` 下正常创建并写入数据库，取决于 LiteFS 呈现的权限位，必须在目标机器上实测（见 §14.4）。

因此实现保留了本项目一贯的降权姿态（`litefs-exec.sh` 里 `su-exec appuser:appgroup /app/server`），并留了逃生开关：实测不通时设 `LITEFS_RUN_AS_ROOT=1` 即可改为 root 运行。

### 10.5 角色切换机制

角色状态存 `/data/litefs-role`。server 内的 HA 协程判定需要换角色时：

```
改写角色文件 → 优雅退出 → litefs 随之退出 → 容器退出
  → restart: unless-stopped 拉起 → entrypoint 重新定角色 → 新角色生效
```

**无独立守护进程、无额外容器、无宿主进程。**

---

## 11. 服务端改动清单（本仓库）

| 模块 | 内容 |
|---|---|
| `internal/litefs/role.go` | 轮询 `/litefs/.primary` 判定角色（文件存在 = 本机是 replica，内容为 primary 主机名；不存在 = 本机是 primary）；读 `/litefs/<db>-pos` 取 TXID；持有 draining 标志；`AllowWrite()` 是写闸门的唯一收口。**未挂载 LiteFS 时（Windows 本地开发）恒返回 primary**，开发体验零变化 |
| `internal/ha/record.go` | 两条 TXT 记录的编解码（`Lease` / `Handoff`） |
| `internal/ha/cloudflare.go` | 最小 CF DNS API 客户端：读写 TXT、幂等维护 A 记录、以响应 `Date` 头为共同时钟 |
| `internal/ha/probe.go` | 对端 `/api/health` 直连探测 + 连续失败计数；支持自签 CA |
| `internal/ha/agent.go` | 运行期决策状态机（§6）+ 回切流程（§8）；时间常量与安全不等式断言 |
| `internal/ha/resolve.go` | 开机角色决议（§7）+ 角色 env 文件原子写入 |
| `cmd/server` 子命令 | `ha-resolve-role`：供 entrypoint 在挂载前定角色。**必须在连接数据库之前处理**——此时 DB 文件还不存在 |
| `/api/health` | 扩展为 `{status, role, nodeId, epoch, txid, draining, busyStreams, version}` —— worker、对端探测、回切判定共用的唯一信号。**replica 上同样返回 200**，否则 Docker HEALTHCHECK 会反复重启备节点 |
| 写拦截中间件 | `middleware.RequirePrimary`：replica / draining 下非 GET 的 `/api/v1/**` 返回 503 + `Retry-After` + code `NODE_READ_ONLY`。同时覆盖无主窗口 |
| 后台循环门控 | `SubtitleService.SetWritableGate` → stale recovery 与优先级老化仅在 primary 运行，否则在只读库上每 30s 失败一次刷满日志 |
| VTT 入库 | 新表 `subtitle_vtts`（`internal/service/subtitle_vtt.go`）。**单独一张表而非给 `subtitle_jobs` 加列**：正文是大字段，而 job 行会被状态轮询、admin 列表、claim 查询高频读取。策略是**写双份、读优先库**：写时磁盘与数据库各一份（磁盘那份继续服务既有备份/恢复流程），读时优先数据库、未命中回退磁盘（尚未回填的历史数据）。启动时后台幂等回填，仅 primary 执行 |
| 前端 | `api.ts` 拦截 503 + `NODE_READ_ONLY` 自动退避重试（最多 3 次，读 `Retry-After`）并派发 `ha:switching` 事件；`HaSwitchingBanner` 组件据此显示提示条。`adminApi.ts` 的 EventSource 改为按 `readyState` 区分瞬时抖动与彻底断开，允许浏览器原生重连最多 3 次 |

---

## 12. Worker 侧改动（两个 worker 仓库各一份）

配置从单个 `SERVER_URL` 改为 `SERVER_URLS=https://主,https://备`，逻辑是**无脑跟随角色**：

- 连接前 / 失败后轮询两地址的 `/api/health`，连 `role=primary` 的那个
- 当前节点连续 3 次失败或返回 `role=replica` → 重新选主
- 两个都不可达 → SEARCHING 状态，每 5–10s 循环探测
- 角色变化时**等当前 job 跑完再切**，不打断进行中的 ASR
- 旧节点上的残留任务靠现有 stale recovery（`SUBTITLE_WORKER_STALE_MINUTES`，默认 10 分钟）回收重派

因为「谁是主」由租约单一裁决，不会出现 audio worker 在主、subtitle worker 在备的分裂——这是 broker 内存桥接（`io.Pipe`）能继续工作的前提。

建议抽成共用的 `endpoint_manager` 模块。

---

## 13. Cloudflare 配置（一次性）

| 项 | 配置 |
|---|---|
| API Token | 权限收窄到 **Zone → DNS → Edit**，且只授权这一个 zone |
| 主域名 A 记录 | 橙云（边缘秒级生效）。若担心视频流经服务端代理触碰 CF 免费版流媒体限制，改灰云 + TTL 60s，切换延迟变 1–2 分钟 |
| `_ha-lease` TXT | 新建（TTL 随意，我们通过 API 读，不走 DNS 解析） |
| `_ha-handoff` TXT | 新建 |

---

## 14. 上线前必须验证的三件事

### 14.1 FUSE 可用性（**唯一 Docker 管不到的宿主前提**）

两台 VPS 各跑一次：

```bash
ls -l /dev/fuse
```

- KVM 虚拟化的 VPS（绝大多数）：有，直接可用
- **OpenVZ / LXC 容器型 VPS：可能没有 → LiteFS 方案不可行，需另行选型**

这不需要在宿主安装任何东西，只是必须确认内核支持。

### 14.2 写入 TPS 实测

FUSE 上限约 100 事务/秒。心跳与任务状态更新远低于此，但若观看/活动日志是每次播放即写，需先实测；超标就改批量提交。

### 14.3 DB 体积

主节点长时间下线（超过 `retention`）后回归会触发**全量快照传输**。体积大就把 `retention` 再调长。

### 14.4 appuser 能否在 /litefs 下写库

LiteFS 无 uid/gid 映射选项，挂载点文件由 root 呈现（见 §10.5）。首次部署时确认：

```bash
docker compose exec app su-exec appuser:appgroup sh -c 'sqlite3 /litefs/probe.db "create table t(x)" && rm -f /litefs/probe.db'
```

失败则在 `.env` 设 `LITEFS_RUN_AS_ROOT=1` 重启。

---

## 15. 两台机器必须一致的文件

部署时手工放一次，之后不用管：

- **`.env`** —— `JWT_SECRET` / `JWT_REFRESH_SECRET` / `PROXY_SECRET` / CF API Token 等
- **`data/ecdh.pem`** —— 登录握手的长寿 ECDH 私钥（`internal/config/config.go:283`，首次启动自动生成）。两台不一致时，DNS 切换瞬间会出现「challenge 从旧节点取、密文发到新节点」而解密失败，用户吃一次登录报错。把主节点的这个文件复制到备节点即可。

---

## 16. 存量数据迁移

**不能用 `cp` 拷进 FUSE 目录**（官方明确警告会损坏），必须：

```bash
docker compose exec app litefs import -name m3u8preview.db /data/m3u8preview.db
```

`uploads/posters`、`uploads/thumbnails` 不在 LiteFS 复制范围内：

- **VTT 已自动处理**：服务启动后后台把历史 `.vtt` 回填进 `subtitle_vtts` 表（幂等，仅 primary 执行），日志形如
  `[subtitle] 回填字幕正文完成: 成功 N 条，文件缺失 M 条，共扫描 K 条`。回填期间读取仍会回退磁盘，不影响可用性。
- 图片如需同步 → 加一个 syncthing 容器即可，仍在 Docker 范围内

### 16.1 摘掉磁盘那一份 VTT 的时机

当前是"写双份"。等两台机器都跑上新版本、回填完成、并做过一轮备份/恢复验证之后，才可以考虑去掉磁盘写入与回退读取。在此之前保留双份，才能让从旧版本备份恢复出来的数据仍然可读。

---

## 17. 实施顺序

1. 两台验证 `/dev/fuse`；改 Dockerfile / entrypoint / litefs.yml / compose / nginx.conf，用测试库跑通复制、手工角色互换、分叉快照重建
2. 服务端：RoleProvider + health 扩展 + replica 写拦截 + 后台循环门控
3. `internal/ha`：CF 租约客户端 + 决策状态机 + `ha-resolve-role` 子命令 + A 记录维护
4. VTT 入库（写新读旧 → 存量迁移 → 移除旧读路径）
5. 两个 worker 仓库的角色跟随逻辑
6. 前端 503 提示 + SSE 重连

---

## 18. 演练清单（八个场景，缺一不可）

| # | 场景 | 期望 |
|---|---|---|
| 1 | 主挂 | 45–60s 内备接管，DNS 切换 |
| 2 | 主备全挂，备先起 | 备接管；主随后起为 replica |
| 3 | 回切 | 零数据丢失，备期间写入完整回流 |
| 4 | **主备断网但都能连 CF** | **不切换**，主继续服务 |
| 5 | **CF API 不可达** | **fail-static**，服务不受影响 |
| 6 | 主完全孤立 | 主自降级只读，备接管，**无双主** |
| 7 | 旧主带未复制写入回归 | 自动快照重建，无需人工介入 |
| 8 | 回切时有进行中 audio 流 | 等待流结束再切（上限 30 分钟） |

---

## 19. 已知代价（明确接受）

| 代价 | 说明 |
|---|---|
| 故障切换 45–60s，计划回切 20–40s 写中断 | 读全程可用；比 Consul 方案慢，换掉了第三台 VPS 和 Worker |
| 主猝死时最后不到一秒的未复制写入丢失 | 异步复制固有，stale recovery 兜底 |
| 备节点不分担读流量 | DNS 只指向 primary，避免 SSE/broker 等节点内存态分裂；目标是 HA 不是扩容 |
| 极端双重故障下开机降级为只读 | CF 与对端同时不可达时；任一方恢复即自动升主 |
| FUSE 写入上限约 100 TPS | 见 §14.2 |
| 故障切换判定依赖 CF API 可用性 | 但**不依赖它维持正常服务**——见 §6.2 fail-static 规则 |

---

## 附录 A：参考资料

- LiteFS How it Works — https://fly.io/docs/litefs/how-it-works/
- LiteFS Config Reference — https://fly.io/docs/litefs/config/
- LiteFS in Docker — https://fly.io/docs/litefs/getting-started-docker/
- LiteFS FAQ（复制取舍、与 Litestream 对比）— https://fly.io/docs/litefs/faq/
- Cloudflare API Rate Limits — https://developers.cloudflare.com/fundamentals/api/reference/limits/
- Cloudflare Error 413（上传体积限制）— https://developers.cloudflare.com/support/troubleshooting/http-status-codes/4xx-client-error/error-413/
