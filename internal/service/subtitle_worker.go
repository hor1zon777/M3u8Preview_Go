// Package service
// subtitle_worker.go 实现远程字幕 worker 协作的 service 层。
//
// 与 subtitle.go 的关系：
//   - subtitle.go 负责 in-process（本地 whisper.cpp）pipeline 与公共操作（EnsureJob、ListJobs 等）
//   - subtitle_worker.go 负责给 /api/v1/worker/* 端点和 /api/v1/admin/subtitle/worker-* 端点提供方法
//
// 核心职责：
//   - RegisterWorker：worker 启动/重启时 upsert 到 subtitle_workers 表
//   - ClaimNextJob：原子认领一条 PENDING（CAS UPDATE）
//   - WorkerHeartbeat / WorkerComplete / WorkerFail：进度上报与终态写入
//   - RecoverStaleJobs：周期回收（claim 超过 cfg.WorkerStaleThreshold 仍无心跳的任务）
//   - ListOnlineWorkers / ListWorkerTokens / CreateWorkerToken / RevokeWorkerToken：admin 用
//
// 并发安全：
//   - claim 用 CAS UPDATE WHERE status=PENDING，确保多 worker 同抢同一任务时只有一个成功
//   - heartbeat / complete / fail 用 worker_id 校验，避免 worker A 误改 worker B 的 job
package service

import (
	"crypto/rand"
	"encoding/base32"
	"errors"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strings"
	"time"

	"golang.org/x/crypto/bcrypt"
	"gorm.io/gorm"

	"github.com/hor1zon777/m3u8-preview-go/internal/dto"
	"github.com/hor1zon777/m3u8-preview-go/internal/middleware"
	"github.com/hor1zon777/m3u8-preview-go/internal/model"
)

// 远程 worker 协作相关错误。
var (
	ErrWorkerJobNotOwned     = errors.New("worker does not own this job")
	ErrWorkerJobNotRunning   = errors.New("job not in RUNNING state")
	ErrWorkerTokenNotFound   = errors.New("worker token not found")
	ErrWorkerTokenAlreadyRev = errors.New("worker token already revoked")
)

// === Worker 协作 ===

// RegisterWorker upsert subtitle_workers 一条。
// tokenID 由 RequireWorkerAuth 注入到 ctx 后由 handler 传入。
func (s *SubtitleService) RegisterWorker(tokenID string, req dto.WorkerRegisterRequest) (*dto.WorkerRegisterResponse, error) {
	if req.WorkerID == "" {
		return nil, fmt.Errorf("workerId required")
	}
	now := time.Now()

	// 先尝试更新，找不到再插入。GORM upsert 用 ON CONFLICT 在 SQLite 也支持，
	// 但这里"找不到再插"逻辑更直观且 worker 数量本身就少（个位数），无需高性能。
	var existing model.SubtitleWorker
	err := s.db.Where("id = ?", req.WorkerID).Take(&existing).Error
	if err != nil && !errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, fmt.Errorf("query worker: %w", err)
	}
	if err == nil {
		// 已注册过：更新 token / name / version / gpu / last_seen
		if err := s.db.Model(&existing).Updates(map[string]any{
			"token_id":     tokenID,
			"name":         req.Name,
			"version":      req.Version,
			"gpu":          req.GPU,
			"last_seen_at": now,
		}).Error; err != nil {
			return nil, fmt.Errorf("update worker: %w", err)
		}
	} else {
		// 新注册
		w := model.SubtitleWorker{
			ID:           req.WorkerID,
			TokenID:      tokenID,
			Name:         req.Name,
			Version:      req.Version,
			GPU:          req.GPU,
			LastSeenAt:   now,
			RegisteredAt: now,
		}
		if err := s.db.Create(&w).Error; err != nil {
			return nil, fmt.Errorf("create worker: %w", err)
		}
	}

	log.Printf("[subtitle/worker] registered worker=%s name=%q gpu=%q", req.WorkerID, req.Name, req.GPU)
	return &dto.WorkerRegisterResponse{
		WorkerID:             req.WorkerID,
		ServerTime:           now.UnixMilli(),
		WorkerStaleThreshold: int64(s.cfg.WorkerStaleThreshold.Seconds()),
	}, nil
}

// ClaimNextJob 原子认领一条 PENDING。
//
// SQLite 没有 SELECT FOR UPDATE，靠 CAS UPDATE 保证：
//  1. 校验全局 / token 限流（超额直接返回 nil 让 worker sleep 重试）
//  2. 找最早的 PENDING（FIFO）
//  3. UPDATE WHERE id=? AND status=PENDING（带条件）
//  4. RowsAffected == 1 = 抢到；== 0 = 同时被别人抢了，重试
//
// 重试 3 次仍失败说明高竞争，返回 nil（worker 下次轮询再试）。
//
// 没有 PENDING 时返回 (nil, nil)，handler 据此回 204 No Content。
//
// 限流：
//   - cfg.GlobalMaxConcurrency > 0 时校验全局 RUNNING 数
//   - 该 worker 所属 token.MaxConcurrency > 0 时校验 token 级 RUNNING 数
//   - 任意一个超额都返回 (nil, nil)，让其它 token 的 worker 有机会抢任务
func (s *SubtitleService) ClaimNextJob(workerID string) (*dto.WorkerClaimedJob, error) {
	if workerID == "" {
		return nil, fmt.Errorf("workerId required")
	}

	// 限流前置校验
	if !s.checkClaimLimits(workerID) {
		return nil, nil
	}

	now := time.Now()

	for attempt := range 3 {
		var job model.SubtitleJob
		err := s.db.Where("status = ?", model.SubtitleStatusPending).
			Order("created_at ASC").
			Take(&job).Error
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, nil // 没活
		}
		if err != nil {
			return nil, fmt.Errorf("find pending job: %w", err)
		}

		// CAS UPDATE
		res := s.db.Model(&model.SubtitleJob{}).
			Where("id = ? AND status = ?", job.ID, model.SubtitleStatusPending).
			Updates(map[string]any{
				"status":             model.SubtitleStatusRunning,
				"stage":              model.SubtitleStageExtracting,
				"progress":           5,
				"started_at":         &now,
				"claimed_by":         workerID,
				"claimed_at":         &now,
				"last_heartbeat_at":  &now,
				"error_msg":          "",
			})
		if res.Error != nil {
			return nil, fmt.Errorf("claim cas update: %w", res.Error)
		}
		if res.RowsAffected == 0 {
			// 被别人抢了，重试
			log.Printf("[subtitle/worker] claim contention attempt=%d worker=%s job=%s", attempt+1, workerID, job.ID)
			continue
		}

		// 抢到：刷新 worker 注册表的 current_job + last_seen
		_ = s.db.Model(&model.SubtitleWorker{}).Where("id = ?", workerID).Updates(map[string]any{
			"current_job_id": job.ID,
			"last_seen_at":   now,
		}).Error

		// 拉 media 信息
		var media model.Media
		if err := s.db.Take(&media, "id = ?", job.MediaID).Error; err != nil {
			// media 不存在了：直接 fail 这个 job 让 worker 跳过
			s.markFailed(job.MediaID, fmt.Errorf("media not found: %w", err))
			continue
		}

		log.Printf("[subtitle/worker] claimed job=%s media=%s by worker=%s", job.ID, job.MediaID, workerID)
		return &dto.WorkerClaimedJob{
			JobID:      job.ID,
			MediaID:    job.MediaID,
			MediaTitle: media.Title,
			M3u8URL:    media.M3u8URL,
			SourceLang: job.SourceLang,
			TargetLang: job.TargetLang,
			Headers:    HeadersForM3u8URL(media.M3u8URL),
		}, nil
	}
	return nil, nil
}

// checkClaimLimits 在 ClaimNextJob 抢占 PENDING 前校验全局 / token 并发上限。
// 返回 true 表示当前 worker 可以认领新任务。
//
// 校验顺序：
//  1. 全局上限（cfg.GlobalMaxConcurrency > 0 时生效）
//  2. Token 上限（worker 所属 token.MaxConcurrency > 0 时生效）
//
// 任一超额或查询失败都返回 false，让 worker 自然 sleep 重试。
// 失败时记录日志但不阻塞业务（保守策略：宁可让任务停一会儿也不打垮上游）。
func (s *SubtitleService) checkClaimLimits(workerID string) bool {
	// 1) 全局上限
	if s.cfg.GlobalMaxConcurrency > 0 {
		var running int64
		if err := s.db.Model(&model.SubtitleJob{}).
			Where("status = ?", model.SubtitleStatusRunning).
			Count(&running).Error; err != nil {
			log.Printf("[subtitle/worker] count global running failed: %v", err)
			return false
		}
		if running >= int64(s.cfg.GlobalMaxConcurrency) {
			return false
		}
	}

	// 2) Token 上限
	//
	// worker 注册表 / token 表查询失败统一视为"无 token 关联"——只走全局上限。
	// 兼容场景：老 worker 没 register 直接 claim、未运行 AutoMigrate 的早期表结构、测试 fixture。
	var worker model.SubtitleWorker
	if err := s.db.Where("id = ?", workerID).Take(&worker).Error; err != nil {
		if !errors.Is(err, gorm.ErrRecordNotFound) {
			log.Printf("[subtitle/worker] load worker failed worker=%s: %v", workerID, err)
		}
		return true
	}
	if worker.TokenID == "" {
		return true
	}
	var tok model.SubtitleWorkerToken
	if err := s.db.Where("id = ?", worker.TokenID).Take(&tok).Error; err != nil {
		if !errors.Is(err, gorm.ErrRecordNotFound) {
			log.Printf("[subtitle/worker] load token failed worker=%s token=%s: %v", workerID, worker.TokenID, err)
		}
		return true
	}
	if tok.MaxConcurrency <= 0 {
		return true // 0 表示不限
	}

	// 当前该 token 名下 worker 持有的 RUNNING 任务数
	var tokenRunning int64
	if err := s.db.Table("subtitle_jobs AS sj").
		Joins("JOIN subtitle_workers AS sw ON sw.id = sj.claimed_by").
		Where("sj.status = ? AND sw.token_id = ?", model.SubtitleStatusRunning, worker.TokenID).
		Count(&tokenRunning).Error; err != nil {
		log.Printf("[subtitle/worker] count token running failed token=%s: %v", worker.TokenID, err)
		return true // 统计失败不阻塞，让全局上限兜底
	}
	return tokenRunning < int64(tok.MaxConcurrency)
}

// WorkerHeartbeat 上报阶段 + 进度。
//
// 校验 ownership：只有 claimed_by == workerID 且 status==RUNNING 的任务能更新。
// 防御 worker 抢到任务后又被 stale 回收时，原 worker 仍在跑会误改新 worker 的状态。
func (s *SubtitleService) WorkerHeartbeat(jobID, workerID, stage string, progress int) error {
	if progress < 0 {
		progress = 0
	}
	if progress > 99 {
		progress = 99
	}
	now := time.Now()
	res := s.db.Model(&model.SubtitleJob{}).
		Where("id = ? AND claimed_by = ? AND status = ?", jobID, workerID, model.SubtitleStatusRunning).
		Updates(map[string]any{
			"stage":             stage,
			"progress":          progress,
			"last_heartbeat_at": &now,
		})
	if res.Error != nil {
		return fmt.Errorf("heartbeat update: %w", res.Error)
	}
	if res.RowsAffected == 0 {
		return ErrWorkerJobNotOwned
	}

	// 顺手刷 worker last_seen
	_ = s.db.Model(&model.SubtitleWorker{}).Where("id = ?", workerID).Update("last_seen_at", now).Error
	return nil
}

// WorkerComplete worker 上传完成的 VTT 文件 + 元数据。
//
// 流程：
//  1. 校验 ownership（CAS）
//  2. 写 VTT 文件到 SubtitlesDir
//  3. UPDATE 任务为 DONE
//  4. 更新 worker 统计
func (s *SubtitleService) WorkerComplete(jobID string, meta dto.WorkerCompleteMeta, vttBody []byte) error {
	if len(vttBody) == 0 {
		return fmt.Errorf("vtt body empty")
	}
	if !looksLikeVTT(vttBody) {
		return fmt.Errorf("payload does not look like a WebVTT file")
	}

	var job model.SubtitleJob
	if err := s.db.Where("id = ? AND claimed_by = ?", jobID, meta.WorkerID).Take(&job).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return ErrWorkerJobNotOwned
		}
		return fmt.Errorf("query job: %w", err)
	}
	if job.Status != model.SubtitleStatusRunning {
		return ErrWorkerJobNotRunning
	}

	// 写 VTT 文件（先 .tmp 后 rename，原子落盘）
	relPath := job.MediaID + ".vtt"
	absPath := filepath.Join(s.cfg.SubtitlesDir, relPath)
	if err := os.MkdirAll(filepath.Dir(absPath), 0o755); err != nil {
		return fmt.Errorf("mkdir subtitles: %w", err)
	}
	tmp := absPath + ".tmp"
	if err := os.WriteFile(tmp, vttBody, 0o644); err != nil {
		return fmt.Errorf("write vtt tmp: %w", err)
	}
	if err := os.Rename(tmp, absPath); err != nil {
		_ = os.Remove(tmp)
		return fmt.Errorf("rename vtt: %w", err)
	}

	finished := time.Now()
	if err := s.db.Model(&model.SubtitleJob{}).Where("id = ?", jobID).Updates(map[string]any{
		"status":            model.SubtitleStatusDone,
		"stage":             model.SubtitleStageDone,
		"progress":          100,
		"vtt_path":          relPath,
		"segment_count":     meta.SegmentCount,
		"asr_model":         meta.ASRModel,
		"mt_model":          meta.MTModel,
		"finished_at":       &finished,
		"last_heartbeat_at": &finished,
		"error_msg":         "",
	}).Error; err != nil {
		return fmt.Errorf("mark done: %w", err)
	}

	// 更新 worker 统计：completed_jobs+1，清空 current_job_id
	_ = s.db.Model(&model.SubtitleWorker{}).Where("id = ?", meta.WorkerID).Updates(map[string]any{
		"current_job_id":  "",
		"last_seen_at":    finished,
		"completed_jobs":  gorm.Expr("completed_jobs + 1"),
	}).Error

	log.Printf("[subtitle/worker] DONE job=%s media=%s worker=%s segments=%d duration=%s",
		jobID, job.MediaID, meta.WorkerID, meta.SegmentCount, finished.Sub(timeOrNow(job.StartedAt)))
	return nil
}

// WorkerFail worker 上报失败。
func (s *SubtitleService) WorkerFail(jobID, workerID, errMsg string) error {
	var job model.SubtitleJob
	if err := s.db.Where("id = ? AND claimed_by = ?", jobID, workerID).Take(&job).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return ErrWorkerJobNotOwned
		}
		return fmt.Errorf("query job: %w", err)
	}

	finished := time.Now()
	msg := truncateString(errMsg, 1000)
	if err := s.db.Model(&model.SubtitleJob{}).Where("id = ?", jobID).Updates(map[string]any{
		"status":      model.SubtitleStatusFailed,
		"stage":       model.SubtitleStageQueued,
		"error_msg":   msg,
		"finished_at": &finished,
	}).Error; err != nil {
		return fmt.Errorf("mark failed: %w", err)
	}

	_ = s.db.Model(&model.SubtitleWorker{}).Where("id = ?", workerID).Updates(map[string]any{
		"current_job_id": "",
		"last_seen_at":   finished,
		"failed_jobs":    gorm.Expr("failed_jobs + 1"),
	}).Error

	log.Printf("[subtitle/worker] FAILED job=%s media=%s worker=%s: %s", jobID, job.MediaID, workerID, msg)
	return nil
}

// === Stale 任务回收 ===

// runStaleRecoveryLoop 周期回收僵尸任务。Start() 启动调用。
//
// 每 60s 跑一次：找出 status=RUNNING 且 last_heartbeat_at + WorkerStaleThreshold < now 的，
// 重置回 PENDING + 清空 claimed_by 让其它 worker 重新认领。
//
// 不会丢已完成的工作：worker 即使在写 VTT 前一刻崩溃，也会在重新跑时从头开始
// （单 ASR 任务幂等，多次跑结果相同）。
func (s *SubtitleService) runStaleRecoveryLoop() {
	ticker := time.NewTicker(60 * time.Second)
	defer ticker.Stop()

	// 启动时立刻跑一次（处理上次崩溃残留）
	s.recoverStaleJobsOnce()

	for {
		select {
		case <-s.stop:
			return
		case <-s.ctx.Done():
			return
		case <-ticker.C:
			s.recoverStaleJobsOnce()
		}
	}
}

// recoverStaleJobsOnce 单次扫描 + 重置。
func (s *SubtitleService) recoverStaleJobsOnce() {
	threshold := time.Now().Add(-s.cfg.WorkerStaleThreshold)

	var stale []model.SubtitleJob
	// 两类需要回收的：
	//  - last_heartbeat_at < threshold（明确超时）
	//  - claimed_at IS NOT NULL AND last_heartbeat_at IS NULL AND claimed_at < threshold
	//    （worker 抢到后没及时 heartbeat 就崩了）
	err := s.db.Where(
		"status = ? AND ((last_heartbeat_at IS NOT NULL AND last_heartbeat_at < ?) OR (last_heartbeat_at IS NULL AND claimed_at IS NOT NULL AND claimed_at < ?))",
		model.SubtitleStatusRunning, threshold, threshold,
	).Find(&stale).Error
	if err != nil {
		log.Printf("[subtitle/worker] stale scan failed: %v", err)
		return
	}
	if len(stale) == 0 {
		return
	}

	ids := make([]string, 0, len(stale))
	for _, j := range stale {
		ids = append(ids, j.ID)
	}
	res := s.db.Model(&model.SubtitleJob{}).
		Where("id IN ? AND status = ?", ids, model.SubtitleStatusRunning).
		Updates(map[string]any{
			"status":            model.SubtitleStatusPending,
			"stage":             model.SubtitleStageQueued,
			"progress":          0,
			"claimed_by":        "",
			"claimed_at":        nil,
			"last_heartbeat_at": nil,
			"error_msg":         fmt.Sprintf("worker stale (no heartbeat in %s), reset to PENDING", s.cfg.WorkerStaleThreshold),
		})
	if res.Error != nil {
		log.Printf("[subtitle/worker] stale reset failed: %v", res.Error)
		return
	}
	log.Printf("[subtitle/worker] reset %d stale RUNNING jobs (threshold=%s)", res.RowsAffected, s.cfg.WorkerStaleThreshold)
}

// === Admin 用 ===

// ListOnlineWorkers 返回 last_seen 在 staleThreshold 内的 worker 列表。
// 实际返回所有 last_seen 在 24h 内的，前端按 Online 字段判断在线。
func (s *SubtitleService) ListOnlineWorkers() ([]dto.SubtitleWorkerItem, error) {
	cutoff := time.Now().Add(-24 * time.Hour)
	var workers []model.SubtitleWorker
	if err := s.db.Where("last_seen_at > ?", cutoff).
		Order("last_seen_at DESC").Find(&workers).Error; err != nil {
		return nil, fmt.Errorf("list workers: %w", err)
	}

	onlineCutoff := time.Now().Add(-s.cfg.WorkerStaleThreshold)
	out := make([]dto.SubtitleWorkerItem, 0, len(workers))
	for _, w := range workers {
		out = append(out, dto.SubtitleWorkerItem{
			ID:            w.ID,
			Name:          w.Name,
			Version:       w.Version,
			GPU:           w.GPU,
			CurrentJobID:  w.CurrentJobID,
			LastSeenAt:    w.LastSeenAt,
			RegisteredAt:  w.RegisteredAt,
			CompletedJobs: w.CompletedJobs,
			FailedJobs:    w.FailedJobs,
			Online:        w.LastSeenAt.After(onlineCutoff),
		})
	}
	return out, nil
}

// ListWorkerTokens 列出所有 token（不含明文）。带每个 token 的 currentRunning 实时统计。
func (s *SubtitleService) ListWorkerTokens() ([]dto.SubtitleWorkerTokenItem, error) {
	var tokens []model.SubtitleWorkerToken
	if err := s.db.Order("created_at DESC").Find(&tokens).Error; err != nil {
		return nil, err
	}
	if len(tokens) == 0 {
		return []dto.SubtitleWorkerTokenItem{}, nil
	}

	// 一次性查所有 token 的 RUNNING 数（避免 N+1）
	type runRow struct {
		TokenID string
		C       int64
	}
	var runs []runRow
	if err := s.db.Table("subtitle_jobs AS sj").
		Select("sw.token_id AS token_id, COUNT(*) AS c").
		Joins("JOIN subtitle_workers AS sw ON sw.id = sj.claimed_by").
		Where("sj.status = ?", model.SubtitleStatusRunning).
		Group("sw.token_id").
		Scan(&runs).Error; err != nil {
		return nil, fmt.Errorf("count running by token: %w", err)
	}
	runMap := make(map[string]int, len(runs))
	for _, r := range runs {
		runMap[r.TokenID] = int(r.C)
	}

	out := make([]dto.SubtitleWorkerTokenItem, 0, len(tokens))
	for _, t := range tokens {
		out = append(out, dto.SubtitleWorkerTokenItem{
			ID:             t.ID,
			Name:           t.Name,
			TokenPrefix:    t.TokenPrefix,
			MaxConcurrency: t.MaxConcurrency,
			CurrentRunning: runMap[t.ID],
			CreatedAt:      t.CreatedAt,
			LastUsedAt:     t.LastUsedAt,
			RevokedAt:      t.RevokedAt,
		})
	}
	return out, nil
}

// CreateWorkerToken 生成一条新 token。明文仅本次返回。
//
// 规则：
//   - 生成 32 字符 base32 随机串（[a-z2-7]，160 bit 熵）
//   - 拼成 "mwt_<32 chars>"，明文长度 36
//   - bcrypt cost=12 存 hash
//   - TokenPrefix 取明文前 12 位，方便面板识别 + 中间件按前缀检索
//   - maxConcurrency 控制该 token 名下 worker 集合并发上限；0 / 负数会被强制为 1
func (s *SubtitleService) CreateWorkerToken(name string, maxConcurrency int) (string, *dto.SubtitleWorkerTokenItem, error) {
	name = strings.TrimSpace(name)
	if name == "" {
		return "", nil, fmt.Errorf("name required")
	}
	if maxConcurrency <= 0 {
		maxConcurrency = 1
	}
	if maxConcurrency > 64 {
		maxConcurrency = 64
	}

	plaintext, err := generateWorkerTokenPlaintext()
	if err != nil {
		return "", nil, fmt.Errorf("generate token: %w", err)
	}
	hash, err := bcrypt.GenerateFromPassword([]byte(plaintext), bcrypt.DefaultCost)
	if err != nil {
		return "", nil, fmt.Errorf("bcrypt: %w", err)
	}

	rec := model.SubtitleWorkerToken{
		Name:           name,
		TokenHash:      string(hash),
		TokenPrefix:    plaintext[:middleware.WorkerTokenPrefix],
		MaxConcurrency: maxConcurrency,
	}
	if err := s.db.Create(&rec).Error; err != nil {
		return "", nil, fmt.Errorf("create token: %w", err)
	}
	log.Printf("[subtitle/worker] admin created token id=%s name=%q prefix=%s maxConcurrency=%d", rec.ID, rec.Name, rec.TokenPrefix, rec.MaxConcurrency)
	return plaintext, &dto.SubtitleWorkerTokenItem{
		ID:             rec.ID,
		Name:           rec.Name,
		TokenPrefix:    rec.TokenPrefix,
		MaxConcurrency: rec.MaxConcurrency,
		CreatedAt:      rec.CreatedAt,
	}, nil
}

// UpdateWorkerToken 修改 token 字段（目前仅支持改 maxConcurrency）。
// 已吊销的 token 不允许修改。
func (s *SubtitleService) UpdateWorkerToken(id string, req dto.SubtitleWorkerTokenUpdateRequest) (*dto.SubtitleWorkerTokenItem, error) {
	var rec model.SubtitleWorkerToken
	if err := s.db.Where("id = ?", id).Take(&rec).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, ErrWorkerTokenNotFound
		}
		return nil, err
	}
	if rec.RevokedAt != nil {
		return nil, ErrWorkerTokenAlreadyRev
	}

	updates := map[string]any{}
	if req.MaxConcurrency != nil {
		v := *req.MaxConcurrency
		if v < 0 {
			v = 0
		}
		if v > 64 {
			v = 64
		}
		updates["max_concurrency"] = v
		rec.MaxConcurrency = v
	}
	if len(updates) == 0 {
		// 没有变更
		return &dto.SubtitleWorkerTokenItem{
			ID:             rec.ID,
			Name:           rec.Name,
			TokenPrefix:    rec.TokenPrefix,
			MaxConcurrency: rec.MaxConcurrency,
			CreatedAt:      rec.CreatedAt,
			LastUsedAt:     rec.LastUsedAt,
			RevokedAt:      rec.RevokedAt,
		}, nil
	}
	if err := s.db.Model(&rec).Updates(updates).Error; err != nil {
		return nil, fmt.Errorf("update token: %w", err)
	}
	log.Printf("[subtitle/worker] admin updated token id=%s maxConcurrency=%d", rec.ID, rec.MaxConcurrency)
	return &dto.SubtitleWorkerTokenItem{
		ID:             rec.ID,
		Name:           rec.Name,
		TokenPrefix:    rec.TokenPrefix,
		MaxConcurrency: rec.MaxConcurrency,
		CreatedAt:      rec.CreatedAt,
		LastUsedAt:     rec.LastUsedAt,
		RevokedAt:      rec.RevokedAt,
	}, nil
}

// RevokeWorkerToken 软删除一条 token，并清缓存。
// 关联的 SubtitleWorker 通过外键 OnDelete:CASCADE 不会级联（这里是软删，DB 行还在）；
// 中间件因 revoked_at IS NOT NULL 拒绝该 token，且缓存被全清。
func (s *SubtitleService) RevokeWorkerToken(id string) error {
	var rec model.SubtitleWorkerToken
	if err := s.db.Where("id = ?", id).Take(&rec).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return ErrWorkerTokenNotFound
		}
		return err
	}
	if rec.RevokedAt != nil {
		return ErrWorkerTokenAlreadyRev
	}
	now := time.Now()
	if err := s.db.Model(&rec).Update("revoked_at", &now).Error; err != nil {
		return fmt.Errorf("revoke: %w", err)
	}
	// 清缓存：吊销后已缓存的 token 不应再放行
	middleware.PurgeWorkerTokenCache()
	log.Printf("[subtitle/worker] admin revoked token id=%s name=%q", rec.ID, rec.Name)
	return nil
}

// === helpers ===

// generateWorkerTokenPlaintext 生成 "mwt_<32 chars base32>"。
func generateWorkerTokenPlaintext() (string, error) {
	const tokenBytes = 20 // 20 bytes → 32 base32 chars (no padding)
	buf := make([]byte, tokenBytes)
	if _, err := rand.Read(buf); err != nil {
		return "", err
	}
	enc := strings.ToLower(strings.TrimRight(base32.StdEncoding.EncodeToString(buf), "="))
	return "mwt_" + enc, nil
}

// looksLikeVTT 简单嗅探：跳过 BOM 后看头几个字符是否是 "WEBVTT"。
func looksLikeVTT(b []byte) bool {
	// 去 UTF-8 BOM
	if len(b) >= 3 && b[0] == 0xEF && b[1] == 0xBB && b[2] == 0xBF {
		b = b[3:]
	}
	if len(b) < 6 {
		return false
	}
	return string(b[:6]) == "WEBVTT"
}

// timeOrNow 返回 *time.Time 解引用值或当前时间（避免 nil 解引用）。
func timeOrNow(t *time.Time) time.Time {
	if t == nil {
		return time.Now()
	}
	return *t
}
