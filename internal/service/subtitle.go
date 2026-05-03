// Package service
// subtitle.go 实现字幕生成的核心编排：
//
//   - 单 worker 串行消费（CPU 服务器上避免 whisper.cpp 多实例竞争 CPU）
//   - 幂等入队：同 media 重复入队不会重复跑
//   - 入队仅由 admin 在字幕管理页手动触发（"重新生成所选" / 批量按钮等）；
//     新建媒体不会自动入队，启动时也不再扫描存量
//   - 流水线：ffmpeg 抽音频 → whisper.cpp ASR → LLM 翻译 → 写 WebVTT
//   - 失败有 error_msg；admin 可手动重试 / 批量重新生成 / 删除 / 禁用 / 取消
package service

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"gorm.io/gorm"

	"github.com/hor1zon777/m3u8-preview-go/internal/config"
	"github.com/hor1zon777/m3u8-preview-go/internal/dto"
	"github.com/hor1zon777/m3u8-preview-go/internal/model"
	"github.com/hor1zon777/m3u8-preview-go/internal/util"
)

// SubtitleService 编排字幕生成。
type SubtitleService struct {
	db *gorm.DB
	// cfgMu 保护 *cfg 的并发读写。所有读取请走 s.snap()；admin 写入必须持锁。
	cfgMu sync.RWMutex
	cfg   *config.SubtitleConfig
	// asrFactory / translatorFactory 接受当前 cfg 快照构造客户端。
	// 持久化配置改了后无需重启服务，processOne 每次跑都会从最新 cfg 实例化客户端。
	asrFactory        func(cfg config.SubtitleConfig) ASRClient
	translatorFactory func(cfg config.SubtitleConfig) Translator
	signer            *util.ProxySigner

	jobs        chan string // mediaID
	stop        chan struct{}
	once        sync.Once
	wg          sync.WaitGroup
	stopped     atomic.Bool
	enqueuedIDs sync.Map // mediaID -> struct{} 防重复入队
	ctx         context.Context
	cancel      context.CancelFunc
}

// NewSubtitleService 构造。
// 当 cfg.Enabled=false 时仍可构造但 worker 不启动，调用方法返回 ErrSubtitleDisabled。
func NewSubtitleService(
	db *gorm.DB,
	cfg *config.SubtitleConfig,
	asrFactory func(cfg config.SubtitleConfig) ASRClient,
	translatorFactory func(cfg config.SubtitleConfig) Translator,
	signer *util.ProxySigner,
) *SubtitleService {
	ctx, cancel := context.WithCancel(context.Background())
	return &SubtitleService{
		db:                db,
		cfg:               cfg,
		asrFactory:        asrFactory,
		translatorFactory: translatorFactory,
		signer:            signer,
		jobs:              make(chan string, 4096),
		stop:              make(chan struct{}),
		ctx:               ctx,
		cancel:            cancel,
	}
}

// snap 返回 cfg 的拷贝，调用方对返回值的修改不会影响内部状态。
// 所有读取 cfg 字段的代码都应走这里以避免与 UpdateSettings 的写竞争。
func (s *SubtitleService) snap() config.SubtitleConfig {
	s.cfgMu.RLock()
	defer s.cfgMu.RUnlock()
	if s.cfg == nil {
		return config.SubtitleConfig{}
	}
	return *s.cfg
}

// ErrSubtitleDisabled 字幕功能未启用。
var ErrSubtitleDisabled = errors.New("subtitle feature disabled")

// Start 启动 worker（单线程串行）+ 自动扫描存量 media。
// 返回 error 仅在 preflight 校验失败时。
// Start 启动字幕模块。
//
// 启动行为按 LocalWorkerEnabled 分两套：
//
//   - LocalWorkerEnabled=true（兼容旧部署）：
//     PreflightCheck → 启动 in-process whisper.cpp worker goroutine →
//     扫描存量 ACTIVE media → 启动 stale 任务回收
//
//   - LocalWorkerEnabled=false（默认，远程 GPU worker 模式）：
//     不做 PreflightCheck（whisper bin 可能根本没装），不启 worker goroutine，
//     仅做：扫描存量 ACTIVE media（让 PENDING 任务等远程 worker 认领）
//     + 启动 stale 任务回收（清理崩溃的远程 worker）
//
// 不论哪种模式，cfg.Enabled=false 都直接 return（端点回 disabled）。
func (s *SubtitleService) Start() error {
	cur := s.snap()
	if !cur.Enabled {
		log.Printf("[subtitle] feature disabled, worker not started")
		return nil
	}

	if cur.LocalWorkerEnabled {
		// preflight：基于当前 cfg 临时构造一份客户端做校验，processOne 会按需重建
		asr := s.buildASR(cur)
		tr := s.buildTranslator(cur)
		if pa, ok := asr.(interface{ PreflightCheck() error }); ok {
			if err := pa.PreflightCheck(); err != nil {
				return fmt.Errorf("asr preflight: %w", err)
			}
		}
		if pt, ok := tr.(interface{ PreflightCheck() error }); ok {
			if err := pt.PreflightCheck(); err != nil {
				return fmt.Errorf("translator preflight: %w", err)
			}
		}
	}

	if err := os.MkdirAll(cur.SubtitlesDir, 0o755); err != nil {
		return fmt.Errorf("mkdir subtitles dir: %w", err)
	}

	if cur.LocalWorkerEnabled {
		// 本地 worker：单 worker，纯 CPU whisper 不能多开
		s.wg.Add(1)
		go s.worker()
		asrName, mtName := "(unset)", "(unset)"
		if asr := s.buildASR(cur); asr != nil {
			asrName = asr.ModelName()
		}
		if tr := s.buildTranslator(cur); tr != nil {
			mtName = tr.ModelName()
		}
		log.Printf("[subtitle] local in-process worker started (asr=%s, mt=%s)", asrName, mtName)
	} else {
		log.Printf("[subtitle] remote worker mode (LocalWorkerEnabled=false), waiting for /api/v1/worker/* clients")
	}

	// 远程 worker stale 回收：无论本地 worker 是否启用都跑（清理上游崩溃的远程 worker 留下的僵尸 RUNNING）
	s.wg.Add(1)
	go func() {
		defer s.wg.Done()
		s.runStaleRecoveryLoop()
	}()

	// 启动时不再扫描存量 media 自动入队；改为在 admin 字幕管理页手动选择后再批量入队，
	// 避免大库一次性把上千条 media 推进 whisper 队列堵死。

	log.Printf("[subtitle] started (lang=%s→%s, localWorker=%v, manual-enqueue-only)",
		cur.WhisperLanguage, cur.TargetLang, cur.LocalWorkerEnabled)
	return nil
}

// buildASR 用当前 cfg 快照构造 ASR 客户端；factory 未设置时返回 nil。
func (s *SubtitleService) buildASR(cur config.SubtitleConfig) ASRClient {
	if s.asrFactory == nil {
		return nil
	}
	return s.asrFactory(cur)
}

// buildTranslator 用当前 cfg 快照构造翻译客户端；factory 未设置时返回 nil。
func (s *SubtitleService) buildTranslator(cur config.SubtitleConfig) Translator {
	if s.translatorFactory == nil {
		return nil
	}
	return s.translatorFactory(cur)
}

// Stop 优雅关停（取消运行中的 ffmpeg/whisper，等待 worker 退出）。
func (s *SubtitleService) Stop() {
	s.once.Do(func() {
		s.stopped.Store(true)
		close(s.stop)
		s.cancel()
	})
	s.wg.Wait()
}

// Enabled 字幕功能是否启用。
func (s *SubtitleService) Enabled() bool { return s.snap().Enabled }

// EnsureJob 幂等入队：
//   - 已存在 DONE：不动
//   - 已存在 RUNNING/PENDING：不动
//   - 已存在 FAILED：重置为 PENDING 重试
//   - 不存在：创建 PENDING 行并投递到 worker
func (s *SubtitleService) EnsureJob(mediaID string) error {
	cur := s.snap()
	if !cur.Enabled {
		return ErrSubtitleDisabled
	}
	if mediaID == "" {
		return fmt.Errorf("mediaId empty")
	}

	var existing model.SubtitleJob
	err := s.db.Where("media_id = ?", mediaID).Take(&existing).Error
	if err != nil && !errors.Is(err, gorm.ErrRecordNotFound) {
		return fmt.Errorf("query subtitle job: %w", err)
	}

	if err == nil {
		// 已有任务
		switch existing.Status {
		case model.SubtitleStatusDone, model.SubtitleStatusRunning, model.SubtitleStatusPending, model.SubtitleStatusDisabled:
			return nil // 幂等：不重新入队
		case model.SubtitleStatusFailed:
			// 重置 + 入队
			if err := s.db.Model(&existing).Updates(map[string]any{
				"status":    model.SubtitleStatusPending,
				"stage":     model.SubtitleStageQueued,
				"progress":  0,
				"error_msg": "",
			}).Error; err != nil {
				return fmt.Errorf("reset failed job: %w", err)
			}
			s.enqueue(mediaID)
			return nil
		}
	}

	// 创建新任务
	job := model.SubtitleJob{
		MediaID:    mediaID,
		Status:     model.SubtitleStatusPending,
		Stage:      model.SubtitleStageQueued,
		SourceLang: cur.WhisperLanguage,
		TargetLang: cur.TargetLang,
	}
	if err := s.db.Create(&job).Error; err != nil {
		// 唯一索引冲突视为竞态，已有其它请求建好了
		if strings.Contains(err.Error(), "UNIQUE") || strings.Contains(err.Error(), "uniqueIndex") {
			return nil
		}
		return fmt.Errorf("create subtitle job: %w", err)
	}
	s.enqueue(mediaID)
	return nil
}

// HookOnMediaCreated 给 MediaService 注册的钩子。
//
// 历史版本会按 cfg.AutoGenerate 自动入队字幕；当前版本一律手动入队，
// 因此这里保持空实现以避免 MediaService 接口断裂；不再依赖此 hook 的代码路径
// 也可以在重构时彻底移除。
func (s *SubtitleService) HookOnMediaCreated(mediaID string) {
	_ = mediaID
}

// HookOnMediaDeleted 给 MediaService 注册的钩子，删除字幕文件。
func (s *SubtitleService) HookOnMediaDeleted(mediaID string) {
	var job model.SubtitleJob
	if err := s.db.Where("media_id = ?", mediaID).Take(&job).Error; err != nil {
		return
	}
	s.deleteVTTFile(&job)
	_ = s.db.Where("media_id = ?", mediaID).Delete(&model.SubtitleJob{}).Error
}

// enqueue 投递到 channel；满了则丢弃（worker 重启后扫描会捡回来）。
func (s *SubtitleService) enqueue(mediaID string) {
	if s.stopped.Load() {
		return
	}
	if _, loaded := s.enqueuedIDs.LoadOrStore(mediaID, struct{}{}); loaded {
		return
	}
	select {
	case s.jobs <- mediaID:
	default:
		s.enqueuedIDs.Delete(mediaID)
		log.Printf("[subtitle] queue full, drop media=%s (will be picked by next scan)", mediaID)
	}
}

// scanExisting 历史版本用于启动期扫描存量 ACTIVE media 自动入队字幕。
// 当前版本字幕仅手动入队，已不再调用此函数；保留实现作为快速回滚 / 未来接入
// "管理员一键扫描"按钮的可选骨架，无副作用。
func (s *SubtitleService) scanExisting() {
	// 给 GORM 一秒预热避免和迁移竞争
	time.Sleep(time.Second)

	// 分页扫描，避免一次加载几千行
	const pageSize = 500
	var lastID string
	for {
		if s.stopped.Load() {
			return
		}
		var medias []model.Media
		q := s.db.Select("id").Where("status = ?", model.MediaStatusActive)
		if lastID != "" {
			q = q.Where("id > ?", lastID)
		}
		if err := q.Order("id ASC").Limit(pageSize).Find(&medias).Error; err != nil {
			log.Printf("[subtitle] scan existing failed: %v", err)
			return
		}
		if len(medias) == 0 {
			return
		}
		for _, m := range medias {
			lastID = m.ID
			if err := s.EnsureJob(m.ID); err != nil {
				log.Printf("[subtitle] scan ensure media=%s failed: %v", m.ID, err)
			}
		}
		if len(medias) < pageSize {
			return
		}
	}
}

// worker 单线程消费。
func (s *SubtitleService) worker() {
	defer s.wg.Done()
	for {
		select {
		case <-s.stop:
			return
		case mediaID, ok := <-s.jobs:
			if !ok {
				return
			}
			s.processOne(mediaID)
			s.enqueuedIDs.Delete(mediaID)
		}
	}
}

// processOne 跑一条任务的全流程。失败时把 error 写入 job。
func (s *SubtitleService) processOne(mediaID string) {
	// 取消/禁用守护：worker 从 channel 拿到 mediaID 后，若用户在排队期间通过
	// "批量取消 / 批量禁用 / 删除"把任务标为 DISABLED 或直接删除，此处提前返回，
	// 避免下方无条件 UPDATE 把 DISABLED 状态覆盖回 RUNNING。
	{
		var job model.SubtitleJob
		if err := s.db.Where("media_id = ?", mediaID).Take(&job).Error; err != nil {
			// 已被批量删除：放弃即可
			return
		}
		if job.Status == model.SubtitleStatusDisabled {
			return
		}
	}

	var media model.Media
	if err := s.db.Take(&media, "id = ?", mediaID).Error; err != nil {
		s.markFailed(mediaID, fmt.Errorf("media not found: %w", err))
		return
	}

	// 进入 pipeline 时取一次 cfg 快照，整个 job 期间使用同一份配置避免中途切换造成文件名/语言串台
	cur := s.snap()
	asr := s.buildASR(cur)
	tr := s.buildTranslator(cur)
	if asr == nil || tr == nil {
		s.markFailed(mediaID, fmt.Errorf("asr/translator factory not configured"))
		return
	}

	now := time.Now()
	if err := s.db.Model(&model.SubtitleJob{}).Where("media_id = ?", mediaID).Updates(map[string]any{
		"status":     model.SubtitleStatusRunning,
		"stage":      model.SubtitleStageExtracting,
		"progress":   5,
		"started_at": &now,
		"error_msg":  "",
		"asr_model":  asr.ModelName(),
		"mt_model":   tr.ModelName(),
	}).Error; err != nil {
		log.Printf("[subtitle] mark running media=%s: %v", mediaID, err)
		return
	}

	// 1) 抽音频
	tmpDir, err := os.MkdirTemp("", "subtitle-*")
	if err != nil {
		s.markFailed(mediaID, fmt.Errorf("mkdir tmp: %w", err))
		return
	}
	defer func() { _ = os.RemoveAll(tmpDir) }()

	audioPath := filepath.Join(tmpDir, "audio.wav")
	if err := util.ExtractAudioForASR(s.ctx, media.M3u8URL, audioPath); err != nil {
		s.markFailed(mediaID, fmt.Errorf("extract audio: %w", err))
		return
	}
	s.updateProgress(mediaID, model.SubtitleStageASR, 25)

	// 2) ASR
	asrResult, err := asr.Transcribe(s.ctx, audioPath)
	if err != nil {
		s.markFailed(mediaID, fmt.Errorf("asr: %w", err))
		return
	}
	if len(asrResult.Segments) == 0 {
		s.markFailed(mediaID, fmt.Errorf("asr produced 0 segments (audio may be silent)"))
		return
	}
	s.updateProgress(mediaID, model.SubtitleStageTranslate, 50)

	// 3) 翻译（按 BatchSize 分批）
	translated, err := s.translateAll(cur, tr, asrResult.Segments)
	if err != nil {
		s.markFailed(mediaID, fmt.Errorf("translate: %w", err))
		return
	}
	s.updateProgress(mediaID, model.SubtitleStageWriting, 90)

	// 4) 写 VTT
	relPath := mediaID + ".vtt"
	absPath := filepath.Join(cur.SubtitlesDir, relPath)
	if err := writeVTT(absPath, asrResult.Segments, translated); err != nil {
		s.markFailed(mediaID, fmt.Errorf("write vtt: %w", err))
		return
	}

	// 5) 写 DONE
	finished := time.Now()
	if err := s.db.Model(&model.SubtitleJob{}).Where("media_id = ?", mediaID).Updates(map[string]any{
		"status":        model.SubtitleStatusDone,
		"stage":         model.SubtitleStageDone,
		"progress":      100,
		"vtt_path":      relPath,
		"segment_count": len(asrResult.Segments),
		"finished_at":   &finished,
	}).Error; err != nil {
		log.Printf("[subtitle] mark done media=%s: %v", mediaID, err)
		return
	}
	log.Printf("[subtitle] done media=%s segments=%d duration=%s", mediaID, len(asrResult.Segments), time.Since(now))
}

// translateAll 按 BatchSize 切片翻译；任何子批失败回退到原文（保证字幕完整性）。
// cur 提供本次 job 期间稳定的 BatchSize / 源/目标语言，避免运行中切配置导致行为不一致。
func (s *SubtitleService) translateAll(cur config.SubtitleConfig, tr Translator, segs []ASRSegment) ([]string, error) {
	out := make([]string, len(segs))
	batch := cur.BatchSize
	if batch <= 0 {
		batch = 8
	}
	for i := 0; i < len(segs); i += batch {
		j := min(i+batch, len(segs))
		texts := make([]string, 0, j-i)
		for _, seg := range segs[i:j] {
			texts = append(texts, seg.Text)
		}
		zh, err := tr.Translate(s.ctx, texts, cur.WhisperLanguage, cur.TargetLang)
		if err != nil {
			log.Printf("[subtitle] translate batch fallback to source: %v", err)
			// 回退原文
			copy(out[i:j], texts)
			continue
		}
		copy(out[i:j], zh)
	}
	return out, nil
}

// updateProgress 更新阶段 + 进度字段。
func (s *SubtitleService) updateProgress(mediaID, stage string, progress int) {
	_ = s.db.Model(&model.SubtitleJob{}).Where("media_id = ?", mediaID).Updates(map[string]any{
		"stage":    stage,
		"progress": progress,
	}).Error
}

// markFailed 把任务置为 FAILED 并写错误信息。
func (s *SubtitleService) markFailed(mediaID string, cause error) {
	msg := truncateString(cause.Error(), 1000)
	log.Printf("[subtitle] FAILED media=%s: %s", mediaID, msg)
	finished := time.Now()
	_ = s.db.Model(&model.SubtitleJob{}).Where("media_id = ?", mediaID).Updates(map[string]any{
		"status":      model.SubtitleStatusFailed,
		"stage":       model.SubtitleStageQueued,
		"error_msg":   msg,
		"finished_at": &finished,
	}).Error
}

// ---- 查询 / Admin 操作 ----

// GetStatus 返回 status payload；当 status=DONE 时附带签名 VTT URL。
func (s *SubtitleService) GetStatus(mediaID, userID string) (*dto.SubtitleStatusResponse, error) {
	cur := s.snap()
	var job model.SubtitleJob
	if err := s.db.Where("media_id = ?", mediaID).Take(&job).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			// 没有任务，返回 disabled-ish 占位
			return &dto.SubtitleStatusResponse{
				MediaID:    mediaID,
				Status:     "MISSING",
				Stage:      "",
				Progress:   0,
				SourceLang: cur.WhisperLanguage,
				TargetLang: cur.TargetLang,
			}, nil
		}
		return nil, err
	}
	resp := &dto.SubtitleStatusResponse{
		MediaID:    job.MediaID,
		Status:     job.Status,
		Stage:      job.Stage,
		Progress:   job.Progress,
		SourceLang: job.SourceLang,
		TargetLang: job.TargetLang,
		ErrorMsg:   job.ErrorMsg,
	}
	if job.Status == model.SubtitleStatusDone && job.VttPath != "" {
		resp.VttURL = s.buildSignedVttURL(mediaID, userID)
	}
	return resp, nil
}

// buildSignedVttURL 构造受 HMAC 签名保护的 VTT URL。
// 复用 ProxySigner 的算法（HMAC-SHA256(PROXY_SECRET, url\nexpires\nuserId)），与代理签名风格一致。
// 签名输入的 URL 用 "subtitle:<mediaId>"，避免和 m3u8 代理签名冲突。
//
// 端点路径与 handler.RegisterPublic 对齐：/api/v1/subtitle/vtt/<mediaId>
func (s *SubtitleService) buildSignedVttURL(mediaID, userID string) string {
	subject := "subtitle:" + mediaID
	signed := s.signer.Sign(subject, userID)
	return "/api/v1/subtitle/vtt/" + url.PathEscape(mediaID) + "?u=" + url.QueryEscape(userID) + signed
}

// ServeVTT 验签后输出 VTT 内容到 w；没找到返回 404。
func (s *SubtitleService) ServeVTT(mediaID, userID, expires, sig string, w io.Writer) (int, error) {
	subject := "subtitle:" + mediaID
	if !s.signer.Verify(subject, expires, sig, userID) {
		return 403, fmt.Errorf("invalid signature")
	}

	var job model.SubtitleJob
	if err := s.db.Where("media_id = ?", mediaID).Take(&job).Error; err != nil {
		return 404, fmt.Errorf("job not found")
	}
	if job.Status != model.SubtitleStatusDone || job.VttPath == "" {
		return 404, fmt.Errorf("vtt not ready")
	}
	abs := filepath.Join(s.snap().SubtitlesDir, job.VttPath)
	f, err := os.Open(abs)
	if err != nil {
		return 404, fmt.Errorf("open vtt: %w", err)
	}
	defer func() { _ = f.Close() }()
	if _, err := io.Copy(w, f); err != nil {
		return 500, err
	}
	return 200, nil
}

// ListJobs 列表查询（admin）。
// categoryId 非空时按 media.category_id 过滤；空字符串特殊语义 "_none" 用于筛"未分类"。
func (s *SubtitleService) ListJobs(page, limit int, statusFilter, search, categoryID string) ([]dto.SubtitleJobItem, int64, error) {
	if page < 1 {
		page = 1
	}
	if limit < 1 || limit > 1000 {
		limit = 20
	}

	q := s.db.Table("subtitle_jobs AS sj").
		Select("sj.*, m.title AS media_title, m.category_id AS media_category_id, c.name AS media_category_name").
		Joins("LEFT JOIN media AS m ON m.id = sj.media_id").
		Joins("LEFT JOIN categories AS c ON c.id = m.category_id")

	if statusFilter != "" {
		q = q.Where("sj.status = ?", statusFilter)
	}
	if search != "" {
		like := "%" + search + "%"
		q = q.Where("m.title LIKE ? OR sj.media_id LIKE ?", like, like)
	}
	switch categoryID {
	case "":
		// 不过滤
	case "_none":
		q = q.Where("m.category_id IS NULL OR m.category_id = ''")
	default:
		q = q.Where("m.category_id = ?", categoryID)
	}

	var total int64
	if err := q.Count(&total).Error; err != nil {
		return nil, 0, err
	}

	type row struct {
		model.SubtitleJob
		MediaTitle        string `gorm:"column:media_title"`
		MediaCategoryID   string `gorm:"column:media_category_id"`
		MediaCategoryName string `gorm:"column:media_category_name"`
	}
	var rows []row
	if err := q.Order("sj.updated_at DESC").
		Limit(limit).Offset((page - 1) * limit).
		Scan(&rows).Error; err != nil {
		return nil, 0, err
	}

	items := make([]dto.SubtitleJobItem, 0, len(rows))
	for _, r := range rows {
		items = append(items, dto.SubtitleJobItem{
			ID:           r.ID,
			MediaID:      r.MediaID,
			MediaTitle:   r.MediaTitle,
			CategoryID:   r.MediaCategoryID,
			CategoryName: r.MediaCategoryName,
			Status:       r.Status,
			Stage:        r.Stage,
			Progress:     r.Progress,
			SourceLang:   r.SourceLang,
			TargetLang:   r.TargetLang,
			ASRModel:     r.ASRModel,
			MTModel:      r.MTModel,
			SegmentCount: r.SegmentCount,
			ErrorMsg:     r.ErrorMsg,
			StartedAt:    r.StartedAt,
			FinishedAt:   r.FinishedAt,
			CreatedAt:    r.CreatedAt,
			UpdatedAt:    r.UpdatedAt,
		})
	}
	return items, total, nil
}

// GetJob 详情。
func (s *SubtitleService) GetJob(mediaID string) (*dto.SubtitleJobDetail, error) {
	type row struct {
		model.SubtitleJob
		MediaTitle        string `gorm:"column:media_title"`
		MediaCategoryID   string `gorm:"column:media_category_id"`
		MediaCategoryName string `gorm:"column:media_category_name"`
	}
	var r row
	if err := s.db.Table("subtitle_jobs AS sj").
		Select("sj.*, m.title AS media_title, m.category_id AS media_category_id, c.name AS media_category_name").
		Joins("LEFT JOIN media AS m ON m.id = sj.media_id").
		Joins("LEFT JOIN categories AS c ON c.id = m.category_id").
		Where("sj.media_id = ?", mediaID).
		Take(&r).Error; err != nil {
		return nil, err
	}
	d := dto.SubtitleJobItem{
		ID:           r.ID,
		MediaID:      r.MediaID,
		MediaTitle:   r.MediaTitle,
		CategoryID:   r.MediaCategoryID,
		CategoryName: r.MediaCategoryName,
		Status:       r.Status,
		Stage:        r.Stage,
		Progress:     r.Progress,
		SourceLang:   r.SourceLang,
		TargetLang:   r.TargetLang,
		ASRModel:     r.ASRModel,
		MTModel:      r.MTModel,
		SegmentCount: r.SegmentCount,
		ErrorMsg:     r.ErrorMsg,
		StartedAt:    r.StartedAt,
		FinishedAt:   r.FinishedAt,
		CreatedAt:    r.CreatedAt,
		UpdatedAt:    r.UpdatedAt,
	}
	return &d, nil
}

// Retry 强制把指定 mediaID 任务重置为 PENDING 并入队。
func (s *SubtitleService) Retry(mediaID string) error {
	if !s.snap().Enabled {
		return ErrSubtitleDisabled
	}
	var job model.SubtitleJob
	if err := s.db.Where("media_id = ?", mediaID).Take(&job).Error; err != nil {
		// 不存在则创建新的
		return s.EnsureJob(mediaID)
	}
	if err := s.db.Model(&job).Updates(map[string]any{
		"status":    model.SubtitleStatusPending,
		"stage":     model.SubtitleStageQueued,
		"progress":  0,
		"error_msg": "",
	}).Error; err != nil {
		return err
	}
	s.enqueue(mediaID)
	return nil
}

// Delete 删除字幕任务和对应 VTT 文件。
func (s *SubtitleService) Delete(mediaID string) error {
	var job model.SubtitleJob
	if err := s.db.Where("media_id = ?", mediaID).Take(&job).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil
		}
		return err
	}
	s.deleteVTTFile(&job)
	return s.db.Delete(&job).Error
}

// SetDisabled 切换禁用状态（DISABLED 状态不会被 worker 消费）。
func (s *SubtitleService) SetDisabled(mediaID string, disabled bool) error {
	cur := s.snap()
	var job model.SubtitleJob
	if err := s.db.Where("media_id = ?", mediaID).Take(&job).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			if !disabled {
				return s.EnsureJob(mediaID)
			}
			// 创建一条 DISABLED 占位
			return s.db.Create(&model.SubtitleJob{
				MediaID:    mediaID,
				Status:     model.SubtitleStatusDisabled,
				Stage:      model.SubtitleStageQueued,
				SourceLang: cur.WhisperLanguage,
				TargetLang: cur.TargetLang,
			}).Error
		}
		return err
	}
	target := model.SubtitleStatusPending
	if disabled {
		target = model.SubtitleStatusDisabled
	}
	if err := s.db.Model(&job).Update("status", target).Error; err != nil {
		return err
	}
	if !disabled {
		s.enqueue(mediaID)
	}
	return nil
}

// BatchRegenerate 批量重新生成（用于 admin 面板）。
//
// 优先级：MediaIDs > OnlyFailed > CategoryID > All
//   - MediaIDs：精确指定一组媒体（admin 面板勾选场景）
//   - OnlyFailed：所有 FAILED 任务
//   - CategoryID：指定分类下所有 ACTIVE 媒体；CategoryID="_none" 表示未分类媒体
//   - All：全部 ACTIVE 媒体
func (s *SubtitleService) BatchRegenerate(req dto.SubtitleBatchRegenerateRequest) (dto.SubtitleBatchRegenerateResponse, error) {
	if !s.snap().Enabled {
		return dto.SubtitleBatchRegenerateResponse{}, ErrSubtitleDisabled
	}

	var mediaIDs []string

	switch {
	case len(req.MediaIDs) > 0:
		mediaIDs = req.MediaIDs
	case req.OnlyFailed:
		if err := s.db.Model(&model.SubtitleJob{}).
			Where("status = ?", model.SubtitleStatusFailed).
			Pluck("media_id", &mediaIDs).Error; err != nil {
			return dto.SubtitleBatchRegenerateResponse{}, err
		}
	case req.CategoryID != "":
		q := s.db.Model(&model.Media{}).
			Where("status = ?", model.MediaStatusActive)
		if req.CategoryID == "_none" {
			q = q.Where("category_id IS NULL OR category_id = ''")
		} else {
			q = q.Where("category_id = ?", req.CategoryID)
		}
		if err := q.Pluck("id", &mediaIDs).Error; err != nil {
			return dto.SubtitleBatchRegenerateResponse{}, err
		}
	case req.All:
		if err := s.db.Model(&model.Media{}).
			Where("status = ?", model.MediaStatusActive).
			Pluck("id", &mediaIDs).Error; err != nil {
			return dto.SubtitleBatchRegenerateResponse{}, err
		}
	}

	resp := dto.SubtitleBatchRegenerateResponse{}
	for _, id := range mediaIDs {
		if err := s.Retry(id); err != nil {
			resp.Skipped++
			log.Printf("[subtitle] batch retry skip media=%s: %v", id, err)
			continue
		}
		resp.Enqueued++
	}
	return resp, nil
}

// BatchSetDisabled 批量设置 DISABLED 状态。
//
//	disabled=true：所选 media 的 SubtitleJob 行（不存在则会按 SetDisabled 语义补建一条占位）
//	  状态切到 DISABLED，worker 后续不会处理；
//	disabled=false：解除禁用，重新入队（FAILED → PENDING；不存在则按 EnsureJob 创建）。
//
// 若字幕功能未启用，返回 ErrSubtitleDisabled。逐条调用，单条失败不影响其它。
func (s *SubtitleService) BatchSetDisabled(mediaIDs []string, disabled bool) (dto.SubtitleBatchOpResponse, error) {
	if !s.snap().Enabled {
		return dto.SubtitleBatchOpResponse{}, ErrSubtitleDisabled
	}
	out := dto.SubtitleBatchOpResponse{}
	for _, id := range mediaIDs {
		if id == "" {
			out.Skipped++
			continue
		}
		if err := s.SetDisabled(id, disabled); err != nil {
			out.Skipped++
			log.Printf("[subtitle] batch set-disabled skip media=%s disabled=%v: %v", id, disabled, err)
			continue
		}
		out.Affected++
	}
	return out, nil
}

// BatchCancel 批量取消任务。
//
// 与"禁用"的语义区分：
//   - 禁用是长效状态切换（DISABLED 行为持续，直到再次手动解除）
//   - 取消针对"当前正在排队 / 处理 / 失败"的任务，把它们从 active pipeline 中拿出来；
//     状态同样落到 DISABLED 占位，但语义上是"放弃这次生成"。
//
// 处理规则：
//   - 不存在 SubtitleJob 行：跳过（没有可取消对象）
//   - 状态 ∈ {PENDING, RUNNING, FAILED}：标记 DISABLED，并从内存 enqueuedIDs 剔除，
//     避免 worker 重复 pick 同一 mediaID
//   - 状态 ∈ {DONE, DISABLED}：跳过（无需取消）
//
// RUNNING 任务：当前没有 per-job cancel 通道，状态切到 DISABLED 后 processOne 末尾的
// 写库动作仍会发生，但不会再被重新入队；下次 worker 拉到同 mediaID 时已通过 processOne
// 入口的 DISABLED 守护提前返回。
func (s *SubtitleService) BatchCancel(mediaIDs []string) (dto.SubtitleBatchOpResponse, error) {
	if !s.snap().Enabled {
		return dto.SubtitleBatchOpResponse{}, ErrSubtitleDisabled
	}
	out := dto.SubtitleBatchOpResponse{}
	for _, id := range mediaIDs {
		if id == "" {
			out.Skipped++
			continue
		}
		var job model.SubtitleJob
		if err := s.db.Where("media_id = ?", id).Take(&job).Error; err != nil {
			out.Skipped++
			continue
		}
		switch job.Status {
		case model.SubtitleStatusDone, model.SubtitleStatusDisabled:
			out.Skipped++
			continue
		}
		if err := s.db.Model(&job).Update("status", model.SubtitleStatusDisabled).Error; err != nil {
			out.Skipped++
			log.Printf("[subtitle] batch cancel skip media=%s: %v", id, err)
			continue
		}
		s.enqueuedIDs.Delete(id)
		out.Affected++
	}
	return out, nil
}

// BatchDelete 批量删除字幕任务和已生成的 VTT 文件。
//
// 与 BatchCancel 不同：行被物理删除，下次再点"重新生成"会创建全新的 PENDING。
// 不存在的行视为"已删除"，跳过但不报错。
func (s *SubtitleService) BatchDelete(mediaIDs []string) (dto.SubtitleBatchOpResponse, error) {
	out := dto.SubtitleBatchOpResponse{}
	for _, id := range mediaIDs {
		if id == "" {
			out.Skipped++
			continue
		}
		var job model.SubtitleJob
		if err := s.db.Where("media_id = ?", id).Take(&job).Error; err != nil {
			out.Skipped++
			continue
		}
		s.deleteVTTFile(&job)
		if err := s.db.Delete(&job).Error; err != nil {
			out.Skipped++
			log.Printf("[subtitle] batch delete skip media=%s: %v", id, err)
			continue
		}
		s.enqueuedIDs.Delete(id)
		out.Affected++
	}
	return out, nil
}

// QueueStatus 返回各状态的任务计数（dashboard 用）。
func (s *SubtitleService) QueueStatus() (dto.SubtitleQueueStatus, error) {
	type row struct {
		Status string
		C      int64
	}
	var rows []row
	if err := s.db.Table("subtitle_jobs").
		Select("status, COUNT(*) AS c").
		Group("status").Scan(&rows).Error; err != nil {
		return dto.SubtitleQueueStatus{}, err
	}
	out := dto.SubtitleQueueStatus{
		GlobalMaxConcurrency: s.snap().GlobalMaxConcurrency,
	}
	for _, r := range rows {
		switch r.Status {
		case model.SubtitleStatusPending:
			out.Pending = r.C
		case model.SubtitleStatusRunning:
			out.Running = r.C
		case model.SubtitleStatusDone:
			out.Done = r.C
		case model.SubtitleStatusFailed:
			out.Failed = r.C
		case model.SubtitleStatusDisabled:
			out.Disabled = r.C
		}
	}
	return out, nil
}

// CurrentSettings 返回当前生效的字幕配置（脱敏 api key）。
func (s *SubtitleService) CurrentSettings() dto.SubtitleSettingsResponse {
	cur := s.snap()
	apiKey := cur.TranslateAPIKey
	if len(apiKey) > 8 {
		apiKey = apiKey[:4] + strings.Repeat("*", len(apiKey)-8) + apiKey[len(apiKey)-4:]
	} else if apiKey != "" {
		apiKey = strings.Repeat("*", len(apiKey))
	}
	return dto.SubtitleSettingsResponse{
		Enabled:          cur.Enabled,
		WhisperBin:       cur.WhisperBin,
		WhisperModel:     cur.WhisperModel,
		WhisperLanguage:  cur.WhisperLanguage,
		WhisperThreads:   cur.WhisperThreads,
		TranslateBaseURL: cur.TranslateBaseURL,
		TranslateModel:   cur.TranslateModel,
		TranslateAPIKey:  apiKey,
		TargetLang:       cur.TargetLang,
		BatchSize:        cur.BatchSize,
	}
}

// UpdateSettings 应用 admin 提交的字幕配置 patch：写 DB + 更新 in-memory cfg。
//
// 行为：
//   - 校验 patch 字段（线程数、批大小范围、URL 格式等）
//   - 持锁写 DB（system_settings upsert）+ 同步更新 *s.cfg 字段
//   - 不重启 worker / 不变 LocalWorkerEnabled / WorkerStaleThreshold 等部署相关字段
//   - 不再扫描存量 media；启用字幕功能后是否对历史媒体生成由管理员在
//     字幕管理页手动批量入队
//
// 返回更新后的脱敏 settings 响应，便于 handler 直接回显前端。
func (s *SubtitleService) UpdateSettings(req dto.SubtitleSettingsUpdateRequest) (dto.SubtitleSettingsResponse, error) {
	s.cfgMu.Lock()
	if err := applySubtitlePatch(s.db, s.cfg, &req); err != nil {
		s.cfgMu.Unlock()
		return dto.SubtitleSettingsResponse{}, err
	}
	s.cfgMu.Unlock()

	return s.CurrentSettings(), nil
}

// deleteVTTFile 物理删除 VTT 文件（如存在）。
func (s *SubtitleService) deleteVTTFile(job *model.SubtitleJob) {
	if job.VttPath == "" {
		return
	}
	abs := filepath.Join(s.snap().SubtitlesDir, job.VttPath)
	if err := os.Remove(abs); err != nil && !os.IsNotExist(err) {
		log.Printf("[subtitle] delete vtt %s failed: %v", abs, err)
	}
}

// ---- VTT 写入辅助 ----

// writeVTT 把 segments + translations 写成 WebVTT 文件。
// 输入两个数组等长（translateAll 已保证回退）。
//
// cue payload 约定：
//   - 第 1 行：目标语言（译文）。translateAll 失败时会回退为原文。
//   - 第 2 行（可选）：源语言（原文）。仅当原文非空且与译文不同步写入。
//
// 该约定与前端自定义渲染层配合：前端按 \n 拆分 cue 文本，
// 第 1 行始终作为主字幕，第 2 行作为可选的"显示原文"开关来源。
// 已存在的旧字幕（单行译文）天然兼容——拆分结果只有一行，原文为空。
func writeVTT(absPath string, segs []ASRSegment, translations []string) error {
	if err := os.MkdirAll(filepath.Dir(absPath), 0o755); err != nil {
		return err
	}
	var b strings.Builder
	b.WriteString("WEBVTT\n\n")
	for i, seg := range segs {
		translated := strings.TrimSpace(translations[i])
		source := strings.TrimSpace(seg.Text)
		// 译文为空时回退原文，避免出现空白 cue
		if translated == "" {
			translated = source
		}
		if translated == "" {
			continue
		}
		b.WriteString(strconv.Itoa(i + 1))
		b.WriteByte('\n')
		b.WriteString(formatVTTTimestamp(seg.StartMs))
		b.WriteString(" --> ")
		b.WriteString(formatVTTTimestamp(seg.EndMs))
		b.WriteByte('\n')
		// VTT 的 cue 文本不能含 "-->"；ASR/翻译输出极少出现，做最小防御。
		// 同时把单条文本里的换行折叠为空格，保证按 \n 切分的"译文/原文"语义不被破坏。
		translated = collapseCueLine(translated)
		b.WriteString(translated)
		if source != "" && source != translated {
			source = collapseCueLine(source)
			b.WriteByte('\n')
			b.WriteString(source)
		}
		b.WriteString("\n\n")
	}
	tmp := absPath + ".tmp"
	if err := os.WriteFile(tmp, []byte(b.String()), 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, absPath)
}

// collapseCueLine 清洗单行 cue 文本：
//   - 把 CR/LF/制表符等空白折叠为空格，避免破坏 "译文\n原文" 的换行约定
//   - 替换 VTT 保留分隔符 "-->"，防止生成非法 cue
func collapseCueLine(s string) string {
	s = strings.ReplaceAll(s, "\r\n", " ")
	s = strings.ReplaceAll(s, "\n", " ")
	s = strings.ReplaceAll(s, "\r", " ")
	s = strings.ReplaceAll(s, "\t", " ")
	s = strings.ReplaceAll(s, "-->", "→")
	return strings.TrimSpace(s)
}

// formatVTTTimestamp 把毫秒数格式化为 HH:MM:SS.mmm（VTT 标准）。
func formatVTTTimestamp(ms int64) string {
	if ms < 0 {
		ms = 0
	}
	h := ms / 3600000
	ms -= h * 3600000
	m := ms / 60000
	ms -= m * 60000
	s := ms / 1000
	ms -= s * 1000
	return fmt.Sprintf("%02d:%02d:%02d.%03d", h, m, s, ms)
}
