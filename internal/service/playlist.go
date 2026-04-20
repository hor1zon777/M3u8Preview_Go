// Package service
// playlist.go 对齐 playlistService.ts。
// 所有写操作要区分 isOwner vs isAdmin：
//   - 创建 / 更新 / 删除 playlist 本身：ADMIN（route 层强制）
//   - add/remove/reorder items：仅 owner 可操作（service 内再校验）
// 读：GetPublic / GetOwned / GetByID（owner 或 public 可见）
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

// PlaylistService 封装播放列表操作。
type PlaylistService struct{ db *gorm.DB }

// NewPlaylistService 构造。
func NewPlaylistService(db *gorm.DB) *PlaylistService { return &PlaylistService{db: db} }

// GetPublic /playlists/public：公开列表，分页。
func (s *PlaylistService) GetPublic(page, limit int) ([]dto.PlaylistResponse, int64, error) {
	page, limit = util.SafePagination(page, limit, 100)
	base := s.db.Model(&model.Playlist{}).Where("is_public = ?", true)
	var total int64
	if err := base.Count(&total).Error; err != nil {
		return nil, 0, middleware.WrapAppError(http.StatusInternalServerError, "统计失败", err)
	}
	var rows []model.Playlist
	if err := base.Order("updated_at DESC").Offset(util.Offset(page, limit)).Limit(limit).Find(&rows).Error; err != nil {
		return nil, 0, middleware.WrapAppError(http.StatusInternalServerError, "查询失败", err)
	}
	return s.buildList(rows), total, nil
}

// GetOwned /playlists：当前用户拥有的播放列表。
func (s *PlaylistService) GetOwned(userID string, page, limit int) ([]dto.PlaylistResponse, int64, error) {
	page, limit = util.SafePagination(page, limit, 100)
	base := s.db.Model(&model.Playlist{}).Where("user_id = ?", userID)
	var total int64
	if err := base.Count(&total).Error; err != nil {
		return nil, 0, middleware.WrapAppError(http.StatusInternalServerError, "统计失败", err)
	}
	var rows []model.Playlist
	if err := base.Order("updated_at DESC").Offset(util.Offset(page, limit)).Limit(limit).Find(&rows).Error; err != nil {
		return nil, 0, middleware.WrapAppError(http.StatusInternalServerError, "查询失败", err)
	}
	return s.buildList(rows), total, nil
}

// GetByID 取单个播放列表。非 owner 且非 public → 403。
func (s *PlaylistService) GetByID(id, viewerID string) (*dto.PlaylistResponse, error) {
	var p model.Playlist
	if err := s.db.Take(&p, "id = ?", id).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, middleware.NewAppError(http.StatusNotFound, "Playlist not found")
		}
		return nil, middleware.WrapAppError(http.StatusInternalServerError, "查询失败", err)
	}
	if !p.IsPublic && p.UserID != viewerID {
		return nil, middleware.NewAppError(http.StatusForbidden, "无权访问")
	}
	var count int64
	s.db.Model(&model.PlaylistItem{}).Where("playlist_id = ?", p.ID).Count(&count)
	r := serializePlaylist(&p, count)
	return &r, nil
}

// GetItems 返回 playlist 的 items 列表；权限同 GetByID。
func (s *PlaylistService) GetItems(playlistID, viewerID string) ([]dto.PlaylistItemResponse, error) {
	var p model.Playlist
	if err := s.db.Take(&p, "id = ?", playlistID).Error; err != nil {
		return nil, middleware.NewAppError(http.StatusNotFound, "Playlist not found")
	}
	if !p.IsPublic && p.UserID != viewerID {
		return nil, middleware.NewAppError(http.StatusForbidden, "无权访问")
	}
	var items []model.PlaylistItem
	err := s.db.Where("playlist_id = ?", playlistID).
		Preload("Media").Preload("Media.Category").
		Order("position ASC").
		Find(&items).Error
	if err != nil {
		return nil, middleware.WrapAppError(http.StatusInternalServerError, "查询失败", err)
	}
	out := make([]dto.PlaylistItemResponse, 0, len(items))
	for i := range items {
		mResp := serializeMedia(&items[i].Media)
		out = append(out, dto.PlaylistItemResponse{
			ID:        items[i].ID,
			MediaID:   items[i].MediaID,
			Position:  items[i].Position,
			CreatedAt: util.FormatISO(items[i].CreatedAt),
			Media:     &mResp,
		})
	}
	return out, nil
}

// Create 新建（管理员调用）；归属设为当前用户。
func (s *PlaylistService) Create(ownerID string, req dto.PlaylistCreateRequest) (*dto.PlaylistResponse, error) {
	p := model.Playlist{
		Name:        req.Name,
		Description: req.Description,
		PosterURL:   req.PosterURL,
		UserID:      ownerID,
		IsPublic:    req.IsPublic,
	}
	if err := s.db.Create(&p).Error; err != nil {
		return nil, middleware.WrapAppError(http.StatusInternalServerError, "创建失败", err)
	}
	r := serializePlaylist(&p, 0)
	return &r, nil
}

// Update 更新（管理员调用）；归属用户必须与操作人一致（对齐 TS 原版 owner 校验）。
func (s *PlaylistService) Update(id, operatorID string, req dto.PlaylistUpdateRequest) (*dto.PlaylistResponse, error) {
	var p model.Playlist
	if err := s.db.Take(&p, "id = ?", id).Error; err != nil {
		return nil, middleware.NewAppError(http.StatusNotFound, "Playlist not found")
	}
	if p.UserID != operatorID {
		return nil, middleware.NewAppError(http.StatusForbidden, "Access denied")
	}
	updates := map[string]any{}
	if req.Name != nil {
		updates["name"] = *req.Name
	}
	if req.Description != nil {
		updates["description"] = *req.Description
	}
	if req.PosterURL != nil {
		updates["poster_url"] = *req.PosterURL
	}
	if req.IsPublic != nil {
		updates["is_public"] = *req.IsPublic
	}
	if len(updates) > 0 {
		if err := s.db.Model(&model.Playlist{}).Where("id = ?", id).Updates(updates).Error; err != nil {
			return nil, middleware.WrapAppError(http.StatusInternalServerError, "更新失败", err)
		}
	}
	var count int64
	s.db.Model(&model.PlaylistItem{}).Where("playlist_id = ?", id).Count(&count)
	var fresh model.Playlist
	if err := s.db.Take(&fresh, "id = ?", id).Error; err != nil {
		return nil, middleware.WrapAppError(http.StatusInternalServerError, "查询失败", err)
	}
	r := serializePlaylist(&fresh, count)
	return &r, nil
}

// Delete 删除（管理员调用）；归属用户必须与操作人一致。
func (s *PlaylistService) Delete(id, operatorID string) error {
	var p model.Playlist
	if err := s.db.Take(&p, "id = ?", id).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return middleware.NewAppError(http.StatusNotFound, "Playlist not found")
		}
		return middleware.WrapAppError(http.StatusInternalServerError, "查询失败", err)
	}
	if p.UserID != operatorID {
		return middleware.NewAppError(http.StatusForbidden, "Access denied")
	}
	if err := s.db.Delete(&model.Playlist{}, "id = ?", id).Error; err != nil {
		return middleware.WrapAppError(http.StatusInternalServerError, "删除失败", err)
	}
	return nil
}

// AddItem 把 media 加到 playlist 末尾。
// 权限：playlist.user_id == operatorID（只能给自己的 playlist 加）。
func (s *PlaylistService) AddItem(playlistID, operatorID, mediaID string) (*dto.PlaylistItemResponse, error) {
	var p model.Playlist
	if err := s.db.Take(&p, "id = ?", playlistID).Error; err != nil {
		return nil, middleware.NewAppError(http.StatusNotFound, "Playlist not found")
	}
	if p.UserID != operatorID {
		return nil, middleware.NewAppError(http.StatusForbidden, "无权修改")
	}
	var m model.Media
	if err := s.db.Take(&m, "id = ?", mediaID).Error; err != nil {
		return nil, middleware.NewAppError(http.StatusNotFound, "Media not found")
	}

	// 计算 next position
	var maxPos int
	s.db.Model(&model.PlaylistItem{}).
		Where("playlist_id = ?", playlistID).
		Select("COALESCE(MAX(position), 0)").
		Scan(&maxPos)

	item := model.PlaylistItem{
		PlaylistID: playlistID,
		MediaID:    mediaID,
		Position:   maxPos + 1,
	}
	if err := s.db.Create(&item).Error; err != nil {
		return nil, mapUniqueErr(err, "该媒体已在播放列表中")
	}
	mResp := serializeMedia(&m)
	return &dto.PlaylistItemResponse{
		ID:        item.ID,
		MediaID:   item.MediaID,
		Position:  item.Position,
		CreatedAt: util.FormatISO(item.CreatedAt),
		Media:     &mResp,
	}, nil
}

// RemoveItem 删除 playlist 中指定 media 的条目（按 mediaId 删，对齐 TS 原版）。
func (s *PlaylistService) RemoveItem(playlistID, operatorID, mediaID string) error {
	var p model.Playlist
	if err := s.db.Take(&p, "id = ?", playlistID).Error; err != nil {
		return middleware.NewAppError(http.StatusNotFound, "Playlist not found")
	}
	if p.UserID != operatorID {
		return middleware.NewAppError(http.StatusForbidden, "Access denied")
	}
	tx := s.db.Where("media_id = ? AND playlist_id = ?", mediaID, playlistID).Delete(&model.PlaylistItem{})
	if tx.Error != nil {
		return middleware.WrapAppError(http.StatusInternalServerError, "删除失败", tx.Error)
	}
	if tx.RowsAffected == 0 {
		return middleware.NewAppError(http.StatusNotFound, "Item not found in playlist")
	}
	return nil
}

// Reorder 事务性按前端传入顺序更新 position。
func (s *PlaylistService) Reorder(playlistID, operatorID string, itemIDs []string) error {
	var p model.Playlist
	if err := s.db.Take(&p, "id = ?", playlistID).Error; err != nil {
		return middleware.NewAppError(http.StatusNotFound, "Playlist not found")
	}
	if p.UserID != operatorID {
		return middleware.NewAppError(http.StatusForbidden, "无权修改")
	}
	return s.db.Transaction(func(tx *gorm.DB) error {
		for i, id := range itemIDs {
			res := tx.Model(&model.PlaylistItem{}).
				Where("id = ? AND playlist_id = ?", id, playlistID).
				Update("position", i+1)
			if res.Error != nil {
				return res.Error
			}
		}
		return nil
	})
}

// ---- helpers ----

func (s *PlaylistService) buildList(rows []model.Playlist) []dto.PlaylistResponse {
	ids := make([]string, 0, len(rows))
	for i := range rows {
		ids = append(ids, rows[i].ID)
	}
	// 批量查 item count 避免 N+1
	countMap := map[string]int64{}
	if len(ids) > 0 {
		type cnt struct {
			PlaylistID string
			C          int64
		}
		var cs []cnt
		s.db.Model(&model.PlaylistItem{}).
			Select("playlist_id, COUNT(id) AS c").
			Where("playlist_id IN ?", ids).
			Group("playlist_id").
			Scan(&cs)
		for _, c := range cs {
			countMap[c.PlaylistID] = c.C
		}
	}
	out := make([]dto.PlaylistResponse, 0, len(rows))
	for i := range rows {
		out = append(out, serializePlaylist(&rows[i], countMap[rows[i].ID]))
	}
	return out
}

func serializePlaylist(p *model.Playlist, itemCount int64) dto.PlaylistResponse {
	return dto.PlaylistResponse{
		ID:          p.ID,
		Name:        p.Name,
		Description: p.Description,
		PosterURL:   p.PosterURL,
		UserID:      p.UserID,
		IsPublic:    p.IsPublic,
		CreatedAt:   util.FormatISO(p.CreatedAt),
		UpdatedAt:   util.FormatISO(p.UpdatedAt),
		ItemCount:   itemCount,
	}
}
