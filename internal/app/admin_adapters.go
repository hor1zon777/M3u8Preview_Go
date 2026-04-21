// Package app
// admin_adapters.go 把 app 内部需要的小适配器集中在一起。
package app

import (
	"log"
	"os"
	"path/filepath"

	"gorm.io/gorm"

	"github.com/hor1zon777/m3u8-preview-go/internal/handler"
	"github.com/hor1zon777/m3u8-preview-go/internal/model"
)

// posterStatsDB 实现 handler.DashboardDB 接口，按 poster_url 前缀是否以 http(s) 开头区分外部/本地。
// uploadsDir 用来扫描本地封面目录汇总字节数（facing /admin/posters/stats）。
type posterStatsDB struct {
	db         *gorm.DB
	uploadsDir string
}

func newPosterStatsDB(db *gorm.DB, uploadsDir string) *posterStatsDB {
	return &posterStatsDB{db: db, uploadsDir: uploadsDir}
}

// CountExternalPosters 返回 (external, local, total)。保留向后兼容。
func (p *posterStatsDB) CountExternalPosters() (int64, int64, int64) {
	s := p.PosterStats()
	return s.External, s.Local, s.Total
}

// PosterStats 返回前端 /admin/posters/stats 需要的全部字段。
// - total/external/local 来源于 media 表对 poster_url 的前缀判断
// - missing 为 total 减去 external 与 local，等价于 poster_url IS NULL OR '' OR 其它
// - totalSizeBytes 扫描 uploadsDir/posters 汇总本地封面字节数（目录不存在或不可读时返回 0）
//
// DB 查询错误通过 log 打印，不向上冒泡（保持向后兼容的返回签名），
// 否则前端看到 0 会误以为"无封面问题"，实际是查询失败。
func (p *posterStatsDB) PosterStats() handler.PosterStats {
	var external, local, total int64
	if err := p.db.Model(&model.Media{}).Count(&total).Error; err != nil {
		log.Printf("[posterStats] count total failed: %v", err)
	}
	if err := p.db.Model(&model.Media{}).
		Where("poster_url LIKE 'http://%' OR poster_url LIKE 'https://%'").
		Count(&external).Error; err != nil {
		log.Printf("[posterStats] count external failed: %v", err)
	}
	if err := p.db.Model(&model.Media{}).
		Where("poster_url IS NOT NULL AND poster_url <> '' AND poster_url NOT LIKE 'http%'").
		Count(&local).Error; err != nil {
		log.Printf("[posterStats] count local failed: %v", err)
	}
	missing := total - external - local
	if missing < 0 {
		missing = 0
	}
	return handler.PosterStats{
		Total:          total,
		External:       external,
		Local:          local,
		Missing:        missing,
		TotalSizeBytes: sumDirSize(filepath.Join(p.uploadsDir, "posters")),
	}
}

// sumDirSize 递归汇总目录中所有常规文件大小；目录不存在或出错返回 0，保证 endpoint 不因文件系统异常 5xx。
func sumDirSize(dir string) int64 {
	var total int64
	_ = filepath.WalkDir(dir, func(_ string, d os.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if d.IsDir() {
			return nil
		}
		info, err := d.Info()
		if err != nil {
			return nil
		}
		if info.Mode().IsRegular() {
			total += info.Size()
		}
		return nil
	})
	return total
}
