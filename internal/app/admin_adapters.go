// Package app
// admin_adapters.go 把 app 内部需要的小适配器集中在一起。
package app

import (
	"gorm.io/gorm"

	"github.com/hor1zon777/m3u8-preview-go/internal/model"
)

// posterStatsDB 实现 handler.DashboardDB 接口，按 poster_url 前缀是否以 http(s) 开头区分外部/本地。
type posterStatsDB struct{ db *gorm.DB }

func newPosterStatsDB(db *gorm.DB) *posterStatsDB { return &posterStatsDB{db: db} }

// CountExternalPosters 返回 (external, local, total)。
func (p *posterStatsDB) CountExternalPosters() (int64, int64, int64) {
	var external, local, total int64
	p.db.Model(&model.Media{}).Count(&total)
	p.db.Model(&model.Media{}).
		Where("poster_url LIKE 'http://%' OR poster_url LIKE 'https://%'").
		Count(&external)
	p.db.Model(&model.Media{}).
		Where("poster_url IS NOT NULL AND poster_url <> '' AND poster_url NOT LIKE 'http%'").
		Count(&local)
	return external, local, total
}
