// update.go 应用内自更新 API（/api/v1/admin/update/*）的请求与响应结构。
// 字段命名对齐 web/shared/src/types/index.ts 中的 UpdateStatus 等前端类型。
package dto

// UpdateStatusResponse 是 GET /admin/update/status 的响应。
type UpdateStatusResponse struct {
	CurrentVersion string `json:"currentVersion"`
	Commit         string `json:"commit,omitempty"`
	// Enabled 自更新是否可用；false 时 DisabledReason 说明原因。
	Enabled bool `json:"enabled"`
	// DisabledReason "dev-build"（开发构建）或 "env-disabled"（UPDATE_DISABLED=1）。
	DisabledReason string `json:"disabledReason,omitempty"`
	// State idle / checking / update-available / downloading / verifying / staged / restarting / failed
	State         string `json:"state"`
	LastCheckedAt string `json:"lastCheckedAt,omitempty"`
	// Latest 最近一次检查到的最新 Release；未检查过时为空。
	Latest *UpdateReleaseInfo `json:"latest,omitempty"`
	// Progress 下载进度；仅 downloading/verifying 阶段有意义。
	Progress *UpdateProgress `json:"progress,omitempty"`
	// Staged 已暂存待重启生效的版本。
	Staged *UpdateStagedInfo `json:"staged,omitempty"`
	// Error 最近一次失败信息；state=failed 或检查失败时非空。
	Error *UpdateErrorInfo `json:"error,omitempty"`
}

// UpdateReleaseInfo 最新 Release 摘要。
type UpdateReleaseInfo struct {
	Version     string `json:"version"`
	Notes       string `json:"notes,omitempty"`
	PublishedAt string `json:"publishedAt,omitempty"`
	AssetSize   int64  `json:"assetSize"`
}

// UpdateProgress 下载进度（字节）。
type UpdateProgress struct {
	DownloadedBytes int64 `json:"downloadedBytes"`
	TotalBytes      int64 `json:"totalBytes"`
}

// UpdateStagedInfo 已暂存版本信息。
type UpdateStagedInfo struct {
	Version  string `json:"version"`
	StagedAt string `json:"stagedAt"`
}

// UpdateErrorInfo 失败信息（Code 机器可读）。
type UpdateErrorInfo struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

// UpdateApplyRequest 是 POST /admin/update/apply 的请求体。
// Version 必须等于最近一次检查到的最新版本——防止"检查后又发了新版"的 TOCTOU。
type UpdateApplyRequest struct {
	Version string `json:"version" binding:"required"`
}
