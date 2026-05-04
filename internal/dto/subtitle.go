// Package dto
// subtitle.go 定义字幕模块的请求 / 响应结构。
//
// 端点契约：
//   - GET  /api/v1/subtitle/:mediaId/status      → SubtitleStatusResponse
//   - GET  /api/v1/subtitle/:mediaId.vtt         → VTT 文件（HMAC 签名）
//   - GET  /api/v1/admin/subtitle/jobs           → 列表，PaginatedData[SubtitleJobItem]
//   - GET  /api/v1/admin/subtitle/jobs/:mediaId  → SubtitleJobDetail
//   - POST /api/v1/admin/subtitle/jobs/:mediaId/retry      → 重新入队
//   - DELETE /api/v1/admin/subtitle/jobs/:mediaId          → 删除任务 + VTT 文件
//   - POST /api/v1/admin/subtitle/jobs/batch-regenerate    → 批量重新生成
//   - GET  /api/v1/admin/subtitle/settings                 → 当前生效的字幕配置（不含 api key）
package dto

import "time"

// SubtitleStatusResponse 给播放页拉取，决定是否挂 <track>。
type SubtitleStatusResponse struct {
	MediaID    string `json:"mediaId"`
	Status     string `json:"status"`
	Stage      string `json:"stage"`
	Progress   int    `json:"progress"`
	SourceLang string `json:"sourceLang"`
	TargetLang string `json:"targetLang"`
	// VttURL 形如 /api/v1/subtitle/<mediaId>.vtt?expires=...&sig=...，仅当 status=DONE 才返回
	VttURL   string `json:"vttUrl,omitempty"`
	ErrorMsg string `json:"errorMsg,omitempty"`
}

// SubtitleJobItem 列表项。
type SubtitleJobItem struct {
	ID           string     `json:"id"`
	MediaID      string     `json:"mediaId"`
	MediaTitle   string     `json:"mediaTitle"`
	CategoryID   string     `json:"categoryId,omitempty"`
	CategoryName string     `json:"categoryName,omitempty"`
	Status       string     `json:"status"`
	Stage        string     `json:"stage"`
	Progress     int        `json:"progress"`
	SourceLang   string     `json:"sourceLang"`
	TargetLang   string     `json:"targetLang"`
	ASRModel     string     `json:"asrModel,omitempty"`
	MTModel      string     `json:"mtModel,omitempty"`
	SegmentCount int        `json:"segmentCount"`
	ErrorMsg     string     `json:"errorMsg,omitempty"`
	StartedAt    *time.Time `json:"startedAt,omitempty"`
	FinishedAt   *time.Time `json:"finishedAt,omitempty"`
	CreatedAt    time.Time  `json:"createdAt"`
	UpdatedAt    time.Time  `json:"updatedAt"`

	// v2 分布式拆分相关字段（M8.2 双阶段 timeline 用）：
	AudioWorkerID           string     `json:"audioWorkerId,omitempty"`
	SubtitleWorkerID        string     `json:"subtitleWorkerId,omitempty"`
	AudioArtifactSize       int64      `json:"audioArtifactSize,omitempty"`
	AudioArtifactFormat     string     `json:"audioArtifactFormat,omitempty"`
	AudioArtifactDurationMs int64      `json:"audioArtifactDurationMs,omitempty"`
	AudioUploadedAt         *time.Time `json:"audioUploadedAt,omitempty"`
}

// SubtitleJobDetail 详情接口返回。等同列表项，预留扩展字段。
type SubtitleJobDetail = SubtitleJobItem

// SubtitleBatchRegenerateRequest 批量重新生成。
//
// 优先级：MediaIDs > OnlyFailed > CategoryID > All；多个互斥字段同时给时按上述顺序生效。
//
//   - MediaIDs：精确指定一组媒体（admin 面板勾选场景）
//   - OnlyFailed：所有 FAILED 任务
//   - CategoryID：指定分类下所有 ACTIVE 媒体
//   - All：全部 ACTIVE 媒体
type SubtitleBatchRegenerateRequest struct {
	MediaIDs []string `json:"mediaIds"`
	All      bool     `json:"all"`
	// OnlyFailed=true 时仅重试 FAILED 状态的任务
	OnlyFailed bool `json:"onlyFailed"`
	// CategoryID 非空时仅重试该分类下所有 ACTIVE 媒体
	CategoryID string `json:"categoryId,omitempty"`
}

// SubtitleBatchRegenerateResponse 批量重新生成响应。
type SubtitleBatchRegenerateResponse struct {
	Enqueued int `json:"enqueued"`
	Skipped  int `json:"skipped"`
}

// SubtitleBatchMediaIDsRequest 一组 mediaId 的批量操作请求体。
// 用于批量取消 / 批量删除 / 批量禁用（共用入参）。
type SubtitleBatchMediaIDsRequest struct {
	MediaIDs []string `json:"mediaIds" binding:"required,min=1,dive,required"`
}

// SubtitleBatchSetDisabledRequest 批量设置启用/禁用状态。
type SubtitleBatchSetDisabledRequest struct {
	MediaIDs []string `json:"mediaIds" binding:"required,min=1,dive,required"`
	// Disabled=true 把所选标记为 DISABLED；false 还原为 PENDING 并重新入队。
	Disabled bool `json:"disabled"`
}

// SubtitleBatchOpResponse 批量操作的统一返回。
//
//	Affected：实际被改动 / 删除的条数
//	Skipped：因状态不允许（如已 DONE）或 row 不存在而跳过的条数
type SubtitleBatchOpResponse struct {
	Affected int `json:"affected"`
	Skipped  int `json:"skipped"`
}

// SubtitleSettingsResponse admin 面板展示当前配置（敏感字段脱敏）。
//
// 注：历史版本曾包含 AutoGenerate（自动为新媒体入队）字段；当前版本字幕仅手动入队，
// 此字段已移除。前端读取本响应若需向后兼容，请把 autoGenerate 视为永远 false。
type SubtitleSettingsResponse struct {
	Enabled          bool   `json:"enabled"`
	WhisperBin       string `json:"whisperBin"`
	WhisperModel     string `json:"whisperModel"`
	WhisperLanguage  string `json:"whisperLanguage"`
	WhisperThreads   int    `json:"whisperThreads"`
	TranslateBaseURL string `json:"translateBaseUrl"`
	TranslateModel   string `json:"translateModel"`
	TranslateAPIKey  string `json:"translateApiKey"` // 已脱敏
	TargetLang       string `json:"targetLang"`
	BatchSize        int    `json:"batchSize"`
}

// SubtitleSettingsUpdateRequest admin 面板提交的字幕配置 patch。
//
// 全部字段使用指针：
//   - nil 表示 "不修改"
//   - 字符串字段允许传空串表示"清除/恢复默认"
//   - 翻译 API Key 字段若包含 "***"（脱敏占位）会被 service 忽略，
//     避免前端展示脱敏值后误覆盖真实 key
//
// 持久化到 system_settings 后立即对后续 ASR / 翻译调用生效；
// LocalWorkerEnabled / WorkerStaleThreshold 等部署相关字段不在此处修改。
type SubtitleSettingsUpdateRequest struct {
	Enabled          *bool   `json:"enabled,omitempty"`
	WhisperBin       *string `json:"whisperBin,omitempty"`
	WhisperModel     *string `json:"whisperModel,omitempty"`
	WhisperLanguage  *string `json:"whisperLanguage,omitempty"`
	WhisperThreads   *int    `json:"whisperThreads,omitempty"`
	TranslateBaseURL *string `json:"translateBaseUrl,omitempty"`
	TranslateModel   *string `json:"translateModel,omitempty"`
	TranslateAPIKey  *string `json:"translateApiKey,omitempty"`
	TargetLang       *string `json:"targetLang,omitempty"`
	BatchSize        *int    `json:"batchSize,omitempty"`
}

// SubtitleQueueStatus 队列概况，admin 顶部 dashboard 用。
type SubtitleQueueStatus struct {
	Pending  int64 `json:"pending"`
	Running  int64 `json:"running"`
	Done     int64 `json:"done"`
	Failed   int64 `json:"failed"`
	Disabled int64 `json:"disabled"`
	// GlobalMaxConcurrency 0=不限；前端可根据此值显示"X / Y"
	GlobalMaxConcurrency int `json:"globalMaxConcurrency"`
}

// === 远程字幕 worker 协议（/api/v1/worker/*） ===

// WorkerRegisterRequest worker 启动时上报基本信息。
// WorkerID 由客户端持久化生成（首次启动时本地 UUID v4），后续重启复用。
//
// Capabilities 字段（v2，分布式拆分）：
//   - 缺省（旧 client）→ 服务端按 ["audio_extract","asr_subtitle"] 兼容（向后兼容单机部署）
//   - 仅 ["audio_extract"]  → 机 A 角色：下载 + 抽音 + FLAC 编码
//   - 仅 ["asr_subtitle"]   → 机 B 角色：ASR + 翻译 + 写 VTT
//   - 两者都有              → 单机模式
type WorkerRegisterRequest struct {
	WorkerID     string   `json:"workerId" binding:"required"`
	Name         string   `json:"name" binding:"required"`
	Version      string   `json:"version,omitempty"`
	GPU          string   `json:"gpu,omitempty"`
	Capabilities []string `json:"capabilities,omitempty"`
}

// WorkerRegisterResponse 注册成功返回值。
//
// AcceptedCapabilities 是服务端实际接受的能力集（默认 = 客户端提交；token 限制时会缩减）。
// 客户端可据此在 UI 上 sanity check（例如收到的不包含自己声明的能力时给出告警）。
type WorkerRegisterResponse struct {
	WorkerID             string   `json:"workerId"`
	ServerTime           int64    `json:"serverTime"`           // unix ms，便于 worker 校时
	WorkerStaleThreshold int64    `json:"workerStaleThreshold"` // 服务端容忍的心跳间隔（秒），worker 应 < 此值上报心跳
	AcceptedCapabilities []string `json:"acceptedCapabilities"`
}

// WorkerClaimRequest 认领任务请求。
type WorkerClaimRequest struct {
	WorkerID string `json:"workerId" binding:"required"`
}

// WorkerClaimedJob 服务端原子认领后返回给 worker 的任务信息。
// 仅在有合适任务时返回；无任务时 handler 返回 204 No Content。
//
// Stage 字段（v2 新增）：
//   - "audio_extract"  → audio worker 派活，需要 M3u8URL + Headers 自行下载
//   - "asr_subtitle"   → subtitle worker 派活，需要 AudioArtifactURL 拉 FLAC
//
// 字段使用约定：
//   - Stage="audio_extract"  时：M3u8URL/Headers 必填；AudioArtifact* 全空
//   - Stage="asr_subtitle"   时：AudioArtifactURL/Size/SHA256/Format/DurationMs 必填；M3u8URL 留空
//
// 旧 worker（不识别 Stage 字段）仍能从 M3u8URL 推断单机一条龙流程，向后兼容。
type WorkerClaimedJob struct {
	JobID      string `json:"jobId"`
	MediaID    string `json:"mediaId"`
	MediaTitle string `json:"mediaTitle,omitempty"` // worker UI 显示用
	Stage      string `json:"stage"`                // "audio_extract" / "asr_subtitle"

	// audio_extract 阶段使用：
	M3u8URL string            `json:"m3u8Url,omitempty"`
	Headers map[string]string `json:"headers,omitempty"`

	// asr_subtitle 阶段使用：
	AudioArtifactURL        string `json:"audioArtifactUrl,omitempty"`
	AudioArtifactSize       int64  `json:"audioArtifactSize,omitempty"`
	AudioArtifactSHA256     string `json:"audioArtifactSha256,omitempty"`
	AudioArtifactFormat     string `json:"audioArtifactFormat,omitempty"`
	AudioArtifactDurationMs int64  `json:"audioArtifactDurationMs,omitempty"`

	SourceLang string `json:"sourceLang"`
	TargetLang string `json:"targetLang"`
}

// WorkerHeartbeatRequest worker 上报阶段 + 进度。
// Stage 必须是 model.SubtitleStage* 之一；Progress ∈ [0, 99]。
type WorkerHeartbeatRequest struct {
	WorkerID string `json:"workerId" binding:"required"`
	Stage    string `json:"stage" binding:"required"`
	Progress int    `json:"progress"`
}

// WorkerCompleteMeta multipart 表单里的元数据 JSON 字段。
type WorkerCompleteMeta struct {
	WorkerID     string `json:"workerId" binding:"required"`
	SegmentCount int    `json:"segmentCount"`
	ASRModel     string `json:"asrModel,omitempty"`
	MTModel      string `json:"mtModel,omitempty"`
}

// WorkerAudioReadyMeta 是 v3 audio_extract worker 完成 FLAC 编码后调用 audio-ready
// 端点的请求体。
//
// v3 broker 模式下，FLAC 文件留在 audio worker 本地，服务端只接收元数据：
//
// Format 取值：
//   - "flac"     → 16 kHz mono FLAC（默认，无损）
//   - "opus_24k" → Opus 24 kbps（小带宽场景，未来扩展）
//   - "wav"      → 16 kHz mono PCM WAV（极简兜底）
//
// Size / SHA256 / DurationMs 用于：
//   - 服务端持久化到 DB，subtitle worker claim 时透传
//   - subtitle worker 收到流后自己校验完整性（broker 不再做 SHA256 校验）
type WorkerAudioReadyMeta struct {
	WorkerID   string `json:"workerId" binding:"required"`
	Size       int64  `json:"size" binding:"required,gt=0"`
	SHA256     string `json:"sha256" binding:"required"`
	Format     string `json:"format" binding:"required"` // "flac" / "opus_24k" / "wav"
	DurationMs int64  `json:"durationMs" binding:"required,gt=0"`
}

// WorkerAudioFetchPollRequest audio worker 长轮询请求体。
type WorkerAudioFetchPollRequest struct {
	WorkerID string `json:"workerId" binding:"required"`
	// TimeoutSec 客户端可接受的最长 hold 时间（默认 25s）；服务端会 clamp 到 [5, 60]。
	TimeoutSec int `json:"timeoutSec,omitempty"`
}

// WorkerAudioLostRequest 是 audio worker 在收到 fetch 通知后发现本地 FLAC 已丢失
// （文件被误删 / storage_dir 改动 / 索引损坏 等）时上报的请求体。
//
// 服务端校验 audio_worker_id == workerId（注意不是 claimed_by，因为 audio_ready
// 之后 claimed_by 已清空，仅 audio_worker_id 标识 owner），通过后清空 audio_artifact_*
// 元数据并把 stage 回到 queued，让其它 audio worker 重新跑。
//
// 设计目的：避免 subtitle worker 反复 GET /audio 触发 broker 死循环——audio worker
// 主动声明丢失比让 stale recovery 等待 ~5 分钟更及时。
type WorkerAudioLostRequest struct {
	WorkerID string `json:"workerId" binding:"required"`
	// ErrorMsg 描述具体原因（"file not found in storage_dir=..." 等），落到
	// subtitle_jobs.error_msg 方便 admin 排查。
	ErrorMsg string `json:"errorMsg"`
}

// WorkerAudioFetchTask audio worker long-poll 拿到的指令。
//
// Action 取值：
//   - "fetch"  ：subtitle worker 在等 jobId 的 FLAC，请上传
//   - "cleanup"：任务已完成 / 已被 stale 回收，请删除本地 jobId.flac + 索引项
type WorkerAudioFetchTask struct {
	Action string `json:"action"`
	JobID  string `json:"jobId"`
}

// WorkerFailRequest 失败上报。
type WorkerFailRequest struct {
	WorkerID string `json:"workerId" binding:"required"`
	ErrorMsg string `json:"errorMsg"`
}

// === Admin worker 管理（/api/v1/admin/subtitle/worker-tokens 等） ===

// SubtitleWorkerTokenItem 列表 / 详情用（不含明文 token）。
type SubtitleWorkerTokenItem struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	TokenPrefix string `json:"tokenPrefix"`
	// 旧字段：不区分能力时的总并发上限兜底（前端可继续显示）
	MaxConcurrency int `json:"maxConcurrency"`
	// v2 新增：分能力维度的并发上限（admin 面板可分别调）
	MaxAudioConcurrency    int `json:"maxAudioConcurrency"`
	MaxSubtitleConcurrency int `json:"maxSubtitleConcurrency"`
	// CurrentRunning 该 token 名下所有 worker 当前持有的 RUNNING 任务数（admin 列表实时统计）
	CurrentRunning int `json:"currentRunning"`
	// CurrentAudioRunning / CurrentSubtitleRunning 分维度统计，便于 admin 看出限流来自哪一侧
	CurrentAudioRunning    int        `json:"currentAudioRunning"`
	CurrentSubtitleRunning int        `json:"currentSubtitleRunning"`
	CreatedAt              time.Time  `json:"createdAt"`
	LastUsedAt             *time.Time `json:"lastUsedAt,omitempty"`
	RevokedAt              *time.Time `json:"revokedAt,omitempty"`
}

// SubtitleWorkerTokenCreateRequest admin 生成 token。
type SubtitleWorkerTokenCreateRequest struct {
	Name           string `json:"name" binding:"required,min=1,max=64"`
	MaxConcurrency int    `json:"maxConcurrency,omitempty"` // 0 时按服务端默认 1
	// 缺省时按服务端默认（audio=2 / subtitle=1）
	MaxAudioConcurrency    int `json:"maxAudioConcurrency,omitempty"`
	MaxSubtitleConcurrency int `json:"maxSubtitleConcurrency,omitempty"`
}

// SubtitleWorkerTokenUpdateRequest admin 编辑 token。所有字段都用指针，nil 表示不修改。
type SubtitleWorkerTokenUpdateRequest struct {
	MaxConcurrency         *int `json:"maxConcurrency,omitempty"`
	MaxAudioConcurrency    *int `json:"maxAudioConcurrency,omitempty"`
	MaxSubtitleConcurrency *int `json:"maxSubtitleConcurrency,omitempty"`
}

// SubtitleWorkerTokenCreateResponse 仅本次返回明文 token。
type SubtitleWorkerTokenCreateResponse struct {
	Token  string                  `json:"token"` // 形如 "mwt_xxx"
	Record SubtitleWorkerTokenItem `json:"record"`
}

// SubtitleWorkerItem 在线 worker 列表项。
type SubtitleWorkerItem struct {
	ID            string    `json:"id"`
	Name          string    `json:"name"`
	Version       string    `json:"version,omitempty"`
	GPU           string    `json:"gpu,omitempty"`
	CurrentJobID  string    `json:"currentJobId,omitempty"`
	LastSeenAt    time.Time `json:"lastSeenAt"`
	RegisteredAt  time.Time `json:"registeredAt"`
	CompletedJobs int64     `json:"completedJobs"`
	FailedJobs    int64     `json:"failedJobs"`
	Online        bool      `json:"online"` // last_seen_at 在 staleThreshold 内
	// v2 新增：worker 自报的 capability 字符串数组（admin UI 渲染 badge）
	Capabilities []string `json:"capabilities"`
}

// IntermediateAudioStats 服务端中转池监控用（admin 面板）。
//
// FileCount / TotalBytes 来自实际扫盘；OldestUploadedAt 取 audio_uploaded 任务里
// audio_uploaded_at 最早的一条；QuotaBytes 来自 cfg（默认 50 GiB）。
type IntermediateAudioStats struct {
	FileCount        int        `json:"fileCount"`
	TotalBytes       int64      `json:"totalBytes"`
	OldestUploadedAt *time.Time `json:"oldestUploadedAt,omitempty"`
	QuotaBytes       int64      `json:"quotaBytes"`
}

// AdminAlert admin 顶部告警条用。
type AdminAlert struct {
	Level   string `json:"level"` // "info" / "warn" / "error"
	Message string `json:"message"`
}
