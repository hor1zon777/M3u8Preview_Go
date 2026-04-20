// Package sse
// progress.go 对齐 TS 版 ExportProgress / BackupProgress 的 phase 常量与字段语义。
// 前端 BackupSection 按这些字段驱动进度条；字段名与枚举必须与 TS 完全一致。
package sse

// ExportProgress 备份导出阶段上报。
type ExportProgress struct {
	Phase      string `json:"phase"`      // db | files | finalize | complete | error
	Progress   int    `json:"progress"`   // 0-100
	Message    string `json:"message,omitempty"`
	Current    int    `json:"current,omitempty"`
	Total      int    `json:"total,omitempty"`
	DownloadID string `json:"downloadId,omitempty"` // complete 阶段回传
	Error      string `json:"error,omitempty"`
}

// BackupProgress 备份恢复（导入）阶段上报。
type BackupProgress struct {
	Phase     string `json:"phase"`     // upload | parse | db | delete | write | files | finalize | complete | error
	Progress  int    `json:"progress"`  // 0-100
	Message   string `json:"message,omitempty"`
	Current   int    `json:"current,omitempty"`
	Total     int    `json:"total,omitempty"`
	Error     string `json:"error,omitempty"`
	RestoreID string `json:"restoreId,omitempty"`
}

// 导出阶段常量
const (
	ExportPhaseDB       = "db"
	ExportPhaseFiles    = "files"
	ExportPhaseFinalize = "finalize"
	ExportPhaseComplete = "complete"
	ExportPhaseError    = "error"
)

// 恢复阶段常量
const (
	BackupPhaseUpload   = "upload"
	BackupPhaseParse    = "parse"
	BackupPhaseDB       = "db"
	BackupPhaseDelete   = "delete"
	BackupPhaseWrite    = "write"
	BackupPhaseFiles    = "files"
	BackupPhaseFinalize = "finalize"
	BackupPhaseComplete = "complete"
	BackupPhaseError    = "error"
)
