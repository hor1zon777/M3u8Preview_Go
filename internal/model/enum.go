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
)
