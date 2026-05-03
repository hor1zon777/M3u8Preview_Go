// Package model
// subtitle_worker.go 定义远程字幕 worker 相关的两张表：
//
//   - SubtitleWorkerToken：admin 在面板生成的长期凭证（"mwt_xxx" Bearer token），
//     bcrypt 存储，吊销走 RevokedAt（soft delete），保留审计痕迹。
//
//   - SubtitleWorker：worker 启动时通过 /worker/register 写入的运行时记录，
//     由 worker 端持续 heartbeat 维护 LastSeenAt；admin 面板据此显示「在线 worker」。
//
// 与 SubtitleJob 的关系：
//   - SubtitleJob.ClaimedBy 引用 SubtitleWorker.ID
//   - SubtitleWorker.TokenID 引用 SubtitleWorkerToken.ID
//
// 这两张表只在远程 worker 工作流里使用；本地 in-process whisper.cpp worker
// （cfg.Subtitle.LocalWorkerEnabled=true 时启用）不会写入它们。
package model

import (
	"time"

	"github.com/google/uuid"
	"gorm.io/gorm"
)

// SubtitleWorkerToken admin 生成的 worker 长期凭证。
//
// 安全设计：
//   - 明文 token 形如 "mwt_<32 chars random base32>"，仅在 POST 创建时返回一次
//   - DB 仅存 bcrypt(token) → TokenHash，无法反推
//   - TokenPrefix 存明文前 8 位（"mwt_abcd"），让 admin 面板能识别"是哪一条"
//   - 吊销走 RevokedAt 软删除，保留审计；中间件在 RevokedAt != nil 时拒绝
//
// 限流：
//   - MaxConcurrency 限制该 token 名下的 worker 集合同时持有的 RUNNING 任务数。
//     ClaimNextJob 抢占前会校验"该 token 当前 RUNNING 数 < MaxConcurrency"。
//     默认 1，admin 可在面板调整。0 表示不限。
type SubtitleWorkerToken struct {
	ID          string `gorm:"primaryKey;type:text" json:"id"`
	Name        string `gorm:"type:text;not null" json:"name"`
	TokenHash   string `gorm:"column:token_hash;type:text;not null" json:"-"`
	TokenPrefix string `gorm:"column:token_prefix;type:text;not null;index:idx_worker_token_prefix" json:"tokenPrefix"`
	// MaxConcurrency 旧字段：不区分 capability 时的总并发上限兜底；0 表示不限。
	MaxConcurrency int `gorm:"column:max_concurrency;type:integer;not null;default:1" json:"maxConcurrency"`
	// MaxAudioConcurrency / MaxSubtitleConcurrency 是 v2 分布式拆分后按 capability 维度的并发上限。
	// 默认 audio=2（带宽密集，机 A 可同时拉多个 m3u8）/ subtitle=1（GPU 密集，单卡一次跑一条）。
	// 0 表示该维度不限（仍受 MaxConcurrency 与 cfg.GlobalMaxConcurrency 兜底）。
	MaxAudioConcurrency    int        `gorm:"column:max_audio_concurrency;type:integer;not null;default:2" json:"maxAudioConcurrency"`
	MaxSubtitleConcurrency int        `gorm:"column:max_subtitle_concurrency;type:integer;not null;default:1" json:"maxSubtitleConcurrency"`
	CreatedAt              time.Time  `gorm:"autoCreateTime" json:"createdAt"`
	LastUsedAt             *time.Time `gorm:"column:last_used_at" json:"lastUsedAt,omitempty"`
	RevokedAt              *time.Time `gorm:"column:revoked_at" json:"revokedAt,omitempty"`
}

// TableName 显式指定。
func (SubtitleWorkerToken) TableName() string { return "subtitle_worker_tokens" }

// BeforeCreate 自动生成 UUID 主键。
func (t *SubtitleWorkerToken) BeforeCreate(tx *gorm.DB) error {
	if t.ID == "" {
		t.ID = uuid.NewString()
	}
	return nil
}

// SubtitleWorker 在线 worker 注册表。
//
// 生命周期：
//   - worker 启动 → POST /worker/register（带 token） → 服务端 upsert 一条
//     （ID 由 worker 端持久化生成，重启后复用，避免每次重启刷新一条）
//   - worker 跑 claim/heartbeat 时刷新 LastSeenAt
//   - admin 面板查询 "LastSeenAt > now-5min" 视为在线
//
// 不做硬下线：worker 异常退出时 LastSeenAt 自然过期；前端按时间戳判定。
type SubtitleWorker struct {
	ID            string    `gorm:"primaryKey;type:text" json:"id"`
	TokenID       string    `gorm:"column:token_id;type:text;not null;index:idx_worker_token" json:"tokenId"`
	Name          string    `gorm:"type:text;not null" json:"name"`
	Version       string    `gorm:"type:text" json:"version,omitempty"`
	GPU           string    `gorm:"column:gpu;type:text" json:"gpu,omitempty"`
	CurrentJobID  string    `gorm:"column:current_job_id;type:text" json:"currentJobId,omitempty"`
	LastSeenAt    time.Time `gorm:"column:last_seen_at;not null;index:idx_worker_last_seen" json:"lastSeenAt"`
	RegisteredAt  time.Time `gorm:"column:registered_at;not null" json:"registeredAt"`
	CompletedJobs int64     `gorm:"column:completed_jobs;type:integer;default:0" json:"completedJobs"`
	FailedJobs    int64     `gorm:"column:failed_jobs;type:integer;default:0" json:"failedJobs"`
	// Capabilities 是 worker 自报的能力 JSON 数组字符串，
	// 形如 `["audio_extract"]` / `["asr_subtitle"]` / `["audio_extract","asr_subtitle"]`。
	// 旧 client（不带 capabilities 字段）默认 `["audio_extract","asr_subtitle"]`，向后兼容单机部署。
	Capabilities string `gorm:"column:capabilities;type:text;not null;default:'[\"audio_extract\",\"asr_subtitle\"]'" json:"capabilities"`

	Token *SubtitleWorkerToken `gorm:"foreignKey:TokenID;references:ID;constraint:OnDelete:CASCADE" json:"-"`
}

// TableName 显式指定。
func (SubtitleWorker) TableName() string { return "subtitle_workers" }

// BeforeCreate 兜底主键。worker 端通常会自带 UUID 提交（持久化在客户端 store），
// 这里只在为空时补一个，避免首次注册没传 ID 写不进去。
func (w *SubtitleWorker) BeforeCreate(tx *gorm.DB) error {
	if w.ID == "" {
		w.ID = uuid.NewString()
	}
	return nil
}
