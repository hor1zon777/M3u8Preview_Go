// Package service
// subtitle_worker_test.go 覆盖远程字幕 worker 协作的关键并发路径。
//
// 重点测试：
//  1. ClaimNextJob 原子性：N 个 goroutine 同时认领同一 PENDING，只有一个抢到
//  2. RecoverStaleJobsOnce：心跳超时的 RUNNING 自动重置回 PENDING
//  3. WorkerHeartbeat ownership 校验：非 claimed_by 的 worker 不能更新进度
//  4. CreateWorkerToken / RevokeWorkerToken 流程
//
// 测试用 in-memory SQLite（":memory:" DSN）+ 完整 AutoMigrate。
package service

import (
	"context"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/glebarez/sqlite"
	"golang.org/x/crypto/bcrypt"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"

	"github.com/hor1zon777/m3u8-preview-go/internal/config"
	"github.com/hor1zon777/m3u8-preview-go/internal/dto"
	"github.com/hor1zon777/m3u8-preview-go/internal/middleware"
	"github.com/hor1zon777/m3u8-preview-go/internal/model"
)

// newTestSubtitleService 构造一个仅用于 worker 协作测试的 SubtitleService。
// asr / translator / signer 不参与本测试涵盖的方法，留 nil。
func newTestSubtitleService(t *testing.T, staleThreshold time.Duration) (*SubtitleService, *gorm.DB) {
	t.Helper()
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{
		Logger: logger.Default.LogMode(logger.Silent),
	})
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	// :memory: 数据库的每个连接独立。并发测试（如 TestClaimNextJob_Atomicity）
	// 多 goroutine 抢占时会拿到不同 connection，AutoMigrate 出来的表只在原始
	// connection 上可见。把 maxOpenConns 限为 1 让所有调用共享同一连接。
	if sqlDB, derr := db.DB(); derr == nil {
		sqlDB.SetMaxOpenConns(1)
	}
	if err := db.AutoMigrate(
		&model.User{},
		&model.Category{},
		&model.Tag{},
		&model.Media{},
		&model.SubtitleJob{},
		&model.SubtitleWorkerToken{},
		&model.SubtitleWorker{},
	); err != nil {
		t.Fatalf("automigrate: %v", err)
	}

	cfg := &config.SubtitleConfig{
		Enabled:              true,
		WhisperLanguage:      "ja",
		TargetLang:           "zh",
		BatchSize:            8,
		MaxRetries:           2,
		SubtitlesDir:         t.TempDir(),
		WorkerStaleThreshold: staleThreshold,
		LocalWorkerEnabled:   false,
	}
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)

	svc := &SubtitleService{
		db:          db,
		cfg:         cfg,
		audioBroker: NewAudioBroker(),
		jobs:        make(chan string, 16),
		stop:        make(chan struct{}),
		ctx:         ctx,
		cancel:      cancel,
	}
	return svc, db
}

// seedPendingJob 直接插入一条 PENDING 的 subtitle_job + media。
func seedPendingJob(t *testing.T, db *gorm.DB, mediaID, m3u8URL string) string {
	t.Helper()
	media := model.Media{
		ID:      mediaID,
		Title:   "test " + mediaID,
		M3u8URL: m3u8URL,
		Status:  model.MediaStatusActive,
	}
	if err := db.Create(&media).Error; err != nil {
		t.Fatalf("seed media: %v", err)
	}
	job := model.SubtitleJob{
		MediaID:    mediaID,
		Status:     model.SubtitleStatusPending,
		Stage:      model.SubtitleStageQueued,
		SourceLang: "ja",
		TargetLang: "zh",
	}
	if err := db.Create(&job).Error; err != nil {
		t.Fatalf("seed job: %v", err)
	}
	return job.ID
}

// TestClaimNextJob_Atomicity 100 个 goroutine 同时 claim 同一 PENDING，应只有一个成功。
func TestClaimNextJob_Atomicity(t *testing.T) {
	svc, db := newTestSubtitleService(t, 10*time.Minute)
	jobID := seedPendingJob(t, db, "media-1", "https://example.com/test.m3u8")

	const goroutines = 100
	var (
		wg          sync.WaitGroup
		claimedCnt  atomic.Int32
		nilCnt      atomic.Int32
		errCnt      atomic.Int32
		claimedJobs sync.Map // jobId -> workerId
	)

	wg.Add(goroutines)
	for i := range goroutines {
		workerID := "w-" + string(rune('a'+i%26)) + string(rune('0'+i/26))
		go func(wid string) {
			defer wg.Done()
			job, err := svc.ClaimNextJob(wid)
			switch {
			case err != nil:
				errCnt.Add(1)
			case job == nil:
				nilCnt.Add(1)
			default:
				claimedCnt.Add(1)
				claimedJobs.Store(job.JobID, wid)
			}
		}(workerID)
	}
	wg.Wait()

	if got := claimedCnt.Load(); got != 1 {
		t.Fatalf("expected exactly 1 claimer, got %d (nil=%d err=%d)", got, nilCnt.Load(), errCnt.Load())
	}
	if errCnt.Load() != 0 {
		t.Fatalf("unexpected errors during concurrent claim: %d", errCnt.Load())
	}

	// 验证 DB 状态：被认领的那一条是 RUNNING + claimed_by 设了
	var job model.SubtitleJob
	if err := db.Where("id = ?", jobID).Take(&job).Error; err != nil {
		t.Fatalf("query job: %v", err)
	}
	if job.Status != model.SubtitleStatusRunning {
		t.Fatalf("expected RUNNING, got %q", job.Status)
	}
	if job.ClaimedBy == "" {
		t.Fatal("expected claimed_by set")
	}
	if job.ClaimedAt == nil {
		t.Fatal("expected claimed_at set")
	}
}

// TestClaimNextJob_NoPending 没有 PENDING 时返回 (nil, nil)。
func TestClaimNextJob_NoPending(t *testing.T) {
	svc, _ := newTestSubtitleService(t, 10*time.Minute)
	job, err := svc.ClaimNextJob("w-1")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if job != nil {
		t.Fatalf("expected nil job (no pending), got %+v", job)
	}
}

// TestClaimNextJob_FIFO 多条 PENDING 时按 created_at ASC 取最早的。
func TestClaimNextJob_FIFO(t *testing.T) {
	svc, db := newTestSubtitleService(t, 10*time.Minute)
	first := seedPendingJob(t, db, "media-first", "https://example.com/1.m3u8")
	time.Sleep(10 * time.Millisecond) // 确保 created_at 不同
	_ = seedPendingJob(t, db, "media-second", "https://example.com/2.m3u8")

	job, err := svc.ClaimNextJob("w-1")
	if err != nil {
		t.Fatalf("claim: %v", err)
	}
	if job == nil || job.JobID != first {
		t.Fatalf("expected first job %s, got %v", first, job)
	}
}

// TestRecoverStaleJobsOnce_HeartbeatTimeout RUNNING 任务超过阈值无心跳被重置。
func TestRecoverStaleJobsOnce_HeartbeatTimeout(t *testing.T) {
	svc, db := newTestSubtitleService(t, 100*time.Millisecond)
	jobID := seedPendingJob(t, db, "media-stale", "https://example.com/stale.m3u8")

	// claim → RUNNING
	if _, err := svc.ClaimNextJob("w-stale"); err != nil {
		t.Fatalf("claim: %v", err)
	}

	// 倒拨 last_heartbeat_at 到很久之前
	old := time.Now().Add(-5 * time.Minute)
	if err := db.Model(&model.SubtitleJob{}).Where("id = ?", jobID).
		Update("last_heartbeat_at", &old).Error; err != nil {
		t.Fatalf("backdate heartbeat: %v", err)
	}

	svc.recoverStaleJobsOnce()

	var job model.SubtitleJob
	if err := db.Where("id = ?", jobID).Take(&job).Error; err != nil {
		t.Fatalf("query: %v", err)
	}
	if job.Status != model.SubtitleStatusPending {
		t.Fatalf("expected PENDING after stale reset, got %q", job.Status)
	}
	if job.ClaimedBy != "" {
		t.Fatalf("expected claimed_by cleared, got %q", job.ClaimedBy)
	}
	if job.LastHeartbeatAt != nil {
		t.Fatalf("expected last_heartbeat_at cleared, got %v", job.LastHeartbeatAt)
	}
	if !strings.Contains(job.ErrorMsg, "stale") {
		t.Fatalf("expected error_msg to mention stale, got %q", job.ErrorMsg)
	}
}

// TestRecoverStaleJobsOnce_ClaimedNoHeartbeat 抢到但还没心跳就崩了的 worker，也应被重置。
func TestRecoverStaleJobsOnce_ClaimedNoHeartbeat(t *testing.T) {
	svc, db := newTestSubtitleService(t, 100*time.Millisecond)
	jobID := seedPendingJob(t, db, "media-noheart", "https://example.com/x.m3u8")

	if _, err := svc.ClaimNextJob("w-1"); err != nil {
		t.Fatalf("claim: %v", err)
	}

	// 模拟"抢到后立刻崩"：清空 last_heartbeat_at，倒拨 claimed_at
	old := time.Now().Add(-5 * time.Minute)
	if err := db.Model(&model.SubtitleJob{}).Where("id = ?", jobID).Updates(map[string]any{
		"claimed_at":        &old,
		"last_heartbeat_at": nil,
	}).Error; err != nil {
		t.Fatalf("backdate: %v", err)
	}

	svc.recoverStaleJobsOnce()

	var job model.SubtitleJob
	if err := db.Where("id = ?", jobID).Take(&job).Error; err != nil {
		t.Fatalf("query: %v", err)
	}
	if job.Status != model.SubtitleStatusPending {
		t.Fatalf("expected PENDING, got %q", job.Status)
	}
}

// TestRecoverStaleJobsOnce_FreshJobNotTouched 心跳健康的 RUNNING 任务不应被重置。
func TestRecoverStaleJobsOnce_FreshJobNotTouched(t *testing.T) {
	svc, db := newTestSubtitleService(t, 10*time.Minute) // 10 min 阈值，下面的任务很新
	jobID := seedPendingJob(t, db, "media-fresh", "https://example.com/fresh.m3u8")

	if _, err := svc.ClaimNextJob("w-1"); err != nil {
		t.Fatalf("claim: %v", err)
	}
	// claim 时已经写了 last_heartbeat_at = now，新鲜
	svc.recoverStaleJobsOnce()

	var job model.SubtitleJob
	if err := db.Where("id = ?", jobID).Take(&job).Error; err != nil {
		t.Fatalf("query: %v", err)
	}
	if job.Status != model.SubtitleStatusRunning {
		t.Fatalf("expected RUNNING preserved, got %q", job.Status)
	}
}

// TestWorkerHeartbeat_OwnershipCheck 非 claimed_by 的 worker 不能更新进度。
func TestWorkerHeartbeat_OwnershipCheck(t *testing.T) {
	svc, db := newTestSubtitleService(t, 10*time.Minute)
	_ = seedPendingJob(t, db, "media-1", "https://example.com/1.m3u8")
	job, err := svc.ClaimNextJob("w-real")
	if err != nil || job == nil {
		t.Fatalf("claim: err=%v job=%v", err, job)
	}

	// 真 owner 心跳：成功
	if err := svc.WorkerHeartbeat(job.JobID, "w-real", model.SubtitleStageASR, 50); err != nil {
		t.Fatalf("legitimate heartbeat: %v", err)
	}

	// 别人冒充：失败
	err = svc.WorkerHeartbeat(job.JobID, "w-evil", model.SubtitleStageASR, 80)
	if err == nil || !strings.Contains(err.Error(), "not own") {
		t.Fatalf("expected ErrWorkerJobNotOwned, got %v", err)
	}

	// 验证 progress 没被改成 80
	var stored model.SubtitleJob
	if err := db.Where("id = ?", job.JobID).Take(&stored).Error; err != nil {
		t.Fatalf("query: %v", err)
	}
	if stored.Progress != 50 {
		t.Fatalf("expected progress=50 (real worker's), got %d", stored.Progress)
	}
}

// TestWorkerComplete_RejectsNonVTT VTT 嗅探拒绝非 WebVTT 内容。
func TestWorkerComplete_RejectsNonVTT(t *testing.T) {
	svc, db := newTestSubtitleService(t, 10*time.Minute)
	_ = seedPendingJob(t, db, "media-1", "https://example.com/1.m3u8")
	job, _ := svc.ClaimNextJob("w-1")

	err := svc.WorkerComplete(job.JobID, dto.WorkerCompleteMeta{
		WorkerID:     "w-1",
		SegmentCount: 1,
	}, []byte("not a vtt file"))
	if err == nil || !strings.Contains(err.Error(), "WebVTT") {
		t.Fatalf("expected vtt sniff failure, got %v", err)
	}
}

// TestWorkerComplete_Success 端到端：claim → heartbeat → complete。
func TestWorkerComplete_Success(t *testing.T) {
	svc, db := newTestSubtitleService(t, 10*time.Minute)
	_ = seedPendingJob(t, db, "media-1", "https://example.com/1.m3u8")
	job, _ := svc.ClaimNextJob("w-1")

	vtt := []byte("WEBVTT\n\n00:00:00.000 --> 00:00:02.000\nhello\n")
	if err := svc.WorkerComplete(job.JobID, dto.WorkerCompleteMeta{
		WorkerID:     "w-1",
		SegmentCount: 1,
		ASRModel:     "whisper-base",
		MTModel:      "deepseek-chat",
	}, vtt); err != nil {
		t.Fatalf("complete: %v", err)
	}

	var stored model.SubtitleJob
	if err := db.Where("id = ?", job.JobID).Take(&stored).Error; err != nil {
		t.Fatalf("query: %v", err)
	}
	if stored.Status != model.SubtitleStatusDone {
		t.Fatalf("expected DONE, got %q", stored.Status)
	}
	if stored.SegmentCount != 1 {
		t.Fatalf("expected segment_count=1, got %d", stored.SegmentCount)
	}
	if stored.VttPath == "" {
		t.Fatal("expected vtt_path set")
	}
}

// TestCreateAndRevokeWorkerToken 完整 token 生命周期。
func TestCreateAndRevokeWorkerToken(t *testing.T) {
	svc, db := newTestSubtitleService(t, 10*time.Minute)

	plaintext, rec, err := svc.CreateWorkerToken("home gpu", 1, 2, 1)
	if err != nil {
		t.Fatalf("create token: %v", err)
	}
	if !strings.HasPrefix(plaintext, "mwt_") {
		t.Fatalf("expected mwt_ prefix, got %q", plaintext)
	}
	if len(plaintext) < middleware.WorkerTokenPrefix+8 {
		t.Fatalf("plaintext too short: %d", len(plaintext))
	}
	if rec.TokenPrefix != plaintext[:middleware.WorkerTokenPrefix] {
		t.Fatalf("prefix mismatch: rec=%q plaintext-prefix=%q", rec.TokenPrefix, plaintext[:middleware.WorkerTokenPrefix])
	}

	// hash 在 DB 里能 verify
	var stored model.SubtitleWorkerToken
	if err := db.Where("id = ?", rec.ID).Take(&stored).Error; err != nil {
		t.Fatalf("query: %v", err)
	}
	if err := bcrypt.CompareHashAndPassword([]byte(stored.TokenHash), []byte(plaintext)); err != nil {
		t.Fatalf("bcrypt verify: %v", err)
	}

	// revoke
	if err := svc.RevokeWorkerToken(rec.ID); err != nil {
		t.Fatalf("revoke: %v", err)
	}
	if err := db.Where("id = ?", rec.ID).Take(&stored).Error; err != nil {
		t.Fatalf("query after revoke: %v", err)
	}
	if stored.RevokedAt == nil {
		t.Fatal("expected revoked_at set")
	}

	// 再 revoke 报 already revoked
	if err := svc.RevokeWorkerToken(rec.ID); err != ErrWorkerTokenAlreadyRev {
		t.Fatalf("expected ErrWorkerTokenAlreadyRev, got %v", err)
	}

	// 不存在的 id 报 not found
	if err := svc.RevokeWorkerToken("does-not-exist"); err != ErrWorkerTokenNotFound {
		t.Fatalf("expected ErrWorkerTokenNotFound, got %v", err)
	}
}

// TestUpdateWorkerToken_MaxConcurrency 校验 admin 编辑 token 并发上限。
func TestUpdateWorkerToken_MaxConcurrency(t *testing.T) {
	svc, _ := newTestSubtitleService(t, 10*time.Minute)

	_, rec, err := svc.CreateWorkerToken("home gpu", 1, 2, 1)
	if err != nil {
		t.Fatalf("create token: %v", err)
	}

	// 改成 4
	v := 4
	out, err := svc.UpdateWorkerToken(rec.ID, dto.SubtitleWorkerTokenUpdateRequest{MaxConcurrency: &v})
	if err != nil {
		t.Fatalf("update: %v", err)
	}
	if out.MaxConcurrency != 4 {
		t.Fatalf("expected 4, got %d", out.MaxConcurrency)
	}

	// 超过 64 会被 clamp
	v = 9999
	out, err = svc.UpdateWorkerToken(rec.ID, dto.SubtitleWorkerTokenUpdateRequest{MaxConcurrency: &v})
	if err != nil {
		t.Fatalf("update clamp: %v", err)
	}
	if out.MaxConcurrency != 64 {
		t.Fatalf("expected clamp to 64, got %d", out.MaxConcurrency)
	}

	// 已吊销不能改
	if err := svc.RevokeWorkerToken(rec.ID); err != nil {
		t.Fatalf("revoke: %v", err)
	}
	if _, err := svc.UpdateWorkerToken(rec.ID, dto.SubtitleWorkerTokenUpdateRequest{MaxConcurrency: &v}); err != ErrWorkerTokenAlreadyRev {
		t.Fatalf("expected ErrWorkerTokenAlreadyRev, got %v", err)
	}
}

// TestClaimNextJob_GlobalLimit 全局并发上限：达到上限后新 worker claim 直接拿不到任务。
func TestClaimNextJob_GlobalLimit(t *testing.T) {
	svc, db := newTestSubtitleService(t, 10*time.Minute)
	svc.cfg.GlobalMaxConcurrency = 1

	// 两条 PENDING
	seedPendingJob(t, db, "media-A", "https://example.com/a.m3u8")
	seedPendingJob(t, db, "media-B", "https://example.com/b.m3u8")

	// 第一个 worker 抢到一条
	first, err := svc.ClaimNextJob("worker-A")
	if err != nil {
		t.Fatalf("claim 1st: %v", err)
	}
	if first == nil {
		t.Fatal("first claim should succeed")
	}

	// 全局上限已满，第二个 worker 应拿不到任何任务
	second, err := svc.ClaimNextJob("worker-B")
	if err != nil {
		t.Fatalf("claim 2nd: %v", err)
	}
	if second != nil {
		t.Fatalf("expected nil due to global limit, got %+v", second)
	}
}

// TestClaimNextJob_TokenLimit Token 维度并发上限：同 token 下两个 worker 受限于 MaxConcurrency=1。
func TestClaimNextJob_TokenLimit(t *testing.T) {
	svc, db := newTestSubtitleService(t, 10*time.Minute)

	// 创建一个 MaxConcurrency=1 的 token
	_, tokenRec, err := svc.CreateWorkerToken("shared", 1, 2, 1)
	if err != nil {
		t.Fatalf("create token: %v", err)
	}

	// 两个 worker 都注册到这个 token 下
	if err := db.Create(&model.SubtitleWorker{
		ID:           "worker-A",
		TokenID:      tokenRec.ID,
		Name:         "A",
		LastSeenAt:   time.Now(),
		RegisteredAt: time.Now(),
	}).Error; err != nil {
		t.Fatalf("seed worker A: %v", err)
	}
	if err := db.Create(&model.SubtitleWorker{
		ID:           "worker-B",
		TokenID:      tokenRec.ID,
		Name:         "B",
		LastSeenAt:   time.Now(),
		RegisteredAt: time.Now(),
	}).Error; err != nil {
		t.Fatalf("seed worker B: %v", err)
	}

	// 两条 PENDING（避免被 FIFO 顺序蒙混）
	seedPendingJob(t, db, "media-A", "https://example.com/a.m3u8")
	seedPendingJob(t, db, "media-B", "https://example.com/b.m3u8")

	// worker-A 抢一条
	first, err := svc.ClaimNextJob("worker-A")
	if err != nil || first == nil {
		t.Fatalf("first claim: %v %+v", err, first)
	}

	// 同 token 上限 1，worker-B 拿不到
	second, err := svc.ClaimNextJob("worker-B")
	if err != nil {
		t.Fatalf("second claim err: %v", err)
	}
	if second != nil {
		t.Fatalf("expected nil due to token limit, got %+v", second)
	}

	// 把 token 上限调到 2 后，worker-B 应该能抢到
	v := 2
	if _, err := svc.UpdateWorkerToken(tokenRec.ID, dto.SubtitleWorkerTokenUpdateRequest{MaxConcurrency: &v}); err != nil {
		t.Fatalf("update limit: %v", err)
	}
	third, err := svc.ClaimNextJob("worker-B")
	if err != nil {
		t.Fatalf("third claim err: %v", err)
	}
	if third == nil {
		t.Fatal("worker-B should claim after raising token limit")
	}
}
