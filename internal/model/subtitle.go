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
	ClaimedBy        string     `gorm:"column:claimed_by;type:text;index:idx_subtitle_claimed_by" json:"claimedBy,omitempty"`
	ClaimedAt        *time.Time `gorm:"column:claimed_at" json:"claimedAt,omitempty"`
	LastHeartbeatAt  *time.Time `gorm:"column:last_heartbeat_at" json:"lastHeartbeatAt,omitempty"`

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
