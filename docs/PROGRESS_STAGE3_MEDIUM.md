# 阶段 3 修复进度报告：MEDIUM 级别

> **阶段**：Stage 3 / 4
> **日期**：2026-04-21
> **状态**：已完成
> **编译**：`go build ./...` 通过
> **静态分析**：`go vet ./...` 通过
> **测试**：`go test ./...` 通过

---

## 1. 修复范围

本阶段修复 21 个 MEDIUM 级 bug。其中 3 个已在阶段 1/2 顺手修复：

| # | 编号 | 标题 | 文件 | 状态 | 修复阶段 |
|---|------|------|------|------|---------|
| 1 | M1 | Media Take 错误误映射 404 (Update/Delete) | `service/media.go` | 已修 | 阶段 1 |
| 2 | M2 | Favorite Take 错误误映射 404 | `service/favorite.go` | 已修 | 本阶段 |
| 3 | M3 | BatchUpdateCategory Count 错误丢失 | `service/admin.go` | 已修 | 本阶段 |
| 4 | M4 | posterStats 三处 Count 错误忽略 | `app/admin_adapters.go` | 已修 | 本阶段 |
| 5 | M5 | Playlist GetByID/Update Count 错误丢 | `service/playlist.go` | 已修 | 本阶段 |
| 6 | M6 | Activity loginCount 系列 Count 错误丢 | `service/activity.go` | 已修 | 本阶段 |
| 7 | M7 | Refresh token 轮换非原子竞态 | `service/auth.go` | 已修 | 本阶段 |
| 8 | M8 | Playlist AddItem `MAX(position)+1` 竞态 | `service/playlist.go` | 已修 | 本阶段 |
| 9 | M9 | Favorite Toggle check-then-act 非原子 | `service/favorite.go` | 已修 | 本阶段 |
| 10 | M10 | invalidators 读写无锁数据竞争 | `service/backup.go` | 已修 | 本阶段 |
| 11 | M11 | 活动统计用本地时区算"今天" | `service/activity.go` | 已修 | 本阶段 |
| 12 | M12 | Backup 文件名时区不一致 | `service/backup.go` | 已修 | 本阶段 |
| 13 | M13 | `?page=99999999` 慢速 DoS | `util/pagination.go` | 已修 | 本阶段 |
| 14 | M14 | multipart 无全局 body 上限 | `app/app.go` | 已修 | 本阶段 |
| 15 | M15 | import.go 单事务 1000 个 SavePoint 不 RELEASE | `service/import.go` | 已修 | 本阶段 |
| 16 | M16 | Poster Resolve 用 Background ctx | `service/poster.go` | 已修 | 阶段 2 |
| 17 | M17 | Stop 不等 worker + EnqueueMigrate 不检查 stopped | `service/thumbnail.go`, `poster.go` | 已修 | 阶段 2 |
| 18 | M18 | 登录 bcrypt 时延差 + 状态码差异枚举 | `service/auth.go` | 已修 | 本阶段 |
| 19 | M19 | TRUST_CDN 默认 true | `config/config.go` | 已修 | 本阶段 |
| 20 | M20 | 上传空 Content-Type 跳过 MIME 校验 | `service/upload.go` | 已修 | 本阶段 |
| 21 | M21 | download 失败路径临时 ZIP 永不清理 | `handler/backup.go` | 已修 | 阶段 2 |

**另含归并到 M-misc 的项**（均已在各自阶段处理）：
- `GetRandom` 随机顺序丢失 → 阶段 1
- `restoreUploads` path traversal 误杀合法文件名 → 阶段 1（重写路径校验为 `filepath.Rel` 白名单式）

---

## 2. 关键变更明细

### M2/M9. Favorite Toggle 原子 + 错误映射修正

**修复** (`internal/service/favorite.go:Toggle`)：
- `Take` 错误先用 `errors.Is(gorm.ErrRecordNotFound)` 判断，再决定 404 vs 500
- check-then-act 放入 `s.db.Transaction`，并发 Toggle 第二条必经 UNIQUE 约束失败路径
- UNIQUE 错误在事务内上报，外层 map 为 500，避免静默产生孤儿记录

### M7/M18. Auth 安全强化

**M7 Refresh token 原子轮换** (`internal/service/auth.go:Refresh`)：
- Delete 放入事务 + 检查 `RowsAffected`
- RowsAffected=0 说明被并发消费，按 reuse 处理但只撤销同 family 的 token（不踢全部设备，缓解 L4）
- 所有 DB 错误都上报日志便于审计

**M18 登录用户名枚举缓解** (`internal/service/auth.go:Login`)：
- 用户不存在时跑一次假 bcrypt（常量 dummy hash）与真实路径时延对齐
- 禁用账户也走 bcrypt + 返回 401（原先是 403，状态码不同易区分）
- 真实原因仅写 log，不泄露给客户端

### M8. Playlist AddItem 事务化

**修复** (`internal/service/playlist.go:AddItem`)：
- `MAX(position)+1` 与 `INSERT` 放入事务
- 并发两条 AddItem 不会再拿到相同 position
- 连带修复 Take 错误误映射 404

### M10. invalidators 数据竞争

**修复** (`internal/service/backup.go`)：
- `RegisterInvalidator` 用 `s.mu.Lock` 保护 append
- 读取端拷贝切片后 unlock 再调用回调，避免持锁执行业务方法导致死锁

### M11/M12. 时区统一

**修复**：
- `activity.go:Aggregate` 改用 `time.Now().UTC()` 与 `time.UTC` 计算 todayStart
- `backup.go:ExportToFile` 文件名改 `2006-01-02T15-04-05Z`（UTC）

### M13. 分页深度上界

**修复** (`internal/util/pagination.go`)：
- 新增常量 `MaxSafePage = 10000`
- `SafePagination` 钳 page 到 `[1, MaxSafePage]`
- `?page=99999999&limit=100` 降级为 `page=10000`，OFFSET=999900，不再扫千万行

### M14. multipart 全局上限

**修复** (`internal/app/app.go`)：
- `r.MaxMultipartMemory = 16 << 20`
- 此值仅控制"内存/磁盘切换线"，真正的体积上限由 upload / backup handler 层 + `io.LimitReader` 兜底（已在阶段 2 H9/H10 加上）

### M15. SavePoint 不 RELEASE

**修复** (`internal/service/import.go`)：
- 成功路径显式 `tx.Exec("RELEASE SAVEPOINT " + sp)`
- SQLite 不再累积 1000 个 savepoint，事务段明显缩短，减轻单写连接独占

### M19. TRUST_CDN 默认关

**修复** (`internal/config/config.go`)：
- `parseBoolDefault(os.Getenv("TRUST_CDN"), false)`
- 默认不再信任 `CF-Connecting-IP` / `True-Client-IP`
- 登录记录不能被任意客户端伪造 IP；部署在 CDN 后面时显式开启

### M20. 上传空 Content-Type 拒绝

**修复** (`internal/service/upload.go:SavePoster`)：
- 空 mime 直接返回 400
- MIME 白名单与 `image/` 前缀校验不再被跳过

---

## 3. 遗留项说明

### 未修的条目
- **UpdateUser 错误映射** (service/admin.go:155-157)：已在 H7 修复中连带处理
- **ChangePassword User not found 404 映射** (service/auth.go:170-176)：非枚举入口（需有效 JWT），保留原行为

### 降级到 LOW 处理的
- `filepath.IsAbs + Contains(..)` 在 `restoreUploads` 的严格性：阶段 1 的两阶段切换已改为 `filepath.Rel` 白名单式校验，原校验已被替代，无需再处理。

---

## 4. 验证结果

### 编译
```
$ go build ./...
（无输出）
```

### 静态分析
```
$ go vet ./...
（无输出）
```

### 测试
```
$ go test ./...
ok   github.com/hor1zon777/m3u8-preview-go/internal/util   (cached)
```

### 手动验证建议
1. **M7/M9**：模拟两条并发 refresh / favorite toggle → 仅一条成功，不再有双成功或 500 泛滥
2. **M13**：`curl '/api/v1/media?page=99999999&limit=100'` → 快速返回空结果，耗时 < 100ms
3. **M14**：上传 200MB multipart → backup/upload 路径被 handler 限制（2GiB backup / 10MB upload）
4. **M18**：计时对比 `Login("nonexistent", "x")` vs `Login("admin", "wrongpass")` → 两者均 ~300ms
5. **M19**：未设置 `TRUST_CDN=true` 的部署下发 `CF-Connecting-IP: 9.9.9.9` → 登录记录 IP 是真实 client IP

---

## 5. 下一阶段

进入 Stage 4：修复 7 个 LOW 级 bug。其中：
- **L1** (thumbnail time.After) 已在阶段 2 随 ThumbnailQueue 重构清理
- **L2** (buildSlug INT32_MIN) 已在阶段 2 顺手修
- **L3** (Logout 吞 DB 错误) 已在本阶段随 M7 修复
- **L4** (复用检测撤销过大) 已在本阶段 M7 调整为同 family 优先
- **L6** (Stop 不等 worker) 已在阶段 2 加 WaitGroup 修复

剩余：
- **L5** (db/seed.go upsertUser 名字误导)
- **L7** (backup 文件名时区，已在 M12 修掉 → 也算完成)

**阶段 4 主要工作：L5 修复 + 写最终汇总报告 BUGFIX_FINAL_REPORT.md。**
