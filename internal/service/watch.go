// Package service
// watch.go 对齐 watchHistoryService.ts：progress 更新 / 列表 / 继续观看 / progress-map / 删除。
package service

import (
	"errors"
	"net/http"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"

	"github.com/hor1zon777/m3u8-preview-go/internal/dto"
	"github.com/hor1zon777/m3u8-preview-go/internal/middleware"
	"github.com/hor1zon777/m3u8-preview-go/internal/model"
	"github.com/hor1zon777/m3u8-preview-go/internal/util"
)

// WatchHistoryService 封装观看进度相关操作。
type WatchHistoryService struct{ db *gorm.DB }

// NewWatchHistoryService 构造。
func NewWatchHistoryService(db *gorm.DB) *WatchHistoryService {
	return &WatchHistoryService{db: db}
}

// UpdateProgress upsert 当前用户对某媒体的进度。
// (user_id, media_id) 唯一约束保证每个用户对每个媒体只有一条，重复调用只更新数值。
//
// 服务端权威计算 percentage / completed：
//   - 老前端只上报 {progress, duration}（不带 percentage/completed），若直接落库 percentage 永远是 0
//   - 即使新前端上报了，恶意客户端也可能传不一致的值（例如 progress=10s 但 percentage=99%）
//   - 统一在服务端按 progress/duration 计算，规范化所有记录条进度展示
func (s *WatchHistoryService) UpdateProgress(userID string, req dto.WatchProgressRequest) (*dto.WatchHistoryResponse, error) {
	var m model.Media
	if err := s.db.Take(&m, "id = ?", req.MediaID).Error; err != nil {
		return nil, middleware.NewAppError(http.StatusNotFound, "Media not found")
	}

	percentage := req.Percentage
	if req.Duration > 0 {
		percentage = req.Progress / req.Duration * 100
		if percentage < 0 {
			percentage = 0
		} else if percentage > 100 {
			percentage = 100
		}
	}
	completed := req.Completed || percentage >= 95

	wh := model.WatchHistory{
		UserID:     userID,
		MediaID:    req.MediaID,
		Progress:   req.Progress,
		Duration:   req.Duration,
		Percentage: percentage,
		Completed:  completed,
	}
	// ON CONFLICT(user_id, media_id) DO UPDATE
	err := s.db.Clauses(clause.OnConflict{
		Columns: []clause.Column{{Name: "user_id"}, {Name: "media_id"}},
		DoUpdates: clause.AssignmentColumns([]string{
			"progress", "duration", "percentage", "completed", "updated_at",
		}),
	}).Create(&wh).Error
	if err != nil {
		return nil, middleware.WrapAppError(http.StatusInternalServerError, "写入失败", err)
	}
	return s.GetByMedia(userID, req.MediaID)
}

// List 当前用户观看历史，按 updated_at DESC 分页。
func (s *WatchHistoryService) List(userID string, page, limit int) (items []dto.WatchHistoryResponse, total int64, err error) {
	page, limit = util.SafePagination(page, limit, 100)
	base := s.db.Model(&model.WatchHistory{}).Where("user_id = ?", userID)
	if err = base.Count(&total).Error; err != nil {
		return nil, 0, middleware.WrapAppError(http.StatusInternalServerError, "统计失败", err)
	}
	var rows []model.WatchHistory
	err = base.Preload("Media").Preload("Media.Category").
		Order("updated_at DESC").
		Offset(util.Offset(page, limit)).Limit(limit).
		Find(&rows).Error
	if err != nil {
		return nil, 0, middleware.WrapAppError(http.StatusInternalServerError, "查询失败", err)
	}
	out := make([]dto.WatchHistoryResponse, 0, len(rows))
	for i := range rows {
		out = append(out, serializeWatchHistory(&rows[i], true))
	}
	return out, total, nil
}

// Continue "继续观看" — 未完成的观看记录，按 updated_at DESC 取 Top N（默认 20）。
func (s *WatchHistoryService) Continue(userID string, limit int) ([]dto.WatchHistoryResponse, error) {
	limit = clampInt(limit, 1, 50)
	var rows []model.WatchHistory
	err := s.db.Where("user_id = ? AND completed = ? AND percentage < 95", userID, false).
		Preload("Media").Preload("Media.Category").
		Order("updated_at DESC").
		Limit(limit).
		Find(&rows).Error
	if err != nil {
		return nil, middleware.WrapAppError(http.StatusInternalServerError, "查询失败", err)
	}
	out := make([]dto.WatchHistoryResponse, 0, len(rows))
	for i := range rows {
		out = append(out, serializeWatchHistory(&rows[i], true))
	}
	return out, nil
}

// GetByMedia 返回当前用户对某媒体的单条进度；不存在返回 404。
func (s *WatchHistoryService) GetByMedia(userID, mediaID string) (*dto.WatchHistoryResponse, error) {
	var wh model.WatchHistory
	err := s.db.Where("user_id = ? AND media_id = ?", userID, mediaID).
		Preload("Media").Preload("Media.Category").
		Take(&wh).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, middleware.NewAppError(http.StatusNotFound, "watch history not found")
	}
	if err != nil {
		return nil, middleware.WrapAppError(http.StatusInternalServerError, "查询失败", err)
	}
	r := serializeWatchHistory(&wh, true)
	return &r, nil
}

// ProgressMap 批量查询当前用户对多个 mediaId 的进度。
// 前端首页渲染 mediaCard 时一次性拿到已观进度，避免 N+1。
func (s *WatchHistoryService) ProgressMap(userID string, mediaIDs []string) (map[string]dto.WatchHistoryResponse, error) {
	if len(mediaIDs) == 0 {
		return map[string]dto.WatchHistoryResponse{}, nil
	}
	var rows []model.WatchHistory
	err := s.db.Where("user_id = ? AND media_id IN ?", userID, mediaIDs).Find(&rows).Error
	if err != nil {
		return nil, middleware.WrapAppError(http.StatusInternalServerError, "查询失败", err)
	}
	out := make(map[string]dto.WatchHistoryResponse, len(rows))
	for i := range rows {
		out[rows[i].MediaID] = serializeWatchHistory(&rows[i], false)
	}
	return out, nil
}

// Clear 清空当前用户的全部观看记录。
func (s *WatchHistoryService) Clear(userID string) error {
	if err := s.db.Where("user_id = ?", userID).Delete(&model.WatchHistory{}).Error; err != nil {
		return middleware.WrapAppError(http.StatusInternalServerError, "清空失败", err)
	}
	return nil
}

// DeleteOne 删除某一条记录，只能删自己的。
func (s *WatchHistoryService) DeleteOne(userID, id string) error {
	tx := s.db.Where("id = ? AND user_id = ?", id, userID).Delete(&model.WatchHistory{})
	if tx.Error != nil {
		return middleware.WrapAppError(http.StatusInternalServerError, "删除失败", tx.Error)
	}
	if tx.RowsAffected == 0 {
		return middleware.NewAppError(http.StatusNotFound, "record not found")
	}
	return nil
}

func serializeWatchHistory(w *model.WatchHistory, withMedia bool) dto.WatchHistoryResponse {
	r := dto.WatchHistoryResponse{
		ID:         w.ID,
		UserID:     w.UserID,
		MediaID:    w.MediaID,
		Progress:   w.Progress,
		Duration:   w.Duration,
		Percentage: w.Percentage,
		Completed:  w.Completed,
		CreatedAt:  util.FormatISO(w.CreatedAt),
		UpdatedAt:  util.FormatISO(w.UpdatedAt),
	}
	if withMedia && w.Media.ID != "" {
		m := serializeMedia(&w.Media)
		r.Media = &m
	}
	return r
}
