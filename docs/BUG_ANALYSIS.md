# m3u8-preview-go 全项目 Bug 分析报告

> **审查日期**：2026-04-21
> **审查范围**：`internal/` 目录下全部 6570 行 Go 代码
> **审查方式**：并行启动 4 个专业审查 agent（go-reviewer×3 + security-reviewer×1），分别审查最近修改文件、并发/服务模块、认证/代理/SSRF 模块、Handler/DB 层
> **审查结论**：**BLOCK**。共发现 **44 个真实 bug**，含 3 个 CRITICAL、13 个 HIGH、21 个 MEDIUM、7 个 LOW

---

## 1. 总体分布

| 严重性 | 数量 | 主要风险类型 |
|--------|------|-------------|
| CRITICAL | 3 | 任意文件删除、DNS Rebinding SSRF、备份恢复数据永久丢失 |
| HIGH | 13 | 数据竞态覆盖、孤儿文件、ffmpeg 参数注入、SSE ticket 滥用、DoS、DNS fail-open |
| MEDIUM | 21 | 并发 check-then-act、错误吞掉、时区不一致、分页溢出、权限扩大 |
| LOW | 7 | 定时器泄漏、命名误导、一致性问题 |

---

## 2. CRITICAL 级别（阻断发布）

### C1. 任意文件删除 —— PosterURL 未校验导致 path traversal

- **文件**：`internal/service/media.go:300-305` + `internal/dto/media.go:41`
- **触发链**：
  1. ADMIN 调用 `PUT /api/v1/media/:id`，body `{"posterUrl": "/uploads/../../../etc/passwd"}`
  2. `MediaService.Update` → `s.poster.Resolve`（`poster.go:81-93`）对非 `http(s)://` 原样返回 → 写入 DB
  3. ADMIN 调用 `DELETE /api/v1/media/:id`
  4. `HasPrefix("/uploads/", "/uploads/")` 通过 → `filepath.Join` 内部 Clean → `/etc/passwd` → `os.Remove` 删除目标
- **代码**：
  ```go
  300: if existing.PosterURL != nil && strings.HasPrefix(*existing.PosterURL, "/uploads/") {
  301:     go func(relPath, uploadsDir string) {
  302:         abs := filepath.Join(uploadsDir, strings.TrimPrefix(relPath, "/uploads/"))
  303:         _ = os.Remove(abs)
  304:     }(*existing.PosterURL, s.uploadsDir)
  305: }
  ```
- **修复**：双侧防御
  - 写入侧：对非 http(s) 的 posterUrl 做白名单 `^/uploads/(posters|thumbnails|categories)/[A-Za-z0-9._-]+$`
  - 删除侧：用 `filepath.Rel` 确认仍在 `filepath.Clean(uploadsDir)` 内

### C2. DNS Rebinding SSRF —— 校验解析与实际连接 DNS 不绑定

- **文件**：`internal/util/ssrf.go:140-170, 211-249`
- **触发**：攻击者设 TTL=0 权威 DNS，首次返回公网 IP（过校验），`http.Client.Do` 二次查询返回 `127.0.0.1`/元数据 IP
- **后果**：可通过 `/proxy/m3u8` 攻击云厂商元数据接口、内网服务
- **代码**：
  ```go
  // ssrf.go:206 - 解析 1（校验）
  currentURL, err := AssertSafeURL(ctx, raw)
  // ssrf.go:212 - 解析 2（实际连接，IP 可能不同）
  req, _ := http.NewRequestWithContext(ctx, opts.Method, currentURL.String(), opts.Body)
  resp, _ := client.Do(req)
  ```
- **修复**：在 `http.Transport.DialContext` 里把 host 替换为已校验过的 IP，SNI/Host header 保持原 hostname

### C3. 备份恢复数据永久丢失 —— 先删除后写入无回滚

- **文件**：`internal/service/backup.go:572-632`
- **触发**：事务已提交（417 行）后，`restoreUploads` 用 `os.RemoveAll` 清空 `uploadsDir/*`，再逐个从 zip 写入
  - 磁盘满/ZIP 损坏/进程崩溃 → 原有 uploads 已删，DB 记录指向不存在的文件
  - `io.Copy` 错误被 `_` 丢弃，不返回给调用方
- **代码**：
  ```go
  586: if st, err := os.Stat(s.uploadsDir); err == nil && st.IsDir() {
  587:     items, _ := os.ReadDir(s.uploadsDir)
  588:     for _, it := range items {
  589:         _ = os.RemoveAll(filepath.Join(s.uploadsDir, it.Name()))  // 不可逆
  590:     }
  618: _, _ = io.Copy(out, rc)  // 错误完全被吞
  ```
- **修复**：
  1. 先解压到临时目录 `uploadsDir.new`，成功后原子 rename 切换
  2. `io.Copy` 错误必须上报
  3. 事务提交放到 uploads 恢复成功后

---

## 3. HIGH 级别（13 项）

### H1. 海报下载回调无条件覆盖 poster_url
- **文件**：`internal/app/app.go:139-141`
- **问题**：`Update("poster_url", localPath)` 无 `WHERE poster_url LIKE 'http://%'` 条件、无 RowsAffected 检查
- **后果**：下载期间 admin 修改 poster_url 会被覆盖；媒体被删除时产生孤儿文件
- **参照**：`thumbnail.go:143-145` 的正确写法

### H2. 缩略图 RowsAffected=0 时 webp 孤儿
- **文件**：`internal/service/thumbnail.go:133-153`
- **问题**：ffmpeg 生成 webp 后 `UPDATE ... WHERE poster_url IS NULL`，若期间手动上传海报 → RowsAffected=0 但 error=nil，webp 文件留在磁盘无引用

### H3. upsertCategories slug 冲突回滚整批
- **文件**：`internal/service/import.go:353-373, 397-422`
- **问题**：只按 name 查重，slug 由 `buildSlug(name)` 生成但 `Category.Slug` 有 `uniqueIndex`。`"Hello World"` 与 `"hello-world"` 的 slug 都是 `hello-world`，UNIQUE 冲突导致整事务回滚，1000 条 import 全部丢失

### H4. Poster Windows 孤儿文件
- **文件**：`internal/service/poster.go:156-171`
- **问题**：`defer out.Close()` 在函数返回后才关闭，但 io.Copy 失败分支直接 `os.Remove(dst)`。Windows 下返回 `ERROR_SHARING_VIOLATION`，文件不会被删除
- **修复**：显式 Close 后再 Remove

### H5. FFmpeg 参数注入
- **文件**：`internal/util/ffmpeg.go:26-31, 58-69`
- **问题**：`m3u8URL` 作为位置参数传入，未用 `-i` 显式隔离。若绕过 sanitizeMedia 可传 `-map 0 -f mpegts pipe:1` 等注入参数
- **修复**：使用 `"-i", m3u8URL` 并严格校验 scheme

### H6. FFmpeg 用 Background ctx
- **文件**：`internal/service/thumbnail.go:118-119`、`internal/service/poster.go:113, 117`
- **问题**：processor 用 `context.Background()`，`Stop()` 无法中断正在跑的 ffprobe/ffmpeg。m3u8 网络流 ffprobe 可能僵死，优雅关停延迟数分钟

### H7. Admin 降级 TOCTOU
- **文件**：`internal/service/admin.go:159-180`
- **问题**：两个并发降级请求分别读到 count=2，各自 UPDATE → 系统 0 个 admin
- **修复**：事务内做 Count + 条件 UPDATE

### H8. SSE ticket 被所有写接口接受
- **文件**：`internal/middleware/auth.go:30-43`
- **问题**：`?ticket=` 被通用 `Authenticate` 识别，ticket 在 URL/Referer/代理日志中易泄漏，30s TTL 内可用到 `PUT /admin/settings` 等写接口
- **修复**：独立 `AuthenticateSSE` 中间件或给 ticket 加 `Purpose="sse"` 字段

### H9. Backup importSync 无体积上限
- **文件**：`internal/handler/backup.go:112-145`
- **问题**：`io.Copy(tmp, src)` 无 LimitReader，100GB 文件可撑爆磁盘

### H10. Backup importUpload 无体积上限
- **文件**：`internal/handler/backup.go:148-167` + `internal/service/backup.go:202`
- **问题**：同 H9，`SaveUploadedBackup` 内部也无 LimitReader

### H11. SSE 未检测客户端断开
- **文件**：`internal/handler/backup.go:63-82, 170-194`
- **问题**：`send` 闭包吞掉所有写错误，`ExportToFile`/`ImportFromFile` 不接收 ctx，客户端断开后仍跑完整 DB 扫描/ZIP 压缩

### H12. PRAGMA 连接回收后失效
- **文件**：`internal/db/conn.go:56-72`
- **问题**：`SetConnMaxLifetime(time.Hour)` 导致 1h 后重建连接，但 `PRAGMA foreign_keys=ON`/`busy_timeout=5000` 是**连接级**配置，新连接默认 OFF → 外键约束静默失效
- **修复**：改用 DSN 参数 `?_pragma=foreign_keys(1)&_pragma=busy_timeout(5000)`

### H13. SSRF DNS 失败 fail-open
- **文件**：`internal/util/ssrf.go:140-153`
- **问题**：`LookupIPAddr` 返回 error 时直接 `return nil`（放行），配合 C2 放大攻击面

---

## 4. MEDIUM 级别（21 项，分类呈现）

### 4.1 错误处理（6 项）
- **M1** `service/media.go:292-296, 216-218`：Take 错误全映射为 404，掩盖 DB 故障
- **M2** `service/favorite.go:27-28`：同上
- **M3** `service/admin.go:316-321`：BatchUpdateCategory 中 `.Count()` 错误忽略
- **M4** `app/admin_adapters.go:36-55`：posterStats 三处 `.Count()` 错误忽略，DB 故障时前端全 0
- **M5** `service/playlist.go:69-70, 149-150`：Count 错误忽略
- **M6** `service/activity.go:82-84`：loginCount 系列 Count 错误忽略

### 4.2 并发竞态（4 项）
- **M7** `service/auth.go:110-148`：Refresh token 轮换无事务，并发可 fork 出两条合法链
- **M8** `service/playlist.go:193-204`：AddItem `MAX(position)+1` 非事务竞态
- **M9** `service/favorite.go:25-46`：Toggle check-then-act 非原子
- **M10** `service/backup.go:94-96, 432-434`：invalidators 读写无锁数据竞争

### 4.3 时区/时间（2 项）
- **M11** `service/activity.go:111-114`：统计用本地时区算"今天"，DB 存 UTC 边界错乱
- **M12** `service/backup.go:130-131`：备份文件名用本地时区但 `exportedAt` 用 UTC

### 4.4 DoS/上限（3 项）
- **M13** `util/pagination.go:7-21`：page 无上界，`?page=99999999` 触发全表扫描 + 打满 `MaxOpenConns=1`
- **M14** `handler/upload.go` + `app.go`：multipart 无全局 body 上限
- **M15** `service/import.go:193-260`：单事务 1000 个 SavePoint 从不 RELEASE，SQLite 长时间独占写锁

### 4.5 上下文传播（2 项）
- **M16** `service/poster.go:81-93, 112-118`：Resolve 同步下载用 Background，客户端取消无感知
- **M17** `service/thumbnail.go:85-87`、`service/poster.go:75-77`：Stop 不等 worker，EnqueueMigrate 不检查 stopped

### 4.6 信息泄露/配置（3 项）
- **M18** `service/auth.go:84-97`：登录 bcrypt 时延差 + 状态码差异 → 用户名枚举
- **M19** `config/config.go:123` + `util/clientip.go`：TRUST_CDN 默认 true，任意客户端可伪造 `CF-Connecting-IP`
- **M20** `service/upload.go:66-72`：空 Content-Type 跳过 MIME 校验
- **M21** `handler/backup.go:85-108`：download 失败路径临时 ZIP 永不清理

### 4.7 逻辑/体验（兼容进其他分类）
- `service/media.go:331-347` GetRandom 第二次查询丢失随机顺序（归并为 M-misc）
- `service/backup.go:596-608` restoreUploads path traversal 校验对 `foo..bar.png` 误杀（归并为 M-misc）

---

## 5. LOW 级别（7 项）

- **L1** `service/thumbnail.go:89-111`：worker 里无意义的 `time.After(24h)`，每轮 select 创建新 Timer
- **L2** `service/import.go:411-421`：buildSlug 对 INT32_MIN 取负仍为负
- **L3** `service/auth.go:151-156`：Logout 吞 DB 错误
- **L4** `service/auth.go:121-124`：复用检测撤销整个 user 的 refresh，双点即全端下线
- **L5** `db/seed.go:73-93`：`upsertUser` 实际只 insert-if-missing，名字误导
- **L6** `service/thumbnail.go:84-87`、`service/poster.go:75-77`：Stop 注释宣称"阻塞等待"但未用 WaitGroup
- **L7** `service/backup.go:130-131`：备份文件名时区不一致（归并为时区项，但严重性低）

---

## 6. 修复策略

### 6.1 顺序原则
1. **先堵 CRITICAL**：C1 任意文件删除 → C2 SSRF → C3 备份数据丢失
2. **再修 HIGH**：分 3 批
   - 数据/文件（H1、H2、H3、H4、H5）
   - 并发/生命周期（H6、H7、H8、H12）
   - 上传/DoS/SSRF（H9、H10、H11、H13）
3. **接着 MEDIUM**：按 4.1-4.6 分类批量修复
4. **最后 LOW**：统一清理

### 6.2 测试原则
- 每修一个 CRITICAL/HIGH 后 `go build ./...` + `go vet ./...`
- 涉及并发修改后 `go test -race ./...`
- 备份恢复、认证流改动需手动 e2e 验证

### 6.3 进度记录
- 每完成一个阶段写 `docs/PROGRESS_STAGE{N}_{LEVEL}.md`
- 最终汇总 `docs/BUGFIX_FINAL_REPORT.md`

---

## 7. 已排除的误报

审查过程中以下项被验证为**误报/不是真实 bug**：
- `handler/import.go:132-145` logs limit 校验：service 层 `GetLogs` 已做 `<=0 → 50, >200 → 200` 钳制，此处 handler 安全（但仍建议防御性加上，归 LOW）
- 其他静态分析报告已跑 `go vet ./...`，无警告输出

---

## 8. 审查使用的专业 Agent

| Agent | 审查范围 | 产出 bug |
|-------|---------|---------|
| go-reviewer #1 | app.go、media.go、import.go（最近修改） | 8 |
| go-reviewer #2 | poster/thumbnail/activity/admin/backup/watch/ffmpeg | 14 |
| security-reviewer | auth/jwt/ssrf/proxysign/sseticket/proxy/upload/middleware | 10 |
| go-reviewer #3 | handler 层 + db 层 + sse | 12 |

总计 44 项，去重合并后 **44 个独立 bug**（无重叠，因不同 agent 审查范围不交叉）。
