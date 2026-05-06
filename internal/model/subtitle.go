// Package model
// subtitle.go 定义字幕生成任务表 SubtitleJob。
//
// 设计要点：
//   - media_id 唯一索引：每个媒体最多一个字幕任务（重新生成走 update，不新建行）
//   - status / progress / stage 三元组对外暴露，前端拉取状态时使用
//   - vtt_path 是相对 UploadsDir 的子路径（如 "subtitles/<id>.vtt"）；解析为本地文件时由 service 拼接
//   - source_lang / target_lang 显式存储，便于后续支持多语言对（jp→zh / en→zh / ko→zh 等）
//   - asr_model / mt_model 留快照便于回溯生成质量
package model

import (
	"time"

	"github.com/google/uuid"
	"gorm.io/gorm"
)

// SubtitleJob 对应 subtitle_jobs 表。
type SubtitleJob struct {
	ID         string `gorm:"primaryKey;type:text" json:"id"`
	MediaID    string `gorm:"column:media_id;type:text;not null;uniqueIndex:idx_subtitle_media" json:"mediaId"`
	Status     string `gorm:"type:text;not null;default:PENDING;index:idx_subtitle_status" json:"status"`
	Stage      string `gorm:"type:text;not null;default:queued" json:"stage"`
	Progress   int    `gorm:"type:integer;not null;default:0" json:"progress"`
	SourceLang string `gorm:"column:source_lang;type:text;not null;default:ja" json:"sourceLang"`
	TargetLang string `gorm:"column:target_lang;type:text;not null;default:zh" json:"targetLang"`
	VttPath    string `gorm:"column:vtt_path;type:text" json:"vttPath,omitempty"`
	ErrorMsg   string `gorm:"column:error_msg;type:text" json:"errorMsg,omitempty"`
	ASRModel   string `gorm:"column:asr_model;type:text" json:"asrModel,omitempty"`
	MTModel    string `gorm:"column:mt_model;type:text" json:"mtModel,omitempty"`
	// 段落数（ASR 输出 cue 数量），用于展示
	SegmentCount int        `gorm:"column:segment_count;type:integer;default:0" json:"segmentCount"`
	StartedAt    *time.Time `gorm:"column:started_at" json:"startedAt,omitempty"`
	FinishedAt   *time.Time `gorm:"column:finished_at" json:"finishedAt,omitempty"`

	// 远程 worker 协作字段：
	//   - ClaimedBy 当前认领此任务的 worker_id（subtitle_workers.id）；空表示无主
	//   - ClaimedAt 认领时间，配合 LastHeartbeatAt 用于检测僵尸任务
	//   - LastHeartbeatAt worker 上次上报进度的时间；超过阈值（默认 10 分钟）由 RecoverStaleJobs 重置回 PENDING
	// 本地 in-process worker 模式下这些字段保持空值。
	ClaimedBy       string     `gorm:"column:claimed_by;type:text;index:idx_subtitle_claimed_by" json:"claimedBy,omitempty"`
	ClaimedAt       *time.Time `gorm:"column:claimed_at" json:"claimedAt,omitempty"`
	LastHeartbeatAt *time.Time `gorm:"column:last_heartbeat_at" json:"lastHeartbeatAt,omitempty"`

	// 远程 worker 协作字段（v2，分布式拆分）：
	//   - audio_extract worker 完成后写入 audio_artifact_*，stage 切到 audio_uploaded；
	//   - asr_subtitle worker 接手后通过 audio_artifact_url 拉取 FLAC，再走 ASR/翻译/写 VTT；
	//   - audio_worker_id / subtitle_worker_id 分别记录两阶段的执行者，便于审计与故障定位。
	AudioArtifactPath       string     `gorm:"column:audio_artifact_path;type:text" json:"audioArtifactPath,omitempty"`
	AudioArtifactSize       int64      `gorm:"column:audio_artifact_size;type:bigint;default:0" json:"audioArtifactSize,omitempty"`
	AudioArtifactSHA256     string     `gorm:"column:audio_artifact_sha256;type:text" json:"audioArtifactSha256,omitempty"`
	AudioArtifactFormat     string     `gorm:"column:audio_artifact_format;type:text" json:"audioArtifactFormat,omitempty"`
	AudioArtifactDurationMs int64      `gorm:"column:audio_artifact_duration_ms;type:bigint;default:0" json:"audioArtifactDurationMs,omitempty"`
	AudioUploadedAt         *time.Time `gorm:"column:audio_uploaded_at" json:"audioUploadedAt,omitempty"`
	AudioWorkerID           string     `gorm:"column:audio_worker_id;type:text;index:idx_subtitle_audio_worker" json:"audioWorkerId,omitempty"`
	SubtitleWorkerID        string     `gorm:"column:subtitle_worker_id;type:text;index:idx_subtitle_subtitle_worker" json:"subtitleWorkerId,omitempty"`

	// === v4 重试 / 调度字段 ===
	//
	// Attempt 当前已重试次数（成功跑完后不重置；FAILED 终态时停止累加）。
	// 每次 WorkerFail 上报 retriable kind 时 +1；neutral kind（worker_capacity / worker_shutdown）
	// 不增；permanent kind 不再走重试路径，直接进 FAILED。
	//
	// 用 attempt < max_attempts 作为"还能再试"的硬门槛；达到上限时 WorkerFail 强制 FAILED，
	// 避免不可重试错误（如 m3u8 永久失效、模型损坏）在三端无限循环消耗算力。
	Attempt int `gorm:"column:attempt;type:integer;not null;default:0" json:"attempt"`
	// MaxAttempts 此任务允许的最大尝试次数（含首次）。
	// 默认 3：一次原始 + 最多 2 次重试。EnsureJob 入队时按服务端默认填；admin 可调。
	MaxAttempts int `gorm:"column:max_attempts;type:integer;not null;default:3" json:"maxAttempts"`
	// LastErrorKind 最近一次失败的错误分类（model.ErrorKind* 枚举）。
	// 与 ErrorMsg 配合：ErrorMsg 给人看具体上下文，LastErrorKind 给机器判断要不要重试 / UI 怎么展示。
	LastErrorKind string `gorm:"column:last_error_kind;type:text;not null;default:''" json:"lastErrorKind,omitempty"`
	// NextRetryAt 下一次允许被 claim 的最早时间。
	// retriable fail 后服务端按退避表填 now+backoff(attempt)；claim 查询用
	// `next_retry_at IS NULL OR next_retry_at <= now()` 过滤掉冷却中的任务，
	// 避免刚失败的任务被立刻重抢导致 thrashing。
	NextRetryAt *time.Time `gorm:"column:next_retry_at;index:idx_subtitle_next_retry" json:"nextRetryAt,omitempty"`
	// Priority claim 排序权重（高优先）。新任务默认 0；retry 任务 -1（让位给新任务，防饥饿）；
	// admin 可手动加到 +10 表示加急；老化 ticker 把超过 10min 没派出去的任务慢慢提优先级。
	Priority int `gorm:"column:priority;type:integer;not null;default:0;index:idx_subtitle_priority" json:"priority"`

	CreatedAt time.Time `gorm:"autoCreateTime" json:"createdAt"`
	UpdatedAt time.Time `gorm:"autoUpdateTime" json:"updatedAt"`

	Media *Media `gorm:"foreignKey:MediaID;references:ID;constraint:OnDelete:CASCADE" json:"-"`
}

// TableName 显式指定，避免复数变形规则差异。
func (SubtitleJob) TableName() string { return "subtitle_jobs" }

// BeforeCreate 自动生成 UUID 主键。
func (s *SubtitleJob) BeforeCreate(tx *gorm.DB) error {
	if s.ID == "" {
		s.ID = uuid.NewString()
	}
	return nil
}
