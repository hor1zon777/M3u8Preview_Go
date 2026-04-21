# 阶段 4 修复进度报告：LOW 级别 + 最终汇总

> **阶段**：Stage 4 / 4
> **日期**：2026-04-21
> **状态**：已完成（全部 44 bug 修复收官）
> **编译**：`go build ./...` 通过
> **静态分析**：`go vet ./...` 通过
> **测试**：`go test ./...` 通过

---

## 1. 修复范围

本阶段仅剩 1 项需主动修复，其余 6 项在前三阶段顺手完成：

| # | 编号 | 标题 | 修复阶段 | 状态 |
|---|------|------|---------|------|
| 1 | L1 | ThumbnailQueue worker `time.After(24h)` 每轮泄漏 | 阶段 2 | 已修 |
| 2 | L2 | `buildSlug` INT32_MIN 取负失效 | 阶段 2 | 已修 |
| 3 | L3 | `Logout` 吞 DB 错误 | 阶段 3 (M7 连带) | 已修 |
| 4 | L4 | 复用检测撤销整个 user refresh | 阶段 3 (M7 内联) | 已修 |
| 5 | L5 | `db/seed.go` `upsertUser` 命名误导 | **本阶段** | 已修 |
| 6 | L6 | Stop 注释宣称阻塞但未用 WaitGroup | 阶段 2 | 已修 |
| 7 | L7 | Backup 文件名时区不一致 | 阶段 3 (M12 归并) | 已修 |

---

## 2. 本阶段变更

### L5. `upsertUser` 改名为 `ensureUser`

**位置**：`internal/db/seed.go:73`

**问题**：函数名 `upsertUser` 暗示 update-or-insert，但实际只 insert-if-missing。
若线上 admin 被误改为 `USER` 或被禁用，重跑 seed 无法修复，且名字会让新人误以为它会同步。

**修复**：
- 函数改名 `upsertUser` → `ensureUser`
- 注释加上明确说明："实际只做 insert-if-missing，不会更新已存在行的 role/is_active/password"
- 指出如需修正 admin role，应另写 `fixAdminUser`
- 两处调用点同步更新

---

## 3. 验证

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

---

## 4. 总工程量统计

### 4.1 修复 bug 总数：44

| 严重性 | 数量 | 顺手修复占比 |
|--------|------|-------------|
| CRITICAL | 3 | 0/3 |
| HIGH | 13 | 1/13 (H13 在 C2 一并修) |
| MEDIUM | 21 | 3/21 (M16/M17/M21 在阶段 2 顺手修) |
| LOW | 7 | 6/7 (仅 L5 本阶段修) |

### 4.2 涉及文件（修改了实际代码的，按改动量降序）

| 文件 | 修改的 bug 数 |
|------|-------------|
| `internal/service/backup.go` | C3, H9-ref, H11-ref, M10, M12 |
| `internal/service/auth.go` | M7, M18, L3, L4 |
| `internal/service/media.go` | C1, M1, 随修顺序 |
| `internal/service/poster.go` | H4, H6, L6, M16, M17 |
| `internal/service/thumbnail.go` | H2, H6, L1, L6, M17 |
| `internal/service/import.go` | H3, L2, M15 |
| `internal/service/admin.go` | H7, M3 |
| `internal/service/playlist.go` | M5, M8 |
| `internal/service/favorite.go` | M2, M9 |
| `internal/service/activity.go` | M6, M11 |
| `internal/service/upload.go` | M20 |
| `internal/util/ssrf.go` | C2, H13 |
| `internal/util/ffmpeg.go` | H5 |
| `internal/util/pagination.go` | M13 |
| `internal/middleware/auth.go` | H8 |
| `internal/handler/backup.go` | H8, H9, H10, H11, M21 |
| `internal/handler/auth.go` | L3（连带） |
| `internal/app/app.go` | H1, H8, M14 |
| `internal/app/admin_adapters.go` | M4 |
| `internal/db/conn.go` | H12 |
| `internal/db/seed.go` | L5 |
| `internal/config/config.go` | M19 |

共 22 个文件改动。

### 4.3 新增公共 API
- `util.lookupSafeIP`, `util.buildPinnedClient`
- `util.MaxSafePage` 常量
- `service.validateLocalPosterURL`, `service.resolveLocalUploadPath`
- `service.uniqueSlug`
- `service.ThumbnailProcessor` 类型
- `middleware.AuthenticateSSE`
- `handler.BackupHandler.RegisterSSE`
- `db.ensureUser`（原 `upsertUser` 改名）

### 4.4 修改了外部行为的函数（需 caller 配合调整）
| 函数 | 旧签名 | 新签名 |
|------|--------|--------|
| `BackupService.restoreUploads` | `int` | `(int, error)` |
| `AuthService.Logout` | `void` | `error` |
| `NewFFmpegProcessor` | `func(task) error` | `func(ctx, task) error` |

已在 handler/app 层同步更新。

---

## 5. 产出文档

| 文件 | 用途 |
|------|------|
| `docs/BUG_ANALYSIS.md` | 44 项 bug 分析报告（修复前） |
| `docs/PROGRESS_STAGE1_CRITICAL.md` | Stage 1 进度 |
| `docs/PROGRESS_STAGE2_HIGH.md` | Stage 2 进度 |
| `docs/PROGRESS_STAGE3_MEDIUM.md` | Stage 3 进度 |
| `docs/PROGRESS_STAGE4_LOW.md` | Stage 4 进度（本文件） |
| `docs/BUGFIX_FINAL_REPORT.md` | 最终汇总报告 |

---

## 6. 遗留与建议

详见 `BUGFIX_FINAL_REPORT.md` 第 8 节。简要：

- **必做**：CI 加一个 Linux `go test -race ./...` 任务（Windows MinGW 不支持 race）
- **推荐**：增补 auth、backup、SSE 三处的集成测试
- **长期**：JWT claims 内嵌 familyID 彻底修复 refresh 复用边界情况

---

**阶段 4 完成。44 bug 全部修复收官。**
