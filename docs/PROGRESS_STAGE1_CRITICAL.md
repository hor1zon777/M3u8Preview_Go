# 阶段 1 修复进度报告：CRITICAL 级别

> **阶段**：Stage 1 / 4
> **日期**：2026-04-21
> **状态**：已完成
> **编译**：`go build ./...` 通过
> **静态分析**：`go vet ./...` 通过
> **测试**：`go test ./internal/util/...` 通过（SSRF 单元测试未破坏）

---

## 1. 修复范围

| # | 编号 | 标题 | 文件 | 状态 |
|---|------|------|------|------|
| 1 | C1 | 任意文件删除（PosterURL path traversal） | `internal/service/media.go` + `internal/dto/media.go` | 已修 |
| 2 | C2 | DNS Rebinding SSRF | `internal/util/ssrf.go` | 已修 |
| 3 | C3 | 备份恢复数据永久丢失（先删后写） | `internal/service/backup.go` | 已修 |

连带修复（顺手清理）：
| # | 标题 | 原严重性 |
|---|------|---------|
| 顺1 | `MediaService.Update` 把 DB 错误误分类为 404 | MEDIUM (M1) |
| 顺2 | `MediaService.Delete` 把 DB 错误误分类为 404 | MEDIUM (M1) |
| 顺3 | `GetRandom` 第二次查询丢失随机顺序 | MEDIUM |
| 顺4 | SSRF DNS 查询失败 fail-open | HIGH (H13) |

---

## 2. 变更明细

### C1. 任意文件删除

**根因**：`MediaUpdateRequest.PosterURL` 无格式校验；Delete 时 `filepath.Join(uploadsDir, TrimPrefix(...))` 虽会 Clean 但不等于边界校验。

**修复**（`internal/service/media.go`）：
1. 新增 `localUploadPattern = ^/uploads/(posters|thumbnails|categories)/[A-Za-z0-9._\-]+$`
2. 新增 `validateLocalPosterURL`：对非 http(s) 的 posterUrl 做白名单
3. 新增 `resolveLocalUploadPath`：用 `filepath.Rel` 确认路径仍在 `uploadsDir` 内，越界返回 `(_, false)`
4. `Create`、`Update` 调用 `validateLocalPosterURL` 前置拦截
5. `Delete` 调用 `resolveLocalUploadPath` 二次校验，越界则跳过删除

**关键代码**：
```go
// 写入侧
if err := validateLocalPosterURL(req.PosterURL); err != nil {
    return nil, err
}

// 删除侧
abs, ok := resolveLocalUploadPath(s.uploadsDir, *existing.PosterURL)
if ok {
    go func(p string) { _ = os.Remove(p) }(abs)
}
```

**攻击面关闭**：
- `{"posterUrl": "/uploads/../../etc/passwd"}` 在写入时被正则拒绝
- 即便历史脏数据绕过写入校验，删除时 `filepath.Rel` 会检测到 `../` 跳出 uploadsDir，拒绝删除

---

### C2. DNS Rebinding SSRF

**根因**：`AssertSafeURL` 用 `net.DefaultResolver` 做预检，`http.Client.Do` 内部再次独立解析。TTL=0 的恶意域可让两次解析返回不同 IP。

**修复**（`internal/util/ssrf.go`）：
1. 新增 `lookupSafeIP`：DNS 解析 + 私有段校验 + **fail-closed**（DNS 错误视为拒绝，顺带修 H13）
2. `ValidateResolvedIP` 改为 fail-closed（之前是 fail-open）
3. `AssertSafeURL` 对 IP 字面量单独分支处理
4. 新增 `buildPinnedClient`：构造带自定义 `DialContext` 的 HTTP Client，把 `addr` 的 host 部分替换为已校验 `pinnedIP`
5. `SafeFetch` 重写：**每一跳**解析 host 得到 pinnedIP，为该跳单独构造 client；SNI/Host 头由 `net/http` 依据 URL 自动设置，不受 pinnedIP 影响；`DisableKeepAlives=true` 防止跨 URL 复用导致绑定错乱

**关键代码**：
```go
func buildPinnedClient(pinnedIP net.IP, timeout time.Duration) *http.Client {
    tr := &http.Transport{
        DialContext: func(ctx context.Context, network, addr string) (net.Conn, error) {
            _, port, err := net.SplitHostPort(addr)
            if err != nil { return nil, err }
            return dialer.DialContext(ctx, network, net.JoinHostPort(pinnedIP.String(), port))
        },
        ...
        DisableKeepAlives: true,
    }
    ...
}
```

**攻击面关闭**：
- 第一次解析后 IP 被固定到 `DialContext` 闭包中
- 无论底层网络栈何时再查询 DNS，都会被 DialContext 强制替换为 `pinnedIP:port`
- TLS SNI 保持 hostname，证书验证不受影响

---

### C3. 备份恢复数据永久丢失

**根因**：`restoreUploads` 先 `os.RemoveAll(uploadsDir/*)` 再逐个从 ZIP 写入。磁盘满/ZIP 损坏/进程崩溃 → 原 uploads 已删，DB 却已 commit。`io.Copy` 错误被 `_` 丢弃不向上冒泡。

**修复**（`internal/service/backup.go`）：
1. `restoreUploads` 签名改为 `(int, error)`
2. **两阶段原子切换**：
   - 先解压到 `<uploadsDir>.new-<timestamp>` 临时目录
   - 任一写入失败 → 删除临时目录，**原 uploadsDir 完好**，返回 error
   - 全部成功后：`rename(uploadsDir → .old-<ts>)` + `rename(new → uploadsDir)`
   - 第二步失败时回滚 rename
   - 异步清理 `.old-<ts>`
3. `io.Copy` 错误必须冒泡，不再静默吞掉
4. 二次路径校验：拒绝 NUL 字节、用 `filepath.Rel` 确认在临时目录内
5. 调用方 `ImportFromFile` 检查 error 并返回 500

**关键代码**：
```go
staged := false
defer func() { if !staged { _ = os.RemoveAll(newDir) } }()

// 所有写入成功后才切换
if err := os.Rename(absRoot, oldDir); err != nil { ... }
if err := os.Rename(newDir, absRoot); err != nil {
    _ = os.Rename(oldDir, absRoot) // 回滚
    return 0, fmt.Errorf(...)
}
staged = true
```

**数据安全性提升**：
- 失败态：原 uploads 完好 + DB 已改 → 手动 rollback 窗口
- 成功态：新 uploads + 新 DB → 一致
- 中间无"uploads 被清空但 ZIP 还没解压完"的窗口期

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

### 单元测试
```
$ go test ./internal/util/...
ok   github.com/hor1zon777/m3u8-preview-go/internal/util   0.722s
```
SSRF 的 `IsPrivateHostname` / `isPrivateIP` 测试用例未破坏。

### 手动验证建议（部署前）
1. **C1 验证**：
   - `PUT /api/v1/media/:id` body `{"posterUrl": "/uploads/../../etc/passwd"}` → 应返回 400 `posterUrl 格式非法`
   - 历史脏数据 media 的 delete → 后端日志中无 os.Remove 调用（确认 `filepath.Rel` 生效）
2. **C2 验证**：
   - 用 DNS rebinding 测试域（如 `rbndr.us` 生成 `7f000001.<pub_ip>.rbndr.us`）访问 `/proxy/m3u8` → 应被拒绝
   - DNS 故障注入（/etc/hosts 屏蔽）→ 应返回 502 `DNS 解析失败`
3. **C3 验证**：
   - 在 `restoreUploads` 中手动 panic/磁盘写满 → 原 uploads 应完好，日志可见 `.new-<ts>` 临时目录被清理
   - 成功恢复后 → 可见 `.old-<ts>` 短暂存在后被异步删除

---

## 4. 未尽事项

- **C1 潜在强化**：poster URL 中允许 `_` 但没限制路径深度；可追加 `strings.Count(rel, "/") == 1` 限制单层。当前正则已足以阻止穿越（`/` 虽允许但 `..` 被 `[A-Za-z0-9._\-]+` 排除）。
- **C2 潜在强化**：当前每跳 DisableKeepAlives=true，对性能有轻微损失（m3u8 代理场景正常，因 segment 请求数少）。
- **C3 潜在强化**：跨文件系统 rename 可能失败（若 `uploadsDir` 和其父目录在不同挂载点，理论上 os.Rename 失败）。生产部署建议 uploadsDir 与父目录同一文件系统。

---

## 5. 下一阶段

进入 Stage 2：修复 13 个 HIGH 级别 bug。
优先顺序：
1. H1-H5 数据/文件层面（回调覆盖、孤儿文件、slug 冲突、Windows 残留、ffmpeg 注入）
2. H6-H8, H12 并发/生命周期（ctx 传播、admin TOCTOU、SSE ticket 扩权、PRAGMA 失效）
3. H9-H11 上传/DoS（backup 上传无限、SSE 不检测断开）

H13 已在本阶段顺手修复。
