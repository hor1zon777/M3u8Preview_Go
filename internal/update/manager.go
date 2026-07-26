// manager.go 应用内自更新的状态机与编排。
//
// 全景数据流见 docs/self-update.md。本文件负责运行期的一半：
//
//	检查（GitHub releases/latest，24h 缓存）→ 下载（限速上限 + 进度）
//	→ sha256 对照 checksums.txt → 安全解包到 staged.partial → 原子 rename 为 staged
//	→ 通知 main 退出进程（restart 策略拉起）
//
// 另一半（重启后的装载判定）在 preflight.go：entrypoint 以 root 调
// `server update-preflight` 决定用 staged 版还是镜像版。
//
// 所有阶段全部异步：HTTP handler 只触发与读快照，前端轮询 status 渲染进度，
// 进程退出发生在响应送达之后，不存在"响应被自己截断"的时序问题。
package update

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"math/rand"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

// State 状态机取值（同步到前端 shared types 的 UpdateState）。
type State string

const (
	StateIdle            State = "idle"
	StateChecking        State = "checking"
	StateUpdateAvailable State = "update-available"
	StateDownloading     State = "downloading"
	StateVerifying       State = "verifying"
	StateStaged          State = "staged"
	StateRestarting      State = "restarting"
	StateFailed          State = "failed"
)

// 手动切换/检查的前置校验错误，handler 映射为对应 HTTP 错误码。
var (
	// ErrDisabled UPDATE_DISABLED=1 显式关闭了自更新。
	ErrDisabled = errors.New("自更新已被 UPDATE_DISABLED 关闭")
	// ErrDevBuild dev/非法版本号构建不支持在线更新。
	ErrDevBuild = errors.New("开发构建不支持在线更新")
	// ErrAlreadyRunning 已有更新流程在进行。
	ErrAlreadyRunning = errors.New("更新已在进行中")
	// ErrNoUpdate 没有可应用的新版本（未检查过或已是最新）。
	ErrNoUpdate = errors.New("没有可应用的新版本，请先检查更新")
	// ErrVersionMismatch apply 请求的版本与缓存的最新版本不一致（防 TOCTOU）。
	ErrVersionMismatch = errors.New("请求的版本与最新版本不一致，请重新检查后再试")
)

// 检查节流参数。
const (
	// checkCacheTTL 检查结果缓存：GitHub 匿名 API 60 req/h，24h 缓存 + 手动强查足够。
	checkCacheTTL = 24 * time.Hour
	// forceCheckMinInterval 手动强查的最小间隔，防止管理员狂点耗尽配额。
	forceCheckMinInterval = 10 * time.Second
	// restartDelay staged 完成到发出退出信号的间隔：让 202 响应与最后一轮
	// status 轮询有机会送达前端。
	restartDelay = 1500 * time.Millisecond
	// checksumsMaxBytes checksums.txt 的下载上限。
	checksumsMaxBytes = 64 * 1024
)

// UpdateError 供状态 API 展示的失败信息。
type UpdateError struct {
	Code    string
	Message string
}

// Progress 下载进度（字节）。
type Progress struct {
	Downloaded int64
	Total      int64
}

// StagedInfo staged.json 的内容。Files 记录逐文件 sha256，
// preflight 装载前重算对照，防半写/损坏的暂存目录被执行。
type StagedInfo struct {
	Version       string            `json:"version"`
	Commit        string            `json:"commit,omitempty"`
	ArchiveSHA256 string            `json:"archiveSha256"`
	Files         map[string]string `json:"files"`
	StagedAt      time.Time         `json:"stagedAt"`
}

// Snapshot 状态快照（handler 转成 DTO）。
type Snapshot struct {
	CurrentVersion string
	Commit         string
	Enabled        bool
	DisabledReason string // "dev-build" / "env-disabled"，Enabled=false 时非空
	State          State
	LastCheckedAt  time.Time
	Latest         *ReleaseInfo
	Progress       Progress
	Staged         *StagedInfo
	Err            *UpdateError
}

// Manager 自更新状态机（进程内单例）。
type Manager struct {
	dataDir        string
	currentVersion string
	commit         string
	disabled       bool
	devBuild       bool
	gh             *ghClient

	mu        sync.Mutex
	state     State
	latest    *ReleaseInfo
	lastCheck time.Time
	lastErr   *UpdateError
	progress  Progress
	staged    *StagedInfo

	// restartCh 通知 main 退出进程（缓冲 1 只发一次，仿 ha.Agent.switchCh）。
	restartCh chan struct{}
	stop      chan struct{}
	stopOnce  sync.Once
}

// New 构造 Manager。currentVersion 传 version.Version（测试可传任意值）。
func New(dataDir, currentVersion, commit string, disabled bool) *Manager {
	m := &Manager{
		dataDir:        dataDir,
		currentVersion: currentVersion,
		commit:         commit,
		disabled:       disabled,
		devBuild:       normalizeVersion(currentVersion) == "",
		gh:             newGHClient(),
		state:          StateIdle,
		restartCh:      make(chan struct{}, 1),
		stop:           make(chan struct{}),
	}
	// 启动时读取遗留的 staged 信息：apply 成功但重启前的窗口、或 preflight
	// 因镜像追平而清理之前，前端都能看到"已暂存待生效"。
	if info, err := readStagedInfo(stagedDir(dataDir)); err == nil {
		m.staged = info
		m.state = StateStaged
	}
	return m
}

// Enabled 自更新是否可用。
func (m *Manager) Enabled() bool { return m != nil && !m.disabled && !m.devBuild }

// RestartRequested 返回退出通知通道（nil-safe，main 的 select 消费）。
func (m *Manager) RestartRequested() <-chan struct{} {
	if m == nil {
		return nil
	}
	return m.restartCh
}

// Start 启动后台自动检查：2-5min 随机延迟后首查，此后每 24h ± 1h。
// 只刷新缓存供前端显示"有新版本"角标，绝不自动 apply。
func (m *Manager) Start() {
	if !m.Enabled() {
		return
	}
	go func() {
		first := time.Duration(2+rand.Intn(4)) * time.Minute
		t := time.NewTimer(first)
		defer t.Stop()
		for {
			select {
			case <-m.stop:
				return
			case <-t.C:
				ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
				if _, err := m.Check(ctx, false); err != nil {
					log.Printf("[update] 自动检查更新失败: %v", err)
				}
				cancel()
				t.Reset(checkCacheTTL + time.Duration(rand.Intn(3600))*time.Second)
			}
		}
	}()
}

// Close 停止后台协程。
func (m *Manager) Close() {
	if m == nil {
		return
	}
	m.stopOnce.Do(func() { close(m.stop) })
}

// Status 返回当前快照。
func (m *Manager) Status() Snapshot {
	m.mu.Lock()
	defer m.mu.Unlock()
	s := Snapshot{
		CurrentVersion: m.currentVersion,
		Commit:         m.commit,
		Enabled:        m.Enabled(),
		State:          m.state,
		LastCheckedAt:  m.lastCheck,
		Latest:         m.latest,
		Progress:       m.progress,
		Staged:         m.staged,
		Err:            m.lastErr,
	}
	switch {
	case m.disabled:
		s.DisabledReason = "env-disabled"
	case m.devBuild:
		s.DisabledReason = "dev-build"
	}
	return s
}

// Check 查询最新版本。force=false 时走 24h 缓存；force=true 绕过缓存但有
// 10s 最小间隔。下载/暂存进行中时不打扰，直接返回当前快照。
func (m *Manager) Check(ctx context.Context, force bool) (Snapshot, error) {
	if m.disabled {
		return m.Status(), ErrDisabled
	}
	if m.devBuild {
		return m.Status(), ErrDevBuild
	}

	m.mu.Lock()
	switch m.state {
	case StateDownloading, StateVerifying, StateStaged, StateRestarting:
		m.mu.Unlock()
		return m.Status(), nil
	default:
	}
	if !m.lastCheck.IsZero() {
		since := time.Since(m.lastCheck)
		if (!force && since < checkCacheTTL) || (force && since < forceCheckMinInterval) {
			m.mu.Unlock()
			return m.Status(), nil
		}
	}
	prev := m.state
	m.state = StateChecking
	m.mu.Unlock()

	rel, err := m.gh.latestRelease(ctx)

	m.mu.Lock()
	defer m.mu.Unlock()
	if err != nil {
		// 检查失败不覆盖既有结论（比如上次已知有新版本），只记录错误。
		m.state = prev
		code := "UPDATE_CHECK_FAILED"
		if errors.Is(err, ErrRateLimited) {
			code = "UPDATE_RATE_LIMITED"
		}
		m.lastErr = &UpdateError{Code: code, Message: err.Error()}
		return m.snapshotLocked(), err
	}
	m.lastCheck = time.Now()
	m.latest = rel
	m.lastErr = nil
	if versionNewer(rel.Version, m.currentVersion) {
		m.state = StateUpdateAvailable
	} else {
		m.state = StateIdle
	}
	return m.snapshotLocked(), nil
}

// Apply 开始下载并暂存 requestVersion（必须等于缓存的最新版本，防 TOCTOU）。
// 异步执行：成功返回即表示流程已启动，进度经 Status 轮询获取。
func (m *Manager) Apply(requestVersion string) error {
	if m.disabled {
		return ErrDisabled
	}
	if m.devBuild {
		return ErrDevBuild
	}

	m.mu.Lock()
	defer m.mu.Unlock()
	switch m.state {
	case StateDownloading, StateVerifying, StateStaged, StateRestarting, StateChecking:
		return ErrAlreadyRunning
	default:
	}
	if m.latest == nil || !versionNewer(m.latest.Version, m.currentVersion) {
		return ErrNoUpdate
	}
	if requestVersion != "" && requestVersion != m.latest.Version {
		return ErrVersionMismatch
	}

	rel := *m.latest
	m.state = StateDownloading
	m.progress = Progress{Total: rel.AssetSize}
	m.lastErr = nil
	go m.run(rel)
	return nil
}

// run 下载 → 校验 → 解包 → 暂存 → 触发重启。任何失败都落 failed 状态并清理现场。
func (m *Manager) run(rel ReleaseInfo) {
	// 总超时 30 分钟：兜住慢速连接，也防挂死的下载占着状态机。
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Minute)
	defer cancel()

	if err := m.stage(ctx, rel); err != nil {
		code := "UPDATE_DOWNLOAD_FAILED"
		var coded interface{ UpdateCode() string }
		if errors.As(err, &coded) {
			code = coded.UpdateCode()
		}
		log.Printf("[update] 更新 %s 失败: %v", rel.Version, err)
		m.mu.Lock()
		m.state = StateFailed
		m.lastErr = &UpdateError{Code: code, Message: err.Error()}
		m.mu.Unlock()
		return
	}

	log.Printf("[update] 版本 %s 已暂存，%s 后退出进程由容器 restart 策略拉起", rel.Version, restartDelay)
	time.Sleep(restartDelay)
	m.mu.Lock()
	m.state = StateRestarting
	m.mu.Unlock()
	select {
	case m.restartCh <- struct{}{}:
	default:
	}
}

// codedError 携带机器可读错误码的 error。
type codedError struct {
	code string
	err  error
}

func (e *codedError) Error() string      { return e.err.Error() }
func (e *codedError) Unwrap() error      { return e.err }
func (e *codedError) UpdateCode() string { return e.code }

func withCode(code string, err error) error { return &codedError{code: code, err: err} }

// stage 执行下载与暂存的全部步骤。
func (m *Manager) stage(ctx context.Context, rel ReleaseInfo) error {
	updates := updatesDir(m.dataDir)
	if err := os.MkdirAll(updates, 0o755); err != nil {
		return withCode("UPDATE_STAGE_FAILED", fmt.Errorf("创建更新目录: %w", err))
	}
	cleanupLeftovers(updates)

	// 1) checksums.txt → 目标资产的期望 sha256
	var checksumBuf strings.Builder
	if err := m.gh.download(ctx, rel.ChecksumURL, &checksumBuf, checksumsMaxBytes, nil); err != nil {
		return withCode("UPDATE_DOWNLOAD_FAILED", fmt.Errorf("下载 checksums.txt: %w", err))
	}
	wantSum, err := parseChecksums(checksumBuf.String(), rel.AssetName)
	if err != nil {
		return withCode("UPDATE_DOWNLOAD_FAILED", err)
	}

	// 2) 流式下载 tar.gz（边下边算 sha256 + 进度）
	tmpPath := filepath.Join(updates, fmt.Sprintf("tmp-%d.tar.gz", time.Now().UnixNano()))
	f, err := os.OpenFile(tmpPath, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o644)
	if err != nil {
		return withCode("UPDATE_STAGE_FAILED", err)
	}
	defer func() { _ = os.Remove(tmpPath) }()

	h := sha256.New()
	err = m.gh.download(ctx, rel.AssetURL, io.MultiWriter(f, h), maxArchiveBytes, func(delta int64) {
		m.mu.Lock()
		m.progress.Downloaded += delta
		m.mu.Unlock()
	})
	if cerr := f.Close(); cerr != nil && err == nil {
		err = cerr
	}
	if err != nil {
		return withCode("UPDATE_DOWNLOAD_FAILED", fmt.Errorf("下载更新包: %w", err))
	}

	// 3) sha256 对照
	m.mu.Lock()
	m.state = StateVerifying
	m.mu.Unlock()
	gotSum := hex.EncodeToString(h.Sum(nil))
	if gotSum != wantSum {
		return withCode("UPDATE_CHECKSUM_MISMATCH",
			fmt.Errorf("更新包 sha256 校验失败（期望 %s，实际 %s）", wantSum[:12], gotSum[:12]))
	}

	// 4) 解包到 staged.partial
	partial := filepath.Join(updates, "staged.partial")
	_ = os.RemoveAll(partial)
	tf, err := os.Open(tmpPath)
	if err != nil {
		return withCode("UPDATE_STAGE_FAILED", err)
	}
	files, err := extractTarGz(tf, partial)
	_ = tf.Close()
	if err != nil {
		_ = os.RemoveAll(partial)
		return withCode("UPDATE_STAGE_FAILED", fmt.Errorf("解包更新包: %w", err))
	}

	// 5) 结构校验：server 必须存在；VERSION 内容必须与 Release 版本一致
	//    （防止 tag 与包内容错位的异常产物被装载）。
	if _, ok := files["server"]; !ok {
		_ = os.RemoveAll(partial)
		return withCode("UPDATE_STAGE_FAILED", errors.New("更新包缺少 server 二进制"))
	}
	verBytes, err := os.ReadFile(filepath.Join(partial, "VERSION"))
	if err != nil || strings.TrimSpace(string(verBytes)) != rel.Version {
		_ = os.RemoveAll(partial)
		return withCode("UPDATE_STAGE_FAILED", errors.New("更新包内 VERSION 与 Release 版本不一致"))
	}
	if err := os.Chmod(filepath.Join(partial, "server"), 0o755); err != nil {
		_ = os.RemoveAll(partial)
		return withCode("UPDATE_STAGE_FAILED", err)
	}

	// 6) 写 staged.json（commit 从 release.json 取，取不到不阻塞）
	info := &StagedInfo{
		Version:       rel.Version,
		ArchiveSHA256: gotSum,
		Files:         files,
		StagedAt:      time.Now(),
	}
	if raw, err := os.ReadFile(filepath.Join(partial, "release.json")); err == nil {
		var meta struct {
			Commit string `json:"commit"`
		}
		if json.Unmarshal(raw, &meta) == nil {
			info.Commit = meta.Commit
		}
	}
	infoJSON, err := json.MarshalIndent(info, "", "  ")
	if err != nil {
		_ = os.RemoveAll(partial)
		return withCode("UPDATE_STAGE_FAILED", err)
	}
	if err := os.WriteFile(filepath.Join(partial, "staged.json"), infoJSON, 0o644); err != nil {
		_ = os.RemoveAll(partial)
		return withCode("UPDATE_STAGE_FAILED", err)
	}

	// 7) 原子上位：旧 staged 移除 → rename（同一文件系统内原子）
	staged := stagedDir(m.dataDir)
	_ = os.RemoveAll(staged)
	if err := os.Rename(partial, staged); err != nil {
		_ = os.RemoveAll(partial)
		return withCode("UPDATE_STAGE_FAILED", fmt.Errorf("暂存目录上位: %w", err))
	}

	m.mu.Lock()
	m.state = StateStaged
	m.staged = info
	m.mu.Unlock()
	return nil
}

// snapshotLocked 调用方必须已持有 m.mu。
func (m *Manager) snapshotLocked() Snapshot {
	s := Snapshot{
		CurrentVersion: m.currentVersion,
		Commit:         m.commit,
		Enabled:        m.Enabled(),
		State:          m.state,
		LastCheckedAt:  m.lastCheck,
		Latest:         m.latest,
		Progress:       m.progress,
		Staged:         m.staged,
		Err:            m.lastErr,
	}
	switch {
	case m.disabled:
		s.DisabledReason = "env-disabled"
	case m.devBuild:
		s.DisabledReason = "dev-build"
	}
	return s
}

// ---- 路径与磁盘辅助 ----

func updatesDir(dataDir string) string { return filepath.Join(dataDir, "updates") }
func stagedDir(dataDir string) string  { return filepath.Join(updatesDir(dataDir), "staged") }

// readStagedInfo 读取 staged.json；目录不存在/内容非法都按"无暂存"处理。
func readStagedInfo(staged string) (*StagedInfo, error) {
	raw, err := os.ReadFile(filepath.Join(staged, "staged.json"))
	if err != nil {
		return nil, err
	}
	var info StagedInfo
	if err := json.Unmarshal(raw, &info); err != nil {
		return nil, err
	}
	if info.Version == "" {
		return nil, errors.New("staged.json 缺少 version")
	}
	return &info, nil
}

// cleanupLeftovers 清理上次中断遗留的临时文件。
func cleanupLeftovers(updates string) {
	entries, err := os.ReadDir(updates)
	if err != nil {
		return
	}
	for _, e := range entries {
		name := e.Name()
		if strings.HasPrefix(name, "tmp-") || name == "staged.partial" {
			_ = os.RemoveAll(filepath.Join(updates, name))
		}
	}
}
