# 接入指南

## 服务端部署

### 环境变量

| 变量 | 必填 | 默认 | 说明 |
|------|------|------|------|
| `CAPTCHA_SECRET` | 是 | — | HMAC 主密钥，≥ 32 字节 |
| `CAPTCHA_BIND` | 否 | `0.0.0.0:8787` | 监听地址 |
| `CAPTCHA_SITES` | 是 | `{}` | 站点配置 JSON |
| `CAPTCHA_TOKEN_TTL_SECS` | 否 | `300` | captcha_token 有效期 |
| `CAPTCHA_CHALLENGE_TTL_SECS` | 否 | `120` | 挑战有效期 |

### `CAPTCHA_SITES` 结构

```json
{
  "pk_test": {
    "secret_key": "sk_test_secret_at_least16",
    "diff": 18,
    "origins": ["https://example.com", "https://www.example.com"]
  },
  "pk_mobile": {
    "secret_key": "sk_mobile_secret",
    "diff": 14,
    "origins": ["https://m.example.com"]
  }
}
```

- `secret_key`：业务后端调用 `/siteverify` 时必须匹配，建议 ≥ 32 字符随机
- `diff`：该站点的默认难度；可针对不同业务（评论/登录/注册）设置不同 `site_key`
- `origins`：CORS / Referer 白名单（v1 尚未强制，会在 v2 收紧）

### 启动

```bash
cargo build --release -p captcha-server
CAPTCHA_SECRET=... CAPTCHA_SITES=... ./target/release/captcha-server
```

### Docker（示例）

```dockerfile
FROM rust:1.93-slim AS build
WORKDIR /app
COPY . .
RUN cargo build --release -p captcha-server

FROM debian:bookworm-slim
COPY --from=build /app/target/release/captcha-server /usr/local/bin/
EXPOSE 8787
CMD ["captcha-server"]
```

### 健康检查

```bash
curl http://localhost:8787/healthz
# -> "ok"
```

## 前端接入

### 方式 A：Auto-Mount（推荐，零 JS 代码）

服务端已内置 SDK 和 WASM 文件，访问 `/sdk/pow-captcha.js` 即可加载。

```html
<script src="https://captcha.example.com/sdk/pow-captcha.js"
        data-site-key="pk_test"></script>

<form>
  <div data-pow-captcha data-target="captcha_token"></div>
  <input type="hidden" name="captcha_token" id="captcha_token" />
  <button type="submit">提交</button>
</form>
```

**原理**：脚本自动从 `<script>` 的 `src` 推导 `endpoint` 和 WASM 路径，扫描所有 `[data-pow-captcha]` 元素并渲染 widget。验证成功后自动填充 `data-target` 指定的 input。

**data 属性**：

| 属性 | 位置 | 说明 |
|------|------|------|
| `data-site-key` | `<script>` 或 `<div>` | 站点公钥，必填 |
| `data-pow-captcha` | `<div>` | 标记容器元素 |
| `data-target` | `<div>` | 成功后自动填充的 input id |
| `data-theme` | `<div>` | `light`（默认）/ `dark` |
| `data-lang` | `<div>` | `zh-CN`（默认）/ `en-US` |
| `data-callback` | `<div>` | 全局回调函数名 |
| `data-endpoint` | `<script>` | 覆盖 endpoint（通常自动推导） |
| `data-wasm-base` | `<script>` | 覆盖 WASM 路径 |

### 方式 B：ES 模块（本地开发 / 自行构建）

```ts
// 从 sdk/src 直接导入（需 Vite / webpack 等构建工具）
import { render } from './sdk/src/index';

const widget = render('#captcha', {
  siteKey: 'pk_test',
  endpoint: 'https://captcha.example.com',
  lang: 'zh-CN',
  theme: 'light',
  onSuccess(token) {
    document.querySelector<HTMLInputElement>('#captcha_token')!.value = token;
  },
});
```

### 方式 C：全局 API

Auto-Mount 同时暴露 `window.PowCaptcha`：

```javascript
const widget = PowCaptcha.render('#my-captcha', {
  siteKey: 'pk_test',
  endpoint: 'https://captcha.example.com',
  onSuccess: (token) => { /* ... */ },
});
widget.reset();
widget.getResponse();
```

### 方式 D：带 SRI 的动态加载（推荐用于高敏感业务）

适用场景：主站 `captchaEndpoint` 在运行时可变（例如管理面板配置），无法在构建期写死 `<script integrity=...>`。

先拉 manifest、按清单里的 `integrity` 再注入 `<script>`：

```typescript
async function loadPortcullis(endpoint: string) {
  const ac = new AbortController();
  const t = setTimeout(() => ac.abort(), 3000);
  let manifest: any;
  try {
    manifest = await fetch(`${endpoint}/sdk/manifest.json`, {
      cache: 'no-store',
      signal: ac.signal,
    }).then((r) => r.json());
  } catch {
    // manifest 拉取失败 → 降级走旧路径（无 SRI）
    return injectScript(`${endpoint}/sdk/pow-captcha.js`, { crossOrigin: 'anonymous' });
  } finally {
    clearTimeout(t);
  }

  const sdk = manifest.artifacts['pow-captcha.js'];
  return injectScript(`${endpoint}${sdk.url}`, {
    integrity: sdk.integrity,
    crossOrigin: 'anonymous',
  });
}

function injectScript(src: string, attrs: Record<string, string> = {}) {
  return new Promise<void>((resolve, reject) => {
    const s = document.createElement('script');
    s.src = src;
    for (const [k, v] of Object.entries(attrs)) {
      s.setAttribute(k, v);
    }
    s.onload = () => resolve();
    s.onerror = () => reject(new Error('SDK 加载失败: ' + src));
    document.head.appendChild(s);
  });
}
```

**manifest 结构**：

```json
{
  "version": "1.1.2",
  "builtAt": 1745400000,
  "artifacts": {
    "pow-captcha.js":       { "url": "/sdk/v1.1.2/pow-captcha.js",       "integrity": "sha384-...", "size": 11284 },
    "captcha_wasm.js":      { "url": "/sdk/v1.1.2/captcha_wasm.js",      "integrity": "sha384-...", "size": ... },
    "captcha_wasm_bg.wasm": { "url": "/sdk/v1.1.2/captcha_wasm_bg.wasm", "integrity": "sha384-...", "size": ... }
  }
}
```

**注意事项**：
- 版本化路径 `/sdk/v{version}/*` 使用 `Cache-Control: immutable`，浏览器可长期缓存
- Portcullis 升级后旧版本字节从二进制消失，旧版本路径整体 404；主站应对 manifest 做短缓存（默认响应已设 `max-age=300`）
- 当前 manifest **不带签名**；威胁模型依赖 HTTPS + HSTS 保护传输层完整性。若需进一步的应用层签名，关注 Tier 2 进展（`docs/CAPTCHA_SDK_HARDENING.md`）
  });
</script>
```

### 配置项

| 字段 | 默认 | 说明 |
|------|------|------|
| `siteKey` | — | 站点公钥 |
| `endpoint` | — | 验证服务基地址 |
| `wasmBase` | `${endpoint}/pkg` | WASM 资源前缀（需托管 `captcha_wasm.js` 与 `captcha_wasm_bg.wasm`） |
| `theme` | `'light'` | `'light' \| 'dark'` |
| `lang` | `'zh-CN'` | `'zh-CN' \| 'en-US'` |
| `maxIters` | `10_000_000` | 最大尝试次数上限 |
| `onSuccess(token)` | — | 成功回调 |
| `onError(err)` | — | 失败回调 |
| `onExpired()` | — | token 过期回调 |

### WASM 资源托管

SDK 运行时需要两个资源：
- `captcha_wasm.js`（约 12 KB）
- `captcha_wasm_bg.wasm`（约 130 KB）

部署方式任选其一：
1. 和验证服务同源，通过静态文件托管
2. CDN（确保开启 CORS：`Access-Control-Allow-Origin: *`）
3. 自建 Nginx/OSS 目录

SDK 会按 `wasmBase` 加载。**必须保证同一版本的 WASM 与服务端 Argon2 参数一致**。

## 业务后端接入

### Node.js / Express

见 [`examples/backend-node/server.js`](../examples/backend-node/server.js)。

### Python / FastAPI

```python
import httpx
from fastapi import FastAPI, HTTPException

app = FastAPI()
CAPTCHA_ENDPOINT = "https://captcha.example.com"
CAPTCHA_SECRET_KEY = "sk_test_secret_at_least16"

@app.post("/login")
async def login(req: dict):
    async with httpx.AsyncClient() as c:
        r = await c.post(
            f"{CAPTCHA_ENDPOINT}/api/v1/siteverify",
            json={"token": req["captcha_token"], "secret_key": CAPTCHA_SECRET_KEY},
            timeout=5.0,
        )
    if not r.json().get("success"):
        raise HTTPException(400, "验证码校验失败")
    # ... 业务逻辑
```

### 通用要点

1. **不要信任浏览器传来的任何「验证已通过」标记**；必须走 `/siteverify`
2. **单次使用**：token 被核验后可考虑写入业务侧短期缓存（5 分钟）防止反复兑换
3. **绑定会话**：可将 token 校验结果与 session/userId 绑定，防止跨会话复用

## 本地开发

```bash
# 一个终端：启动验证服务
export CAPTCHA_SECRET="this-is-a-dev-secret-must-be-32-bytes+"
export CAPTCHA_SITES='{"pk_test":{"secret_key":"sk_test_secret_at_least16","diff":12,"origins":["http://localhost:5173"]}}'
cargo run -p captcha-server

# 另一个终端：启动 SDK 开发服务器
cd sdk && pnpm dev
```

打开 `http://localhost:5173` 即可看到 widget。

## 升级算法参数

当需要调整 Argon2 参数（例如由 m=4096 提升到 m=8192）：

1. 在 Rust 侧修改 `captcha-core/src/pow.rs` 中的常量
2. 重新构建 WASM：`bash scripts/build-wasm.sh`
3. 同步部署服务端与 WASM
4. 预期影响：灰度期间存量 token（5 分钟内）会被拒绝
