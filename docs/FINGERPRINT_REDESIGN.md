# 设备指纹重设计（H8）

> 背景：2026-04 审查提出 H8——当前 `fingerprint` 由客户端自报 → 服务端原样混入 HKDF salt，
> 既不能防越权（攻击者任意构造合法 fingerprint），也不构成风控信号（无历史对比），
> 但对合法用户引入可感知的失败（浏览器升级 / 隐身切换 / 硬件变化 → 登录失败）。
>
> 本文给出"现状分析 → 威胁模型重写 → 三阶段演进方案"的完整设计，用于后续决策与实施。
> 决策后按阶段分别提 issue / PR 执行。
>
> 关联代码：`web/client/src/utils/fingerprint.ts`、`internal/util/challenge.go`、`internal/util/ecdh.go:BlendSalt`、
> `web/crypto-wasm/src/lib.rs:blend_salt`、`internal/handler/auth.go:decryptAuth`

---

## 目录

- [1. 现状分析](#1-现状分析)
- [2. 诚实的威胁模型](#2-诚实的威胁模型)
- [3. 目标重定义](#3-目标重定义)
- [4. 三阶段方案](#4-三阶段方案)
  - [Phase 1：解除加密硬绑定（1-2 天）](#phase-1解除加密硬绑定1-2-天)
  - [Phase 2：升级为风控信号（1-2 周）](#phase-2升级为风控信号1-2-周)
  - [Phase 3：真正设备绑定（WebAuthn / Passkeys）](#phase-3真正设备绑定webauthn--passkeys)
- [5. 评估准则](#5-评估准则)
- [6. 不做的事 + 理由](#6-不做的事--理由)

---

## 1. 现状分析

### 1.1 数据流

```
浏览器                                Go 后端
───────                              ────────
[A] getDeviceFingerprint()
    ├── UA / 语言 / 平台
    ├── 分辨率 / 色深 / DPR
    ├── 时区
    ├── Canvas 绘制结果 dataURL
    └── WebGL vendor/renderer
    → SHA-256 hex(64 字符)
    └──────── POST /auth/challenge { fingerprint } ───────→
                                              ChallengeStore.Issue(fp, ip)
                                              ├── 生成 32B 随机 salt
                                              └── 存: {id, salt, fingerprint, ip, expiresAt}
    ←─────── { serverPub, challenge(=salt id), ttl } ──────
[B] WASM: blend_salt(challenge, fingerprint)
    → HKDF(shared, salt=blendedSalt, info) → AES key
    → AES-GCM(plaintext=登录凭据)
    └──────── POST /auth/login { challenge, iv, ct } ───────→
                                              decryptAuth:
                                              ├── ChallengeStore.Consume(id) → salt, storedFp
                                              ├── BlendSalt(salt, hex(storedFp))  ← 服务端用**它存的 fp**，不是客户端这次送的
                                              └── HKDF → AES key → GCM.Decrypt
```

### 1.2 关键观察

| 观察 | 含义 |
|---|---|
| fingerprint **仅在 challenge 签发时上报一次**，服务端存入 map | 登录解密时不再从客户端取新值，只用存储的旧值 |
| 客户端必须在 **blend_salt 时重新计算一次 fingerprint** 才能派生出相同的 key | 浏览器内 fingerprint 抖动 → key 不一致 → GCM 解密失败 |
| 服务端从未对 fingerprint 做任何校验 | 攻击者提交任意 64 字符 hex（现在只要求 hex+len64）即可通过 |
| fingerprint 和 userId **完全解耦**；挑战签发时用户未登录 | 即便 fp 稳定，也不知道哪个 fp 属于哪个用户 |

### 1.3 合法用户被打扰的真实场景

- **Canvas/WebGL 结果漂移**：显卡驱动更新、GPU 切换（独显 ↔ 核显）、浏览器新版本重写渲染栈
- **隐身/普通模式切换**：部分浏览器在隐身模式把 Canvas/WebGL 指纹做扰动（Brave、Firefox RFP、Safari ITP）
- **屏幕 DPR 变化**：外接显示器拔插、系统缩放切换
- **时区切换**：出差 / VPN
- **同设备多浏览器**：Chrome / Firefox 互相登录

这些情况**正好**是合法用户会遇到的。每次都要重新走"密码错误"的错误提示（因为 decryptAuth 统一返"请求无效"，M2 修复后用户看到的是"请求无效"而非"解密失败"，更难 debug）。

### 1.4 对攻击者的真实阻力

对目标的静态逆向 + 自动化脚本攻击场景：
- 攻击者能读 `fingerprint.ts` 源码 → 知道计算逻辑
- 攻击者能在**任何环境**复现：只要在脚本里补齐 6 项输入（UA 字符串、时区、Canvas 固定输出、WebGL 固定字符串），用 SHA-256 算出 64 字符 hex 即可
- 对"每个受害账户的 fp 必须与浏览器里实际运行的一致"也没要求——因为 fp 由攻击者**自己决定**，服务端不查历史

结论：**fingerprint 对攻击者 ~ 10 行代码的额外工作，对合法用户 ~ 经常性的登录失败**。ROI 负值。

---

## 2. 诚实的威胁模型

重新定义 fingerprint **实际能承担**的角色：

| 威胁 | fingerprint 能防吗？ |
|---|---|
| 密码明文泄露 | ❌（那是 ECDH+AES-GCM 负责的） |
| 密码被 MITM 替换 | ❌（TLS 负责） |
| 重放攻击 | ❌（challenge 一次性 + ts 窗口负责） |
| 跨设备会话盗用（cookie 被偷） | ❌（fingerprint 和 cookie 未绑定，攻击者不提供 fp 也能 refresh） |
| 暴力撞库 | ❌（那是 captcha + 限流负责的） |
| 大规模自动化登录 | 🟡（略增脚本作者工作量，但不是 blocker） |
| 账号在新设备首次登录检测 | ✅（**如果**服务端持久化历史 fp 与用户关联——当前没做） |
| 可疑登录行为风控 | ✅（**如果**有历史 fp 做基线对比——当前没做） |

**核心结论**：把 fingerprint 从"加密派生材料"改造为"风控审计信号"才能**兑现**其价值，而不是继续当"仪式感安全"。

---

## 3. 目标重定义

**舍弃**：fingerprint → HKDF salt → AES key 的硬绑定路径。

**保留**：fingerprint 上报通道（前端已有、UA 变化慢、升级成本低）。

**新增**：服务端持久化 `user → fingerprint[]` 历史 + 异常检测规则。

**最终目标**：

1. 合法用户：无感 —— fingerprint 变化不再导致登录失败
2. 攻击者：尝试从陌生设备登录 → 后端识别为"新设备" → 触发二次验证 / 邮件告警
3. 管理员：在后台能看到每个账户的登录设备历史与异常事件

---

## 4. 三阶段方案

### Phase 1：解除加密硬绑定（1-2 天）

**目标**：让 fingerprint 变化**不再**导致登录失败，但保留采集通道以供 Phase 2 使用。

#### 4.1.1 改动清单

| 文件 | 改动 |
|---|---|
| `internal/util/challenge.go` | `challengeEntry` 删 `fingerprint` 字段（或保留但不参与 key 派生）；`Issue/Consume` 签名保留以平滑迁移 |
| `internal/util/ecdh.go` | 删除 `BlendSalt`（或标记 Deprecated 保留一个 release cycle） |
| `internal/handler/auth.go:decryptAuth` | 直接用 `salt` 派生 AES key，不再做 `BlendSalt(salt, fpBytes)` |
| `web/crypto-wasm/src/lib.rs` | `blend_salt` 删除；`encrypt_auth_payload` 的 `fingerprint_hex` 参数保留但仅作**可选上报**给后端，不参与 salt 构造 |
| `web/client/src/utils/crypto.ts` | 调用 WASM 时不再把 fp 混进 salt；保留上报到 challenge endpoint 的路径 |
| `internal/dto/auth.go` | `ChallengeRequest.Fingerprint` 保留 `binding:"omitempty,len=64,hexadecimal"`（可选，为 Phase 2 提前铺路） |
| `internal/handler/auth.go:challenge` | 继续把 fp 写 challenge store，供 Phase 2 读出做审计（不再参与加密） |

#### 4.1.2 兼容性

- **前端先改，后端后改**会有一天窗口期两端协议不一致 → 必须同次发布
- 建议版本号 ↑ 一位（bugfix patch 级即可，非 breaking），在 CHANGELOG 明确"fingerprint 不再参与加密"

#### 4.1.3 测试

- 单元：`ecdh.go` deriveAESKey 直接用 raw salt，已有 tests 覆盖
- 集成：handler/auth_test.go 里两个 `util.BlendSalt(salt, fpBytes)` 调用删除，AES key 派生改为直接用 salt
- 手动：换浏览器 / 隐身模式登录，应都成功

#### 4.1.4 代码改动量估计

- Go：删除 ~30 行，改 ~10 行
- Rust：删除 ~15 行
- TS：删除 ~5 行（frontend/crypto.ts 里 blendSalt 调用）
- 重新构建 WASM 一次

---

### Phase 2：升级为风控信号（1-2 周）

**目标**：把 fingerprint 变成"新设备检测 + 异常登录告警"的依据。

#### 4.2.1 数据模型

新表 `user_devices`：

```go
type UserDevice struct {
    ID          string    `gorm:"primaryKey;type:text"`     // UUID
    UserID      string    `gorm:"index:idx_user_fp;type:text"`
    Fingerprint string    `gorm:"index:idx_user_fp;type:text;size:64"`  // SHA-256 hex
    FirstSeenAt time.Time
    LastSeenAt  time.Time
    LoginCount  int64
    TrustedAt   *time.Time  // 用户手动"信任此设备"的时刻；null = 未信任
    UserAgent   string      // 首次登录时的 UA，用于面板展示
    IPFirstSeen string
    IPLastSeen  string
}
```

唯一索引 `(user_id, fingerprint)`。

#### 4.2.2 登录成功时的副作用（write path）

```go
// internal/service/auth.go:Login 成功后
// 放在事务外，avoid锁 users 表
go s.deviceSvc.RecordLogin(user.ID, fingerprint, c.ClientIP(), c.GetHeader("User-Agent"))
```

`RecordLogin` 逻辑：
1. `UPSERT user_devices SET last_seen_at=now(), login_count+=1, ip_last_seen=? WHERE user_id=? AND fingerprint=?`
2. 若是首次 insert（新设备）：
   - 发送"新设备登录"通知邮件（如果启用 SMTP）
   - 写一条 `activity_logs` 记录（已有的活动日志表）
   - 返回 `isNewDevice=true`

#### 4.2.3 可选策略层

在 `system_settings` 增加：

| key | 默认 | 含义 |
|---|---|---|
| `newDeviceRequires2FA` | `false` | 新设备登录要求邮件 OTP 才能签发 refresh token |
| `newDeviceNotifyEmail` | `true` | 新设备登录发通知邮件给账户邮箱 |
| `maxUntrustedDevices` | `5` | 单用户最多保留多少"未信任"设备；超出时老 refresh token 失效 |

这些策略可 admin 按需开启，不强制。

#### 4.2.4 用户可见的 Account Settings 面板

新页 `/account/devices`：
- 列出该用户所有登录过的设备：UA、首次/末次登录时间、IP、登录次数
- 每行两个按钮：**信任此设备**（写 `trusted_at`）/ **撤销此设备**（删记录 + 作废该设备历史 refresh token family）
- admin 能看所有用户的设备列表

#### 4.2.5 数据保留与 GDPR

- `fingerprint` 本身是 SHA-256 hex，已经是派生值而非原始 PII，风险较小
- `ip_*` 字段保存 IP 地址，某些法域视为个人信息
- 添加配置项 `deviceHistoryRetentionDays`（默认 90 天），后台定时清理

#### 4.2.6 代码改动量估计

- GORM 模型 + 迁移：+50 行
- `DeviceService` + handler + route：+200 行
- 前端面板（/account/devices）：+150 行
- 邮件通知（如果启用）：+80 行
- 测试：+200 行

---

### Phase 3：真正设备绑定（WebAuthn / Passkeys）

**目标**：提供**真正** "换设备无法登录"的强安全选项。fingerprint 到此已成纯风控信号，WebAuthn 才是绑定载体。

#### 4.3.1 适用场景

- 高价值账户（如企业 admin 账号）
- 对现场可控性要求高的内网部署
- 用户手动 opt-in 启用

#### 4.3.2 协议选型

**WebAuthn + Passkeys (discoverable credentials)**：
- 浏览器 API：`navigator.credentials.create/get`
- Server 用 Go 生态库 `github.com/go-webauthn/webauthn`
- 认证器选项：platform authenticator（TouchID / Windows Hello）或 roaming（YubiKey）
- Relying Party = 主站域名

#### 4.3.3 用户流程

1. 登录成功后，用户在 `/account/security` 点"添加设备密钥"
2. 浏览器弹出系统 UI 注册 authenticator
3. 后端存储 public key + credential id
4. 下次登录：密码 + passkey 双因子，或纯 passkey（跳过密码）

#### 4.3.4 与 Phase 2 的关系

- Phase 2 的"新设备检测 / 通知"作为 Phase 3 的 fallback：用户未启用 passkey 时仍有风控信号
- 启用 passkey 后，攻击者即便偷到密码，没有注册过的 authenticator 也进不来

#### 4.3.5 代码改动量估计

- 依赖：`github.com/go-webauthn/webauthn`
- 后端：注册 / 认证 endpoint，credential 表，handler ~400 行
- 前端：两个新页面（注册 / 登录 passkey 分支）~250 行
- 测试：~300 行

**整体工作量 > Phase 2 的总和**。只有在 Phase 2 数据证明"新设备告警有价值"之后再做 Phase 3。

---

## 5. 评估准则

每个 Phase 落地后，用以下指标验收：

### Phase 1 验收

- [ ] 清空浏览器数据 → 两次登录 fingerprint 变化 → 登录仍成功
- [ ] 隐身模式 / 普通模式互切 → 登录成功
- [ ] `go test ./... -race` 全绿
- [ ] 生产灰度 48h 后 /api/v1/auth/login 的"请求无效"返回率应**下降**（因为 fp 抖动引起的失败消失）

### Phase 2 验收

- [ ] 新账户首次登录 → 不触发新设备告警（是首设备）
- [ ] 已有账户在新浏览器登录 → 邮箱收到"新设备登录"邮件 + activity_log 有对应条目
- [ ] `/account/devices` 能列出全部登录设备
- [ ] 用户点"撤销此设备" → 对应 refresh token family 立即失效，该设备再调用 `/auth/refresh` 得 401
- [ ] 配置 `newDeviceRequires2FA=true` → 新设备登录拿不到 refresh token，只返 `{action:"verify_otp"}` 要求 OTP

### Phase 3 验收

- [ ] 用户启用 passkey 后，攻击者即便拿到正确密码也无法登录（必须过 webauthn 挑战）
- [ ] 丢失 passkey 的恢复路径（紧急邮件 OTP + 账户冻结 24h）工作正常

---

## 6. 不做的事 + 理由

| 不做 | 理由 |
|---|---|
| 把 fingerprint 做成签名 token 塞回 cookie 做二次校验 | 攻击者偷 cookie 时连带偷 fp 签名，徒增复杂度不增防护 |
| 前端 fingerprint 算法加更多混淆（canvas/audio/webgpu） | 采样越多，合法用户抖动概率越高；对逆向者额外工作量几十分钟；不值 |
| IP 地理位置做风控 | VPN / CDN / 移动网络切换导致大量假阳性；本项目不是银行级风控平台 |
| 强制 fingerprint 全局唯一（拒绝重复 fp 登录不同账户） | 同一设备多账户使用是合法场景（家庭共享） |

---

## 7. 建议的决策路径

```
          立刻决定
              │
              ▼
      是否接受 fingerprint
      目前在制造登录假阳性？
        ┌─────┴─────┐
       Yes          No
        │           │
        ▼           ▼
    执行 Phase 1   保留现状
    （小改动，    （必须在文档里
     2 天搞定）    写清楚它"啥也不防"）
        │
        ▼
   生产观察 1 个月：
   - 登录成功率变化
   - 用户对"新设备检测"的期待
        │
        ▼
   再决定是否 Phase 2
        │
        ▼
   Phase 2 上线后 3 个月：
   - 新设备告警真的抓到过攻击吗？
   - 用户会不会因为误告警烦躁？
        │
        ▼
   再决定是否 Phase 3
```

**推荐**：先执行 Phase 1（低代价，解除合法用户假阳性），再按实际需求推进 Phase 2/3。
