// Package service
// favorite.go 对齐 favoriteService.ts：toggle / check / list by user。
package service

import (
	"errors"
	"net/http"

	"gorm.io/gorm"

	"github.com/hor1zon777/m3u8-preview-go/internal/dto"
	"github.com/hor1zon777/m3u8-preview-go/internal/middleware"
	"github.com/hor1zon777/m3u8-preview-go/internal/model"
	"github.com/hor1zon777/m3u8-preview-go/internal/util"
)

// FavoriteService 封装收藏操作。
type FavoriteService struct{ db *gorm.DB }

// NewFavoriteService 构造。
func NewFavoriteService(db *gorm.DB) *FavoriteService { return &FavoriteService{db: db} }

// Toggle 切换当前用户对 mediaId 的收藏状态，返回切换后是否处于收藏中。
// 媒体不存在返回 404。
func (s *FavoriteService) Toggle(userID, mediaID string) (bool, error) {
	var m model.Media
	if err := s.db.Take(&m, "id = ?", mediaID).Error; err != nil {
		return false, middleware.NewAppError(http.StatusNotFound, "Media not found")
	}
	var f model.Favorite
	err := s.db.Where("user_id = ? AND media_id = ?", userID, mediaID).Take(&f).Error
	if err == nil {
		if err := s.db.Delete(&f).Error; err != nil {
			return false, middleware.WrapAppError(http.StatusInternalServerError, "取消收藏失败", err)
		}
		return false, nil
	}
	if !errors.Is(err, gorm.ErrRecordNotFound) {
		return false, middleware.WrapAppError(http.StatusInternalServerError, "查询失败", err)
	}
	fav := model.Favorite{UserID: userID, MediaID: mediaID}
	if err := s.db.Create(&fav).Error; err != nil {
		return false, middleware.WrapAppError(http.StatusInternalServerError, "收藏失败", err)
	}
	return true, nil
}

// Check 返回当前用户是否已收藏指定媒体。
func (s *FavoriteService) Check(userID, mediaID string) (bool, error) {
	var count int64
	err := s.db.Model(&model.Favorite{}).
		Where("user_id = ? AND media_id = ?", userID, mediaID).
		Count(&count).Error
	if err != nil {
		return false, middleware.WrapAppError(http.StatusInternalServerError, "查询失败", err)
	}
	return count > 0, nil
}

// List 当前用户收藏列表，分页；按 createdAt DESC。附带 Media 预加载。
func (s *FavoriteService) List(userID string, page, limit int) (items []dto.FavoriteResponse, total int64, err error) {
	page, limit = util.SafePagination(page, limit, 100)
	base := s.db.Model(&model.Favorite{}).Where("user_id = ?", userID)
	if err = base.Count(&total).Error; err != nil {
		return nil, 0, middleware.WrapAppError(http.StatusInternalServerError, "统计失败", err)
	}
	var rows []model.Favorite
	err = base.Preload("Media").Preload("Media.Category").
		Order("created_at DESC").
		Offset(util.Offset(page, limit)).Limit(limit).
		Find(&rows).Error
	if err != nil {
		return nil, 0, middleware.WrapAppError(http.StatusInternalServerError, "查询失败", err)
	}
	out := make([]dto.FavoriteResponse, 0, len(rows))
	for i := range rows {
		m := serializeMedia(&rows[i].Media)
		out = append(out, dto.FavoriteResponse{
			ID:        rows[i].ID,
			MediaID:   rows[i].MediaID,
			CreatedAt: util.FormatISO(rows[i].CreatedAt),
			Media:     &m,
		})
	}
	return out, total, nil
}
