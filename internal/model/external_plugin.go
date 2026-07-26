// external_plugin.go 声明式外部插件（插件中心导入的 manifest）。
//
// "外部插件"不含任何可执行代码——它是一份描述外部服务集成的声明
// （名称/图标/说明，可选健康检查地址），由管理员在插件中心上传 manifest.json
// 导入，与编译期内置插件（如字幕 worker）共用同一套插件卡片 UI 与启停 API。
package model

import "time"

// ExternalPlugin 一条已导入的外部插件声明。
type ExternalPlugin struct {
	// ID 即 manifest.id（kebab-case，全局唯一，与内置插件共用命名空间）。
	ID          string `gorm:"primaryKey;type:text" json:"id"`
	Name        string `gorm:"type:text;not null" json:"name"`
	Description string `gorm:"type:text;not null;default:''" json:"description"`
	Version     string `gorm:"type:text;not null" json:"version"`
	// Icon lucide 图标名提示；前端白名单映射，未知名称兜底 Puzzle。
	Icon     string `gorm:"type:text;not null;default:''" json:"icon"`
	Category string `gorm:"type:text;not null;default:''" json:"category"`
	// HealthURL 可选：外部服务健康检查地址，插件卡片按它显示健康状态。
	HealthURL *string `gorm:"column:health_url;type:text" json:"healthUrl,omitempty"`
	// Homepage 可选：仅展示用外链，服务端不请求。
	Homepage *string `gorm:"type:text" json:"homepage,omitempty"`
	Enabled  bool    `gorm:"not null;default:1" json:"enabled"`
	// RawManifest 导入时的 manifest 原文存档（排障与未来 schema 升级用）。
	RawManifest string `gorm:"column:raw_manifest;type:text;not null" json:"-"`
	CreatedAt   time.Time
	UpdatedAt   time.Time
}

// TableName 显式表名。
func (ExternalPlugin) TableName() string { return "external_plugins" }
