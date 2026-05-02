// Package model 汇总全部 GORM 实体定义。
// enum.go 存放跨模型使用的枚举常量，与 TS 版 shared/types.ts 保持字符串一致。
package model

// 用户角色
const (
	RoleUser  = "USER"
	RoleAdmin = "ADMIN"
)

// 媒体状态
const (
	MediaStatusActive   = "ACTIVE"
	MediaStatusInactive = "INACTIVE"
	MediaStatusError    = "ERROR"
)

// 导入格式
const (
	ImportFormatText  = "TEXT"
	ImportFormatCSV   = "CSV"
	ImportFormatExcel = "EXCEL"
	ImportFormatJSON  = "JSON"
)

// 导入状态
const (
	ImportStatusPending = "PENDING"
	ImportStatusSuccess = "SUCCESS"
	ImportStatusPartial = "PARTIAL"
	ImportStatusFailed  = "FAILED"
)

// 字幕任务状态
const (
	SubtitleStatusPending  = "PENDING"  // 已入队，未开始
	SubtitleStatusRunning  = "RUNNING"  // 正在处理（音频抽取/ASR/翻译/写文件）
	SubtitleStatusDone     = "DONE"     // 完成，VTT 已落盘
	SubtitleStatusFailed   = "FAILED"   // 失败，error_msg 含原因
	SubtitleStatusDisabled = "DISABLED" // 管理员禁用此 media 的字幕生成
)

// 字幕处理阶段（progress 推送用）
const (
	SubtitleStageQueued     = "queued"
	SubtitleStageExtracting = "extracting" // ffmpeg 抽音频
	SubtitleStageASR        = "asr"        // whisper.cpp 识别
	SubtitleStageTranslate  = "translate"  // LLM 翻译
	SubtitleStageWriting    = "writing"    // 写 VTT
	SubtitleStageDone       = "done"
)

// 系统设置 key（集中管理，避免拼写错误）
const (
	SettingSiteName               = "siteName"
	SettingAllowRegistration      = "allowRegistration"
	SettingEnableRateLimit        = "enableRateLimit"
	SettingProxyAllowedExtensions = "proxyAllowedExtensions"
	// 新建/更新媒体时，若 posterUrl 为外部 http(s)，是否同步下载到本地。
	// 默认 false：保留原 URL，避免首次入库同步阻塞。
	SettingDownloadExternalPosters = "downloadExternalPosters"

	SettingEnableCaptcha    = "enableCaptcha"
	SettingCaptchaEndpoint  = "captchaEndpoint"
	SettingCaptchaSiteKey   = "captchaSiteKey"
	SettingCaptchaSecretKey = "captchaSecretKey"
	// SettingCaptchaManifestPubKey 是 Portcullis Tier 2 manifest 签名的 Ed25519 公钥，base64(32B)。
	// 配置后前端强制校验 /sdk/manifest.json 的 X-Portcullis-Signature；缺失或失配直接拒绝加载 SDK。
	// 留空则跳过签名校验，行为与 Tier 1 一致（仅 SRI integrity 防篡改）。
	SettingCaptchaManifestPubKey = "captchaManifestPubKey"
)
