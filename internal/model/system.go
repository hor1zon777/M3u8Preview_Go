// Package model
// system.go 对齐 SystemSetting 与 ImportLog。
package model

import (
	"time"

	"github.com/google/uuid"
	"gorm.io/gorm"
)

// SystemSetting 用 key 作为主键，简单的键值存储。value 统一用字符串，布尔以 "true"/"false" 文本保存。
type SystemSetting struct {
	Key       string    `gorm:"primaryKey;type:text" json:"key"`
	Value     string    `gorm:"type:text;not null" json:"value"`
	UpdatedAt time.Time `gorm:"autoUpdateTime" json:"updatedAt"`
}

func (SystemSetting) TableName() string { return "system_settings" }

// ImportLog 记录每次批量导入的汇总与错误。
// Errors 存储 JSON 字符串（[{row, title?, error}]）。
type ImportLog struct {
	ID           string    `gorm:"primaryKey;type:text" json:"id"`
	UserID       string    `gorm:"column:user_id;type:text;not null;index:idx_implog_user" json:"userId"`
	Format       string    `gorm:"type:text;not null" json:"format"`
	FileName     *string   `gorm:"column:file_name;type:text" json:"fileName,omitempty"`
	TotalCount   int       `gorm:"column:total_count;type:integer;not null;default:0" json:"totalCount"`
	SuccessCount int       `gorm:"column:success_count;type:integer;not null;default:0" json:"successCount"`
	FailedCount  int       `gorm:"column:failed_count;type:integer;not null;default:0" json:"failedCount"`
	Status       string    `gorm:"type:text;not null;default:PENDING" json:"status"`
	Errors       *string   `gorm:"type:text" json:"errors,omitempty"`
	CreatedAt    time.Time `gorm:"autoCreateTime;index:idx_implog_created" json:"createdAt"`

	User User `gorm:"foreignKey:UserID;constraint:OnDelete:CASCADE" json:"-"`
}

func (ImportLog) TableName() string { return "import_logs" }

func (i *ImportLog) BeforeCreate(tx *gorm.DB) error {
	if i.ID == "" {
		i.ID = uuid.NewString()
	}
	return nil
}
