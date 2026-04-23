# 验证码服务（Portcullis）端加固建议

> 背景：2026-04 审查提出 M9——CaptchaWidget 前端通过 `<script src="${captchaEndpoint}/sdk/pow-captcha.js">` 动态加载 SDK，无 `integrity=` (SRI)。
> Captcha 服务若被攻陷，可在登录 / 注册页注入任意 JS 窃取凭据。
>
> 因 `captchaEndpoint` 由 admin 面板动态配置，无法在构建期生成固定 SRI hash。本文给出两条可组合的配合路径，**由验证码服务端主动配合实现**，把主站的攻击面收敛到协议级。

---

## 问题定义

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

## 本项目暂不实施的原因

1. SRI 需要 captcha 服务端先支持版本化 URL + manifest
2. Trusted Types 需要全站 React 树审计 `dangerouslySetInnerHTML` 与 `eval`（项目目前默认禁用）
3. Permissions-Policy 可以低成本加，但不解决"SDK 偷密码"的核心风险，只是降级

若 captcha 服务升级到支持 SRI manifest，主站会在 `CaptchaWidget.tsx` 切换到方案 A。

---

## 现状兜底

主站已经做的风险抑制（即使没有 SRI 也起作用）：
- `captchaEndpoint` 经 `ValidateCaptchaEndpoint` 白名单 → 不能指向内网
- siteverify 走 `util.SafeFetch` → DNS rebinding 防护
- siteverify hostname / challenge_ts 校验 → token 跨站重放拦截
- CSP `frame-ancestors 'none'` / `object-src 'none'` / `base-uri 'self'` → 通用注入面收窄
- siteverify 熔断 → captcha 服务不可用时快速失败不拖累登录

这些措施使"captcha 服务临时异常"和"captcha 服务被攻陷后的短期时间窗"两种情况的影响被压到最低，在 SRI 方案落地前可以接受。
