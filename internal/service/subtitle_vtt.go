// subtitle_vtt.go 负责字幕正文的存取。
//
// 背景：主备双节点用 LiteFS 复制 SQLite（见 docs/ha-failover.md），它只复制数据库
// 文件，磁盘上的 .vtt 不在复制范围内。故障切换后备节点能看到任务状态却拿不到字幕
// 内容，字幕功能等于整体失效。因此正文改存数据库，随复制天然同步。
//
// 迁移策略是"写双份、读优先库"：
//   - 写：同时写数据库与磁盘文件，磁盘文件继续服务既有的备份/恢复流程
//   - 读：优先数据库，未命中回退磁盘（尚未回填的历史数据）
//   - 回填：启动时把历史 .vtt 灌进数据库，幂等
//
// 等两台机器都跑上新版本、回填完成并做过一轮备份验证后，才可以考虑摘掉磁盘那一份。
package service

import (
	"errors"
	"fmt"
	"log"
	"os"
	"path/filepath"

	"gorm.io/gorm"

	"github.com/hor1zon777/m3u8-preview-go/internal/model"
)

// storeVTT 把字幕正文写入数据库（存在则更新）。
//
// 用"先 UPDATE 命中行、不存在再 INSERT"而非 ON CONFLICT：
// 与本仓库既有的 upsert 写法保持一致（见 subtitle_settings.go 的 upsertSetting）。
func (s *SubtitleService) storeVTT(mediaID string, body []byte) error {
	res := s.db.Model(&model.SubtitleVTT{}).
		Where("media_id = ?", mediaID).
		Update("content", string(body))
	if res.Error != nil {
		return fmt.Errorf("update vtt content: %w", res.Error)
	}
	if res.RowsAffected > 0 {
		return nil
	}
	if err := s.db.Create(&model.SubtitleVTT{MediaID: mediaID, Content: string(body)}).Error; err != nil {
		return fmt.Errorf("insert vtt content: %w", err)
	}
	return nil
}

// loadVTT 读取字幕正文：优先数据库，未命中时回退磁盘文件。
//
// 回退路径同时兼顾两种情况：尚未回填的历史任务，以及从旧版本备份恢复出来的数据。
func (s *SubtitleService) loadVTT(job *model.SubtitleJob) ([]byte, error) {
	var row model.SubtitleVTT
	err := s.db.Where("media_id = ?", job.MediaID).Take(&row).Error
	if err == nil && row.Content != "" {
		return []byte(row.Content), nil
	}
	if err != nil && !errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, fmt.Errorf("query vtt content: %w", err)
	}

	if job.VttPath == "" {
		return nil, os.ErrNotExist
	}
	abs := filepath.Join(s.snap().SubtitlesDir, job.VttPath)
	body, err := os.ReadFile(abs)
	if err != nil {
		return nil, err
	}
	return body, nil
}

// dropVTT 删除字幕正文（数据库行 + 磁盘文件）。
func (s *SubtitleService) dropVTT(job *model.SubtitleJob) {
	if err := s.db.Where("media_id = ?", job.MediaID).Delete(&model.SubtitleVTT{}).Error; err != nil {
		log.Printf("[subtitle] 删除字幕正文 media=%s 失败: %v", job.MediaID, err)
	}
	s.deleteVTTFile(job)
}

// backfillVTT 把历史磁盘 .vtt 回填进数据库。
//
// 幂等：只处理"任务已完成且有 vtt_path、但数据库里没有正文行"的记录。
// 只能由 primary 执行（replica 的库是只读的）。
func (s *SubtitleService) backfillVTT() {
	if !s.canWrite() {
		return
	}

	var jobs []model.SubtitleJob
	err := s.db.
		Where("status = ? AND vtt_path <> ''", model.SubtitleStatusDone).
		Where("media_id NOT IN (?)", s.db.Model(&model.SubtitleVTT{}).Select("media_id")).
		Find(&jobs).Error
	if err != nil {
		log.Printf("[subtitle] 回填字幕正文：查询待回填任务失败: %v", err)
		return
	}
	if len(jobs) == 0 {
		return
	}

	dir := s.snap().SubtitlesDir
	var done, missing int
	for i := range jobs {
		job := &jobs[i]
		body, err := os.ReadFile(filepath.Join(dir, job.VttPath))
		if err != nil {
			// 文件丢失不是致命错误：任务状态仍是 DONE，用户请求时会拿到 404，
			// 与回填前的行为一致。这里只统计，不改任务状态。
			missing++
			continue
		}
		if err := s.storeVTT(job.MediaID, body); err != nil {
			log.Printf("[subtitle] 回填字幕正文 media=%s 失败: %v", job.MediaID, err)
			continue
		}
		done++
	}
	log.Printf("[subtitle] 回填字幕正文完成: 成功 %d 条，文件缺失 %d 条，共扫描 %d 条", done, missing, len(jobs))
}
