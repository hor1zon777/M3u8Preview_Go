# 阶段 2 修复进度报告：HIGH 级别

> **阶段**：Stage 2 / 4
> **日期**：2026-04-21
> **状态**：已完成
> **编译**：`go build ./...` 通过
> **静态分析**：`go vet ./...` 通过
> **测试**：`go test ./...` 通过（Windows MinGW 不支持 `-race`，改为普通测试）

---

## 1. 修复范围

**本阶段 12 项 + 阶段 1 顺手修的 H13 = 13 项全部 HIGH 完成**

| # | 编号 | 标题 | 文件 | 状态 |
|---|------|------|------|------|
| 1 | H1 | Poster 回调无条件覆盖 poster_url | `internal/app/app.go` | 已修 |
| 2 | H2 | 缩略图 RowsAffected=0 时 webp 孤儿 | `internal/service/thumbnail.go` | 已修 |
| 3 | H3 | upsertCategories slug 冲突回滚整批 | `internal/service/import.go` | 已修 |
| 4 | H4 | Poster Windows 孤儿文件 | `internal/service/poster.go` | 已修 |
| 5 | H5 | FFmpeg 参数注入 | `internal/util/ffmpeg.go` | 已修 |
| 6 | H6 | FFmpeg 用 Background ctx | `internal/service/thumbnail.go`, `poster.go` | 已修 |
| 7 | H7 | Admin 降级 TOCTOU 竞态 | `internal/service/admin.go` | 已修 |
| 8 | H8 | SSE ticket 被所有写接口接受 | `internal/middleware/auth.go`, `internal/handler/backup.go`, `internal/app/app.go` | 已修 |
| 9 | H9 | Backup importSync 无体积上限 | `internal/handler/backup.go` | 已修 |
| 10 | H10 | Backup importUpload 无体积上限 | `internal/handler/backup.go` | 已修 |
| 11 | H11 | SSE 不检测客户端断开 | `internal/handler/backup.go` | 已修 |
| 12 | H12 | PRAGMA 连接回收后失效 | `internal/db/conn.go` | 已修 |
| - | H13 | SSRF DNS 失败 fail-open | 已在阶段 1 C2 一并修复 | 已修 |

**顺手修复**：
| # | 标题 | 原严重性 |
|---|------|---------|
| 顺5 | `PosterDownloader.Stop` 不等 worker | LOW (L6) |
| 顺6 | `PosterDownloader.EnqueueMigrate` 不检查 stopped | MEDIUM (M17) |
| 顺7 | `PosterDownloader.Resolve` 用 Background ctx | MEDIUM (M16) |
| 顺8 | `ThumbnailQueue.Stop` 不等 worker | LOW (L6) |
| 顺9 | `ThumbnailQueue.Enqueue` 不检查 stopped | MEDIUM (M17) |
| 顺10 | `ThumbnailQueue` worker 里无意义的 `time.After(24h)` | LOW (L1) |
| 顺11 | `buildSlug` 对 INT32_MIN 取负失效 | LOW (L2) |
| 顺12 | `download` handler 失败路径临时 ZIP 永不清理 | MEDIUM (M21) |

---

## 2. 变更明细

### H1. Poster 回调无条件覆盖

**修复** (`internal/app/app.go:137-158`)：
- 加 WHERE 条件 `poster_url LIKE 'http://%' OR poster_url LIKE 'https://%'`，仅当仍是外部 URL 时才回写
- 检查 `result.Error`，失败写日志
- 检查 `result.RowsAffected == 0`：说明 admin 已手动改了 poster_url 或记录被删除，清理已下载的本地文件防孤儿

### H2. 缩略图 RowsAffected=0 清理 webp

**修复** (`internal/service/thumbnail.go` NewFFmpegProcessor)：
- `Update` 返回后检查 `RowsAffected == 0`：说明期间用户上传了封面或媒体被删，主动 `os.Remove(outPath)`
- 失败时记录日志；成功清理时也记日志便于追踪

### H3. upsertCategories slug 冲突

**修复** (`internal/service/import.go:353-415`)：
- 新增 `uniqueSlug(tx, base)` 函数：查询是否已被占用，冲突时尝试 `base-1`、`base-2`...`base-99`
- `upsertCategories` 改用 `uniqueSlug`，避免 UNIQUE 约束失败导致整事务回滚

### H4. Poster Windows 孤儿文件

**修复** (`internal/service/poster.go:downloadOnce`)：
- 引入 `closeOnce` / `cleanup` helper：显式 `out.Close()` 后再 `os.Remove(dst)`
- Windows 下不能删除仍被打开的文件（`ERROR_SHARING_VIOLATION`），现在先关再删
- 文件成功写入后显式 close 一次，确保落盘

### H5. FFmpeg 参数注入

**修复** (`internal/util/ffmpeg.go`)：
- 新增 `assertM3U8URLSafe`：强制 URL 以 `http://` 或 `https://` 开头
- `FFProbeDuration` 改用 `-i m3u8URL` 显式指定为输入选项（原先是位置参数，若 URL 以 `-` 开头可能被解释为 ffprobe 选项）
- `FFmpegThumbnail` 也调用 `assertM3U8URLSafe`

### H6. FFmpeg Background ctx

**修复** (`internal/service/thumbnail.go`, `poster.go`)：
- `ThumbnailQueue` 持 `context.Context` + `CancelFunc`，`Stop()` 调用 cancel 中断进行中的 ffmpeg
- `ThumbnailProcessor` 签名改为 `func(ctx, task) error`
- `PosterDownloader` 同样持根 ctx；`downloadOnce` 接 ctx 参数；worker 为每次下载派生子 ctx 加超时
- `Resolve` 内部也用根 ctx 派生超时 ctx，Stop 可中断

### H7. Admin 降级 TOCTOU

**修复** (`internal/service/admin.go:UpdateUser`)：
- 把 `Count(admin)` 检查 + `UPDATE` 放入同一个 `s.db.Transaction`
- Count 改查 `role='ADMIN' AND is_active=true`（禁用的 admin 不算有效 admin）
- Count 的 error 不再被忽略；检查失败直接返回 500
- 已包装的 AppError 直接透传

### H8. SSE ticket 扩权

**修复** (`internal/middleware/auth.go`, `internal/handler/backup.go`, `internal/app/app.go`)：
- 从 `Authenticate` 中移除 `?ticket=` 分支
- 新增 `AuthenticateSSE` 中间件，仅此中间件识别 ticket
- `BackupHandler` 拆分 `Register` / `RegisterSSE` 两个方法
- `app.go` 为 `/admin/backup` 挂普通 `Authenticate`，为 stream 路径挂 `AuthenticateSSE`
- ticket 泄漏到 Referer/日志时不再等同 admin token

### H9 + H10. Backup 上传无体积上限

**修复** (`internal/handler/backup.go`)：
- 新增常量 `maxBackupUploadBytes = 2 << 30`（2 GiB）
- `importSync` 与 `importUpload` 前置检查 `fh.Size`，超限返回 413
- 用 `io.LimitReader(src, maxBackupUploadBytes+1)` 二次防护（防篡改 Content-Length）
- 写入后检查实际字节数

### H11. SSE 不检测客户端断开

**修复** (`internal/handler/backup.go`)：
- `exportStream` / `importStream` 的 `send` 闭包：
  - 每次写入前 `select { case <-ctx.Done(): }` 检查断开
  - `Write` 返回 error 时标记 `cancelled=true`，后续跳过
- 客户端断开后服务端立即停止推送，不再白跑完整 DB 扫描 + ZIP 打包
- 业务层错误在 `cancelled=true` 时不再尝试发送（避免 panic on closed writer）

### H12. PRAGMA 连接回收后失效

**修复** (`internal/db/conn.go`)：
- 改用 DSN 查询参数 `?_pragma=foreign_keys(1)&_pragma=busy_timeout(5000)&_pragma=journal_mode(WAL)&_pragma=synchronous(NORMAL)`
- glebarez/sqlite 驱动在每次建连时会执行这些 PRAGMA，因此 `ConnMaxLifetime=1h` 重建连接后仍然生效
- 首连接上再跑一次 `PRAGMA foreign_keys = ON` 作为防御性校验

---

## 3. 验证结果

### 编译
```
$ go build ./...
（无输出，成功）
```

### 静态分析
```
$ go vet ./...
（无输出，无警告）
```

### 测试
```
$ go test ./...
ok   github.com/hor1zon777/m3u8-preview-go/internal/util   0.713s
```

### 手动验证建议（部署前）
1. **H1**：并发 1 条 admin 改 poster_url + 1 条下载完成回调 → admin 改动保留
2. **H2**：ffmpeg 跑到一半 admin 上传 poster → webp 文件应被清理，日志有 `[thumbnail] skip`
3. **H3**：导入包含 `"Sci-Fi"` 和 `"sci fi"`（slug 均为 `sci-fi`）→ 后者应被分配 `sci-fi-1`
4. **H4**：Windows 下模拟超过 5MB 的 poster → `%TEMP%/poster/*` 应无残留
5. **H5**：传 m3u8Url = `-map 0 -f mpegts pipe:1` → 被 `assertM3U8URLSafe` 拒绝
6. **H6**：Enqueue 1000 个 ffmpeg 任务后 Ctrl+C → 应在数秒内优雅退出（无僵死子进程）
7. **H7**：并发 2 条 admin 降级请求 → 最终仍剩 ≥1 admin
8. **H8**：用 ticket 调 `PUT /admin/settings` → 应返回 401（ticket 仅在 `/admin/backup/*/stream` 识别）
9. **H9/H10**：上传 3 GiB 虚假 multipart 文件 → 返回 413
10. **H11**：SSE 连接打开后 curl 主动中断 → 服务端日志 `exportStream client disconnected, abort`
11. **H12**：等 1h+ 后做外键级联删除 → 外键仍然生效

---

## 4. 下一阶段

进入 Stage 3：修复 21 个 MEDIUM 级 bug。分类推进：
- **4.1 错误处理** (M1-M6)：Take 错误映射为 404、多处 `.Count()` 错误被丢
- **4.2 并发竞态** (M7-M10)：refresh token 轮换、playlist AddItem、favorite Toggle、invalidators 无锁
- **4.3 时区/时间** (M11-M12)：活动统计用本地时区、backup 文件名时区不一致
- **4.4 DoS/上限** (M13-M15)：page 无上界、multipart 无全局上限、SavePoint 不释放
- **4.5 上下文传播** (M16-M17)：Resolve ctx（已顺手修）、Stop 不等 worker（已顺手修）
- **4.6 信息泄露/配置** (M18-M21)：登录枚举、TRUST_CDN 默认 true、空 MIME 绕过、download 清理（已顺手修）

阶段 2 顺手修掉的 M16、M17、M21 可直接标完成；阶段 3 聚焦剩余项。
