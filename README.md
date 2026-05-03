# m3u8-preview-go

M3u8Preview 的 Go 版**全栈**项目（Gin 后端 + React/Vite 前端 + nginx）。
与 `M3u8Preview_R`（Express + Prisma + TypeScript）共享同一套 REST 接口 / Cookie / SSE 契约，
可直接挂原 SQLite 数据库切换过来，**不再依赖** `M3u8Preview_R` 目录即可独立构建部署。

- **后端**：Gin + GORM + glebarez/sqlite（纯 Go，无 CGO）+ ffmpeg（PATH 可见）
- **前端**：React 18 + Vite 6 + TailwindCSS + Zustand + Tanstack Query + hls.js
- **加密**：Rust/WASM 核心（ECDH P-256 + HKDF-SHA256 + AES-256-GCM）
- **代理**：nginx（生产），Vite dev server proxy（开发 HMR）
- **字幕**：v3 分布式 worker 架构。任务由两台 worker 协作：
  - [m3u8-preview-audio-worker](../m3u8-preview-audio-worker) — 大带宽机：下载 m3u8 + 抽音 + FLAC 编码（FLAC 留本地）
  - [m3u8-preview-worker](../m3u8-preview-worker) — GPU 机：通过服务端 broker 实时拉 FLAC + ASR + 翻译 + 写 VTT
  - 服务端只做任务派发 + broker 实时桥接（**0 持久化音频文件**）+ VTT 仓库；保留本地 whisper.cpp 兼容模式
  - 协议详见 [`docs/worker-protocol.md`](docs/worker-protocol.md)；架构详见 [`m3u8-preview-worker/docs/distributed-worker.md`](../m3u8-preview-worker/docs/distributed-worker.md)
- **目标**：单仓库一键 `docker compose up` 即获得完整服务

---

## 目录

- [快速开始](#快速开始)
- [安全架构](#安全架构)
- [从 TS 版 M3u8Preview_R 迁移](#从-ts-版-m3u8preview_r-迁移)
- [从命名卷升级到 bind mount](#从命名卷升级到-bind-mount)
- [目录结构](#目录结构)
- [路由覆盖](#路由覆盖)
- [与 TS 版行为兼容](#与-ts-版行为兼容)
- [环境变量](#环境变量)
- [生产上线检查清单](#生产上线检查清单)
- [FAQ / 故障排查](#faq--故障排查)
- [开发命令](#开发命令)
- [字幕功能（v3 分布式 worker broker 架构）](#字幕功能v3-分布式-worker-broker-架构)
- [附录：本地 whisper.cpp 兼容模式](#附录本地-whispercpp-兼容模式)
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
| 密钥派生 | HKDF-SHA256(shared, salt=challenge, info="m3u8preview-auth-v1") | 共享密钥固定化 |
| 代码 | Rust/WASM 加密核心 + wasm-opt flatten | 静态分析 |
| 混淆 | javascript-obfuscator Medium 预设（crypto.ts / authApi.ts） | JS 可读性 |
| CSP | `script-src 'self' 'wasm-unsafe-eval'` + frame-ancestors 'none' + object-src 'none' | eval/XSS 注入 |

#### 协议流程

```
1. 前端 POST /auth/challenge {fingerprint} → {serverPub, challenge, ttl=60s}
   （fingerprint 上报供服务端风控使用，不参与密钥派生——详见 docs/FINGERPRINT_REDESIGN.md）
2. 前端 WASM 内部：
   - 生成一次性 ECDH P-256 密钥对
   - ECDH(clientPriv, serverPub) → 32B 共享密钥
   - HKDF-SHA256(shared, salt=challenge, info="m3u8preview-auth-v1") → 32B AES key
   - AES-256-GCM(key, iv=12B随机, aad=端点常量, plaintext={password,ts}) → ct
3. 前端 POST /auth/login {challenge, clientPub, iv, ct}
4. 后端用同样的流程反向解密，得到明文 password → bcrypt 校验
```

- **challenge 单次消费**：录制密文重放返 400
- **AAD 绑端点**：login 的密文当 register 投送会 GCM tag 校验失败

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

> **完整教程**：[docs/MIGRATION_FROM_NODE.md](docs/MIGRATION_FROM_NODE.md) — 含零停机灰度、回滚预案、行为差异清单、FAQ。
> 本节给出精简 5 步版本。

迁移核心原则：**数据库字段、Cookie 名、JWT 签名、代理签名** 都逐字节兼容。

1. **备份现有数据**

   ```bash
   cp M3u8Preview_R/data/m3u8preview.db m3u8-preview-go/data/m3u8preview.db
   cp -a M3u8Preview_R/uploads/. m3u8-preview-go/uploads/
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

## 从命名卷升级到 bind mount

> 仅影响在 **commit `24a9509` 之前**（即使用 `db-data` / `uploads` 命名卷的旧镜像）部署过本项目的用户。
> 新装无需阅读本节。

`docker-compose.yml` 已将数据卷从命名卷改为 `./data` / `./uploads` bind mount：
- 数据直接可见（`ls data/`、`ls uploads/`），备份 / rsync 不再需要进容器
- 重建镜像不会出现"老命名卷没同步新 seed"的惊喜

**老用户直接 `docker compose pull && up -d` 会看到数据"凭空消失"**——
其实数据还在 `/var/lib/docker/volumes/<prefix>_db-data/_data` 和 `<prefix>_uploads/_data`，
只是容器挂载点换成了空的 `./data` / `./uploads` bind 目录。

### 迁移步骤（Linux / macOS）

```bash
# 1. 停容器，避免 sqlite 脏页
docker compose down

# 2. 查出旧命名卷实际路径（替换 <prefix> 为你的 compose 项目名，通常是目录名）
docker volume inspect <prefix>_db-data   # 取 Mountpoint 路径
docker volume inspect <prefix>_uploads

# 3. 把卷内容拷到 bind mount 目录（用一次性 alpine 容器避免宿主 root 权限问题）
mkdir -p ./data ./uploads
docker run --rm \
  -v <prefix>_db-data:/src:ro \
  -v "$(pwd)/data:/dst" \
  alpine sh -c 'cp -a /src/. /dst/'
docker run --rm \
  -v <prefix>_uploads:/src:ro \
  -v "$(pwd)/uploads:/dst" \
  alpine sh -c 'cp -a /src/. /dst/'

# 4. 启动新栈
docker compose up -d

# 5. 确认登录正常、媒体文件可访问后，再清理旧卷（可选）
docker volume rm <prefix>_db-data <prefix>_uploads
```

### 关于文件属主

entrypoint 启动时默认 **只把属主是 `root` 的文件** 改为容器内 `appuser`，
保留宿主上用户自管的文件属主（旧行为是整树 `chown`，会破坏宿主端 `./data`/`./uploads` 的 UID）。

如果你需要完全跳过 chown（自管权限场景），在 `docker-compose.yml` 的 app service 加：

```yaml
environment:
  - SKIP_CHOWN=1
```

---

## 目录结构

```
m3u8-preview-go/
├── cmd/server/                 # 启动入口：Load config → Open DB → AutoMigrate → Gin
├── internal/
│   ├── app/                    # Gin engine 组装 + 路由挂载 + 依赖注入
│   ├── config/                 # .env 加载 + 生产密钥强度校验 + CORS 多值解析
│   ├── db/                     # GORM 连接 / AutoMigrate / seed
│   ├── dto/                    # 请求/响应结构 + 加密 DTO + 字幕 worker 协议 DTO
│   │   └── subtitle.go         # subtitle / worker / admin 端点共用 schema
│   ├── handler/                # HTTP handlers（按模块拆分）
│   │   ├── subtitle.go         # /subtitle/* + /admin/subtitle/* 端点
│   │   └── subtitle_worker.go  # /worker/* 端点（远程 GPU worker）
│   ├── middleware/             # Auth / RateLimit / Error / Validator / WorkerAuth
│   │   └── worker_auth.go      # mwt_xxx Bearer token 鉴权 + bcrypt 缓存
│   ├── model/                  # GORM 模型
│   │   ├── subtitle.go         # subtitle_jobs 表
│   │   └── subtitle_worker.go  # subtitle_workers / subtitle_worker_tokens 表
│   ├── parser/                 # CSV / Excel / JSON / Text 导入解析
│   ├── service/                # 业务层（无依赖 Gin）
│   │   ├── subtitle.go         # 字幕任务调度 + 远程 worker 协议实现
│   │   ├── subtitle_worker.go  # worker token / worker 注册管理
│   │   ├── asr.go              # 本地 whisper.cpp 调用（兼容模式）
│   │   └── translation.go      # OpenAI 兼容 LLM 翻译（兼容模式）
│   ├── sse/                    # SSE writer + 进度常量
│   └── util/                   # jwt / ecdh / challenge / proxysign / ssrf / uaparser / ffmpeg
│       └── ffmpeg_subtitle.go  # 抽音频 / 装载头部 helper
├── docs/
│   ├── MIGRATION_FROM_NODE.md  # Node → Go 迁移指南
│   └── worker-protocol.md      # 远程字幕 worker HTTP 契约（v1）
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
│           ├── components/admin/SubtitleWorkersPanel.tsx  # 在线 worker / token 面板
│           ├── pages/AdminSubtitlesPage.tsx               # 字幕任务管理面板
│           ├── services/subtitleApi.ts                    # 字幕相关前端 API
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
| `/api/v1/subtitle/:mediaId/status`、`/subtitle/vtt/:mediaId` | 字幕状态查询 + HMAC 签名 VTT 拉取 |
| `/api/v1/admin/subtitle/*` | 字幕任务列表 / 详情 / 重试 / 删除 / 禁用 / 批量重生 / 队列概况 / 配置回显 / 在线 worker / worker token CRUD |
| `/api/v1/worker/*` | **远程字幕 worker 专用**：register / claim / heartbeat / complete / fail / retry（`mwt_xxx` Bearer 鉴权） |
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

### 字幕功能（v3 分布式 worker broker 架构）

> 默认架构下，**所有 ASR / 翻译 / 模型 / 默认语言** 等字幕参数都在 admin 面板「字幕管理」中配置，不再走环境变量；此处仅是开关 + 路径 + 心跳超时。

**v3 关键设计**：
- 任务派发按 worker capabilities：`audio_extract`（下载抽音）/ `asr_subtitle`（ASR + 翻译）
- audio worker 完成 FLAC 后**留在本地**，仅向服务端注册元数据（`audio-ready`）
- subtitle worker 拉 FLAC 时由服务端 broker 通过 `io.Pipe` 实时桥接 audio worker 的上传流——**服务端 0 持久化音频**
- 任务 DONE 后服务端通过 long-poll 通道通知 audio worker 删本地 FLAC

部署需要至少一台 [m3u8-preview-audio-worker](../m3u8-preview-audio-worker) + 一台 [m3u8-preview-worker](../m3u8-preview-worker)（可同机但通常分开以利用各自硬件优势）。详见：
- 协议契约：[`docs/worker-protocol.md`](docs/worker-protocol.md)
- 架构设计：[`../m3u8-preview-worker/docs/distributed-worker.md`](../m3u8-preview-worker/docs/distributed-worker.md)

#### 环境变量

| 变量 | 默认值 | 说明 |
|---|---|---|
| `SUBTITLE_ENABLED` | `false` | 总开关。关闭时 `/subtitle/*` 与 `/worker/*` 端点全部短路返回 |
| `SUBTITLE_DIR` | `<UPLOADS_DIR>/subtitles` | VTT 文件存放目录 |
| `PUBLIC_BASE_URL` | _空_ | 服务端对外可见 URL（如 `https://media.example.com`，无尾斜杠）。v3 用于在 `audioArtifactUrl` 里返回绝对地址；空时返回相对路径，要求 worker 与服务端共享同一 host |
| `SUBTITLE_WORKER_STALE_MINUTES` | `10` | 远程 worker 心跳超时（1-120 分钟），超过后任务被回收 |
| `SUBTITLE_GLOBAL_MAX_CONCURRENCY` | `0` | 全局正在 RUNNING 的字幕任务上限（0=不限）。多 worker 共享 ASR / 翻译 API 配额时用于防止打垮上游 |
| `SUBTITLE_LOCAL_WORKER_ENABLED` | `false` | **兼容旧部署**。设 `true` 时启动 in-process whisper.cpp worker（详见附录） |

> v3 不再有"中转池配额"环境变量——FLAC 不在服务端落盘。

> 当 `SUBTITLE_LOCAL_WORKER_ENABLED=true` 时，下列变量才生效，用于配置内置 worker：
> `SUBTITLE_WHISPER_BIN` / `SUBTITLE_WHISPER_MODEL` / `SUBTITLE_WHISPER_LANG` / `SUBTITLE_WHISPER_THREADS` /
> `SUBTITLE_TRANSLATE_BASE_URL` / `SUBTITLE_TRANSLATE_API_KEY` / `SUBTITLE_TRANSLATE_MODEL` /
> `SUBTITLE_TARGET_LANG` / `SUBTITLE_BATCH_SIZE` / `SUBTITLE_MAX_RETRIES` / `SUBTITLE_AUTO_GENERATE`。
> 默认（远程 worker 模式）下这些变量被忽略。

#### Admin UI（v3 新增）

「字幕管理」面板顶部展示：
- **AlertsBar**：自动检测"无 audio worker / subtitle worker 在线 + 有任务等待"等异常并以橙色横幅展示
- **WorkersOnlineCard**：worker 列表，每条显示 capability badge（蓝色"下载抽音"/ 紫色"ASR 字幕"）
- **IntermediatePoolCard**：v3 broker 模式标注"FLAC 留 audio worker 本地，服务端 0 落盘"，显示当前等待 ASR 的任务数 + 总字节估算
- **TokensCard** → 创建/编辑：双维度并发配置（audio / subtitle / 总上限兜底）
- **任务详情**：点击列表行的「详情」按钮打开 JobDetailModal，显示双阶段 timeline（蓝色 audio worker 段 + 紫色 subtitle worker 段，每段独立时间戳与耗时）

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

**Q1. Docker 起来后 SQLite 报 `unable to open database file: out of memory (14)` 或权限错误？**

这是 `/data` 目录权限不足导致 SQLite 无法创建或写入数据库文件。错误码 14 是 SQLite 的 `SQLITE_CANTOPEN`，`out of memory` 是其误导性的错误描述。

**快速修复**：在 docker-compose.yml 所在目录执行：

```bash
chmod 777 ./data
```

**原因分析**：
- entrypoint 以 root 进入后会 `chown` 再用 `su-exec` 降权到 `appuser`
- 但 bind-mount 场景下，宿主目录的权限由宿主机决定，容器内的 `chown` 可能不生效
- 不同机器即使系统相同，`./data` 目录的初始权限也可能不同（取决于创建方式、umask、文件系统等）

**其他解决方式**：
- `chown -R 100:101 data uploads`（100:101 是容器内 appuser 的 uid:gid）
- 或在 `docker-compose.yml` 的 app service 中添加 `user: "0:0"` 以 root 身份运行（不推荐）
- 推荐生产使用命名 volume 而非 bind-mount，命名卷首次创建时会自动继承镜像内的权限

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

---

## 字幕功能（日语音频 → 中文字幕，纯 CPU 部署）

后端在新建媒体时自动入队、worker 串行处理：**ffmpeg 抽音频 → whisper.cpp ASR → OpenAI 兼容 LLM 翻译 → 写 WebVTT**。
前端播放页查询字幕状态，DONE 后给 `<video>` 挂签名 `<track>`，浏览器原生显示中文字幕。

### 一、依赖准备

#### 1) 编译 whisper.cpp（一次性，CPU 部署只需 8~15 分钟）

```bash
git clone https://github.com/ggerganov/whisper.cpp.git
cd whisper.cpp
make -j                              # 产物：./main 与 ./build/bin/whisper-cli
sudo cp ./build/bin/whisper-cli /usr/local/bin/
whisper-cli --help                   # 验证可用
```

> 推荐用 `cmake -B build && cmake --build build -j` 拿到位于 `build/bin/whisper-cli` 的产物。
> 如果只编出了 `main`（旧版本），请把它当作 whisper-cli 使用：`SUBTITLE_WHISPER_BIN=/usr/local/bin/main`。

#### 2) 下载 GGML 模型（CPU 量化版本）

| 模型 | 文件大小 | 1 小时音频耗时（8 核 CPU 参考） | 准确率 |
|---|---|---|---|
| `ggml-base-q5_0.bin` | ~58 MB | 5~10 分钟 | 一般 |
| `ggml-small-q5_0.bin` | ~187 MB | 10~15 分钟 | 较好 |
| `ggml-medium-q5_0.bin` ✅ 推荐 | ~514 MB | 20~30 分钟 | 好 |
| `ggml-large-v3-q5_0.bin` | ~1.05 GB | 60~90 分钟 | 最佳 |

```bash
mkdir -p data/whisper
wget -O data/whisper/ggml-medium-q5_0.bin \
  https://huggingface.co/ggerganov/whisper.cpp/resolve/main/ggml-medium-q5_0.bin
```

#### 3) 准备 OpenAI 兼容的翻译 API

任意支持 OpenAI Chat Completions 协议的服务都可以：

| 服务 | base URL | 推荐模型 |
|---|---|---|
| DeepSeek | `https://api.deepseek.com` | `deepseek-chat` |
| 通义千问 | `https://dashscope.aliyuncs.com/compatible-mode` | `qwen2.5-7b-instruct` |
| OpenAI | `https://api.openai.com` | `gpt-4o-mini` |
| 智谱 | `https://open.bigmodel.cn/api/paas` | `glm-4-flash` |
| 自建（vLLM / Ollama / OneAPI） | `http://your-host:port` | 任意 |

> 翻译批量调用：默认每 8 条字幕一起请求，1 小时视频约 75~100 次调用，DeepSeek 单视频成本 < ¥0.05。

### 二、配置 .env

```bash
SUBTITLE_ENABLED=true
SUBTITLE_AUTO_GENERATE=true
SUBTITLE_WHISPER_BIN=whisper-cli
SUBTITLE_WHISPER_MODEL=/app/data/whisper/ggml-medium-q5_0.bin
SUBTITLE_WHISPER_LANG=ja
SUBTITLE_WHISPER_THREADS=0           # 0=自动用全部核心
SUBTITLE_TRANSLATE_BASE_URL=https://api.deepseek.com
SUBTITLE_TRANSLATE_API_KEY=sk-xxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxx
SUBTITLE_TRANSLATE_MODEL=deepseek-chat
SUBTITLE_TARGET_LANG=zh
SUBTITLE_BATCH_SIZE=8
```

### 三、运行 / 验证

```bash
# 重启服务后查日志：
docker compose restart app
docker compose logs -f app | grep '\[subtitle\]'

# 期望输出：
# [subtitle] worker started (asr=ggml-medium-q5_0.bin, mt=deepseek-chat, lang=ja→zh, autoGenerate=true)
```

启动时 worker 会扫描所有 `status=ACTIVE` 的媒体，给没有字幕的逐个入队（单线程 CPU 跑，不会过载）。

### 四、字幕管理面板

管理员登录后访问 `/admin/subtitles`：

- 顶部 5 张状态卡：排队中 / 处理中 / 已完成 / 失败 / 已禁用 计数
- 列表分页 + **分类筛选** + 状态筛选 + 搜索（按 mediaId 或标题）
- **行勾选 + 全选当前页（带 indeterminate 半选态）+ 跨页保留选择**
- 每行操作：**重新生成 / 禁用切换 / 删除（含 VTT 文件）/ 失败时查看错误信息**
- 顶部按钮：**重新生成所选（按 mediaId 数组）/ 重试全部失败 / 重新生成全部 / 按当前分类重新生成**
- 配置弹窗：当前生效的 whisper / 翻译配置回显（API Key 已脱敏）

数据每 5 秒自动刷新，可手动点 "刷新" 立即更新。

### 四.1、播放页字幕设置

播放控制条上新增**字幕（Captions / CaptionsOff）**按钮：

- 显示开关：一键开/关字幕（关闭时也不再触发 cue 渲染）
- **显示原文（双语模式）**：同时显示译文与原文，仅对包含原文的字幕生效
- 字号：50% – 250% 滑块
- 文字颜色 / 文字不透明度 / 背景颜色 / 背景不透明度
- 边缘修饰：无 / 阴影 / 描边 / 发光
- 字重：常规 / 加粗
- 垂直位置：距视频底部 0% – 40%（用于避开控制栏）
- 恢复默认：一键回退所有设置

设置自动持久化到 localStorage（`subtitle-settings-v1`），跨视频、跨会话保留。

> **VTT 双语格式约定**：cue payload 第 1 行为译文（主字幕），第 2 行（可选）为原文。前后端 + worker（Rust）三处实现已统一对齐。

### 四.2、远程 GPU Worker 限流

字幕任务调度有两层并发上限保护，避免 worker 集群把上游 ASR / 翻译 API 打垮：

| 维度 | 配置位置 | 默认值 | 说明 |
|---|---|---|---|
| 全局 | `SUBTITLE_GLOBAL_MAX_CONCURRENCY` 环境变量 | `0`（不限） | 所有 worker 集群共同上限 |
| Token | admin 面板「Worker Token 管理」编辑 | `1` | 该 token 名下所有 worker 共同上限 |

工作机制：
- `ClaimNextJob` 抢占 PENDING 前先校验「全局 RUNNING < globalMax」与「该 token RUNNING < tokenMax」
- 任一超额返回 `nil`，worker 进入 sleep 重试，让其它 token 的 worker 自然有机会抢任务
- admin 面板 Token 列表显示 `current / max` 进度条（满载红、≥70% 黄、其它绿）
- 编辑 token 上限不会中断已经在跑的任务，仅影响后续 claim

### 五、Docker 部署

`Dockerfile` 暂未内嵌 whisper.cpp 二进制（节省镜像体积）。两种集成方式：

1. **挂载方式（推荐）**：宿主机编译 whisper-cli + 模型，bind-mount 进容器
   ```yaml
   # docker-compose.yml
   volumes:
     - ./bin/whisper-cli:/usr/local/bin/whisper-cli:ro
     - ./data/whisper:/app/data/whisper:ro
   ```

2. **自定义镜像**：在 `Dockerfile` 里加一个 build stage 编译 whisper.cpp 并复制产物。

### 六、安全 / 合规

- VTT 端点用 HMAC 签名（复用 `PROXY_SECRET`）保护，仅当前用户可拉取自己请求过状态的媒体字幕
- 翻译 API Key 仅从环境变量读取，**绝不入库**；管理面板回显时强制脱敏（保留首尾 4 位）
- 字幕文件存放在 `<UPLOADS_DIR>/subtitles/<mediaId>.vtt`，删除媒体时级联清理

### 七、常见问题

**Q1. ASR 段落都是日文，没翻成中文？**
检查 worker 日志是否有 `translate batch fallback to source`，常见原因：
- `SUBTITLE_TRANSLATE_API_KEY` 错误或额度耗尽 → 看 LLM 平台账单
- `SUBTITLE_TRANSLATE_BASE_URL` 多了尾部 `/v1` → 必须不含 `/v1`，代码会自动追加
- 防火墙拦截出站 → 在容器内 `curl <base_url>/v1/models` 测试连通性

**Q2. 整个视频字幕都是空的（segments=0）？**
- ffmpeg 抽出的音频是静音 → 用 `ffmpeg -i <m3u8> -t 30 -vn audio.wav && ffplay audio.wav` 验证
- 模型文件损坏 → 重新下载并校验 sha256

**Q3. 一个长视频生成耗时数小时怎么办？**
- 换更小的模型（small-q5_0 比 medium 快约 2 倍，准确率下降 5~10%）
- 提高 `SUBTITLE_WHISPER_THREADS`，或绑定到性能核（taskset / numactl）
- 暂时不需要字幕的视频在面板里点"禁用"

**Q4. CPU 跑爆影响其它服务？**
worker 是**单线程**的，同一时间只跑一个 whisper 进程。`SUBTITLE_WHISPER_THREADS` 控制单次内部并行度。
如果想全局降级，设 `SUBTITLE_WHISPER_THREADS=2` 把 ASR 限制到 2 核。

**Q5. 想换成本地 ollama 跑翻译？**
```bash
SUBTITLE_TRANSLATE_BASE_URL=http://localhost:11434
SUBTITLE_TRANSLATE_API_KEY=ollama          # ollama 不校验，任意非空字符串
SUBTITLE_TRANSLATE_MODEL=qwen2.5:7b
```

**Q6. 想为某个视频禁用字幕生成？**
管理面板列表中点该行的暂停按钮（PauseCircle 图标），或 `PUT /api/v1/admin/subtitle/jobs/<mediaId>/disabled?value=true`。

