# 验证码服务（Portcullis）端加固建议

> 背景：2026-04 审查提出 M9——CaptchaWidget 前端通过 `<script src="${captchaEndpoint}/sdk/pow-captcha.js">` 动态加载 SDK，无 `integrity=` (SRI)。
> Captcha 服务若被攻陷，可在登录 / 注册页注入任意 JS 窃取凭据。
>
> **状态：Portcullis Tier 1 + Tier 2 均已落地**，主站已完成对接：
> - Tier 1：manifest + SRI（2026-04-23 接入）
> - Tier 2：Ed25519 manifest 签名（2026-04-23 接入）
>
> 参考：`captcha/docs/INTEGRATION.md`（方式 D）、`captcha/docs/TIER1_IMPLEMENTATION.md`、`captcha/docs/TIER2_IMPLEMENTATION.md`

---

## 本项目接入现状

**Tier 1 已完成（2026-04-23）**：
- `CaptchaWidget.tsx` 切换到 manifest + SRI 加载：启动时先 `GET /sdk/manifest.json`（3s 超时、`cache: 'no-store'`），
  解析出 `artifacts['pow-captcha.js']` 的 `url` 与 `integrity`，注入 `<script integrity=... crossorigin=anonymous src=.../sdk/v1.1.2/...>`
- 降级保护：manifest 拉取失败 / 老 captcha 服务器未部署 Tier 1 → 回退到旧路径 `${endpoint}/sdk/pow-captcha.js`（无 integrity，依赖 HTTPS+HSTS）
- 同 endpoint 的 manifest 结果在会话内缓存；降级路径每次 render 重试一次（部署后无感升级）

**Tier 2 已完成（2026-04-23）**：
- 新增系统设置项 `captchaManifestPubKey`（admin 面板可配置，`/admin/settings` 白名单已纳入）
- 后端 `internal/service/captcha.go::ValidateEd25519PubKey` 校验：base64 可解码且正好 32 字节
- `/api/v1/auth/captcha-config` 公开接口返回 `manifestPubKey` 字段（不是秘密，公钥）
- 前端 `CaptchaWidget` 收到 `manifestPubKey`（非空）时：
  1. 用 `fetch().arrayBuffer()` 拿 manifest 原始字节（不能先 parse 后重新 serialize，签名对象是原始字节）
  2. 从 `X-Portcullis-Signature` 响应头读 base64 签名
  3. `@noble/ed25519` `verifyAsync(sig, rawBytes, pubKey)`
  4. 缺 header / 签名失败 / pubKey 解码失败 → `throw` 让 `onError` 捕获，**不降级**
- `manifestPubKey` 为空时：跳过签名校验，仅依赖 Tier 1 的 SRI（完全向后兼容）

**CSP 兼容性**：`script-src` 和 `connect-src` 都已包含 `captchaEndpoint` origin，无需任何 CSP 改动即可接入 Tier 1 和 Tier 2。

**当前未做**：
- **双密钥轮换**：Portcullis Tier 2 暂时只支持单 signing key；轮换走两步部署方案（先主站加新 pubKey、Portcullis 切换私钥、主站再删旧 pubKey）。如频次高再设计。
- **WASM 文件 SRI enforce**：manifest 声明了 WASM integrity，但 SDK 内部 fetch WASM 主站无法 enforce。SDK 自校验是更合适的位置（非主站范围）。

---

## 运维 Checklist（启用 Tier 2）

1. **生成密钥对**：在 Portcullis 主机上执行 `captcha-server gen-manifest-key`，记下私钥 seed + 公钥
2. **私钥配置**：`CAPTCHA_MANIFEST_SIGNING_KEY=<base64 seed>` 写入 Portcullis 运行环境
3. **重启 Portcullis**：`curl -sI <endpoint>/sdk/manifest.json` 确认响应头里有 `X-Portcullis-Signature`
4. **Portcullis admin 导出公钥**：`GET /admin/api/manifest-pubkey`（需 admin token），拿到 `{pubkey: base64}`
5. **主站 admin 面板**：进入 Dashboard → 验证码配置 → 填入 `Manifest 签名公钥`，保存
6. **验证**：前端打开登录页 → DevTools Network 看 `manifest.json` 被 fetch + 后续 `sdk/v*/pow-captcha.js` 加载成功。篡改 pubKey（改一个字符）应立即让 widget 报错。

---

```html
<!-- CaptchaWidget.tsx:58-67 运行时注入 -->
<script src="https://challenge.example.com/sdk/pow-captcha.js" async></script>
```

- 无 `integrity=sha384-...`：任何修改 SDK 字节的攻击都不会被浏览器拒绝
- 无 `crossorigin=anonymous`：就算加了 SRI 也不一定生效（同源以外资源必须 CORS 正确）
- SDK 运行在 `main-world`，和登录表单同 realm，能读 `password` 字段值

常规 SRI 方案（构建期注入固定 hash）不适用：
- SDK 可能随 captcha 服务升级变更
- 每个部署 endpoint 指向不同 SaaS 实例，各有各自的版本
- admin 改 `captchaEndpoint` 后主站前端无需重新构建

---

## 方案 A：版本化 URL + 服务端发布 SRI 清单

**主站按 admin 配置拉 SRI 清单，客户端按清单发起带 `integrity` 的 `<script>` 加载。**

### Captcha 服务端需要提供

新增 endpoint `GET /sdk/manifest.json`：

```json
{
  "version": "1.2.3",
  "builtAt": "2026-04-23T08:00:00Z",
  "artifacts": {
    "pow-captcha.js": {
      "url": "/sdk/v1.2.3/pow-captcha.js",
      "integrity": "sha384-ABCxyz...",
      "size": 48291
    },
    "pow-captcha-wasm.wasm": {
      "url": "/sdk/v1.2.3/pow-captcha-wasm.wasm",
      "integrity": "sha384-DEFuvw...",
      "size": 102400
    }
  },
  "signature": "<Ed25519(JSON) base64url>"
}
```

### 关键要求

1. **发布流程写死只读路径**：`/sdk/v1.2.3/...` 内容不可变，后续 bug fix 发 `1.2.4` 新目录；旧版本不删除，为主站缓存做兼容
2. **manifest 签名**：用 captcha 服务长寿 Ed25519 私钥签 JSON body；主站启动时通过 `captchaSiteKey` 对应的 Ed25519 公钥（通过 admin 面板配置）做本地校验，防攻击者改 manifest
3. **`Cache-Control: public, max-age=86400, immutable`**：URL 里带版本号，浏览器可长缓存
4. **`Cross-Origin-Resource-Policy: cross-origin`**：允许主站跨域加载
5. **`Access-Control-Allow-Origin: <主站 origin>`**：SRI 要求 CORS 正确

### 主站侧改造（本项目需要做的）

```typescript
// web/client/src/components/auth/CaptchaWidget.tsx
const manifest = await fetch(`${endpoint}/sdk/manifest.json`).then(r => r.json());
// 1. 验证 manifest.signature (用配置里的 Ed25519 公钥)
// 2. 取 artifacts['pow-captcha.js']
const sdk = manifest.artifacts['pow-captcha.js'];
script.src = `${endpoint}${sdk.url}`;
script.integrity = sdk.integrity;
script.crossOrigin = 'anonymous';
```

### 优势
- SRI 生效：SDK 被篡改浏览器直接拒加载
- 版本随 captcha 升级自动跟进，无需主站重新构建
- Ed25519 签名防 manifest 本身被中间人替换

### 成本
- Captcha 服务端新增一个静态 endpoint + 签名流程
- 主站 admin 面板要能配置 Ed25519 公钥（`captchaManifestPubKey`）

---

## 方案 B：Trusted Types + 严格 CSP（防御深度，不替代 SRI）

即便 SDK 被篡改、SRI 未启用，也能降低"SDK 偷密码"的爆炸半径：

### 在 `internal/app/app.go` secureHeaders 追加

```go
// 需要前端配合用 TrustedHTML / TrustedScriptURL，现有 React 生态大多默认兼容
"require-trusted-types-for 'script'; "
"trusted-types 'none'; " // 或白名单 React policy 名
```

### 主站前端避免直接访问 password 字段

把密码输入放进 `iframe[sandbox="allow-forms"]`，让第三方 SDK 无法同域访问 DOM。
（本项目当前未做这层隔离，属于架构级改造。）

---

## 方案 C：Permissions Policy 收窄 captcha origin 权能

Captcha SDK 本质只需要 WASM / fetch / DOM render。主站可在 CSP 之外追加 Permissions-Policy：

```go
c.Header("Permissions-Policy",
    "geolocation=(),camera=(),microphone=(),"+
    "payment=(),usb=(),clipboard-write=(self)")
```

降低即使 SDK 被攻陷后可调用的敏感 API 面。

---

## 给 Portcullis 维护者的优先级建议

| 优先级 | 能力 | 工作量 |
|---|---|---|
| P0 | 版本化 URL + 只读路径（方案 A 前半） | 小 |
| P0 | `Cross-Origin-Resource-Policy: cross-origin` + CORS | 极小 |
| P1 | SRI manifest + Ed25519 签名（方案 A 全部） | 中 |
| P2 | SDK 做最小权限原则：不读取非 captcha 容器外的 DOM | 视实现 |
| P3 | 主站配合 Trusted Types | 中大 |

---

## 本项目暂不实施的原因（后续演进参考）

1. **双密钥轮换**：Portcullis Tier 2 暂时只支持单 signing key；如频次高再设计"current + previous"双密钥
2. **Trusted Types + 严格 CSP（方案 B）**：需要全站 React 树审计 `dangerouslySetInnerHTML` 与 `eval`（项目目前默认禁用）
3. **Permissions-Policy（方案 C）**：可以低成本加，但不解决"SDK 偷密码"的核心风险，只是降级

---

## 现状兜底（多层纵深防御）

主站已做的风险抑制：
- `CaptchaWidget.tsx` 使用 SRI manifest + Ed25519 签名校验（首要防线，Tier 1+2 已落地）
- `captchaEndpoint` 经 `ValidateCaptchaEndpoint` 白名单 → 不能指向内网
- `captchaManifestPubKey` 经 `ValidateEd25519PubKey` 校验 → 写入前必须 base64(32B)
- siteverify 走 `util.SafeFetch` → DNS rebinding 防护
- siteverify hostname / challenge_ts 校验 → token 跨站重放拦截
- CSP `frame-ancestors 'none'` / `object-src 'none'` / `base-uri 'self'` → 通用注入面收窄
- siteverify 熔断 → captcha 服务不可用时快速失败不拖累登录

即使 Portcullis 主机被攻陷（私钥泄露）、或 TLS 链路被中间人，这些措施组合可让攻击窗口 / 爆炸半径压到最低。
