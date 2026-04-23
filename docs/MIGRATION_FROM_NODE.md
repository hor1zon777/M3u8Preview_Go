# 从 Node 版（M3u8Preview_R）迁移到 Go 版教程

> 适用版本：
> - 源：`M3u8Preview_R`（Express + Prisma + TypeScript，SQLite）
> - 目：`m3u8-preview-go`（Gin + GORM + Go，SQLite）
>
> 迁移核心原则：**数据库字段 / Cookie 名 / JWT 签名 / 代理签名** 全部逐字节兼容，已发出的 token 和签名链接跨版本继续有效。
> 典型停机窗口：**2-5 分钟**（看 DB 体积）。用灰度方案可做到零停机。

---

## 目录

- [0. 迁移前检查](#0-迁移前检查)
- [1. 备份现有数据](#1-备份现有数据)
- [2. 环境变量映射与配置迁移](#2-环境变量映射与配置迁移)
- [3. 部署 Go 版（全新拉起 / 原地替换）](#3-部署-go-版)
- [4. 首次启动：AutoMigrate + seed](#4-首次启动automigrate--seed)
- [5. 验证登录链路](#5-验证登录链路)
- [6. 回滚预案](#6-回滚预案)
- [7. 零停机灰度方案](#7-零停机灰度方案)
- [8. 行为差异清单（必读）](#8-行为差异清单必读)
- [9. 常见问题](#9-常见问题)

---

## 0. 迁移前检查

执行前逐项确认：

### 0.1 Node 版侧

- [ ] 记录当前版本 git commit hash，以便回滚时精确定位
- [ ] 当前 `M3u8Preview_R/.env` 里的三个密钥已备案：
  - `JWT_SECRET`
  - `JWT_REFRESH_SECRET`
  - `PROXY_SECRET`
- [ ] 确认 `CORS_ORIGIN` 值（逗号分隔多值写法要保留原样）
- [ ] 确认 Cookie 名仍是 `refreshToken`（两个版本都用这个）
- [ ] 如果 R 版开启了 CDN 反代（Cloudflare 等），记录 `TRUST_CDN` 当前值

### 0.2 Go 版侧

- [ ] 已拉取或克隆 `m3u8-preview-go`；分支对齐到稳定 tag
- [ ] 宿主机有 Docker + Docker Compose v2（`docker compose version` 返回 ≥ 2.x）
- [ ] 端口 `DOCKER_PORT`（默认 80）不被占用
- [ ] 磁盘预留 ≥ `<现有 data 大小> × 2`（备份 + 新数据目录）

### 0.3 数据侧

- [ ] `data/m3u8preview.db` 文件大小 ≤ 5GB（超过请走[灰度方案](#7-零停机灰度方案)，否则迁移时 WAL 合并时间会超标）
- [ ] `uploads/` 下的海报 + 缩略图体积已知，迁移目录也要预留

---

## 1. 备份现有数据

**无论走哪条迁移路径，先做两份备份**，其中一份离机存储（U 盘 / 对象存储）。

### 1.1 热备份（不停机）

```bash
cd /path/to/M3u8Preview_R

# SQLite 自带一致性备份：即使 app 在写也安全
sqlite3 data/m3u8preview.db ".backup data/m3u8preview.backup.db"

# 封面 + 缩略图
tar -czf uploads-$(date -u +%Y%m%dT%H%M%SZ).tgz uploads/
```

### 1.2 冷备份（停机，更干净）

```bash
# 1. 停 R 版
docker compose stop   # 或 pm2 stop / systemctl stop

# 2. 拷整个数据目录（此时 WAL 已合并）
cp -a data data.backup-$(date -u +%Y%m%dT%H%M%SZ)
cp -a uploads uploads.backup-$(date -u +%Y%m%dT%H%M%SZ)

# 3. 先保持 R 版停机状态，直到 Go 版验证通过（或重启 R 版继续提供服务）
```

---

## 2. 环境变量映射与配置迁移

### 2.1 核心映射（必须原样保留）

| R 版 `.env`（TS） | Go 版 `.env`（同名） | 备注 |
|---|---|---|
| `JWT_SECRET` | `JWT_SECRET` | **原样保留**，否则老 access token 失效 |
| `JWT_REFRESH_SECRET` | `JWT_REFRESH_SECRET` | **原样保留**，否则所有用户被迫重新登录 |
| `PROXY_SECRET` | `PROXY_SECRET` | **原样保留**，否则已分发的 m3u8 签名 URL 失效 |
| `DATABASE_URL` | `DATABASE_URL` | `file:./data/m3u8preview.db` 可直接复用 |
| `CORS_ORIGIN` | `CORS_ORIGIN` | **原样保留**，多值逗号分隔格式两版一致 |
| `PORT` | `PORT` | 两版默认都是 3000 |
| `NODE_ENV` | `NODE_ENV` | 生产务必 `production`（触发密钥强度校验） |
| `ADMIN_SEED_PASSWORD` | `ADMIN_SEED_PASSWORD` | 已存在 admin 用户时本字段被忽略 |
| `DEMO_SEED_PASSWORD` | `DEMO_SEED_PASSWORD` | 同上 |

### 2.2 R 版独有字段（Go 版无需映射，可丢弃）

| R 版字段 | 处理 |
|---|---|
| `NODE_VERSION` / `PNPM_VERSION` | Go 版不吃 Node |
| `DATABASE_PROVIDER=sqlite` | Go 版仅支持 SQLite，无需配置 |
| `PRISMA_LOG_LEVEL` | Go 版没有 Prisma |
| `NEXT_TELEMETRY_DISABLED` | 无 Next.js |

### 2.3 Go 版新增字段（按需配置）

```ini
# 登录加密协议（新增）—— 首次启动自动生成，不需手填
# ECDH_PRIVATE_KEY_PATH=/data/ecdh.pem

# Cookie Secure 标志动态判定（新增）
# 默认：CORS_ORIGIN 含 https:// 自动启用；反代场景也按 X-Forwarded-Proto 动态判定
# COOKIE_SECURE=true     # 强制启用
# COOKIE_SECURE=false    # 强制关闭（仅内网 HTTP 测试用）

# 反代属主（新增）
# TRUST_CDN=true    # 默认 true；直接暴露到公网或未部署 CDN 时设 false

# 并发度（新增，建议保留默认）
# THUMBNAIL_CONCURRENCY=5
# POSTER_MIGRATION_CONCURRENCY=2

# Docker 权限逃生舱（新增，bind mount 场景用）
# SKIP_CHOWN=1          # entrypoint 不再改属主
```

### 2.4 生成最终 `.env`

建议在 Go 版项目目录下复制一份 R 版的 `.env`，然后：

1. 删掉 2.2 里 R 版独有的字段
2. 按需添加 2.3 里 Go 版新增字段
3. 保存为 `m3u8-preview-go/.env`

---

## 3. 部署 Go 版

根据原 R 版部署方式分两种路径。

### 3.A Docker Compose 部署（推荐，也是 R 版主流部署方式）

```bash
cd /path/to/m3u8-preview-go

# 1. 准备目录（bind mount 要求）
mkdir -p data uploads

# 2. 把备份的 DB 和 uploads 放进来
cp /path/to/M3u8Preview_R/data/m3u8preview.db data/
cp -a /path/to/M3u8Preview_R/uploads/. uploads/

# 3. 写入 .env（见 2.4）
cp .env.example .env
$EDITOR .env

# 4. 拉镜像 + 启动（docker-compose.yml 已内置 app + nginx 两个服务）
docker compose pull
docker compose up -d

# 5. 实时查看启动日志
docker compose logs -f app
```

典型健康输出：

```
[config] loaded from .env ...
[db] connected: file:/data/m3u8preview.db
[db] AutoMigrate: 14 tables synced
[db] seed: admin exists, skip
[app] ECDH private key loaded from /data/ecdh.pem
[gin] listening on 127.0.0.1:3000
```

看到 `listening on ...` 即启动成功。

#### 3.A.1 从 R 版镜像直接切换

如果 R 版原本用 `docker-compose.yml`，改造步骤：

```bash
# 1. 停 R 版栈（保留数据卷）
cd /path/to/M3u8Preview_R
docker compose down   # 不要加 -v，否则会删卷

# 2. 启动 Go 版栈
cd /path/to/m3u8-preview-go
docker compose up -d
```

**R 版的命名卷（`db-data` / `uploads`）不会被 Go 版直接挂载**——Go 版改用 bind mount（`./data` / `./uploads`）。
必须按[从命名卷升级到 bind mount](../README.md#从命名卷升级到-bind-mount)一节把卷内容迁进来。

### 3.B 裸机 / 非 Docker 部署

```bash
cd /path/to/m3u8-preview-go

# 需求：Go 1.25+；前端构建用 Node 20 + pnpm
# 编译 Go 二进制
go build -o m3u8preview-go ./cmd/server

# 构建前端
cd web && pnpm install && pnpm -F client build && cd ..

# 拷备份数据
cp /path/to/M3u8Preview_R/data/m3u8preview.db data/
cp -a /path/to/M3u8Preview_R/uploads/. uploads/

# 写 .env
cp .env.example .env
$EDITOR .env

# 启动
./m3u8preview-go
```

静态资源由外部 nginx 托管，反代 `127.0.0.1:3000`——配置参考 `nginx.conf`。

---

## 4. 首次启动：AutoMigrate + seed

Go 版启动时 `AutoMigrate` 会兜底所有缺失字段 / 索引，对存量库完全幂等：

- Prisma 已有的表 / 字段 / 唯一索引：保持原样
- Go 版新增的字段（例如 `users.password_hash_algo`）：ADD COLUMN NOT NULL DEFAULT 兜底
- 新增的 `system_settings` 默认键（`enableCaptcha` / `captchaEndpoint` 等）：通过 `ensureDefaultSettings()` 仅插入缺失项

**第一次启动 DB 被修改是正常的**，改动均为 additive——回滚到 R 版不会因为多了字段而 crash（Prisma 对未知字段默认忽略）。

验证方法：

```bash
docker compose exec app sqlite3 /data/m3u8preview.db ".schema users"
# 看 password_hash / refresh_token_family / password_hash_algo 等字段都在
```

---

## 5. 验证登录链路

### 5.1 API 层直测

```bash
# 1. 挑战握手（新增：Go 版加密登录第一步）
curl -s http://localhost/api/v1/auth/challenge \
  -H 'Content-Type: application/json' \
  -d '{"fingerprint":"0000000000000000000000000000000000000000000000000000000000000000"}' | jq
# 应返回 {serverPub, challenge, ttl:60}
```

### 5.2 浏览器登录

- 打开 Go 版前端地址
- 用 **R 版已有账号** 登录
- 预期：
  - 登录成功，`refreshToken` cookie 下发
  - 刷新页面登录态保持
  - 已收藏 / 已播放历史 / 已创建播放列表都在

如果登录失败但账号密码正确，检查：
- `JWT_SECRET` 是否**与 R 版完全一致**（包括尾部换行、空格）
- `CORS_ORIGIN` 是否精确匹配浏览器地址栏（含协议、域名、端口）

### 5.3 m3u8 签名链接兼容性

```bash
# R 版生成的签名链接直接复制过来，在 Go 版应该仍然播放
# 仅当 signature TTL（默认 4h）内有效
curl -I "http://localhost/api/v1/proxy/sign?url=...&expires=...&sig=..."
# 预期 302 或 200，返回 m3u8 内容
```

---

## 6. 回滚预案

**最大的两个回滚触发点**：
1. 登录全量失败（密钥不一致 / 数据结构变更）
2. 媒体播放全量失败（proxy 签名变更）

### 6.1 立刻回滚（< 5 分钟）

```bash
# 1. 停 Go 版
cd /path/to/m3u8-preview-go
docker compose down

# 2. 恢复 R 版数据
cp data/m3u8preview.db /path/to/M3u8Preview_R/data/m3u8preview.db

# 3. 启 R 版
cd /path/to/M3u8Preview_R
docker compose up -d
```

### 6.2 为什么回滚是安全的

- Go 版 AutoMigrate 只 **追加字段**，不删改；R 版 Prisma 读到额外字段默认忽略，不影响行为
- Cookie / JWT / proxy 签名逐字节兼容，已发出的凭据两版都认
- 用户密码哈希算法没变（bcrypt，同 cost），两版 `verify` 结果一致

### 6.3 为什么 Go 版 seed 的新 `system_settings` 不会污染 R 版

- Go 版新增的 `enableCaptcha` / `captchaEndpoint` 等键 R 版不读取
- R 版 admin 面板只显示 R 版白名单内的设置项，新键被忽略
- Go 版 admin 白名单更大但向后兼容 R 版所有键

---

## 7. 零停机灰度方案

适用场景：公网 7×24 服务，不能接受分钟级停机。

### 7.1 架构

```
[CDN / L7 LB]
   ├── 90% → R 版（现网，端口 3000）
   └── 10% → Go 版（新版，端口 3001，同一套 data/ uploads/ bind mount）
```

两个版本**挂同一份 SQLite 文件**（bind mount 或 NFS 共享卷）。

### 7.2 前置条件

- SQLite 必须在 WAL 模式下（Go 版默认开启，R 版 Prisma 也默认开启）
- 两个进程访问同一个 `data/m3u8preview.db`
- **ECDH 私钥文件（`data/ecdh.pem`）是 Go 版独占**，R 版不读取

### 7.3 灰度步骤

```bash
# 1. Go 版用独立端口起，共享数据卷
cp docker-compose.yml docker-compose.canary.yml
# 编辑 docker-compose.canary.yml，把 app 的 ports 改成 3001:3000，container_name 改名避免冲突
docker compose -f docker-compose.canary.yml up -d

# 2. nginx / CDN 层加金丝雀权重
# （nginx upstream 配置 example，留作参考）
upstream m3u8_backend {
    server 127.0.0.1:3000 weight=9;  # R 版
    server 127.0.0.1:3001 weight=1;  # Go 版
}

# 3. 观察 24h：错误率 / 登录成功率 / m3u8 播放成功率
# 4. 无异常则把 weight 切到 1:9，再切到 0:10
# 5. 下线 R 版
docker compose -f /path/to/M3u8Preview_R/docker-compose.yml down
```

### 7.4 并发写的注意事项

SQLite WAL 支持多读一写，两个进程同时写会串行化，不是数据一致性问题但会增加 P99 延迟。**避免在高峰切灰度**。

---

## 8. 行为差异清单（必读）

迁移后以下行为会变，请提前通知团队：

### 8.1 登录链路（变化最大）

- **R 版**：`POST /auth/login` 直接提交 `{username, password}` 明文（走 HTTPS 不算泄露，但 DevTools / 代理可见）
- **Go 版**：增加 `GET /auth/challenge` 握手，登录密码用 **ECDH P-256 + HKDF + AES-256-GCM** 加密，DevTools 和代理看不到明文
- 对用户无感，但：
  - 前端必须加载 WASM crypto 核心（首次 ~35KB gzip）
  - **R 版的 API 客户端脚本不能直接打 Go 版登录接口**；需要改写客户端或保留 R 版 API 临时适配层（本项目未提供，按需自行实现）

### 8.2 新增 CAPTCHA

- 默认关闭。admin 面板可配 `captchaEndpoint` 启用（需外部 Portcullis 服务）
- 启用后登录 / 注册接口要带 `captchaToken` 字段，否则 `400 请求无效`

### 8.3 Cookie Secure 自动判定

- **R 版**：`COOKIE_SECURE` 未设置时不加 `Secure` 标志
- **Go 版**：未设置时根据 `CORS_ORIGIN` 协议 + 运行时 `X-Forwarded-Proto` 动态判定
  - HTTPS 反代 → 自动 `Secure=true`
  - 纯 HTTP 内网 → `Secure=false`
  - 特殊场景可显式写 `COOKIE_SECURE=true/false` 覆盖

### 8.4 上传体积上限

- **R 版**：poster 上传无硬上限
- **Go 版**：
  - poster 上传：沿用 nginx `client_max_body_size 20m`
  - backup 导入：nginx 单独开到 `2g`（Go 层 `maxBackupUploadBytes = 2 GiB`）

### 8.5 SPA 路由兼容

- 前端路径里带点号（`/media/movie.2024`、`/tags/sci-fi.v2`）在 Go 版可以正常 fallback 到 `index.html`
- R 版某些 nginx 配置下会 404，Go 版默认支持（扩展名白名单机制）

### 8.6 备份恢复期间的数据可见性

- **R 版**：不支持在线恢复
- **Go 版**：`admin/backup/import` 采用"staging 子目录 + 按顶层逐项 rename"原子切换，恢复期间旧数据保持可读；失败自动回滚

---

## 9. 常见问题

**Q1. 登录后立即被踢回登录页，循环 302？**
大概率 `COOKIE_SECURE` 与访问协议不一致：
- 访问 `http://` 地址但 `CORS_ORIGIN` 配了 `https://` → cookie 带 Secure 下发，浏览器不回传
- 解决：`CORS_ORIGIN` 必须与浏览器地址栏一致，或者显式 `COOKIE_SECURE=false`

**Q2. 老账号能登录但账号信息显示空白？**
检查 `users` 表是否完整（`sqlite3 data/m3u8preview.db "SELECT count(*) FROM users"`）。
Go 版不会在 migration 里动用户数据，数据缺失必然是 DB 本身没拷全。

**Q3. 已收藏 / 播放历史等自关联数据丢失？**
检查关联表：`favorites` / `watch_histories` / `playlists` / `playlist_items`，均应跟随 `m3u8preview.db` 一起迁移。如果只拷了主表没拷关联表，就会出现此现象。

**Q4. proxy 签名链接在 Go 版返回 403 Invalid signature？**
- `PROXY_SECRET` 没和 R 版对齐
- 或者 R 版用了老格式（签名算法不同）。`git log -- packages/server/src/proxy` 查一下 R 版是否有过重大改动；Go 版对齐 R 版**最新**的签名格式

**Q5. `admin/backup/export` 导出的 ZIP 能被 R 版导入吗？**
**不能**。Go 版 backup 新增了 `password_hash_algo` 等字段，JSON schema 不完全向下兼容。
备份之间的可移植性：R→Go 不支持（R 版没有 export 功能），Go→Go 完全支持。

**Q6. 迁移完能否删 R 版目录？**
建议至少保留 **2 周**，确认运行稳定、没有回滚需求再删。存储占用可以接受的话，保留整个 R 目录有备无患。

**Q7. systemSettings 里的 `captchaSecretKey` 如何保护？**
- 仅 admin 可读（`GET /admin/settings` 要求 ADMIN 角色）
- backup export 会包含 secret（这是刻意的——否则恢复后 captcha 不可用）
- backup 文件本身请按秘钥级别保管

---

## 附：最小操作时间线

适合参考，针对中等规模（DB 500MB 左右）部署：

| 时间 | 动作 | 停机 |
|---|---|---|
| T-30min | 通知用户维护窗口，公网挂维护页 | 否 |
| T-5min  | R 版 `docker compose stop` | 是 |
| T+0     | `cp data uploads` 到 Go 版目录 | 是 |
| T+2min  | Go 版 `docker compose up -d` | 是 |
| T+3min  | 观察日志 `listening on ...` | 是 |
| T+5min  | 撤维护页，开放流量 | 否 |
| T+10min | 监控 30 分钟错误率 / 登录成功率 | 否 |
| T+24h   | 无异常，关闭 R 版并归档 | 否 |

遇到问题走[回滚预案](#6-回滚预案)即可恢复服务。
