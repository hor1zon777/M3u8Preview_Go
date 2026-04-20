# Go 重写进度记录

> 对应计划文件：`C:\Users\Captain\.claude\plans\snazzy-munching-eich.md`
>
> 原项目：`E:\Desktop\foldder\Code\Claude\M3u8Preview_Go\M3u8Preview_R`（Express + Prisma + SQLite）
>
> 新项目：`E:\Desktop\foldder\Code\Claude\M3u8Preview_Go\m3u8-preview-go`（Gin + GORM + glebarez/sqlite）

---

## 阶段 A：脚手架（已完成）

落地内容：

- `cmd/server/main.go` — 启动入口：加载 config → 连 DB → AutoMigrate → 构建 Gin engine → 优雅关闭
- `internal/config/config.go` — 读取 `.env`（`joho/godotenv`），生产环境强校验密钥长度
- `internal/app/app.go` — Gin engine 组装：中间件链 + `/api/v1` 路由组 + CORS/gzip/secure 头 + NoRoute 兜底
- `internal/dto/response.go` — `APIResponse[T]` 统一信封 + `PageMeta` 分页元信息
- `internal/middleware/error.go` — `AppError` 类型 + 全局 `ErrorHandler` + `Recovery`（带 eventId）
- `internal/handler/health.go` — `GET /api/health`
- `go.mod` / `go.sum` / `.env.example` / `.gitignore`

交付验证：`go build ./cmd/server` 成功、二进制可 `curl http://localhost:3000/api/health` 返回 OK。

---

## 阶段 B：数据层（已完成）

落地内容：

- `internal/model/enum.go` — `Role` / `MediaStatus` / `ImportFormat` / `ImportStatus` / `SettingKey` 常量
- `internal/model/user.go` — `User` / `RefreshToken` / `LoginRecord`
- `internal/model/media.go` — `Media` / `Category` / `Tag` / `MediaTag`
- `internal/model/playlist.go` — `Playlist` / `PlaylistItem` / `Favorite` / `WatchHistory`
- `internal/model/system.go` — `SystemSetting` / `ImportLog`
- `internal/db/conn.go` — GORM 连接 + SQLite PRAGMA（`foreign_keys=ON` + WAL）
- `internal/db/migrate.go` — `AutoMigrate` 全部模型 + 唯一索引 + 复合索引（对齐 Prisma 9 个迁移）
- `internal/db/seed.go` — 首次启动创建 admin/demo + 默认分类 + `ensureDefaultSettings()`

交付验证：首次启动生成 `data/m3u8preview.db`，`SELECT * FROM users` 看到两个账号。

---

## 阶段 C：通用工具（已完成）

落地内容：

- `internal/util/jwt.go` — 签发/验签带 `kid` 路由主/上一代密钥，`Purpose` 区分 access/refresh
- `internal/util/proxysign.go` — `HMAC-SHA256(secret, url\nexpires\nuserId)`，`hmac.Equal` 防时序
- `internal/util/sseticket.go` — TTL 30s、max 1000、一次性消费、sync.Map + mutex
- `internal/util/ssrf.go` — IPv4/IPv6 私有段覆盖 + `.local/.internal/.localhost` 拒绝 + `safeFetch`（最多 3 跳重定向，每跳重解析 IP）
- `internal/util/uaparser.go` — `mileusna/useragent` 解析，device 为空填 `Desktop`
- `internal/util/clientip.go` — `trustCdn=true` 时优先 `CF-Connecting-IP`/`True-Client-IP`，否则 `X-Forwarded-For` 第一跳
- `internal/util/timefmt.go` — `FormatISO` 毫秒 ISO8601
- `internal/util/ratelimit_bucket.go` — 令牌桶 100/60s（阶段 I poster 下载使用）
- `internal/util/hash.go` / `internal/util/pagination.go`
- `internal/sse/writer.go` — 统一 `data: <json>\n\n` + `X-Accel-Buffering: no`
- `internal/sse/progress.go` — 导入/导出阶段常量（与 R 版字符串完全一致）
- `internal/util/*_test.go` — 覆盖 jwt / proxysign / sseticket / ssrf 核心分支

交付验证：`go test ./internal/util/... -race` 全部通过。

---

## 阶段 D：中间件（已完成）

落地内容：

- `internal/middleware/auth.go` — `Authenticate` 支持 Bearer + `?ticket=` 分支；`OptionalAuth`；`RequireRole`；`CurrentUserID`/`CurrentRole`
- `internal/middleware/ratelimit.go` — 固定窗口 `WindowLimiter` + `ConditionalRateLimit`（`singleflight` 合并 DB 读 + 1s 缓存）
- `internal/middleware/validator.go` — 注册 `username_chars` / `password_complex` 自定义校验
- `app.go` 中挂载：CORS/gzip（代理与 SSE 路径跳过）/secure 头 / NoRoute

交付验证：构建通过，`curl -H "Authorization: Bearer bad" /api/v1/auth/me` 返回 401。

---

## 阶段 E：Auth（已完成）

落地内容：

- `internal/dto/auth.go` — `RegisterRequest` / `LoginRequest` / `ChangePasswordRequest` / `UserResponse` / `AuthResponse` / `RegisterStatusResponse` / `SSETicketResponse`
- `internal/service/auth.go` — register / login / refresh（含家族复用检测 → 强制全端登出）/ logout / getProfile / changePassword / getRegisterStatus + `LoginRecord` 写入（IP、UA、device）
- `internal/handler/auth.go` — 路由 `register / login / refresh / logout / register-status / me / change-password / sse-ticket`，统一 `setRefreshCookie` / `clearRefreshCookie`
- Cookie 名保持 `refreshToken`，SameSite=Lax，生产 Secure

交付验证：`POST /auth/register` → `POST /auth/login` → `POST /auth/sse-ticket` 全链路通。

---

## 阶段 F：核心业务 CRUD（已完成 — 2026-04-20）

### Service 层（已补齐）

- `internal/service/media.go` — 分页/筛选/排序/原子自增 views/Recent/Random/Artists；`ThumbnailEnqueuer` + `PosterResolver` 接口留空实现（阶段 I 注入真实实现）
- `internal/service/category.go` — 分类与标签 CRUD（两者模式一致）；`mapUniqueErr` 处理 SQLite UNIQUE 冲突 → 409
- `internal/service/favorite.go` — toggle / check / list（按 userId 分页）
- `internal/service/watch.go` — `UpsertProgress`（`ON CONFLICT(user_id, media_id)`）/ List / Continue / ProgressMap / GetByMedia / Clear / DeleteOne
- `internal/service/playlist.go` — GetPublic / GetOwned / GetByID（owner 或 public）/ Create / Update / Delete / AddItem / RemoveItem / Reorder

#### 本阶段对 service 的兼容性修正

- `PlaylistService.Update(id, operatorID, req)` — 新增 `operatorID` 参数做 owner 校验（对齐 TS 原版）
- `PlaylistService.Delete(id, operatorID)` — 同上
- `PlaylistService.RemoveItem(playlistID, operatorID, mediaID)` — 改按 `mediaID` 删条目（对齐 `DELETE /:id/items/:mediaId`）

### Handler 层（本阶段落地）

- `internal/handler/media.go` — public(findAll/recent/random/findById) + authed(artists) + views + admin(create/update/delete/thumbnail-501-stub)
- `internal/handler/category.go` — public(list/get) + admin(create/update/delete)
- `internal/handler/tag.go` — 同上
- `internal/handler/favorite.go` — authed(toggle/check/list)
- `internal/handler/watch.go` — authed(updateProgress / list / continue / progress-map / getByMedia / clear / deleteOne)；`DELETE /clear` 必须注册在 `DELETE /:id` 前
- `internal/handler/playlist.go` — public(public 列表) + optional(`/:id/items`) + authed(findAll/findById/addItem/removeItem/reorder) + admin(create/update/delete)

### 路由挂载（`internal/app/app.go` 更新）

| 路径 | 方法 | 中间件链 | 备注 |
|---|---|---|---|
| `/api/v1/media` | GET | global limiter | 分页列表，匿名可读 |
| `/api/v1/media/recent` / `/random` | GET | global limiter | 同上 |
| `/api/v1/media/:id` | GET | global limiter | 详情 |
| `/api/v1/media/artists` | GET | + authenticate | 需登录 |
| `/api/v1/media/:id/views` | POST | + optionalAuth + viewsLimiter(按 userId / ip 分桶) | 刷量限制 |
| `/api/v1/media` | POST/PUT/DELETE/:id/thumbnail | + authenticate + requireAdmin | 管理员写入 |
| `/api/v1/categories`、`/tags` | GET/POST/PUT/DELETE | 公开查询 + admin 写入 | 同 TS 版 |
| `/api/v1/favorites/**` | * | + authenticate | 全部需登录 |
| `/api/v1/history/**` | * | + authenticate | 全部需登录 |
| `/api/v1/playlists/public` | GET | global limiter | 匿名 |
| `/api/v1/playlists/:id/items` | GET | + optionalAuth | 公开 playlist 匿名可看 |
| `/api/v1/playlists`、`/:id`、items、reorder | GET/POST/DELETE/PUT | + authenticate | owner 校验在 service 层 |
| `/api/v1/playlists`（body） | POST/PUT/DELETE/:id | + authenticate + requireAdmin | 创建/更新/删除 playlist |

### 交付验证

- `go build ./cmd/server` — 通过（binary 写入 `/tmp/server-test`）
- `go test ./... -race` — `internal/util` 全绿；其他包暂无测试
- IDE gopls 报 "cannot find package" 系 workspace 未包含子目录所致，不影响命令行编译

---

## 后续阶段（已全部完成）

### 阶段 G：Upload / Import（已完成 — 2026-04-20）

**落地：**

- `internal/dto/import.go` — `ImportItem` / `ImportPreviewResponse` / `ImportExecuteRequest` / `ImportResult` / `ImportLogResponse` / `UploadPosterResponse`
- `internal/parser/csv.go` — `encoding/csv` + BOM 去除 + 中英文表头别名
- `internal/parser/excel.go` — `xuri/excelize/v2` 读第 1 个 sheet + 中英文表头别名
- `internal/parser/json.go` — 根节点数组 / `{items}` / `{media}` 三种包装；字段别名（m3u8_url、url、poster、artistName、作者 等）
- `internal/parser/text.go` — `#` 注释 + pipe 分隔 2/3/4/5 段 + URL-only 下从 filename 推 title
- `internal/service/upload.go` — 仅允许 jpg/jpeg/png/gif/webp；≤10MB；MIME 强校验；UUID 命名
- `internal/handler/upload.go` — `POST /api/v1/upload/poster`（admin）
- `internal/service/import.go` —
  - `DetectAndParseFile`（扩展名分派 + xlsx magic bytes `PK\x03\x04` 校验）
  - `DetectAndParseBody`（body.content + format）
  - `Preview`（逐条校验 + 错误列表）
  - `Execute`（事务前 `PosterResolver.Resolve` 预下载、事务内批量 upsert Category/Tag + 创建 Media + MediaTag、事务外 `ThumbnailEnqueuer.Enqueue` 排队；日志写入）
  - `TemplateCSV` / `TemplateJSON` 模板
- `internal/handler/import.go` — `/template/:format`（public）/ `/preview` / `/execute` / `/logs`（admin）

**路由：**

| 路径 | 方法 | 中间件 |
|---|---|---|
| `/api/v1/upload/poster` | POST | authenticate + requireAdmin |
| `/api/v1/import/template/:format` | GET | （匿名） |
| `/api/v1/import/preview` | POST | authenticate + requireAdmin |
| `/api/v1/import/execute` | POST | authenticate + requireAdmin |
| `/api/v1/import/logs` | GET | authenticate + requireAdmin |

**风险与后续：**

- 当前 `PosterResolver` / `ThumbnailEnqueuer` 仍是 no-op stub；阶段 I 会替换为真正的下载与 ffmpeg 队列
- execute 单次上限 1000 条，超限返回 400（对齐 TS H7）
- xlsx magic bytes 校验拦截伪装 zip（对齐 importController.ts 的 `validateFileMagic`）

### 阶段 H：Proxy（已完成 — 2026-04-20）

**落地：**

- `internal/service/proxy.go` — 扩展名白名单缓存（30s TTL）、`ValidateMediaExists`（精确 + 前缀兜底）、`ValidateSegmentDomain`、`InvalidateExtensionsCache`（admin settings 改白名单后调用）
- `internal/handler/proxy.go` —
  - `GET /proxy/sign`：`AssertSafeURL` → 扩展名检查 → media 存在校验 → `ProxySigner.Sign` → 返回 `/api/v1/proxy/m3u8?url=<encoded>&expires=&sig=`
  - `GET /proxy/m3u8`：验签 → `AssertSafeURL`（每跳重解）→ segment 域名白名单 → `SafeFetch(maxRedirects=3)` → m3u8 逐行重写 / segment 流式 `io.Copy`
  - Range 头白名单（仅允许 `bytes=<n>-<n?>`），`surrit.com`/子域注入 `Referer: https://missav.ws`
  - m3u8 路径禁共享缓存 `Cache-Control: no-store`；segment 用 `private, max-age=600`
  - 超时：连接 15s，整体 120s；`context.DeadlineExceeded` → 504
- `app.go` 挂路由：
  - `/proxy/sign`：`signLimiter(15m/60)` + Authenticate
  - `/proxy/m3u8`：`proxyLimiter(15m/1500)` + Authenticate
  - 两条路径均在 `gzip.WithExcludedPathsRegexs` 中跳过压缩（保持二进制流正确）
- `Deps.ProxySvc` 暴露给后续 admin settings 更新代理白名单用

**对齐 TS 原版的关键点：**

| TS 行为 | Go 实现 |
|---|---|
| 签名绑定 userId，跨用户失效 | `compute(rawURL, expires, userId)` 严格对齐 |
| 每跳 DNS 重解 | `util.SafeFetch` CheckRedirect 禁用 + 手动循环 |
| m3u8 `URI="..."` 只替换 URI | `rewriteURIAttribute` 用 regex |
| 非注释、非空行整行替换 | `proxyPrefix + encoded + sign` |
| segment 域名白名单 | `m3u8_url LIKE 'scheme://host%'` 命中 ACTIVE media |
| 扩展名白名单可配置 | `ProxyService.AllowedExtensions()` 读 `system_settings.proxyAllowedExtensions` |

### 阶段 I：Admin + 备份 + 缩略图（已完成 — 2026-04-20）

**I-1 Admin 主体：**

- `internal/dto/admin.go` — dashboard / users / settings / media batch / activity 聚合 DTO
- `internal/service/admin.go` — Dashboard（6 并行聚合 goroutine）/ ListUsers（含 favorites/playlists/watchHistory 三项 count）/ UpdateUser（最后 ADMIN 降级、禁自停用）/ DeleteUser（禁删 ADMIN）/ GetSettings / UpdateSetting / AdminListMedia / BatchDelete / BatchUpdateStatus / BatchUpdateCategory（上限均来自 DTO 校验 `max=500`）
- `internal/service/activity.go` — CreateLoginRecord（走 util.ParseUserAgent）/ ListUserLoginRecords / UserSummary / Aggregate（10 并行聚合 + topN 关联查询 + username/mediaTitle 补齐）
- `internal/handler/admin.go` — 27 条路由全部挂到 `/api/v1/admin/*`

**I-2 Backup 导出/导入：**

- `internal/service/backup.go` —
  - `ExportToStream`（同步打包 ZIP 到 writer）
  - `ExportToFile`（SSE 进度回调 → 临时文件 + `downloadId`，10 分钟 TTL）
  - `SaveUploadedBackup`（admin 上传 → `restoreId`）
  - `ImportFromFile`（校验 version=1.0 + 白名单表 + zip-bomb 防护 2GB/50k 条目 + 事务清空/写入 + 恢复 uploads 时 path traversal 防护）
  - `RegisterInvalidator` 钩子：import 完成后清 RateLimit + Proxy 扩展名缓存
- `internal/handler/backup.go` — 6 条路由：`export` / `export/stream` / `download/:id` / `import` / `import/upload` / `import/stream/:id`
- SSE 统一头：`Content-Type: text/event-stream` + `X-Accel-Buffering: no` + 立即 Flush

**I-3 Thumbnail + Poster 队列：**

- `internal/service/thumbnail.go` — `ThumbnailQueue(concurrency=5)`：bounded channel + 去重 enqueuedIDs + active/queued/processed/failed 计数
- `internal/service/poster.go` — `PosterDownloader(concurrency=2)`：`util.TokenBucket(100 req/min)` + `util.SafeFetch` + 5MB 上限 + 扩展名白名单 + `fourhoi.com`/`surrit.com` 注入 `Referer: https://missav.ws`；作为 `MediaService.PosterResolver` 注入（真实下载取代 passthrough）
- `internal/app/admin_adapters.go` — `posterStatsDB` 适配器，按 `poster_url` 是否 `http(s)://` 前缀统计 external / local / total
- `handler/admin.go` 的 thumbnail/poster 端点全部接上真实队列统计（生成接口目前仅入队；实际 ffmpeg 生成逻辑在后续阶段按需接入）

**前端 API 覆盖：**

| 前端调用 | Go 端对应 |
|---|---|
| `/api/v1/admin/dashboard` | 6 并行聚合 |
| `/api/v1/admin/users` CRUD | 业务约束全到位 |
| `/api/v1/admin/settings` PUT enableRateLimit | 后续 invalidate 钩子已注册 |
| `/api/v1/admin/settings` PUT proxyAllowedExtensions | 自动清 `ProxyService` 缓存 |
| `/api/v1/admin/media/batch-*` | 批量 ≤500 + GORM 事务保护 |
| `/api/v1/admin/backup/*` | ZIP 导出/导入 + SSE |
| `/api/v1/admin/posters/*` | 队列 + TokenBucket + safeFetch |
| `/api/v1/admin/thumbnails/*` | 队列框架（ffmpeg 生成待扩展） |

### 阶段 J：Docker + 文档（已完成 — 2026-04-20）

**J-1 Dockerfile（多阶段 + 非 root）：**

- Stage 1 `golang:1.25-alpine`：`go mod download` → `CGO_ENABLED=0` 构建 `/out/server`（`-trimpath -ldflags="-s -w"`，无符号表）
- Stage 2 `alpine:3.20`：`ffmpeg`（缩略图）+ `curl`（HEALTHCHECK）+ `su-exec`（权限下降）+ `tzdata` + `ca-certificates`
- `addgroup appgroup` / `adduser appuser` 并预建 `/data` 与 `/app/uploads/{posters,thumbnails}`
- `HEALTHCHECK`：30s 轮询 `curl -sf /api/health`
- **root 启动 → entrypoint chown volume → su-exec 降到 appuser**，避免 bind-mount owner 不匹配写入失败
- 默认 `ENV`：`PORT=3000 BIND_ADDRESS=0.0.0.0 NODE_ENV=production DATABASE_URL=file:/data/m3u8preview.db DATA_DIR=/data UPLOADS_DIR=/app/uploads TZ=UTC`

**J-2 docker-entrypoint.sh（对齐 R 版）：**

- 纯 `/bin/sh`，无 bash 依赖，`set -eu`
- `mkdir -p "$DATA_DIR" "$UPLOADS_DIR/{posters,thumbnails}"` 兜底首次挂载
- `id -u != 0` 时直接 `exec "$@"`；root 时 `chown -R appuser:appgroup` 后 `su-exec appuser:appgroup "$@"`

**J-3 docker-compose.yml（生产）：**

- 两服务：`m3u8preview-go-app`（本地 build）+ `m3u8preview-go-nginx`（官方 `nginx:alpine`）
- `network_mode: host`（与 R 版一致，让 Go 版也能用 `127.0.0.1:3000` upstream）
- 直接挂载 `../M3u8Preview_R/nginx.conf` 与 `../M3u8Preview_R/packages/client/dist`（前端零修改复用）
- 卷：`db-data:/data`、`uploads:/app/uploads`
- 环境变量覆盖：密钥 / 并发 / CORS / 种子密码 / `TRUST_CDN`

**J-4 docker-compose.dev.yml（本地开发）：**

- 仅 app 容器，直接映射 `${PORT:-3000}:3000`
- bind-mount `./data` 与 `./uploads` 到宿主，方便 IDE 查看 SQLite
- `NODE_ENV=development`、`TRUST_CDN=false`、`BIND_ADDRESS=0.0.0.0`
- 默认用占位密钥（≥32 字符以绕过生产校验假定）；无 nginx

**J-5 .dockerignore：**

- 排除 `.git`、`.idea`、`.vscode`、`node_modules`、`server.exe`、`vendor/`、`bin/`、`dist/`
- 运行期数据：`data/` `uploads/` `.env*`、日志 `*.log`、`tmp/`
- 开发文档 `docs/`（README 通过 `COPY . .` 保留）
- Dockerfile / compose 自身（不需要打进镜像）

**J-6 README：**

- 目录索引 + 快速开始（本地 / Docker 生产 / Docker 开发 三条路径）
- **从 TS 版迁移 5 步清单**（备份 DB → 同步 `.env` → AutoMigrate → systemSettings 保留 → 灰度建议）
- 环境变量 6 张表（基础 / 密钥 / CORS·CDN / 容量·并发 / Docker·种子）
- **生产上线 10 项 checklist**
- **FAQ 7 条**（权限 / 401 / 代理 403 / 504 / 扩展名白名单缓存 / dashboard 慢 / 缩略图）
- 路由覆盖表 + TS 版兼容矩阵
- 未落地项矩阵（缩略图 ffmpeg 生成 / admin watch-history / 扫库入队 / metrics）

**交付验证：**

- `go build ./cmd/server` — 通过（42 MB 二进制）
- `go vet ./...` — 通过，无警告
- `go test ./internal/util/...` — 通过（Windows 本地无 cgo，`-race` 需 Linux/macOS 或开启 CGO 的环境；CI 跑 race 版本）
- Dockerfile `docker build .` 依赖 Docker Desktop 实际环境，未在会话中跑；所有指令来源于 R 版已验证的等价命令

---

### 阶段 J+：前端一体化（已完成 — 2026-04-20）

把前端源码、shared 类型包与 nginx 配置全部拷进 Go 项目下，让仓库可独立构建部署，
不再对 `../M3u8Preview_R` 存在路径依赖。

**落地：**

- `web/client/` — 从 `M3u8Preview_R/packages/client` 拷贝的 React 18 + Vite 6 前端源码
- `web/shared/` — 从 `M3u8Preview_R/packages/shared` 拷贝的 TS 类型 + zod 校验
- `web/package.json` — workspace root，提供 `build:shared` / `build:client` / `dev -w client`
- `web/.gitignore` — 排除 `node_modules`、`dist`、`dist-image`、`*.tsbuildinfo`、本地 `.env`
- `web/client/vite.config.ts` — API proxy 从原来的 `13000` 改为默认 `3000`，加载项目根 `.env`
- `nginx.conf` — 复制原版并调整注释，upstream 仍指 `127.0.0.1:3000`

**Dockerfile 重构为 3 阶段：**

| Stage | 基础镜像 | 产出 |
|---|---|---|
| 1. `web-builder` | `node:20-alpine` | `web/client/dist/`（`npm ci -w` + `build:shared` + `build:client`） |
| 2. `go-builder` | `golang:1.25-alpine` | `/out/server`（CGO=0 + trimpath + `-s -w`） |
| 3. `runner` | `alpine:3.20` | `server` + `web/dist` + `web/dist-image` + ffmpeg/su-exec/tzdata/ca-certs |

**docker-entrypoint.sh 新增 dist 同步：**

- 每次启动 `rm -rf /app/web/dist/* && cp -a /app/web/dist-image/. /app/web/dist/`
- 这样命名卷 `client-dist` 在镜像重建后也能被自动刷新（Docker 命名卷只在首次创建时从镜像拷贝）
- root 场景下顺带 `chown` web dist 让 appuser 可读

**docker-compose.yml 改造：**

- **去掉** `../M3u8Preview_R/nginx.conf` 与 `../M3u8Preview_R/packages/client/dist` 的跨目录挂载
- 改用本项目自带的 `./nginx.conf` + 新增 `client-dist` 命名卷
- nginx 只读挂 `client-dist:/usr/share/nginx/html`
- `CORS_ORIGIN` 默认改 `http://localhost`（生产走 nginx 同源，不需要跨域）

**.dockerignore 补充：**

- `web/**/node_modules/`、`web/**/dist/`、`web/**/dist-image/`、`web/**/*.tsbuildinfo`、`web/.env*`
- `nginx.conf` 也排除（通过 `docker-compose.yml` 挂载而非 COPY 进镜像）

**.gitignore 补充：** `web/**/node_modules/`、`web/**/dist/`、`web/package-lock.json` 等

**README 重写：**

- 3 种启动方式（A 本机 HMR / B Docker 全栈 / C 仅容器化后端）
- 目录结构加入 `web/` 树
- FAQ 新增 Q2~Q4（Docker 构建 npm 卡住 / nginx 404 / volume 不刷新）
- 生产 checklist 加入 "前端换域名同步 CSP"
- 开发命令分后端 / 前端两组

**回归验证：**

- `go build ./cmd/server` — 通过
- `go vet ./...` — 通过
- `go test ./internal/util/...` — 通过
- 前端因未在会话中装 npm deps，`web/` 的 `vite build` 留给 Dockerfile 或本地验证

---

## 进度汇总

| 阶段 | 状态 | 完成时间 |
|---|---|---|
| A. 脚手架 | ✅ | 2026-04-19 前 |
| B. 数据层 | ✅ | 2026-04-19 前 |
| C. 通用工具 | ✅ | 2026-04-19 前 |
| D. 中间件 | ✅ | 2026-04-20 |
| E. Auth | ✅ | 2026-04-20 |
| F. 核心业务 CRUD | ✅ | 2026-04-20 |
| G. Upload/Import | ✅ | 2026-04-20 |
| H. Proxy | ✅ | 2026-04-20 |
| I. Admin/备份/缩略图 | ✅ | 2026-04-20 |
| J. Docker/文档 | ✅ | 2026-04-20 |
| J+. 前端一体化（web/ + nginx.conf 内置） | ✅ | 2026-04-20 |
