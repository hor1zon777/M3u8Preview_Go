// Package db
// migrate.go 负责 GORM AutoMigrate 与系统设置默认值补全。
package db

import (
	"fmt"

	"gorm.io/gorm"

	"github.com/hor1zon777/m3u8-preview-go/internal/model"
)

// 全部模型清单；AutoMigrate 按顺序建表，外键依赖在关联里用 constraint:OnDelete 声明。
func allModels() []any {
	return []any{
		&model.User{},
		&model.RefreshToken{},
		&model.Category{},
		&model.Tag{},
		&model.Media{},
		&model.MediaTag{},
		&model.Favorite{},
		&model.Playlist{},
		&model.PlaylistItem{},
		&model.WatchHistory{},
		&model.ImportLog{},
		&model.SystemSetting{},
		&model.LoginRecord{},
	}
}

// AutoMigrate 建表并补全索引。
// GORM 的 AutoMigrate 只增不减：已有表的列变动要自己写 SQL 或用 Migrator。
// 对新部署足够；从 R 版历史库迁移请参考 README 中的操作步骤。
func AutoMigrate(db *gorm.DB) error {
	if err := db.AutoMigrate(allModels()...); err != nil {
		return fmt.Errorf("automigrate: %w", err)
	}
	return nil
}

// EnsureDefaultSettings 对齐 services/settingsMigration.ts：
// 不覆盖已有值，只补全缺失的 key。启动时调用。
func EnsureDefaultSettings(db *gorm.DB) error {
	defaults := map[string]string{
		model.SettingSiteName:               "M3u8 Preview",
		model.SettingAllowRegistration:      "true",
		model.SettingEnableRateLimit:        "true",
		model.SettingProxyAllowedExtensions: ".m3u8,.ts,.m4s,.mp4,.aac,.key,.jpg,.jpeg,.png,.webp",
		model.SettingDownloadExternalPosters: "false",
		model.SettingEnableCaptcha:           "false",
		model.SettingCaptchaEndpoint:         "",
		model.SettingCaptchaSiteKey:          "",
		model.SettingCaptchaSecretKey:        "",
	}

	// 一次性查出已存在的 key，避免每条都走一次 SELECT
	var existing []string
	if err := db.Model(&model.SystemSetting{}).Pluck("key", &existing).Error; err != nil {
		return fmt.Errorf("list settings: %w", err)
	}
	existingSet := make(map[string]bool, len(existing))
	for _, k := range existing {
		existingSet[k] = true
	}

	missing := make([]model.SystemSetting, 0, len(defaults))
	for k, v := range defaults {
		if !existingSet[k] {
			missing = append(missing, model.SystemSetting{Key: k, Value: v})
		}
	}
	if len(missing) == 0 {
		return nil
	}
	if err := db.Create(&missing).Error; err != nil {
		return fmt.Errorf("insert default settings: %w", err)
	}
	return nil
}
