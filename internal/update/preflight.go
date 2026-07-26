// preflight.go 重启后的 staged 更新装载判定（`server update-preflight` 子命令）。
//
// 由 docker-entrypoint.sh 在启动主进程之前以 root 调用，退出码即判定结果：
//
//	0 = 使用 staged 版本（entrypoint 会把 staged/server cp 到容器层执行）
//	1 = 使用镜像内版本（无 staged / 镜像已追平 / 连续启动失败 / 校验不过）
//
// 版本比较、逐文件哈希自检、启动尝试计数判定全部放在这里而不是 shell——
// BusyBox sh 里写 semver 比较既脆又不可测。执行本子命令的二进制永远是镜像内的
// /app/server（entrypoint 固定路径调用），所以 version.Version 就是镜像版本。
//
// 失败回滚闭环：
//   - entrypoint 每次决定用 staged 启动前把 attempts 计数 +1
//   - server 监听成功后调 MarkStartupOK 删除计数文件
//   - 计数 ≥ maxStartAttempts 时本函数把 staged 改名为 failed-<ver>-<ts> 留证并回退镜像版
package update

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

// maxStartAttempts staged 版本允许的连续启动失败次数。
const maxStartAttempts = 3

// attemptsFile 启动尝试计数文件（entrypoint 写、MarkStartupOK 删、preflight 读）。
func attemptsFile(dataDir string) string { return filepath.Join(updatesDir(dataDir), "attempts") }

// RunPreflight 执行装载判定，返回进程退出码（main 直接 os.Exit）。
func RunPreflight(dataDir, imageVersion string) int {
	staged := stagedDir(dataDir)

	if _, err := os.Stat(filepath.Join(staged, "server")); err != nil {
		return 1 // 无 staged，正常走镜像版
	}

	info, err := readStagedInfo(staged)
	if err != nil {
		log.Printf("[update-preflight] staged.json 不可读（%v），弃用暂存目录", err)
		_ = os.RemoveAll(staged)
		return 1
	}

	// 镜像版本非法（dev 构建等）时不比较也不删除：保守回退镜像版即可。
	if normalizeVersion(imageVersion) == "" {
		log.Printf("[update-preflight] 镜像版本 %q 非法，回退镜像版", imageVersion)
		return 1
	}

	// 镜像已追平/超过 staged（用户 docker compose pull 了新镜像）：staged 完成使命，清理。
	if !versionNewer(info.Version, imageVersion) {
		log.Printf("[update-preflight] 镜像版本 %s ≥ 暂存版本 %s，清理暂存目录", imageVersion, info.Version)
		_ = os.RemoveAll(staged)
		_ = os.Remove(attemptsFile(dataDir))
		return 1
	}

	// 连续启动失败 → 弃用并留证。
	if n := readAttempts(dataDir); n >= maxStartAttempts {
		failed := filepath.Join(updatesDir(dataDir),
			fmt.Sprintf("failed-%s-%s", info.Version, time.Now().UTC().Format("20060102T150405Z")))
		if err := os.Rename(staged, failed); err != nil {
			log.Printf("[update-preflight] 移动失败目录出错（%v），直接删除", err)
			_ = os.RemoveAll(staged)
		}
		_ = os.Remove(attemptsFile(dataDir))
		log.Printf("[update-preflight] 暂存版本 %s 连续 %d 次启动失败，已弃用并回退镜像版 %s（现场保留在 %s）",
			info.Version, n, imageVersion, failed)
		return 1
	}

	// 逐文件哈希自检：防半写/位腐蚀的暂存目录被执行。
	for rel, want := range info.Files {
		got, err := fileSHA256(filepath.Join(staged, filepath.FromSlash(rel)))
		if err != nil || got != want {
			log.Printf("[update-preflight] 文件 %s 哈希自检失败（err=%v），弃用暂存目录", rel, err)
			_ = os.RemoveAll(staged)
			_ = os.Remove(attemptsFile(dataDir))
			return 1
		}
	}

	log.Printf("[update-preflight] 暂存版本 %s 通过预检（镜像版 %s，第 %d 次启动尝试）",
		info.Version, imageVersion, readAttempts(dataDir)+1)
	return 0
}

// MarkStartupOK 清空启动尝试计数。main 在监听成功后调用——
// "成功绑定端口"即判定本次启动成功，崩溃循环的定义就是走不到这一步。
func MarkStartupOK(dataDir string) {
	_ = os.Remove(attemptsFile(dataDir))
}

// readAttempts 读取当前计数；缺失/非法按 0。
func readAttempts(dataDir string) int {
	raw, err := os.ReadFile(attemptsFile(dataDir))
	if err != nil {
		return 0
	}
	n, err := strconv.Atoi(strings.TrimSpace(string(raw)))
	if err != nil || n < 0 {
		return 0
	}
	return n
}

// fileSHA256 计算文件 sha256（hex）。
func fileSHA256(path string) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer func() { _ = f.Close() }()
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return "", err
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

// WriteStagedInfoForTest 测试辅助：把 StagedInfo 写进 staged 目录。
// 也供本地模拟 staged 更新的脚本复用（docs/self-update.md 的验证章节）。
func WriteStagedInfoForTest(staged string, info *StagedInfo) error {
	raw, err := json.MarshalIndent(info, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(staged, "staged.json"), raw, 0o644)
}
