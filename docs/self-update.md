# 应用内自更新（管理面板一键更新）

管理面板「软件更新」卡片可检查新版本并一键更新，全程在容器内完成，**用户无需执行任何 docker 命令、无需重建容器**。

## 数据流

```
CI (打 v* tag) ──→ GitHub Release:
                     m3u8preview_<tag>_linux_amd64.tar.gz   (server 二进制 + web-dist + VERSION + release.json)
                     checksums.txt                           (sha256)

管理页「检查更新」→ GET api.github.com/.../releases/latest（24h 缓存 + 手动强查 10s 节流）
管理页「立即更新」→ 下载到 /data/updates/ → sha256 对照 checksums.txt
                 → 安全解包到 staged.partial → 原子 rename 为 staged → 进程 exit 0
                 → restart: unless-stopped 拉起容器
entrypoint(root) → `server update-preflight` 判定（版本比较 / 逐文件哈希自检 / 启动尝试计数）
                 → 通过: cp staged/server → /app/server-staged（容器层，规避 /data noexec）
                        dist 同步改用 staged/web-dist（前端与二进制同源切换）
                 → su-exec appuser 启动新版
server 监听成功  → 清零启动尝试计数
```

## 安全模型

- **更新源 pin 死**：owner/repo 是编译期常量，不提供环境变量覆盖；资产 URL 必须落在本仓库 `releases/download/` 前缀下；重定向逐跳校验（仅 https，host 限 github.com / *.githubusercontent.com）。
- **完整性**：下载后 sha256 对照同一 Release 的 checksums.txt；解包拒绝路径穿越/符号链接/超限条目；staged.json 记录逐文件哈希，装载前重算对照（防半写损坏）。
- **权限边界不变**：staged 二进制由 root entrypoint 复制到容器层执行，appuser 仍然无法写 /app；新版本仍以 appuser 运行，无提权。/data/updates 属"持久性"攻击面而非"提权"面（appuser 本就能执行任意代码）。

## 失败回滚

- entrypoint 每次用 staged 启动前把 `/data/updates/attempts` 计数 +1；server 成功绑定端口后清零。
- 连续 **3 次**启动失败：下次 preflight 自动弃用 staged（现场保留为 `failed-<ver>-<ts>` 目录供排障）并回退镜像版本，服务恢复。
- 用户 `docker compose pull` 了新镜像后：preflight 发现镜像版本 ≥ 暂存版本，自动清理 staged。两条更新路径可共存，永远运行较新者。

## HA 双节点的滚动升级

更新 API 只作用于**当前访问的节点**（不做跨节点代理；更新端点不受只读写闸门限制，备节点可直接执行）。建议顺序：

1. 在**备节点**管理页执行更新，确认恢复后运行新版本（`/api/health` 的 version）；
2. 在高可用管理页执行主备切换；
3. 在新的备节点（原主）上再执行更新。

管理面板的更新卡片会同时显示本机与对端版本（对端版本来自 HA 探测缓存）。

## 相关配置

| 项 | 说明 |
|---|---|
| `UPDATE_DISABLED=1` | 逃生开关：彻底关闭在线更新（检查与安装均拒绝） |
| dev 构建 | `version.Version` 为 `dev`/非法 semver 时自更新自动禁用 |

## 本地模拟验证（无需 CI）

```bash
# 1. 构建一个"高版本"二进制并手摆 staged 目录
GOOS=linux GOARCH=amd64 go build -ldflags "-X github.com/hor1zon777/m3u8-preview-go/internal/version.Version=9.9.9" -o server-999 ./cmd/server
mkdir -p data/updates/staged
cp server-999 data/updates/staged/server
printf '9.9.9\n' > data/updates/staged/VERSION
# staged.json 需含 files.server 的 sha256（可用 go test 里的 WriteStagedInfoForTest 或手工生成）

# 2. docker compose up 观察 entrypoint 日志走 staged 分支，/api/health 版本变 9.9.9
# 3. 篡改 staged/server 内容 → 重启 → 预检拒绝并回退镜像版
# 4. 把 staged/server 换成必退出的脚本 → 连续 3 次重启后出现 failed-* 目录并回退
```

## 已知限制

- Release 产物仅 linux/amd64（与镜像平台一致）。
- 本功能自包含它的版本起生效——**部署该版本本身仍需一次 `docker compose pull && up -d`**。
- 不支持在线降级：staged 版本必须严格新于镜像版本才会被装载。
