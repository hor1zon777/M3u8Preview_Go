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

// SubtitleSettingsResponse admin 面板展示当前配置（敏感字段脱敏）。
type SubtitleSettingsResponse struct {
	Enabled          bool   `json:"enabled"`
	AutoGenerate     bool   `json:"autoGenerate"`
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
type WorkerRegisterRequest struct {
	WorkerID string `json:"workerId" binding:"required"`
	Name     string `json:"name" binding:"required"`
	Version  string `json:"version,omitempty"`
	GPU      string `json:"gpu,omitempty"`
}

// WorkerRegisterResponse 注册成功返回值。
type WorkerRegisterResponse struct {
	WorkerID             string `json:"workerId"`
	ServerTime           int64  `json:"serverTime"`            // unix ms，便于 worker 校时
	WorkerStaleThreshold int64  `json:"workerStaleThreshold"`  // 服务端容忍的心跳间隔（秒），worker 应 < 此值上报心跳
}

// WorkerClaimRequest 认领任务请求。
type WorkerClaimRequest struct {
	WorkerID string `json:"workerId" binding:"required"`
}

// WorkerClaimedJob 服务端原子认领后返回给 worker 的任务信息。
// 仅在有 PENDING 时返回；无任务时 handler 返回 204 No Content。
type WorkerClaimedJob struct {
	JobID      string `json:"jobId"`
	MediaID    string `json:"mediaId"`
	MediaTitle string `json:"mediaTitle,omitempty"` // worker UI 显示用
	M3u8URL    string `json:"m3u8Url"`
	SourceLang string `json:"sourceLang"`
	TargetLang string `json:"targetLang"`
	// Headers 是下载 m3u8 / 分片时应携带的 HTTP 头（域名相关的 Referer / User-Agent 等）。
	// worker 端把这些转成下载器（N_m3u8DL-RE）的 --header 参数。
	// 服务端代理播放时也是同样的注入逻辑，避免 worker 直连源站 403。
	Headers map[string]string `json:"headers,omitempty"`
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

// WorkerFailRequest 失败上报。
type WorkerFailRequest struct {
	WorkerID string `json:"workerId" binding:"required"`
	ErrorMsg string `json:"errorMsg"`
}

// === Admin worker 管理（/api/v1/admin/subtitle/worker-tokens 等） ===

// SubtitleWorkerTokenItem 列表 / 详情用（不含明文 token）。
type SubtitleWorkerTokenItem struct {
	ID             string     `json:"id"`
	Name           string     `json:"name"`
	TokenPrefix    string     `json:"tokenPrefix"`
	MaxConcurrency int        `json:"maxConcurrency"`
	// CurrentRunning 该 token 名下所有 worker 当前持有的 RUNNING 任务数（admin 列表实时统计）
	CurrentRunning int        `json:"currentRunning"`
	CreatedAt      time.Time  `json:"createdAt"`
	LastUsedAt     *time.Time `json:"lastUsedAt,omitempty"`
	RevokedAt      *time.Time `json:"revokedAt,omitempty"`
}

// SubtitleWorkerTokenCreateRequest admin 生成 token。
type SubtitleWorkerTokenCreateRequest struct {
	Name           string `json:"name" binding:"required,min=1,max=64"`
	MaxConcurrency int    `json:"maxConcurrency,omitempty"` // 0 时按服务端默认 1
}

// SubtitleWorkerTokenUpdateRequest admin 编辑 token（目前仅支持改并发上限）。
type SubtitleWorkerTokenUpdateRequest struct {
	MaxConcurrency *int `json:"maxConcurrency,omitempty"`
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
}
