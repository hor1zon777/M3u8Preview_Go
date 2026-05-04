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
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"golang.org/x/crypto/bcrypt"
	"gorm.io/gorm"

	"github.com/hor1zon777/m3u8-preview-go/internal/dto"
	"github.com/hor1zon777/m3u8-preview-go/internal/middleware"
	"github.com/hor1zon777/m3u8-preview-go/internal/model"
)

// v3 分布式 worker 错误码（与 distributed-worker.md v3 §0.4 保持一致）。
const (
	WorkerErrCodeAudioSHA256Mismatch = "WORKER_AUDIO_SHA256_MISMATCH"
	WorkerErrCodeAudioNotReady       = "WORKER_AUDIO_NOT_READY"
	WorkerErrCodeAudioGone           = "WORKER_AUDIO_GONE"
	WorkerErrCodeJobNotOwned         = "WORKER_JOB_NOT_OWNED"
	WorkerErrCodeCapabilityMismatch  = "WORKER_CAPABILITY_MISMATCH"
	// v3 broker 专属错误码：
	WorkerErrCodeAudioOwnerOffline = "WORKER_AUDIO_OWNER_OFFLINE" // audio worker 不在 long-poll 队列内 / 超时
	WorkerErrCodeAudioStreamStuck  = "WORKER_AUDIO_STREAM_STUCK"  // audio worker 收到通知但没及时上传
)

// fileExists 简单包装 os.Stat 错误检查。仍保留供 stale recovery 等本地路径检查用。
func fileExists(path string) bool {
	if path == "" {
		return false
	}
	_, err := os.Stat(path)
	return err == nil
}

// 远程 worker 协作相关错误。
var (
	ErrWorkerJobNotOwned     = errors.New("worker does not own this job")
	ErrWorkerJobNotRunning   = errors.New("job not in RUNNING state")
	ErrWorkerTokenNotFound   = errors.New("worker token not found")
	ErrWorkerTokenAlreadyRev = errors.New("worker token already revoked")
)

// 全部已知的 worker capability 字符串。注册时不识别的 capability 会被丢弃，避免污染 DB。
var knownCapabilities = map[string]struct{}{
	model.WorkerCapAudioExtract: {},
	model.WorkerCapASRSubtitle:  {},
}

// === Worker 协作 ===

// RegisterWorker upsert subtitle_workers 一条。
// tokenID 由 RequireWorkerAuth 注入到 ctx 后由 handler 传入。
//
// Capabilities 兼容策略：
//   - req.Capabilities 为空 → 旧 client，按 ["audio_extract","asr_subtitle"] 处理（向后兼容单机部署）
//   - 含未知值 → 静默丢弃未知值；若全部被丢弃则回落到全能默认
//   - 服务端实际接受的能力集会通过 RegisterResponse.AcceptedCapabilities 回显，
//     客户端可据此 sanity check
func (s *SubtitleService) RegisterWorker(tokenID string, req dto.WorkerRegisterRequest) (*dto.WorkerRegisterResponse, error) {
	if req.WorkerID == "" {
		return nil, fmt.Errorf("workerId required")
	}
	now := time.Now()

	// 兼容旧 client：缺失 capabilities → 默认全能
	caps := normalizeCapabilities(req.Capabilities)
	capsJSON, err := json.Marshal(caps)
	if err != nil {
		return nil, fmt.Errorf("marshal capabilities: %w", err)
	}

	// 先尝试更新，找不到再插入。GORM upsert 用 ON CONFLICT 在 SQLite 也支持，
	// 但这里"找不到再插"逻辑更直观且 worker 数量本身就少（个位数），无需高性能。
	var existing model.SubtitleWorker
	err = s.db.Where("id = ?", req.WorkerID).Take(&existing).Error
	if err != nil && !errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, fmt.Errorf("query worker: %w", err)
	}
	if err == nil {
		// 已注册过：更新 token / name / version / gpu / capabilities / last_seen
		if err := s.db.Model(&existing).Updates(map[string]any{
			"token_id":     tokenID,
			"name":         req.Name,
			"version":      req.Version,
			"gpu":          req.GPU,
			"capabilities": string(capsJSON),
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
			Capabilities: string(capsJSON),
			LastSeenAt:   now,
			RegisteredAt: now,
		}
		if err := s.db.Create(&w).Error; err != nil {
			return nil, fmt.Errorf("create worker: %w", err)
		}
	}

	log.Printf("[subtitle/worker] registered worker=%s name=%q gpu=%q caps=%v", req.WorkerID, req.Name, req.GPU, caps)
	return &dto.WorkerRegisterResponse{
		WorkerID:             req.WorkerID,
		ServerTime:           now.UnixMilli(),
		WorkerStaleThreshold: int64(s.snap().WorkerStaleThreshold.Seconds()),
		AcceptedCapabilities: caps,
	}, nil
}

// normalizeCapabilities 接受 worker 上报的 capability 列表，返回服务端实际接受的去重排序结果。
//
// 规则：
//   - 空 / 全 unknown → ["audio_extract","asr_subtitle"]（兼容旧单机 worker）
//   - 含 unknown 值 → 丢弃 unknown，保留已知部分
//   - 输出按字母序排序便于 DB 中字符串比较稳定
func normalizeCapabilities(in []string) []string {
	seen := make(map[string]struct{}, len(in))
	out := make([]string, 0, len(in))
	for _, c := range in {
		c = strings.TrimSpace(c)
		if c == "" {
			continue
		}
		if _, ok := knownCapabilities[c]; !ok {
			continue
		}
		if _, dup := seen[c]; dup {
			continue
		}
		seen[c] = struct{}{}
		out = append(out, c)
	}
	if len(out) == 0 {
		return []string{model.WorkerCapAudioExtract, model.WorkerCapASRSubtitle}
	}
	sort.Strings(out)
	return out
}

// parseCapabilities 把存储在 DB 里的 capabilities JSON 字符串解析回 []string，
// 任何解析错误都回落到默认全能集合（与 normalizeCapabilities 的兜底一致）。
func parseCapabilities(stored string) []string {
	if strings.TrimSpace(stored) == "" {
		return []string{model.WorkerCapAudioExtract, model.WorkerCapASRSubtitle}
	}
	var caps []string
	if err := json.Unmarshal([]byte(stored), &caps); err != nil {
		log.Printf("[subtitle/worker] parse stored capabilities failed (%q): %v; fallback to full", stored, err)
		return []string{model.WorkerCapAudioExtract, model.WorkerCapASRSubtitle}
	}
	return normalizeCapabilities(caps)
}

// hasCapability 在 capability 切片中查目标字符串。
func hasCapability(caps []string, target string) bool {
	for _, c := range caps {
		if c == target {
			return true
		}
	}
	return false
}

// ClaimNextJob 按 worker 自报的 capability 派发任务。
//
// v2 双 worker 协作流程：
//
//   - 派活优先级：subtitle 任务（GPU 资源稀缺，先满负荷）> audio 任务（带宽充足，机 A 可并发）
//   - audio_extract worker：从 status=PENDING / stage=queued 中抢占；
//     抢到后切到 status=RUNNING / stage=downloading，DTO 返回 m3u8Url + Headers
//   - asr_subtitle worker：从 status=RUNNING / stage=audio_uploaded 中抢占（按 audio_uploaded_at 升序）；
//     抢到后切到 stage=asr，DTO 返回 audioArtifactUrl + 校验信息
//
// 限流：见 checkClaimCapacity。
//
// 没有任务时返回 (nil, nil)，handler 据此回 204 No Content。
//
// SQLite 没有 SELECT FOR UPDATE，靠 CAS UPDATE 保证：
//  1. 找最早的候选（FIFO）
//  2. UPDATE WHERE id=? AND status=? AND stage=?（带状态条件）
//  3. RowsAffected == 1 = 抢到；== 0 = 同时被别人抢了，重试
//
// 重试 3 次仍失败说明高竞争，返回 nil（worker 下次轮询再试）。
func (s *SubtitleService) ClaimNextJob(workerID string) (*dto.WorkerClaimedJob, error) {
	if workerID == "" {
		return nil, fmt.Errorf("workerId required")
	}

	// 1) 拉 worker 自身记录与 capability 集合
	var worker model.SubtitleWorker
	if err := s.db.Where("id = ?", workerID).Take(&worker).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			// worker 没注册过：兼容旧 client（直接 claim 没 register）按全能默认放行
			worker = model.SubtitleWorker{
				ID:           workerID,
				Capabilities: "",
			}
		} else {
			return nil, fmt.Errorf("load worker: %w", err)
		}
	}
	caps := parseCapabilities(worker.Capabilities)
	canAudio := hasCapability(caps, model.WorkerCapAudioExtract)
	canSubtitle := hasCapability(caps, model.WorkerCapASRSubtitle)
	if !canAudio && !canSubtitle {
		// 不应发生（normalizeCapabilities 兜底过），保险起见
		return nil, nil
	}

	// 2) 限流前置校验（全局 + token 分维度）
	allowAudio, allowSubtitle := s.checkClaimCapacity(workerID, canAudio, canSubtitle)
	if !allowAudio && !allowSubtitle {
		return nil, nil
	}

	// 3) 优先派 subtitle 任务（GPU 稀缺先满负荷），audio 次之
	if canSubtitle && allowSubtitle {
		job, err := s.tryClaimSubtitleJob(workerID)
		if err != nil {
			return nil, err
		}
		if job != nil {
			return s.toClaimedJobDTO(job)
		}
	}
	if canAudio && allowAudio {
		job, err := s.tryClaimAudioJob(workerID)
		if err != nil {
			return nil, err
		}
		if job != nil {
			return s.toClaimedJobDTO(job)
		}
	}
	return nil, nil
}

// tryClaimAudioJob 抢占一条 status=PENDING / stage=queued 的任务。
// 切换到 status=RUNNING / stage=downloading 并写 audio_worker_id。
func (s *SubtitleService) tryClaimAudioJob(workerID string) (*model.SubtitleJob, error) {
	now := time.Now()
	for attempt := 0; attempt < 3; attempt++ {
		var job model.SubtitleJob
		err := s.db.Where("status = ? AND stage = ?", model.SubtitleStatusPending, model.SubtitleStageQueued).
			Order("created_at ASC").
			Take(&job).Error
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, nil
		}
		if err != nil {
			return nil, fmt.Errorf("find queued job: %w", err)
		}

		res := s.db.Model(&model.SubtitleJob{}).
			Where("id = ? AND status = ? AND stage = ?", job.ID, model.SubtitleStatusPending, model.SubtitleStageQueued).
			Updates(map[string]any{
				"status":            model.SubtitleStatusRunning,
				"stage":             model.SubtitleStageDownloading,
				"progress":          5,
				"started_at":        &now,
				"claimed_by":        workerID,
				"audio_worker_id":   workerID,
				"claimed_at":        &now,
				"last_heartbeat_at": &now,
				"error_msg":         "",
			})
		if res.Error != nil {
			return nil, fmt.Errorf("audio claim cas: %w", res.Error)
		}
		if res.RowsAffected == 0 {
			log.Printf("[subtitle/worker] audio claim contention attempt=%d worker=%s job=%s", attempt+1, workerID, job.ID)
			continue
		}

		// 抢到后再读一次最新值，避免使用 stale 字段
		if err := s.db.Where("id = ?", job.ID).Take(&job).Error; err != nil {
			return nil, fmt.Errorf("reload job after audio claim: %w", err)
		}

		// 刷 worker 注册表
		_ = s.db.Model(&model.SubtitleWorker{}).Where("id = ?", workerID).Updates(map[string]any{
			"current_job_id": job.ID,
			"last_seen_at":   now,
		}).Error

		log.Printf("[subtitle/worker] audio claimed job=%s media=%s by worker=%s", job.ID, job.MediaID, workerID)
		return &job, nil
	}
	return nil, nil
}

// tryClaimSubtitleJob 抢占一条 status=RUNNING / stage=audio_uploaded 的任务。
// 切换到 stage=asr 并写 subtitle_worker_id。
func (s *SubtitleService) tryClaimSubtitleJob(workerID string) (*model.SubtitleJob, error) {
	now := time.Now()
	for attempt := 0; attempt < 3; attempt++ {
		var job model.SubtitleJob
		err := s.db.Where("status = ? AND stage = ?", model.SubtitleStatusRunning, model.SubtitleStageAudioUploaded).
			Order("audio_uploaded_at ASC").
			Take(&job).Error
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, nil
		}
		if err != nil {
			return nil, fmt.Errorf("find audio_uploaded job: %w", err)
		}

		res := s.db.Model(&model.SubtitleJob{}).
			Where("id = ? AND status = ? AND stage = ?", job.ID, model.SubtitleStatusRunning, model.SubtitleStageAudioUploaded).
			Updates(map[string]any{
				"stage":              model.SubtitleStageASR,
				"progress":           40,
				"claimed_by":         workerID,
				"subtitle_worker_id": workerID,
				"claimed_at":         &now,
				"last_heartbeat_at":  &now,
				"error_msg":          "",
			})
		if res.Error != nil {
			return nil, fmt.Errorf("subtitle claim cas: %w", res.Error)
		}
		if res.RowsAffected == 0 {
			log.Printf("[subtitle/worker] subtitle claim contention attempt=%d worker=%s job=%s", attempt+1, workerID, job.ID)
			continue
		}

		if err := s.db.Where("id = ?", job.ID).Take(&job).Error; err != nil {
			return nil, fmt.Errorf("reload job after subtitle claim: %w", err)
		}
		_ = s.db.Model(&model.SubtitleWorker{}).Where("id = ?", workerID).Updates(map[string]any{
			"current_job_id": job.ID,
			"last_seen_at":   now,
		}).Error

		log.Printf("[subtitle/worker] subtitle claimed job=%s media=%s by worker=%s", job.ID, job.MediaID, workerID)
		return &job, nil
	}
	return nil, nil
}

// toClaimedJobDTO 把抢到的 job 行转换成给 worker 返回的 DTO。
//
// 根据当前 stage 决定填充哪些字段：
//   - stage 在 audio 集合（downloading/extracting/encoding_intermediate）→ 派活类型 audio_extract
//   - stage 在 subtitle 集合（asr/translate/writing）→ 派活类型 asr_subtitle
//
// media 不存在时直接 markFailed 该 job 并返回 nil（让 worker 下次重试拿到下一个）。
func (s *SubtitleService) toClaimedJobDTO(job *model.SubtitleJob) (*dto.WorkerClaimedJob, error) {
	var media model.Media
	if err := s.db.Take(&media, "id = ?", job.MediaID).Error; err != nil {
		// media 不存在了：直接 markFailed 让 worker 跳过
		s.markFailed(job.MediaID, fmt.Errorf("media not found during claim: %w", err))
		return nil, nil
	}

	out := &dto.WorkerClaimedJob{
		JobID:      job.ID,
		MediaID:    job.MediaID,
		MediaTitle: media.Title,
		SourceLang: job.SourceLang,
		TargetLang: job.TargetLang,
	}

	switch {
	case model.SubtitleSubtitleStages[job.Stage]:
		// asr_subtitle 派活：返回 audio artifact url + 校验信息
		out.Stage = model.WorkerCapASRSubtitle
		out.AudioArtifactURL = s.audioArtifactURL(job.ID)
		out.AudioArtifactSize = job.AudioArtifactSize
		out.AudioArtifactSHA256 = job.AudioArtifactSHA256
		out.AudioArtifactFormat = job.AudioArtifactFormat
		out.AudioArtifactDurationMs = job.AudioArtifactDurationMs
	default:
		// audio_extract 派活（包含旧 worker 抢到的 stage=extracting 兜底）：返回 m3u8 url + headers
		out.Stage = model.WorkerCapAudioExtract
		out.M3u8URL = media.M3u8URL
		out.Headers = HeadersForM3u8URL(media.M3u8URL)
	}
	return out, nil
}

// audioArtifactURL 拼出 subtitle worker 拉 FLAC 用的 URL。
//
// 有 publicBaseURL 时返回绝对地址；否则返回相对路径（worker 会按 server URL 拼）。
func (s *SubtitleService) audioArtifactURL(jobID string) string {
	rel := fmt.Sprintf("/api/v1/worker/jobs/%s/audio", jobID)
	if s.publicBaseURL == "" {
		return rel
	}
	return s.publicBaseURL + rel
}

// checkClaimCapacity 在 ClaimNextJob 抢占前校验全局 + token 维度的并发上限。
// 返回 (allowAudio, allowSubtitle)：分别表示 audio_extract / asr_subtitle 这条线还能不能再接一个新任务。
//
// 校验语义：
//  1. 全局上限（cfg.GlobalMaxConcurrency > 0 时生效）—— 只看 RUNNING 总数，不分维度
//  2. Token 上限：
//     - MaxAudioConcurrency > 0 时校验该 token 名下所有 worker 当前持有的 audio 阶段任务数
//     - MaxSubtitleConcurrency > 0 时校验该 token 名下所有 worker 当前持有的 subtitle 阶段任务数
//     - 旧字段 MaxConcurrency > 0 仍然作为总上限兜底
//
// 任一查询失败时保守放行（让 task 拥堵也别打垮上游 LLM/磁盘）。
func (s *SubtitleService) checkClaimCapacity(workerID string, canAudio, canSubtitle bool) (allowAudio bool, allowSubtitle bool) {
	allowAudio = canAudio
	allowSubtitle = canSubtitle
	cur := s.snap()

	// 1) 全局上限
	if cur.GlobalMaxConcurrency > 0 {
		var running int64
		if err := s.db.Model(&model.SubtitleJob{}).
			Where("status = ?", model.SubtitleStatusRunning).
			Count(&running).Error; err != nil {
			log.Printf("[subtitle/worker] count global running failed: %v", err)
			return false, false
		}
		if running >= int64(cur.GlobalMaxConcurrency) {
			return false, false
		}
	}

	// 2) Token 上限
	var worker model.SubtitleWorker
	if err := s.db.Where("id = ?", workerID).Take(&worker).Error; err != nil {
		// 老 client 没 register（worker 不在表中）：仅走全局上限
		if !errors.Is(err, gorm.ErrRecordNotFound) {
			log.Printf("[subtitle/worker] load worker failed worker=%s: %v", workerID, err)
		}
		return allowAudio, allowSubtitle
	}
	if worker.TokenID == "" {
		return allowAudio, allowSubtitle
	}
	var tok model.SubtitleWorkerToken
	if err := s.db.Where("id = ?", worker.TokenID).Take(&tok).Error; err != nil {
		if !errors.Is(err, gorm.ErrRecordNotFound) {
			log.Printf("[subtitle/worker] load token failed worker=%s token=%s: %v", workerID, worker.TokenID, err)
		}
		return allowAudio, allowSubtitle
	}

	// 旧字段总上限兜底（不区分能力）
	if tok.MaxConcurrency > 0 {
		var total int64
		if err := s.db.Table("subtitle_jobs AS sj").
			Joins("JOIN subtitle_workers AS sw ON sw.id = sj.claimed_by").
			Where("sj.status = ? AND sw.token_id = ?", model.SubtitleStatusRunning, worker.TokenID).
			Count(&total).Error; err != nil {
			log.Printf("[subtitle/worker] count token total running failed token=%s: %v", worker.TokenID, err)
		} else if total >= int64(tok.MaxConcurrency) {
			return false, false
		}
	}

	// 分能力维度上限：只统计落在对应 stage 集合的 RUNNING 任务
	if allowAudio && tok.MaxAudioConcurrency > 0 {
		audioStages := []string{
			model.SubtitleStageDownloading,
			model.SubtitleStageExtracting,
			model.SubtitleStageEncodingIntermediate,
		}
		var audioRunning int64
		if err := s.db.Model(&model.SubtitleJob{}).
			Where("status = ? AND stage IN ? AND audio_worker_id IN (?)",
				model.SubtitleStatusRunning, audioStages,
				s.db.Model(&model.SubtitleWorker{}).Where("token_id = ?", worker.TokenID).Select("id"),
			).
			Count(&audioRunning).Error; err != nil {
			log.Printf("[subtitle/worker] count token audio running failed: %v", err)
		} else if audioRunning >= int64(tok.MaxAudioConcurrency) {
			allowAudio = false
		}
	}
	if allowSubtitle && tok.MaxSubtitleConcurrency > 0 {
		subStages := []string{
			model.SubtitleStageASR,
			model.SubtitleStageTranslate,
			model.SubtitleStageWriting,
		}
		var subRunning int64
		if err := s.db.Model(&model.SubtitleJob{}).
			Where("status = ? AND stage IN ? AND subtitle_worker_id IN (?)",
				model.SubtitleStatusRunning, subStages,
				s.db.Model(&model.SubtitleWorker{}).Where("token_id = ?", worker.TokenID).Select("id"),
			).
			Count(&subRunning).Error; err != nil {
			log.Printf("[subtitle/worker] count token subtitle running failed: %v", err)
		} else if subRunning >= int64(tok.MaxSubtitleConcurrency) {
			allowSubtitle = false
		}
	}
	return allowAudio, allowSubtitle
}

// validHeartbeatStages 是 worker 心跳 / 失败上报允许的 stage 集合。
// queued / done 也允许（极端情况下 worker 可能上报，服务端不主动写 done 但兜底容忍）。
var validHeartbeatStages = map[string]struct{}{
	model.SubtitleStageQueued:                {},
	model.SubtitleStageDownloading:           {},
	model.SubtitleStageExtracting:            {},
	model.SubtitleStageEncodingIntermediate:  {},
	model.SubtitleStageAudioUploaded:         {},
	model.SubtitleStageASR:                   {},
	model.SubtitleStageTranslate:             {},
	model.SubtitleStageWriting:               {},
	model.SubtitleStageDone:                  {},
}

// WorkerHeartbeat 上报阶段 + 进度。
//
// 校验 ownership：只有 claimed_by == workerID 且 status==RUNNING 的任务能更新。
// 防御 worker 抢到任务后又被 stale 回收时，原 worker 仍在跑会误改新 worker 的状态。
//
// stage 必须是 validHeartbeatStages 之一；非法值返回 BadRequest（避免 worker 把任意字符串
// 写进 DB 污染状态机）。
func (s *SubtitleService) WorkerHeartbeat(jobID, workerID, stage string, progress int) error {
	if _, ok := validHeartbeatStages[stage]; !ok {
		return fmt.Errorf("unknown stage %q", stage)
	}
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

// === v3 分布式 audio broker：audio worker 注册元数据 + 直拉直传 ===

// audioReadyAllowedStages 是 WorkerAudioReady 接受的 source stage 集合。
//
// 背景（v3.1）：v3 早期实现要求 stage 必须严格 == encoding_intermediate；但实际部署中
// audio worker 心跳间隔默认 30s，而 set_phase("encoding_intermediate") → FLAC 编码完成
// → audio_ready 整个区间通常 < 30s，心跳极易在两次 stage 切换之间被读到旧值（如
// "extracting"），导致服务端 stage 滞后于 worker 真实进度，最终 audio_ready 被 409 拒绝。
//
// 放宽到整个 audio 阶段集合（downloading / extracting / encoding_intermediate）后，
// audio_ready 端点本身成为最终裁决：worker 携带 size/sha256/duration 等元数据已经
// 充分证明 FLAC 已就绪，stage 仅用作进度展示，CAS 推进交由本端点完成。
var audioReadyAllowedStages = []string{
	model.SubtitleStageDownloading,
	model.SubtitleStageExtracting,
	model.SubtitleStageEncodingIntermediate,
}

func isAudioReadyStageAllowed(stage string) bool {
	for _, s := range audioReadyAllowedStages {
		if stage == s {
			return true
		}
	}
	return false
}

// WorkerAudioReady 是 v3 audio worker 完成本地 FLAC 编码后调用的端点处理。
//
// 与 v2 (audio-complete) 的差别：
//   - 不接收文件 body，只接受元数据 JSON
//   - audio worker 把 FLAC 留在本地，subtitle worker 拉取时通过 broker 实时桥接
//
// 行为：
//  1. 校验 ownership：claimed_by == meta.WorkerID
//  2. 校验 stage ∈ {downloading, extracting, encoding_intermediate}（v3.1 放宽，
//     避免心跳异步同步滞后于 worker 实际进度造成 409）
//  3. CAS UPDATE 任务：stage=audio_uploaded，写入 size / sha256 / format / duration_ms 元数据
//  4. claimed_by 清空，让 subtitle worker 可以抢占；audio_worker_id 保留作为 owner
//
// 注意：audio_artifact_path 字段不再使用（保留 schema 兼容旧 worker 的 v2 上传路径），
// owner 关系完全通过 audio_worker_id 表达。
func (s *SubtitleService) WorkerAudioReady(jobID string, meta dto.WorkerAudioReadyMeta) error {
	var job model.SubtitleJob
	if err := s.db.Where("id = ?", jobID).Take(&job).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return middleware.NewAppError(http.StatusNotFound, "job not found")
		}
		return middleware.WrapAppError(http.StatusInternalServerError, "query job", err)
	}
	if job.ClaimedBy != meta.WorkerID {
		return middleware.NewAppErrorWithCode(http.StatusGone, WorkerErrCodeJobNotOwned, "job not owned by this worker")
	}
	if !isAudioReadyStageAllowed(job.Stage) {
		return middleware.NewAppErrorWithCode(http.StatusConflict, WorkerErrCodeAudioNotReady,
			fmt.Sprintf("job stage %s does not allow audio-ready", job.Stage))
	}

	// 元数据合法性
	expectedSHA := strings.ToLower(strings.TrimSpace(meta.SHA256))
	if expectedSHA == "" || meta.Size <= 0 || meta.DurationMs <= 0 || strings.TrimSpace(meta.Format) == "" {
		return middleware.NewAppError(http.StatusBadRequest, "meta missing required fields")
	}

	now := time.Now()
	res := s.db.Model(&model.SubtitleJob{}).
		Where("id = ? AND claimed_by = ? AND stage IN ?", jobID, meta.WorkerID, audioReadyAllowedStages).
		Updates(map[string]any{
			"stage":                      model.SubtitleStageAudioUploaded,
			"progress":                   35,
			"audio_artifact_path":        "", // v3 不再存路径
			"audio_artifact_size":        meta.Size,
			"audio_artifact_sha256":      expectedSHA,
			"audio_artifact_format":      meta.Format,
			"audio_artifact_duration_ms": meta.DurationMs,
			"audio_uploaded_at":          &now,
			"claimed_by":                 "",
			"last_heartbeat_at":          &now,
			"error_msg":                  "",
		})
	if res.Error != nil {
		return middleware.WrapAppError(http.StatusInternalServerError, "update job after audio-ready", res.Error)
	}
	if res.RowsAffected == 0 {
		return middleware.NewAppErrorWithCode(http.StatusGone, WorkerErrCodeJobNotOwned, "job state changed")
	}

	_ = s.db.Model(&model.SubtitleWorker{}).Where("id = ?", meta.WorkerID).Updates(map[string]any{
		"current_job_id": "",
		"last_seen_at":   now,
	}).Error

	log.Printf("[subtitle/worker] audio-ready job=%s media=%s owner=%s size=%d sha=%s dur=%dms format=%s",
		jobID, job.MediaID, meta.WorkerID, meta.Size, expectedSHA[:min(8, len(expectedSHA))], meta.DurationMs, meta.Format)
	return nil
}

// WorkerAudioFetchPoll 是 audio worker long-poll 的处理：阻塞等待服务端下发 fetch / cleanup 指令。
//
// timeout 为客户端可接受的最长 hold 时间（默认 25s，handler 据此设 read deadline）。
// 返回 nil（无任务）或 *AudioFetchTask。
func (s *SubtitleService) WorkerAudioFetchPoll(workerID string, timeout time.Duration) (*AudioFetchTask, error) {
	if workerID == "" {
		return nil, middleware.NewAppError(http.StatusBadRequest, "missing workerId")
	}
	// 顺手刷一下 last_seen，让 admin 仍能感知 audio worker 在线
	now := time.Now()
	_ = s.db.Model(&model.SubtitleWorker{}).
		Where("id = ?", workerID).
		Update("last_seen_at", now).Error
	return s.audioBroker.Poll(workerID, timeout)
}

// WorkerAudioStreamReceive 是 audio worker POST /audio-stream 的处理：把 body 流式送到等待中的
// subtitle worker GET。
//
// 处理路径：
//  1. 找到 jobID 对应的 broker fetchCoupling（必须先有 subtitle worker 在等）
//  2. broker.ReceiveStream(body) → io.Copy 到 pipe writer → subtitle worker 拿到流
//
// 服务端不做 SHA256 校验（subtitle worker 收到完整流后自己校验）。
func (s *SubtitleService) WorkerAudioStreamReceive(jobID, workerID string, body io.Reader) error {
	if jobID == "" || workerID == "" {
		return middleware.NewAppError(http.StatusBadRequest, "missing jobId or workerId")
	}
	// 顺手 ownership 校验：避免别的 worker 误传
	var job model.SubtitleJob
	if err := s.db.Where("id = ?", jobID).Take(&job).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return middleware.NewAppError(http.StatusNotFound, "job not found")
		}
		return middleware.WrapAppError(http.StatusInternalServerError, "query job", err)
	}
	if job.AudioWorkerID != "" && job.AudioWorkerID != workerID {
		return middleware.NewAppErrorWithCode(http.StatusForbidden, WorkerErrCodeJobNotOwned,
			"this audio worker is not the FLAC owner of this job")
	}
	written, err := s.audioBroker.ReceiveStream(jobID, body)
	if err != nil {
		if errors.Is(err, ErrAudioNoFetcher) {
			return middleware.NewAppErrorWithCode(http.StatusGone, WorkerErrCodeAudioGone,
				"no subtitle worker is currently waiting for this audio (timed out?)")
		}
		return middleware.WrapAppError(http.StatusInternalServerError, "audio stream", err)
	}
	log.Printf("[subtitle/worker] audio-stream pushed: job=%s owner=%s bytes=%d", jobID, workerID, written)
	return nil
}

// WorkerAudioFetchBroker 是 subtitle worker GET /audio 的处理：协调 audio worker 实时上传，
// 把 body 流式 pipe 到调用方提供的 ResponseWriter。
//
// 流程：
//  1. 查 job：claimed_by 必须 == subtitle workerID；audio_worker_id 必须有值（owner）
//  2. 把 fetch 通知推给 owner audio worker（broker EnqueueFetch）
//  3. broker.RequestFetch 阻塞等待 audio worker POST /audio-stream，
//     然后 io.Copy(pipe reader → respWriter)
//
// 返回 (sha256, error)：sha 给 handler 设 ETag 用；error 出现时调用方应 abort 响应。
func (s *SubtitleService) WorkerAudioFetchBroker(jobID, workerID string, respWriter io.Writer) (string, error) {
	if jobID == "" || workerID == "" {
		return "", middleware.NewAppError(http.StatusBadRequest, "missing jobId or workerId")
	}
	var job model.SubtitleJob
	if err := s.db.Where("id = ?", jobID).Take(&job).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return "", middleware.NewAppError(http.StatusNotFound, "job not found")
		}
		return "", middleware.WrapAppError(http.StatusInternalServerError, "query job", err)
	}
	if job.ClaimedBy != workerID {
		return "", middleware.NewAppErrorWithCode(http.StatusForbidden, WorkerErrCodeJobNotOwned,
			"job not owned by this worker")
	}
	if job.AudioWorkerID == "" {
		return "", middleware.NewAppErrorWithCode(http.StatusGone, WorkerErrCodeAudioGone,
			"audio artifact has no owner (audio_worker_id empty)")
	}

	// broker 桥接（broker 内部会 hold 30s 等 audio worker 上传）
	if err := s.audioBroker.RequestFetch(jobID, job.AudioWorkerID, respWriter); err != nil {
		switch {
		case errors.Is(err, ErrAudioOwnerOffline):
			return job.AudioArtifactSHA256, middleware.NewAppErrorWithCode(http.StatusServiceUnavailable,
				WorkerErrCodeAudioOwnerOffline,
				fmt.Sprintf("audio worker %s offline (no long-poll within %s)", job.AudioWorkerID, audioFetchHoldTimeout))
		case errors.Is(err, ErrAudioStreamTaken):
			return job.AudioArtifactSHA256, middleware.NewAppError(http.StatusConflict,
				"another fetch is in progress for this job")
		default:
			return job.AudioArtifactSHA256, err
		}
	}
	return job.AudioArtifactSHA256, nil
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
	absPath := filepath.Join(s.snap().SubtitlesDir, relPath)
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

	// v3 分布式：DONE 后通过 broker long-poll 通道通知 audio worker 删除本地 FLAC + 索引项。
	// 不阻塞主流程；audio worker 离线则下次启动扫盘时仍会上报 audio-ready，服务端会回 410，
	// 让 worker 自己判断"任务不存在 → 删本地"。
	if job.AudioWorkerID != "" {
		s.audioBroker.EnqueueFetch(job.AudioWorkerID, AudioFetchTask{
			Action: "cleanup",
			JobID:  jobID,
		})
		log.Printf("[subtitle/worker] cleanup task enqueued: job=%s owner=%s", jobID, job.AudioWorkerID)
	}

	log.Printf("[subtitle/worker] DONE job=%s media=%s worker=%s segments=%d duration=%s",
		jobID, job.MediaID, meta.WorkerID, meta.SegmentCount, finished.Sub(timeOrNow(job.StartedAt)))
	return nil
}

// WorkerFail worker 上报失败。
//
// v2 双 worker 协作下，根据当前 stage 决定回滚目标：
//   - audio 阶段失败（downloading / extracting / encoding_intermediate）→ 回 PENDING/queued，
//     audio_worker_id 清空，让其它 audio worker 重试
//   - subtitle 阶段失败（asr / translate / writing）：
//     · 中转 FLAC 还在 → 回 stage=audio_uploaded，subtitle_worker_id 清空，让其它 subtitle worker 重试
//     · 中转 FLAC 已被 GC 或丢失 → 回 PENDING/queued，audio/subtitle worker_id 都清空 + 清 audio_artifact_*
//   - 其它（兼容旧 worker 上报 stage=extracting 但走的是单机一条龙）→ 直接 FAILED
//
// claimed_by 在所有分支都清空，让 worker 不再尝试更新该任务（防止 stale worker 再写心跳）。
//
// 旧字段：MaxConcurrency / failed_jobs 统计仅在终态 FAILED 时增加；中间态回滚不计入失败统计，
// 避免一个 audio worker 网络抖动几次就把 FailedJobs 刷得很难看。
func (s *SubtitleService) WorkerFail(jobID, workerID, errMsg string) error {
	var job model.SubtitleJob
	if err := s.db.Where("id = ? AND claimed_by = ?", jobID, workerID).Take(&job).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return ErrWorkerJobNotOwned
		}
		return fmt.Errorf("query job: %w", err)
	}

	now := time.Now()
	msg := truncateString(errMsg, 1000)
	updates := map[string]any{
		"error_msg":         msg,
		"claimed_by":        "",
		"last_heartbeat_at": &now,
	}
	terminal := false // 是否进入终态（影响 worker.failed_jobs 统计与 finished_at）
	rollbackKind := "unknown"

	switch {
	case model.SubtitleAudioStages[job.Stage]:
		// audio 阶段失败 → 回 queued，audio_worker_id 清空
		updates["status"] = model.SubtitleStatusPending
		updates["stage"] = model.SubtitleStageQueued
		updates["progress"] = 0
		updates["audio_worker_id"] = ""
		rollbackKind = "audio_to_queued"

	case model.SubtitleSubtitleStages[job.Stage]:
		// subtitle 阶段失败：v3 看 audio_worker_id 是否还存在（FLAC 仍在 audio worker 本地）
		ownerStillKnown := job.AudioWorkerID != ""
		if ownerStillKnown {
			// owner 仍在记录里 → 回到 audio_uploaded 等下一个 subtitle worker
			updates["stage"] = model.SubtitleStageAudioUploaded
			updates["progress"] = 35
			updates["subtitle_worker_id"] = ""
			rollbackKind = "subtitle_to_audio_uploaded"
		} else {
			// 没 owner 了 → 整条任务回 queued 重头来
			updates["status"] = model.SubtitleStatusPending
			updates["stage"] = model.SubtitleStageQueued
			updates["progress"] = 0
			updates["audio_worker_id"] = ""
			updates["subtitle_worker_id"] = ""
			updates["audio_artifact_path"] = ""
			updates["audio_artifact_size"] = 0
			updates["audio_artifact_sha256"] = ""
			updates["audio_artifact_format"] = ""
			updates["audio_artifact_duration_ms"] = 0
			updates["audio_uploaded_at"] = nil
			rollbackKind = "subtitle_to_queued_no_owner"
		}

	default:
		// 未知 stage（兼容旧 worker 上报 extracting / writing 等老语义但中途没经过 audio 拆分）
		// → 直接 FAILED
		updates["status"] = model.SubtitleStatusFailed
		updates["stage"] = job.Stage // 保留失败时的 stage，方便排查
		updates["finished_at"] = &now
		terminal = true
		rollbackKind = "terminal_failed"
	}

	if err := s.db.Model(&model.SubtitleJob{}).Where("id = ?", jobID).Updates(updates).Error; err != nil {
		return fmt.Errorf("worker fail update: %w", err)
	}

	workerUpdates := map[string]any{
		"current_job_id": "",
		"last_seen_at":   now,
	}
	if terminal {
		workerUpdates["failed_jobs"] = gorm.Expr("failed_jobs + 1")
	}
	_ = s.db.Model(&model.SubtitleWorker{}).Where("id = ?", workerID).Updates(workerUpdates).Error

	log.Printf("[subtitle/worker] fail job=%s media=%s worker=%s stage=%s rollback=%s: %s",
		jobID, job.MediaID, workerID, job.Stage, rollbackKind, msg)
	return nil
}

// === Stale 任务回收 ===

// runStaleRecoveryLoop 周期回收僵尸任务。Start() 启动调用。
//
// v3 broker 模式：服务端不再有中转池文件，无需周期 GC。仅做 stale recovery：
//   - 每 60s 跑一次 recoverStaleJobsOnce
//
// 不会丢已完成的工作：worker 即使在写 VTT 前一刻崩溃，也会在重新跑时从头开始
// （单 ASR 任务幂等，多次跑结果相同）。
func (s *SubtitleService) runStaleRecoveryLoop() {
	staleTicker := time.NewTicker(60 * time.Second)
	defer staleTicker.Stop()

	// 启动时立刻跑一次（处理上次崩溃残留）
	s.recoverStaleJobsOnce()

	for {
		select {
		case <-s.stop:
			return
		case <-s.ctx.Done():
			return
		case <-staleTicker.C:
			s.recoverStaleJobsOnce()
		}
	}
}

// recoverStaleJobsOnce 单次扫描 + 按 stage 分组重置。
//
// v3 分布式 worker（broker 模式）下，stale 处理按当前 stage 决定回滚目标：
//  1. audio 阶段（downloading / extracting / encoding_intermediate）超时
//     → 回 PENDING/queued，audio_worker_id 清空，让其它 audio worker 重试
//
//  2. audio_uploaded 阶段长 TTL（24h）
//     → audio worker 长时间没收到 fetch 请求；通过 broker 通知 owner 清理本地 FLAC，
//       任务回 queued 重头来
//
//  3. subtitle 阶段（asr / translate / writing）超时
//     → 任务回 audio_uploaded 让其它 subtitle worker 重试（FLAC 仍在 audio worker 本地）。
//       如果 audio_worker_id 已不存在（注册表丢失）则整条任务回 queued。
//
//  4. 其它（兼容旧值或 queued 误触发） → 走兜底路径：直接回 PENDING/queued
func (s *SubtitleService) recoverStaleJobsOnce() {
	cur := s.snap()
	staleThreshold := cur.WorkerStaleThreshold
	hbCutoff := time.Now().Add(-staleThreshold)
	// audio_uploaded 单独长 TTL：24h 内没 subtitle worker 接 → 重置
	auCutoff := time.Now().Add(-24 * time.Hour)

	totalReset := 0

	// 1. audio 阶段超时 → 回 queued
	audioStages := []string{
		model.SubtitleStageDownloading,
		model.SubtitleStageExtracting,
		model.SubtitleStageEncodingIntermediate,
	}
	res := s.db.Model(&model.SubtitleJob{}).
		Where("status = ? AND stage IN ? AND (last_heartbeat_at IS NULL OR last_heartbeat_at < ?)",
			model.SubtitleStatusRunning, audioStages, hbCutoff).
		Updates(map[string]any{
			"status":            model.SubtitleStatusPending,
			"stage":             model.SubtitleStageQueued,
			"progress":          0,
			"claimed_by":        "",
			"audio_worker_id":   "",
			"claimed_at":        nil,
			"last_heartbeat_at": nil,
			"error_msg":         fmt.Sprintf("stale recovery (audio stage, threshold=%s)", staleThreshold),
		})
	if res.Error != nil {
		log.Printf("[subtitle/worker] stale audio reset failed: %v", res.Error)
	} else if res.RowsAffected > 0 {
		log.Printf("[subtitle/worker] stale recovery: reset %d audio-stage jobs to queued", res.RowsAffected)
		totalReset += int(res.RowsAffected)
	}

	// 2. audio_uploaded 长 TTL：通知 owner 删 FLAC + 任务回 queued
	var auStale []model.SubtitleJob
	if err := s.db.Where("status = ? AND stage = ? AND (audio_uploaded_at IS NULL OR audio_uploaded_at < ?)",
		model.SubtitleStatusRunning, model.SubtitleStageAudioUploaded, auCutoff).Find(&auStale).Error; err != nil {
		log.Printf("[subtitle/worker] stale audio_uploaded scan failed: %v", err)
	} else {
		for _, j := range auStale {
			// v3：通过 broker long-poll 通道通知 owner audio worker 删本地 FLAC
			if j.AudioWorkerID != "" {
				s.audioBroker.EnqueueFetch(j.AudioWorkerID, AudioFetchTask{
					Action: "cleanup",
					JobID:  j.ID,
				})
			}
			if err := s.db.Model(&model.SubtitleJob{}).Where("id = ?", j.ID).Updates(map[string]any{
				"status":                     model.SubtitleStatusPending,
				"stage":                      model.SubtitleStageQueued,
				"progress":                   0,
				"claimed_by":                 "",
				"audio_worker_id":            "",
				"subtitle_worker_id":         "",
				"audio_artifact_path":        "",
				"audio_artifact_size":        0,
				"audio_artifact_sha256":      "",
				"audio_artifact_format":      "",
				"audio_artifact_duration_ms": 0,
				"audio_uploaded_at":          nil,
				"last_heartbeat_at":          nil,
				"error_msg":                  "stale recovery (no subtitle worker for 24h)",
			}).Error; err != nil {
				log.Printf("[subtitle/worker] stale audio_uploaded reset failed job=%s: %v", j.ID, err)
			} else {
				totalReset++
				log.Printf("[subtitle/worker] stale recovery: cleaned audio_uploaded job=%s media=%s owner=%s",
					j.ID, j.MediaID, j.AudioWorkerID)
			}
		}
	}

	// 3. subtitle 阶段超时 → 回 audio_uploaded 让其它 subtitle worker 重试
	//    v3：FLAC 仍在 audio worker 本地，subtitle worker 重新拉即可
	subtitleStages := []string{
		model.SubtitleStageASR,
		model.SubtitleStageTranslate,
		model.SubtitleStageWriting,
	}
	var subStale []model.SubtitleJob
	if err := s.db.Where("status = ? AND stage IN ? AND (last_heartbeat_at IS NULL OR last_heartbeat_at < ?)",
		model.SubtitleStatusRunning, subtitleStages, hbCutoff).Find(&subStale).Error; err != nil {
		log.Printf("[subtitle/worker] stale subtitle scan failed: %v", err)
	} else {
		for _, j := range subStale {
			// audio_worker_id 仍在记录里就回 audio_uploaded；丢失则整条回 queued
			updates := map[string]any{
				"claimed_by":         "",
				"subtitle_worker_id": "",
				"last_heartbeat_at":  nil,
				"error_msg":          fmt.Sprintf("stale recovery (subtitle stage, threshold=%s)", staleThreshold),
			}
			if j.AudioWorkerID != "" {
				updates["stage"] = model.SubtitleStageAudioUploaded
				updates["progress"] = 35
			} else {
				updates["status"] = model.SubtitleStatusPending
				updates["stage"] = model.SubtitleStageQueued
				updates["progress"] = 0
				updates["audio_worker_id"] = ""
				updates["audio_artifact_path"] = ""
				updates["audio_artifact_size"] = 0
				updates["audio_artifact_sha256"] = ""
				updates["audio_artifact_format"] = ""
				updates["audio_artifact_duration_ms"] = 0
				updates["audio_uploaded_at"] = nil
			}
			if err := s.db.Model(&model.SubtitleJob{}).Where("id = ?", j.ID).Updates(updates).Error; err != nil {
				log.Printf("[subtitle/worker] stale subtitle reset failed job=%s: %v", j.ID, err)
			} else {
				totalReset++
				log.Printf("[subtitle/worker] stale recovery: subtitle job=%s ownerStillKnown=%v → stage=%v",
					j.ID, j.AudioWorkerID != "", updates["stage"])
			}
		}
	}

	// 4. 兜底：其它 stage（兼容旧 worker 上报的 stage 或 queued 异常停留）
	res2 := s.db.Model(&model.SubtitleJob{}).
		Where("status = ? AND stage NOT IN ? AND last_heartbeat_at IS NOT NULL AND last_heartbeat_at < ?",
			model.SubtitleStatusRunning,
			append(append([]string{}, audioStages...), append(subtitleStages, model.SubtitleStageAudioUploaded)...),
			hbCutoff).
		Updates(map[string]any{
			"status":             model.SubtitleStatusPending,
			"stage":              model.SubtitleStageQueued,
			"progress":           0,
			"claimed_by":         "",
			"audio_worker_id":    "",
			"subtitle_worker_id": "",
			"claimed_at":         nil,
			"last_heartbeat_at":  nil,
			"error_msg":          fmt.Sprintf("stale recovery (legacy stage, threshold=%s)", staleThreshold),
		})
	if res2.Error != nil {
		log.Printf("[subtitle/worker] stale legacy reset failed: %v", res2.Error)
	} else if res2.RowsAffected > 0 {
		log.Printf("[subtitle/worker] stale recovery: reset %d legacy-stage jobs to queued", res2.RowsAffected)
		totalReset += int(res2.RowsAffected)
	}

	if totalReset > 0 {
		log.Printf("[subtitle/worker] stale recovery total reset=%d (threshold=%s)", totalReset, staleThreshold)
	}
}

// RecoverStaleJobs 是 recoverStaleJobsOnce 的导出版本，便于 admin / 测试触发。
// 返回本次回收的任务总数。
func (s *SubtitleService) RecoverStaleJobs() int {
	before := time.Now()
	s.recoverStaleJobsOnce()
	log.Printf("[subtitle/worker] manual stale recovery done in %s", time.Since(before))
	return 0 // 计数信息已在 recoverStaleJobsOnce 内日志输出；这里不重复扫
}

// === v3 broker：admin 监控 ===

// IntermediateAudioStats 给 admin 监控页用：统计当前 audio_uploaded 状态的任务数 + 估算总字节。
//
// v3 broker 模式下，文件留在 audio worker 本地，服务端只持有元数据：
//   - FileCount：DB 中 stage=audio_uploaded 的任务数（即"在等被拉走"的 FLAC 个数）
//   - TotalBytes：这些任务的 audio_artifact_size 之和
//   - OldestUploadedAt：最早 audio_uploaded_at（最久没人来拉的那条）
//   - QuotaBytes：仍保留为 0 表示"无配额限制"——broker 模式服务端不再占盘
func (s *SubtitleService) IntermediateAudioStats() (*dto.IntermediateAudioStats, error) {
	out := &dto.IntermediateAudioStats{
		QuotaBytes: 0, // v3：服务端不存文件，无配额概念
	}

	type aggRow struct {
		Cnt   int64 `gorm:"column:cnt"`
		Total int64 `gorm:"column:total"`
	}
	var agg aggRow
	if err := s.db.Table("subtitle_jobs").
		Select("COUNT(*) AS cnt, COALESCE(SUM(audio_artifact_size), 0) AS total").
		Where("status = ? AND stage = ?", model.SubtitleStatusRunning, model.SubtitleStageAudioUploaded).
		Scan(&agg).Error; err != nil {
		return nil, fmt.Errorf("aggregate audio_uploaded: %w", err)
	}
	out.FileCount = int(agg.Cnt)
	out.TotalBytes = agg.Total

	// oldestUploadedAt
	var earliestJob model.SubtitleJob
	if err := s.db.
		Where("status = ? AND stage = ? AND audio_uploaded_at IS NOT NULL",
			model.SubtitleStatusRunning, model.SubtitleStageAudioUploaded).
		Order("audio_uploaded_at ASC").
		Limit(1).
		Take(&earliestJob).Error; err == nil && earliestJob.AudioUploadedAt != nil {
		t := *earliestJob.AudioUploadedAt
		out.OldestUploadedAt = &t
	}
	return out, nil
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

	onlineCutoff := time.Now().Add(-s.snap().WorkerStaleThreshold)
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
			Capabilities:  parseCapabilities(w.Capabilities),
		})
	}
	return out, nil
}

// ListWorkerTokens 列出未吊销的 token（不含明文）。带每个 token 的 currentRunning 实时统计。
//
// 已吊销（revoked_at != NULL）的 token 不再返回——admin 面板需要的是当前可用凭证视图。
// 审计 / 历史诉求由数据库行本体保留，吊销动作走 RevokeWorkerToken 留痕。
//
// v2 新增：分维度统计 currentAudioRunning / currentSubtitleRunning，便于 admin 看清楚
// 限流来自哪一侧（audio_extract 还是 asr_subtitle）。
func (s *SubtitleService) ListWorkerTokens() ([]dto.SubtitleWorkerTokenItem, error) {
	var tokens []model.SubtitleWorkerToken
	if err := s.db.
		Where("revoked_at IS NULL").
		Order("created_at DESC").
		Find(&tokens).Error; err != nil {
		return nil, err
	}
	if len(tokens) == 0 {
		return []dto.SubtitleWorkerTokenItem{}, nil
	}

	// 一次性查所有 token 的 RUNNING 数（避免 N+1）
	type runRow struct {
		TokenID string `gorm:"column:token_id"`
		C       int64  `gorm:"column:c"`
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

	// audio 维度：通过 audio_worker_id 关联，且 stage 在 audio 集合内
	audioStages := []string{
		model.SubtitleStageDownloading,
		model.SubtitleStageExtracting,
		model.SubtitleStageEncodingIntermediate,
	}
	var audioRuns []runRow
	if err := s.db.Table("subtitle_jobs AS sj").
		Select("sw.token_id AS token_id, COUNT(*) AS c").
		Joins("JOIN subtitle_workers AS sw ON sw.id = sj.audio_worker_id").
		Where("sj.status = ? AND sj.stage IN ?", model.SubtitleStatusRunning, audioStages).
		Group("sw.token_id").
		Scan(&audioRuns).Error; err != nil {
		log.Printf("[subtitle/worker] count audio running by token failed: %v", err)
	}
	audioMap := make(map[string]int, len(audioRuns))
	for _, r := range audioRuns {
		audioMap[r.TokenID] = int(r.C)
	}

	// subtitle 维度：通过 subtitle_worker_id 关联，且 stage 在 subtitle 集合内
	subStages := []string{
		model.SubtitleStageASR,
		model.SubtitleStageTranslate,
		model.SubtitleStageWriting,
	}
	var subRuns []runRow
	if err := s.db.Table("subtitle_jobs AS sj").
		Select("sw.token_id AS token_id, COUNT(*) AS c").
		Joins("JOIN subtitle_workers AS sw ON sw.id = sj.subtitle_worker_id").
		Where("sj.status = ? AND sj.stage IN ?", model.SubtitleStatusRunning, subStages).
		Group("sw.token_id").
		Scan(&subRuns).Error; err != nil {
		log.Printf("[subtitle/worker] count subtitle running by token failed: %v", err)
	}
	subMap := make(map[string]int, len(subRuns))
	for _, r := range subRuns {
		subMap[r.TokenID] = int(r.C)
	}

	out := make([]dto.SubtitleWorkerTokenItem, 0, len(tokens))
	for _, t := range tokens {
		out = append(out, dto.SubtitleWorkerTokenItem{
			ID:                     t.ID,
			Name:                   t.Name,
			TokenPrefix:            t.TokenPrefix,
			MaxConcurrency:         t.MaxConcurrency,
			MaxAudioConcurrency:    t.MaxAudioConcurrency,
			MaxSubtitleConcurrency: t.MaxSubtitleConcurrency,
			CurrentRunning:         runMap[t.ID],
			CurrentAudioRunning:    audioMap[t.ID],
			CurrentSubtitleRunning: subMap[t.ID],
			CreatedAt:              t.CreatedAt,
			LastUsedAt:             t.LastUsedAt,
			RevokedAt:              t.RevokedAt,
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
// CreateWorkerToken 生成一条新 token。明文仅本次返回。
//
// 规则：
//   - 生成 32 字符 base32 随机串（[a-z2-7]，160 bit 熵）
//   - 拼成 "mwt_<32 chars>"，明文长度 36
//   - bcrypt cost=12 存 hash
//   - TokenPrefix 取明文前 12 位，方便面板识别 + 中间件按前缀检索
//   - maxConcurrency 控制该 token 名下 worker 集合并发上限；0 / 负数会被强制为 1
//   - maxAudioConcurrency / maxSubtitleConcurrency 是 v2 分能力维度上限；
//     0 = 走默认（audio=2 / subtitle=1），其它正值原样使用，超过 64 截断
func (s *SubtitleService) CreateWorkerToken(name string, maxConcurrency, maxAudioConcurrency, maxSubtitleConcurrency int) (string, *dto.SubtitleWorkerTokenItem, error) {
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
	if maxAudioConcurrency <= 0 {
		maxAudioConcurrency = 2
	}
	if maxAudioConcurrency > 64 {
		maxAudioConcurrency = 64
	}
	if maxSubtitleConcurrency <= 0 {
		maxSubtitleConcurrency = 1
	}
	if maxSubtitleConcurrency > 64 {
		maxSubtitleConcurrency = 64
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
		Name:                   name,
		TokenHash:              string(hash),
		TokenPrefix:            plaintext[:middleware.WorkerTokenPrefix],
		MaxConcurrency:         maxConcurrency,
		MaxAudioConcurrency:    maxAudioConcurrency,
		MaxSubtitleConcurrency: maxSubtitleConcurrency,
	}
	if err := s.db.Create(&rec).Error; err != nil {
		return "", nil, fmt.Errorf("create token: %w", err)
	}
	log.Printf("[subtitle/worker] admin created token id=%s name=%q prefix=%s max=%d audio=%d subtitle=%d",
		rec.ID, rec.Name, rec.TokenPrefix, rec.MaxConcurrency, rec.MaxAudioConcurrency, rec.MaxSubtitleConcurrency)
	return plaintext, &dto.SubtitleWorkerTokenItem{
		ID:                     rec.ID,
		Name:                   rec.Name,
		TokenPrefix:            rec.TokenPrefix,
		MaxConcurrency:         rec.MaxConcurrency,
		MaxAudioConcurrency:    rec.MaxAudioConcurrency,
		MaxSubtitleConcurrency: rec.MaxSubtitleConcurrency,
		CreatedAt:              rec.CreatedAt,
	}, nil
}

// UpdateWorkerToken 修改 token 字段（maxConcurrency / maxAudioConcurrency / maxSubtitleConcurrency）。
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
		v := clampIntInRange(*req.MaxConcurrency, 0, 64)
		updates["max_concurrency"] = v
		rec.MaxConcurrency = v
	}
	if req.MaxAudioConcurrency != nil {
		v := clampIntInRange(*req.MaxAudioConcurrency, 0, 64)
		updates["max_audio_concurrency"] = v
		rec.MaxAudioConcurrency = v
	}
	if req.MaxSubtitleConcurrency != nil {
		v := clampIntInRange(*req.MaxSubtitleConcurrency, 0, 64)
		updates["max_subtitle_concurrency"] = v
		rec.MaxSubtitleConcurrency = v
	}
	item := dto.SubtitleWorkerTokenItem{
		ID:                     rec.ID,
		Name:                   rec.Name,
		TokenPrefix:            rec.TokenPrefix,
		MaxConcurrency:         rec.MaxConcurrency,
		MaxAudioConcurrency:    rec.MaxAudioConcurrency,
		MaxSubtitleConcurrency: rec.MaxSubtitleConcurrency,
		CreatedAt:              rec.CreatedAt,
		LastUsedAt:             rec.LastUsedAt,
		RevokedAt:              rec.RevokedAt,
	}
	if len(updates) == 0 {
		return &item, nil
	}
	if err := s.db.Model(&rec).Updates(updates).Error; err != nil {
		return nil, fmt.Errorf("update token: %w", err)
	}
	log.Printf("[subtitle/worker] admin updated token id=%s max=%d audio=%d subtitle=%d",
		rec.ID, rec.MaxConcurrency, rec.MaxAudioConcurrency, rec.MaxSubtitleConcurrency)
	return &item, nil
}

// clampIntInRange 把 v 限制到 [lo, hi] 范围（lo 在最小，hi 在最大）。
// service 包内 media.go 里有同名 clampInt（仅 ≤ 1 截断），名字撞车故此处用别名。
func clampIntInRange(v, lo, hi int) int {
	if v < lo {
		return lo
	}
	if v > hi {
		return hi
	}
	return v
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

// Alerts 给 admin 顶部告警条用：检测有任务等待但对应 capability 没在线 worker 等异常情况。
//
// 当前实现的告警规则（按严重度由高到低）：
//   - 队列里有 status=PENDING / stage=queued 任务，但 audio_extract 在线 worker = 0
//     → "无 audio_extract worker 在线，N 条任务等待中"
//   - 队列里有 status=RUNNING / stage=audio_uploaded 任务，但 asr_subtitle 在线 worker = 0
//     → "无 asr_subtitle worker 在线，N 条 FLAC 等待 ASR"
//   - audio_uploaded 任务的 owner（audio worker）当前不在 broker long-poll 队列内
//     → "N 条任务的 audio worker 不在线，subtitle worker 拉取会失败"
//
// v3 broker 模式不再有"中转池满"告警（服务端不存文件）。
//
// 在线判定：last_seen_at 在 cfg.WorkerStaleThreshold 内（默认 10min）。
// 没有任何告警时返回空切片。
func (s *SubtitleService) Alerts() []dto.AdminAlert {
	out := []dto.AdminAlert{}
	cur := s.snap()
	onlineCutoff := time.Now().Add(-cur.WorkerStaleThreshold)

	// 1) audio 等待但无 audio_extract worker
	var pendingAudio int64
	if err := s.db.Model(&model.SubtitleJob{}).
		Where("status = ? AND stage = ?", model.SubtitleStatusPending, model.SubtitleStageQueued).
		Count(&pendingAudio).Error; err != nil {
		log.Printf("[subtitle/alert] count pending audio failed: %v", err)
	}
	if pendingAudio > 0 {
		online, err := s.countOnlineWorkersByCapability(model.WorkerCapAudioExtract, onlineCutoff)
		if err != nil {
			log.Printf("[subtitle/alert] count audio workers failed: %v", err)
		} else if online == 0 {
			out = append(out, dto.AdminAlert{
				Level:   "warn",
				Message: fmt.Sprintf("无 audio_extract worker 在线，%d 条任务等待中", pendingAudio),
			})
		}
	}

	// 2) audio_uploaded 等待但无 asr_subtitle worker
	var pendingSubtitle int64
	if err := s.db.Model(&model.SubtitleJob{}).
		Where("status = ? AND stage = ?", model.SubtitleStatusRunning, model.SubtitleStageAudioUploaded).
		Count(&pendingSubtitle).Error; err != nil {
		log.Printf("[subtitle/alert] count pending subtitle failed: %v", err)
	}
	if pendingSubtitle > 0 {
		online, err := s.countOnlineWorkersByCapability(model.WorkerCapASRSubtitle, onlineCutoff)
		if err != nil {
			log.Printf("[subtitle/alert] count subtitle workers failed: %v", err)
		} else if online == 0 {
			out = append(out, dto.AdminAlert{
				Level:   "warn",
				Message: fmt.Sprintf("无 asr_subtitle worker 在线，%d 条 FLAC 等待 ASR", pendingSubtitle),
			})
		}
	}

	// 3) audio_uploaded 任务的 owner audio worker 是否在 broker long-poll 队列内
	//    如果 owner 离线，subtitle worker 即使 claim 到任务也拉不到 FLAC（broker 30s 超时）。
	if pendingSubtitle > 0 && s.audioBroker != nil {
		var ownerIDs []string
		if err := s.db.Model(&model.SubtitleJob{}).
			Where("status = ? AND stage = ? AND audio_worker_id <> ''",
				model.SubtitleStatusRunning, model.SubtitleStageAudioUploaded).
			Distinct().
			Pluck("audio_worker_id", &ownerIDs).Error; err == nil {
			missingOwners := 0
			for _, id := range ownerIDs {
				if !s.audioBroker.IsWorkerPolling(id) {
					missingOwners++
				}
			}
			if missingOwners > 0 {
				out = append(out, dto.AdminAlert{
					Level: "warn",
					Message: fmt.Sprintf(
						"%d 个 audio worker 不在线（其持有的 FLAC 暂时拉不到，subtitle worker 会等待最多 30 秒）",
						missingOwners,
					),
				})
			}
		}
	}
	return out
}

// countOnlineWorkersByCapability 统计某 capability 在 last_seen_at >= cutoff 范围内的在线 worker 数。
//
// SQLite 不支持 JSON 函数，capabilities 列里就是普通字符串（如 `["audio_extract","asr_subtitle"]`），
// 用 LIKE 匹配 `"<cap>"` 子串足够稳：JSON 数组元素会被引号包裹，不会有歧义匹配。
func (s *SubtitleService) countOnlineWorkersByCapability(cap string, cutoff time.Time) (int64, error) {
	var n int64
	pattern := fmt.Sprintf(`%%"%s"%%`, cap)
	if err := s.db.Model(&model.SubtitleWorker{}).
		Where("capabilities LIKE ? AND last_seen_at >= ?", pattern, cutoff).
		Count(&n).Error; err != nil {
		return 0, err
	}
	return n, nil
}
