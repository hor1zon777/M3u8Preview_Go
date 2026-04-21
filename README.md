# m3u8-preview-go

M3u8Preview 的 Go 版**全栈**项目（Gin 后端 + React/Vite 前端 + nginx）。
与 `M3u8Preview_R`（Express + Prisma + TypeScript）共享同一套 REST 接口 / Cookie / SSE 契约，
可直接挂原 SQLite 数据库切换过来，**不再依赖** `M3u8Preview_R` 目录即可独立构建部署。

- **后端**：Gin + GORM + glebarez/sqlite（纯 Go，无 CGO）+ ffmpeg（PATH 可见）
- **前端**：React 18 + Vite 6 + TailwindCSS + Zustand + Tanstack Query + hls.js
- **加密**：Rust/WASM 核心（ECDH P-256 + HKDF-SHA256 + AES-256-GCM）+ 设备指纹绑定
- **代理**：nginx（生产），Vite dev server proxy（开发 HMR）
- **目标**：单仓库一键 `docker compose up` 即获得完整服务

---

## 目录

- [快速开始](#快速开始)
- [安全架构](#安全架构)
- [从 TS 版 M3u8Preview_R 迁移](#从-ts-版-m3u8preview_r-迁移)
- [目录结构](#目录结构)
- [路由覆盖](#路由覆盖)
- [与 TS 版行为兼容](#与-ts-版行为兼容)
- [环境变量](#环境变量)
- [生产上线检查清单](#生产上线检查清单)
- [FAQ / 故障排查](#faq--故障排查)
- [开发命令](#开发命令)
- [未落地 / 后续扩展](#未落地--后续扩展)

---

## 快速开始

### 方式 A — 本机开发（一键同时起 Go 后端 + Vite HMR，**推荐**）

```bash
cp .env.example .env            # 按需改端口 / 密钥
cd web
npm install                      # 首次装 workspace 依赖（含 concurrently）
npm run dev:all                  # 一条命令并发跑 Go API (:3000) 和 Vite (:5173)
# 浏览器访问 http://localhost:5173
```

`dev:all` 会用 [concurrently](https://github.com/open-cli-tools/concurrently) 把两个进程的输出打到同一个终端，
加前缀 `[api]` / `[web]` 方便区分，Ctrl+C 会同时停两个。

**想让 Go 代码改动也热重载？** 装一次 [air](https://github.com/air-verse/air)：

```bash
go install github.com/air-verse/air@latest   # 确保 $GOPATH/bin 在 PATH
npm run dev:all:hot                          # 改 Go 源码后 air 自动重新编译并重启
```

air 配置已放在仓库根的 `.air.toml`，监听 `cmd/` 和 `internal/`，排除 `web/`、`data/`、`uploads/`。

健康检查：`curl http://127.0.0.1:3000/api/health`

首次启动会自动建表 + 种子账号：

- admin / `${ADMIN_SEED_PASSWORD:-Admin123}`
- demo / `${DEMO_SEED_PASSWORD:-Demo1234}`

> **分开两个终端跑也可以**（老方式）：终端 1 `go run ./cmd/server`，终端 2 `cd web && npm run dev -w client`。

### 方式 B — Docker 一键全栈部署（生产）

```bash
cp .env.example .env
# 改 JWT_SECRET / JWT_REFRESH_SECRET / PROXY_SECRET（≥32 字符且互不相同）

docker compose up -d
# 访问 http://localhost（默认 80；改 DOCKER_PORT 可改端口）
```

`docker-compose.yml` 直接拉取 GHCR 镜像（`ghcr.io/hor1zon777/m3u8preview_go:main`），
推送到 `main` 分支或打 `v*` tag 时由 GitHub Actions 自动构建并推送。

如需本地构建镜像：

```bash
docker build -t m3u8preview-go:latest .
```

运行时：

- `m3u8preview-go-app`：跑 Go 二进制 + 把前端 `dist-image` 每次启动 `rsync` 到 `client-dist` volume
- `m3u8preview-go-nginx`：官方 `nginx:alpine` 只读挂 `client-dist` + 本项目的 `nginx.conf`

### 方式 C — 仅容器化后端（开发后端、前端本机跑）

```bash
docker compose -f docker-compose.dev.yml up --build
# Go 后端容器：http://localhost:3000
# 前端：终端另开 cd web && npm run dev -w client，访问 :5173
```

---

## 安全架构

### 登录密码加密传输

前端登录/注册/改密 **不发送明文密码**。整条加密链分多层叠加：

| 层 | 机制 | 防什么 |
|---|---|---|
| 协议 | ECDH P-256 一次性密钥 + challenge 单次消费 + ts 60s 窗 | 重放攻击 |
| 密钥绑定 | HKDF salt = SHA256(challenge \|\| 设备指纹) | 换设备/浏览器复现 |
| 代码 | Rust/WASM 加密核心 + XOR 混淆常量 + wasm-opt flatten | 静态分析 |
| 混淆 | javascript-obfuscator Medium 预设（crypto.ts / fingerprint.ts / authApi.ts） | JS 可读性 |
| CSP | `script-src 'self' 'wasm-unsafe-eval'` | eval/XSS 注入 |

#### 协议流程

```
1. 前端 POST /auth/challenge {fingerprint} → {serverPub, challenge, ttl=60s}
2. 前端 WASM 内部：
   - 生成一次性 ECDH P-256 密钥对
   - ECDH(clientPriv, serverPub) → 32B 共享密钥
   - salt = SHA256(challenge_bytes || fingerprint_bytes)
   - HKDF-SHA256(shared, salt, info="m3u8preview-auth-v1") → 32B AES key
   - AES-256-GCM(key, iv=12B随机, aad=端点常量, plaintext={password,ts}) → ct
3. 前端 POST /auth/login {challenge, clientPub, iv, ct}
4. 后端用同样的流程反向解密，得到明文 password → bcrypt 校验
```

- **challenge 单次消费**：录制密文重放返 400
- **设备指纹混入 salt**：换设备 → fp 变 → AES key 不同 → 解密失败
- **AAD 绑端点**：login 的密文当 register 投送会 GCM tag 校验失败
- **HKDF info 在 WASM 内 XOR(0xA7) 存储**：wasm-decompile 看到乱码字节

#### ECDH 私钥管理

- 服务端长寿 ECDH P-256 密钥对存于 `data/ecdh.pem`
- 首次启动自动生成（0600 权限），之后重启复用
- 支持通过 `ECDH_PRIVATE_KEY_PATH` 环境变量指定路径
- **绝不入库**（.gitignore 已配置）

#### WASM 加密核心

加密逻辑编译为 WebAssembly，逆向需要 wasm-decompile 或 Ghidra：

- 源码位于 `web/crypto-wasm/`（Rust crate）
- Vendored 产物位于 `web/client/src/wasm/`（入库，日常开发无需 Rust 工具链）
- wasm-opt 加固（`--flatten --rse --dce --coalesce-locals`）：控制流打散为 br_table
- 仅当修改加密核心时才需要重新构建 WASM（详见 `web/crypto-wasm/README.md`）

---

## 从 TS 版 M3u8Preview_R 迁移

迁移核心原则：**数据库字段、Cookie 名、JWT 签名、代理签名** 都逐字节兼容。

1. **备份现有数据**

   ```bash
   cp M3u8Preview_R/data/m3u8preview.db m3u8-preview-go/data/m3u8preview.db
   # 或直接挂卷复用同一份 db-data（推荐 Docker 场景）
   ```

2. **`.env` 整个复制过来**，**保持 `JWT_SECRET` / `JWT_REFRESH_SECRET` / `PROXY_SECRET` 不变**

   - 未变则原有用户刷新 token 继续有效
   - 已颁发的 `/proxy/sign` URL 在签名 TTL 内继续可播放

3. **首次启动 Go 版会 `AutoMigrate` 兜底**

   - 字段与 Prisma schema 完全对齐，无需运行 prisma migrate
   - 新增的 `system_settings` 默认键会通过 `ensureDefaultSettings()` 补齐

4. **管理员在 admin 面板改过的配置**（`proxyAllowedExtensions`、`enableRateLimit` 等）原样保留

5. **灰度建议**

   - 先用 `docker-compose.dev.yml` 在 3001 端口起 Go 版，挂测试副本 DB 跑一轮回归
   - 确认无误再 `docker compose up` 切生产栈
   - Go 版异常时回滚到 R 版 compose，DB 结构兼容，无需 rollback migration

---

## 目录结构

```
m3u8-preview-go/
├── cmd/server/                 # 启动入口：Load config → Open DB → AutoMigrate → Gin
├── internal/
│   ├── app/                    # Gin engine 组装 + 路由挂载 + 依赖注入
│   ├── config/                 # .env 加载 + 生产密钥强度校验 + CORS 多值解析
│   ├── db/                     # GORM 连接 / AutoMigrate / seed
│   ├── dto/                    # 请求/响应结构 + 加密 DTO（EncryptedAuthRequest / ChallengeRequest）
│   ├── handler/                # HTTP handlers（按模块拆分）
│   ├── middleware/             # Auth / RateLimit / Error / Validator
│   ├── model/                  # 12 张表的 GORM 模型
│   ├── parser/                 # CSV / Excel / JSON / Text 导入解析
│   ├── service/                # 业务层（无依赖 Gin）
│   ├── sse/                    # SSE writer + 进度常量
│   └── util/                   # jwt / ecdh / challenge / proxysign / ssrf / uaparser / ffmpeg
├── web/                        # 前端 workspace（npm workspaces）
│   ├── package.json            # workspace root，提供 build:shared / build:client
│   ├── shared/                 # @m3u8-preview/shared（TS 类型 + zod 校验）
│   ├── crypto-wasm/            # Rust crate → 编译为 WASM 加密核心
│   │   ├── Cargo.toml
│   │   ├── src/lib.rs          # encrypt_auth_payload() + XOR 混淆 + dummy 控制流
│   │   └── README.md           # WASM 构建指南
│   └── client/                 # React 18 + Vite 6 + Tailwind
│       ├── vite.config.ts      # dev proxy + javascript-obfuscator 生产混淆插件
│       └── src/
│           ├── utils/
│           │   ├── crypto.ts       # 加密入口：fetchChallenge → WASM → envelope
│           │   └── fingerprint.ts  # 设备指纹采集（Canvas/WebGL/UA/屏幕/时区）
│           ├── wasm/               # Vendored WASM 产物（入库，无需 Rust 工具链）
│           ├── components/ hooks/ pages/ services/ stores/
│           └── main.tsx
├── nginx.conf                  # 生产 nginx（CSP + wasm-unsafe-eval + upstream）
├── Dockerfile                  # 3 阶段：node builder → go builder → alpine runner
├── docker-entrypoint.sh        # 卷权限修正 + 前端 dist 同步 + su-exec 降 appuser
├── docker-compose.yml          # 生产：GHCR 镜像 + nginx（host network）
├── docker-compose.dev.yml      # 本地：仅 app，端口映射 3000
├── .env.example
└── README.md
```

---

## 路由覆盖

| 模块 | 范围 |
|---|---|
| `/api/health` | 健康检查 |
| `/api/v1/auth/*` | challenge / register / login / refresh / logout / me / change-password / sse-ticket / register-status |
| `/api/v1/media/*` | 列表 / 详情 / recent / random / artists / views / admin CRUD |
| `/api/v1/categories/*`、`/tags/*` | 公开查询 + admin CRUD |
| `/api/v1/favorites/*` | toggle / check / list |
| `/api/v1/history/*` | progress / list / continue / progress-map / clear / delete |
| `/api/v1/playlists/*` | public / owned / items / CRUD / addItem / removeItem / reorder |
| `/api/v1/upload/poster` | 封面上传（admin） |
| `/api/v1/import/*` | preview / execute / logs / template |
| `/api/v1/proxy/sign`、`/proxy/m3u8` | HMAC 签名 + SSRF 代理 |
| `/api/v1/admin/*` | dashboard / users / settings / media batch / activity / thumbnails / posters / backup |

---

## 与 TS 版行为兼容

| 维度 | 契约 |
|---|---|
| 响应信封 | `{ success, data?, error?, message?, meta? }` 未变 |
| JWT | 带 `kid`，`JWT_KID_PREV` 过渡期双密钥解码 |
| 代理签名 | `HMAC-SHA256(PROXY_SECRET, url\nexpires\nuserId)` 逐字节一致；`hmac.Equal` 防时序 |
| m3u8 重写 | 按行扫描；非注释整行替换，`#... URI="..."` 只替 URI |
| SSRF | IPv4 + IPv6 覆盖相同私有段；`.local/.internal/.localhost` 拒绝；`SafeFetch` 每跳重验 IP |
| SSE | `data: <json>\n\n` + `X-Accel-Buffering: no`；握手用一次性 ticket |
| Cookie | `refreshToken`，`SameSite=Lax`，`Secure` 根据 `CORS_ORIGIN` 协议自动推断（亦可通过 `COOKIE_SECURE` 覆盖） |
| 批量操作 | 上限 500 条（admin media batch / import execute ≤1000） |
| Admin 约束 | 最后一个 ADMIN 不可降级 / 不可自停用 / 不可删除 ADMIN |
| 登录记录 | IP、UA、device 解析口径与 TS 版一致（`mileusna/useragent`） |
| 前端 API 基址 | 走 `/api/v1`（相对路径），无需修改 |

---

## 环境变量

### 基础运行

| 变量 | 默认值 | 说明 |
|---|---|---|
| `PORT` | `3000` | HTTP 监听端口 |
| `BIND_ADDRESS` | 生产 `127.0.0.1` / 开发 `0.0.0.0` | 监听地址，生产默认走 nginx 反代 |
| `NODE_ENV` | `development` | `production` 时启用密钥强度校验 |
| `DATABASE_URL` | `file:./data/m3u8preview.db` | 可加 `file:` 前缀；支持绝对 / 相对路径 |
| `DATA_DIR` | `./data` | SQLite 文件与 SQLite WAL 所在目录 |
| `UPLOADS_DIR` | `./uploads` | 封面与缩略图存储目录 |
| `WEB_DIST_DIR` | `/app/web/dist`（容器内） | 前端静态产物路径，entrypoint 同步用 |

### 密钥（生产必须，≥32 字符且互不相同）

| 变量 | 用途 |
|---|---|
| `JWT_SECRET` | access token 签名密钥 |
| `JWT_REFRESH_SECRET` | refresh token 签名密钥 |
| `PROXY_SECRET` | `/proxy/sign` 的 HMAC 密钥 |
| `JWT_KID` | 当前密钥的 `kid`，默认 `v1` |
| `JWT_KID_PREV` | 轮换过渡期保留上一代 `kid`（可选） |
| `JWT_SECRET_PREV` | 上一代 access 密钥（配合 `JWT_KID_PREV`） |
| `JWT_REFRESH_SECRET_PREV` | 上一代 refresh 密钥 |

### CORS / CDN / Cookie / 加密

| 变量 | 默认值 | 说明 |
|---|---|---|
| `CORS_ORIGIN` | `http://localhost:5173` | 支持逗号分隔多个 origin（如 `http://localhost:5173,http://127.0.0.1:5173`）。生产必须显式配置，禁止 `*`；**必须与浏览器实际访问地址一致** |
| `COOKIE_SECURE` | 自动推断 | Cookie `Secure` 标志。未设置时根据 `CORS_ORIGIN` 协议自动推断：任一 origin 为 `https://` → `true`，全部 `http://` → `false`。也可显式覆盖 |
| `TRUST_CDN` | `true` | 是否信任 `CF-Connecting-IP` / `True-Client-IP`；未部署 CDN 请设 `false` 防伪造 |
| `ECDH_PRIVATE_KEY_PATH` | `<DATA_DIR>/ecdh.pem` | 登录加密协议的 ECDH P-256 私钥路径。首次启动自动生成（0600）。Docker 部署请确保 `data/` 是持久卷 |

### 容量 / 并发

| 变量 | 默认值 | 范围 |
|---|---|---|
| `THUMBNAIL_CONCURRENCY` | `5` | 1-20，缩略图生成并发 |
| `POSTER_MIGRATION_CONCURRENCY` | `2` | 1-10，外部封面下载并发 |

### Docker / 种子

| 变量 | 默认值 | 说明 |
|---|---|---|
| `DOCKER_PORT` | `80` | nginx 对外端口 |
| `ADMIN_SEED_PASSWORD` | `Admin123` | 首次启动 admin 用户密码 |
| `DEMO_SEED_PASSWORD` | `Demo1234` | 首次启动 demo 用户密码 |

---

## 生产上线检查清单

- [ ] `NODE_ENV=production`
- [ ] `JWT_SECRET` / `JWT_REFRESH_SECRET` / `PROXY_SECRET` 三者互不相同，每项 ≥ 32 字符
- [ ] `CORS_ORIGIN` 设为实际前端地址（不是 `*`），**必须与浏览器地址栏一致**（含协议和端口）
- [ ] `TRUST_CDN` 与实际 CDN 链路匹配（未部署请设 `false`）
- [ ] `ADMIN_SEED_PASSWORD` / `DEMO_SEED_PASSWORD` 首次启动后立刻登录改掉
- [ ] nginx 层已开启 HTTPS，并补全 `X-Forwarded-For` / `X-Forwarded-Proto`
- [ ] `data/` 与 `uploads/` 是独立 volume，有定期备份（`data/ecdh.pem` 须持久化）
- [ ] `admin/backup/export` 的 ZIP 备份纳入外部存储策略
- [ ] 健康检查 `curl -sf http://127.0.0.1:3000/api/health` 在部署流水线中生效
- [ ] `systemSettings.proxyAllowedExtensions` 按实际源站类型收窄
- [ ] 前端若要换域名，记得同步 `CORS_ORIGIN` 和 `nginx.conf` 的 CSP

---

## FAQ / 故障排查

**Q1. Docker 起来后 `/data/m3u8preview.db` 权限报错？**
entrypoint 以 root 进入后会 `chown -R appuser:appgroup /data /app/uploads`，再用 `su-exec` 下降。
若仍失败，通常是 bind-mount 的宿主目录 owner 与镜像内 uid 不匹配：
- 容器内 appuser uid 由 `adduser -S` 动态分配，宿主改成 `chown -R 100:101 data uploads` 通常可解
- 推荐生产使用 `volume` 而非 `bind-mount`

**Q2. Docker 构建 Stage 1 卡在 `npm install`？**
多半是 `web/**/node_modules/` 没被 `.dockerignore` 正确排除导致 COPY 时体积爆炸。
检查 `.dockerignore` 里是否包含：

```
web/**/node_modules/
web/**/dist/
```

**Q3. nginx 容器起来但访问 `/` 返回 404？**
app 容器 entrypoint 负责把镜像里的 `/app/web/dist-image` 同步到 `client-dist` volume。
若 app 没有起来 / 起来早于 nginx 拉起，会出现短暂 404。
docker-compose.yml 里 nginx `depends_on: [app]` 已保证顺序；若仍有问题：

```bash
docker compose restart nginx
```

**Q4. 改了前端代码，`docker compose up -d --build` 后没生效？**
命名 volume 只在首次创建时从镜像拷内容，之后不会自动刷新。
entrypoint 已处理这个：每次 app 容器启动会强制覆盖 `client-dist` volume。所以：

- `docker compose up -d --build` → app 容器会重启 → entrypoint 同步 → nginx 读到新 dist
- 若依然看到旧内容：浏览器强刷（Ctrl+Shift+R）或 `docker compose down -v && docker compose up -d --build`（会清 volume，注意 DB 也会清）

**Q5. 登录后刷新页面跳回登录页？**
Cookie `Secure` 标志与访问协议不匹配。`Secure` Cookie 只能通过 HTTPS 发送，若通过 HTTP 访问则浏览器不会携带 refresh token。

- 纯 HTTP 内网访问 → 确保 `CORS_ORIGIN` 以 `http://` 开头（`COOKIE_SECURE` 自动为 `false`）
- 外部 HTTPS 反代 → `CORS_ORIGIN` 必须设为 `https://你的域名`（`COOKIE_SECURE` 自动为 `true`）
- 特殊场景 → 可通过 `COOKIE_SECURE=true/false` 显式覆盖

**Q6. 宝塔 nginx 反代 HTTPS，内部容器走 HTTP，怎么配？**

```
浏览器 (HTTPS) → 宝塔 Nginx (SSL 终止) → 容器 Nginx (HTTP :28000) → Go (:3000)
```

只需设置 `CORS_ORIGIN=https://你的域名`，`COOKIE_SECURE` 会自动推断为 `true`。
`Secure` Cookie 只影响浏览器与最外层 nginx 之间的连接，内网 HTTP 跳转不受影响。

**Q7. Go 版启动但 `/api/v1/auth/login` 返回 401，账号确认是对的？**
检查 `JWT_SECRET` 是否与 TS 版完全一致（包括尾部换行）。
如果确实轮换了密钥，登录时应使用**当前 `JWT_SECRET`**；对**已发 refresh token** 的解码，
通过 `JWT_SECRET_PREV` + `JWT_KID_PREV` 让老 token 在过渡期继续可用。

**Q8. 登录/注册点击后提示"登录失败，请重试"但后端没有请求日志？**
前端加密流程在发送请求前执行，如果 WASM 加载失败或 challenge 请求被拦截，POST 不会发出。常见原因：

- **CSP 阻止 WASM**：nginx.conf 里 `script-src` 需包含 `'wasm-unsafe-eval'`。检查 F12 Console 是否有 CSP 错误
- **CORS 不匹配**：`CORS_ORIGIN` 支持逗号分隔多值（如 `http://localhost:5173,http://127.0.0.1:5173`），必须与浏览器地址栏的 origin（协议+域名+端口）精确匹配
- **challenge 请求 403**：`POST /auth/challenge` 在 auth limiter 下，频繁请求会被限流

**Q9. `/proxy/m3u8` 报 403 "segment domain not allowed"？**
代理只放行已在 DB 中出现过的 `media.m3u8_url` 的 scheme + host。新加源站需要：

1. 先通过正常 `POST /admin/media` 建一条记录，让该域进入白名单
2. 或者临时 `INSERT INTO media(..., status='ACTIVE', m3u8_url='https://new.host/xxx.m3u8')`

**Q10. 管理后台改了 `proxyAllowedExtensions` 但没生效？**
`ProxyService` 对扩展名白名单做了 30s 缓存。admin 更新设置后，handler 会调
`ProxyService.InvalidateExtensionsCache()`，若看到 "not invalidated" 日志，
检查 `Deps.ProxySvc` 是否挂到了 app，以及 settings handler 路径是否命中。

---

## 开发命令

```bash
# ---------- 后端 ----------
go test -race ./...               # 跑全部测试（race 需 cgo 环境）
CGO_ENABLED=0 go test ./internal/util/... ./internal/handler/... ./internal/config/...
go vet ./...                      # 静态分析
go build -o bin/server ./cmd/server
CGO_ENABLED=0 GOOS=linux GOARCH=amd64 \
  go build -trimpath -ldflags="-s -w" -o bin/server-linux-amd64 ./cmd/server

# ---------- 前端 ----------
cd web
npm install                       # 一次性装 workspace 依赖
npm run build                     # = build:shared && build:client
npm run dev -w client             # Vite HMR dev server（默认 :5173）
npm run preview -w client         # 预览 build 产物

# ---------- 前后端一键并发 ----------
cd web
npm run dev:all                   # go run + Vite，concurrently 合并输出
npm run dev:all:hot               # 需要本机装 air，Go 代码改动也自动重启

# ---------- WASM 加密核心（仅改加密逻辑时需要）----------
cd web/crypto-wasm
cargo test --lib                  # Rust 单测
wasm-pack build --target web --release --out-dir ../client/src/wasm --out-name crypto_wasm
rm ../client/src/wasm/.gitignore  # wasm-pack 自动生成的 .gitignore 需删除以入库
# 可选：wasm-opt 加固（需安装 binaryen）
wasm-opt ../client/src/wasm/crypto_wasm_bg.wasm \
  -o ../client/src/wasm/crypto_wasm_bg.wasm \
  --flatten --rse --dce --coalesce-locals --reorder-functions --merge-blocks \
  --enable-bulk-memory -O2
```

---

## 未落地 / 后续扩展

| 项 | 当前状态 | 备注 |
|---|---|---|
| Prometheus / metrics 端点 | 未实现 | 如需可在 `/api/v1/admin/*` 下新增 |
| 前端 `useVideoThumbnail` 客户端截帧 | Hook 为空壳 | 后端 ffmpeg 方案已覆盖此需求 |
| i18n / 多语言 | 未实现 | 全部 UI 文案为硬编码中文 |
| 主题切换（亮色模式） | 未实现 | 当前仅暗色主题 |
| WebAuthn / Passkey | 未实现 | 可彻底消除密码（T5 档），`go-webauthn/webauthn` 库已就绪 |
| 请求签名链 | 未实现 | 全站 API 签名链改动面大，需整体规划 |
