// Package service
// media.go 对齐 mediaService.ts：分页 / 筛选 / 排序 / 原子自增 / 随机 / 艺人聚合。
package service

import (
	"errors"
	"net/http"
	"os"
	"path/filepath"
	"strings"

	"gorm.io/gorm"

	"github.com/hor1zon777/m3u8-preview-go/internal/dto"
	"github.com/hor1zon777/m3u8-preview-go/internal/middleware"
	"github.com/hor1zon777/m3u8-preview-go/internal/model"
	"github.com/hor1zon777/m3u8-preview-go/internal/util"
)

// ThumbnailEnqueuer 供 media service 触发缩略图生成；阶段 I 提供真实实现。
type ThumbnailEnqueuer interface {
	Enqueue(mediaID, m3u8URL string)
}

// PosterResolver 把外部 posterUrl 下载到本地并返回本地路径；阶段 I 提供实现。
// 同步路径，适合单条 Create/Update；大批量导入请使用 PosterMigrator 异步入队。
type PosterResolver interface {
	Resolve(rawPosterURL *string) (*string, error)
}

// PosterMigrator 把外部 posterUrl 异步入队下载，下载完成后由 worker 回写 poster_url。
// 用于批量导入，避免阻塞主请求；PosterDownloader 同时实现 PosterResolver 与 PosterMigrator。
type PosterMigrator interface {
	EnqueueMigrate(mediaID, rawURL string)
}

// NoopThumbnailEnqueuer 默认 no-op 实现。
type NoopThumbnailEnqueuer struct{}

// Enqueue is a no-op.
func (NoopThumbnailEnqueuer) Enqueue(string, string) {}

// PassthroughPosterResolver 默认：原样返回。
type PassthroughPosterResolver struct{}

// Resolve 原样返回输入。
func (PassthroughPosterResolver) Resolve(in *string) (*string, error) { return in, nil }

// NoopPosterMigrator 默认 no-op 实现。
type NoopPosterMigrator struct{}

// EnqueueMigrate is a no-op.
func (NoopPosterMigrator) EnqueueMigrate(string, string) {}

// MediaService 聚合 media 相关查询与写入。
type MediaService struct {
	db         *gorm.DB
	uploadsDir string
	thumb      ThumbnailEnqueuer
	poster     PosterResolver
}

// NewMediaService 构造。
func NewMediaService(db *gorm.DB, uploadsDir string, thumb ThumbnailEnqueuer, poster PosterResolver) *MediaService {
	if thumb == nil {
		thumb = NoopThumbnailEnqueuer{}
	}
	if poster == nil {
		poster = PassthroughPosterResolver{}
	}
	return &MediaService{db: db, uploadsDir: uploadsDir, thumb: thumb, poster: poster}
}

// SetPosterResolver 替换 poster resolver（app 启动阶段注入真实实现）。
func (s *MediaService) SetPosterResolver(p PosterResolver) {
	if p == nil {
		p = PassthroughPosterResolver{}
	}
	s.poster = p
}

// SetThumbnailEnqueuer 替换 thumbnail enqueuer。
func (s *MediaService) SetThumbnailEnqueuer(t ThumbnailEnqueuer) {
	if t == nil {
		t = NoopThumbnailEnqueuer{}
	}
	s.thumb = t
}

// FindAll 分页列表，支持 search / categoryId / tagId / artist / status / sortBy/Order。
func (s *MediaService) FindAll(q dto.MediaQuery) (dto.MediaListResponse, error) {
	page, limit := util.SafePagination(q.Page, q.Limit, 100)
	offset := util.Offset(page, limit)

	base := s.db.Model(&model.Media{})
	if q.TagID != "" {
		base = base.Joins("JOIN media_tags ON media_tags.media_id = media.id").
			Where("media_tags.tag_id = ?", q.TagID)
	}
	if q.Search != "" {
		kw := "%" + q.Search + "%"
		base = base.Where("media.title LIKE ? OR media.description LIKE ? OR media.artist LIKE ?", kw, kw, kw)
	}
	if q.CategoryID != "" {
		base = base.Where("media.category_id = ?", q.CategoryID)
	}
	if q.Status != "" {
		base = base.Where("media.status = ?", q.Status)
	}
	if q.Artist != "" {
		base = base.Where("media.artist = ?", q.Artist)
	}

	var total int64
	if err := base.Session(&gorm.Session{}).Distinct("media.id").Count(&total).Error; err != nil {
		return dto.MediaListResponse{}, middleware.WrapAppError(http.StatusInternalServerError, "统计失败", err)
	}

	orderCol := sortColumn(q.SortBy)
	orderDir := strings.ToUpper(q.SortOrder)
	if orderDir != "ASC" && orderDir != "DESC" {
		orderDir = "DESC"
	}

	var rows []model.Media
	err := base.
		Preload("Category").
		Preload("Tags").
		Preload("Tags.Tag").
		Order("media." + orderCol + " " + orderDir).
		Offset(offset).Limit(limit).
		Find(&rows).Error
	if err != nil {
		return dto.MediaListResponse{}, middleware.WrapAppError(http.StatusInternalServerError, "查询失败", err)
	}

	totalPages := int((total + int64(limit) - 1) / int64(limit))
	return dto.MediaListResponse{
		Items:      serializeMediaList(rows),
		Total:      total,
		Page:       page,
		Limit:      limit,
		TotalPages: totalPages,
	}, nil
}

// FindByID 详情查询，附带 category + tags。
func (s *MediaService) FindByID(id string) (*dto.MediaResponse, error) {
	var m model.Media
	err := s.db.Preload("Category").Preload("Tags").Preload("Tags.Tag").
		Take(&m, "id = ?", id).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, middleware.NewAppError(http.StatusNotFound, "Media not found")
	}
	if err != nil {
		return nil, middleware.WrapAppError(http.StatusInternalServerError, "查询失败", err)
	}
	resp := serializeMedia(&m)
	return &resp, nil
}

// Create 新建媒体。事务保证 media + media_tag 原子。
func (s *MediaService) Create(req dto.MediaCreateRequest) (*dto.MediaResponse, error) {
	if !strings.Contains(req.M3u8URL, ".m3u8") {
		return nil, middleware.NewAppError(http.StatusBadRequest, "m3u8Url 必须包含 .m3u8 路径")
	}
	resolvedPoster, err := s.poster.Resolve(req.PosterURL)
	if err != nil {
		return nil, middleware.WrapAppError(http.StatusBadGateway, "下载封面失败", err)
	}

	m := model.Media{
		Title:       req.Title,
		M3u8URL:     req.M3u8URL,
		PosterURL:   resolvedPoster,
		Description: req.Description,
		Year:        req.Year,
		Rating:      req.Rating,
		Duration:    req.Duration,
		Artist:      req.Artist,
		CategoryID:  req.CategoryID,
		Status:      model.MediaStatusActive,
	}
	if req.Status != nil {
		m.Status = *req.Status
	}

	err = s.db.Transaction(func(tx *gorm.DB) error {
		if err := tx.Create(&m).Error; err != nil {
			return err
		}
		if len(req.TagIDs) > 0 {
			links := make([]model.MediaTag, 0, len(req.TagIDs))
			for _, tid := range req.TagIDs {
				links = append(links, model.MediaTag{MediaID: m.ID, TagID: tid})
			}
			if err := tx.Create(&links).Error; err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		return nil, middleware.WrapAppError(http.StatusInternalServerError, "创建失败", err)
	}

	if m.PosterURL == nil {
		s.thumb.Enqueue(m.ID, m.M3u8URL)
	}
	return s.FindByID(m.ID)
}

// Update 部分更新，tagIds 传入时替换全部关联。
func (s *MediaService) Update(id string, req dto.MediaUpdateRequest) (*dto.MediaResponse, error) {
	var existing model.Media
	if err := s.db.Take(&existing, "id = ?", id).Error; err != nil {
		return nil, middleware.NewAppError(http.StatusNotFound, "Media not found")
	}

	if req.PosterURL != nil {
		resolved, err := s.poster.Resolve(req.PosterURL)
		if err != nil {
			return nil, middleware.WrapAppError(http.StatusBadGateway, "下载封面失败", err)
		}
		req.PosterURL = resolved
	}

	updates := map[string]any{}
	if req.Title != nil {
		updates["title"] = *req.Title
	}
	if req.M3u8URL != nil {
		if !strings.Contains(*req.M3u8URL, ".m3u8") {
			return nil, middleware.NewAppError(http.StatusBadRequest, "m3u8Url 必须包含 .m3u8 路径")
		}
		updates["m3u8_url"] = *req.M3u8URL
	}
	if req.PosterURL != nil {
		updates["poster_url"] = *req.PosterURL
	}
	if req.Description != nil {
		updates["description"] = *req.Description
	}
	if req.Year != nil {
		updates["year"] = *req.Year
	}
	if req.Rating != nil {
		updates["rating"] = *req.Rating
	}
	if req.Duration != nil {
		updates["duration"] = *req.Duration
	}
	if req.Artist != nil {
		updates["artist"] = *req.Artist
	}
	if req.Status != nil {
		updates["status"] = *req.Status
	}
	if req.CategoryID != nil {
		updates["category_id"] = *req.CategoryID
	}

	err := s.db.Transaction(func(tx *gorm.DB) error {
		if req.TagIDs != nil {
			if err := tx.Where("media_id = ?", id).Delete(&model.MediaTag{}).Error; err != nil {
				return err
			}
			if len(*req.TagIDs) > 0 {
				links := make([]model.MediaTag, 0, len(*req.TagIDs))
				for _, tid := range *req.TagIDs {
					links = append(links, model.MediaTag{MediaID: id, TagID: tid})
				}
				if err := tx.Create(&links).Error; err != nil {
					return err
				}
			}
		}
		if len(updates) > 0 {
			if err := tx.Model(&model.Media{}).Where("id = ?", id).Updates(updates).Error; err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		return nil, middleware.WrapAppError(http.StatusInternalServerError, "更新失败", err)
	}
	return s.FindByID(id)
}

// Delete 删除媒体。外键级联会自动清掉 media_tags / favorites / playlist_items / watch_history。
func (s *MediaService) Delete(id string) error {
	var existing model.Media
	if err := s.db.Take(&existing, "id = ?", id).Error; err != nil {
		return middleware.NewAppError(http.StatusNotFound, "Media not found")
	}
	if err := s.db.Delete(&existing).Error; err != nil {
		return middleware.WrapAppError(http.StatusInternalServerError, "删除失败", err)
	}
	if existing.PosterURL != nil && strings.HasPrefix(*existing.PosterURL, "/uploads/") {
		go func(relPath, uploadsDir string) {
			abs := filepath.Join(uploadsDir, strings.TrimPrefix(relPath, "/uploads/"))
			_ = os.Remove(abs)
		}(*existing.PosterURL, s.uploadsDir)
	}
	return nil
}

// IncrementViews 原子 +1。
func (s *MediaService) IncrementViews(id string) error {
	return s.db.Model(&model.Media{}).Where("id = ?", id).
		UpdateColumn("views", gorm.Expr("views + ?", 1)).Error
}

// GetRecent 最新 ACTIVE 媒体。
func (s *MediaService) GetRecent(count int) ([]dto.MediaResponse, error) {
	count = clampInt(count, 1, 50)
	var rows []model.Media
	err := s.db.Preload("Category").
		Where("status = ?", model.MediaStatusActive).
		Order("created_at DESC").
		Limit(count).
		Find(&rows).Error
	if err != nil {
		return nil, middleware.WrapAppError(http.StatusInternalServerError, "查询失败", err)
	}
	return serializeMediaList(rows), nil
}

// GetRandom 随机 ACTIVE 媒体。
func (s *MediaService) GetRandom(count int) ([]dto.MediaResponse, error) {
	count = clampInt(count, 1, 50)
	var ids []string
	err := s.db.Raw("SELECT id FROM media WHERE status = ? ORDER BY RANDOM() LIMIT ?",
		model.MediaStatusActive, count).Scan(&ids).Error
	if err != nil {
		return nil, middleware.WrapAppError(http.StatusInternalServerError, "查询失败", err)
	}
	if len(ids) == 0 {
		return []dto.MediaResponse{}, nil
	}
	var rows []model.Media
	if err := s.db.Preload("Category").Where("id IN ?", ids).Find(&rows).Error; err != nil {
		return nil, middleware.WrapAppError(http.StatusInternalServerError, "查询失败", err)
	}
	return serializeMediaList(rows), nil
}

// GetArtists 按 artist 聚合计数。
func (s *MediaService) GetArtists() ([]dto.ArtistInfo, error) {
	type row struct {
		Artist     string
		VideoCount int64
	}
	var rs []row
	err := s.db.Model(&model.Media{}).
		Select("artist, COUNT(id) AS video_count").
		Where("status = ? AND artist IS NOT NULL AND artist <> ''", model.MediaStatusActive).
		Group("artist").
		Order("video_count DESC").
		Scan(&rs).Error
	if err != nil {
		return nil, middleware.WrapAppError(http.StatusInternalServerError, "查询失败", err)
	}
	out := make([]dto.ArtistInfo, 0, len(rs))
	for _, r := range rs {
		out = append(out, dto.ArtistInfo{Name: r.Artist, VideoCount: r.VideoCount})
	}
	return out, nil
}

// ---- helpers ----

func sortColumn(sortBy string) string {
	switch sortBy {
	case "updatedAt":
		return "updated_at"
	case "title":
		return "title"
	case "rating":
		return "rating"
	case "year":
		return "year"
	case "views":
		return "views"
	default:
		return "created_at"
	}
}

func clampInt(n, lo, hi int) int {
	if n < lo {
		return lo
	}
	if n > hi {
		return hi
	}
	return n
}

func serializeMedia(m *model.Media) dto.MediaResponse {
	resp := dto.MediaResponse{
		ID:          m.ID,
		Title:       m.Title,
		M3u8URL:     m.M3u8URL,
		PosterURL:   m.PosterURL,
		Description: m.Description,
		Year:        m.Year,
		Rating:      m.Rating,
		Duration:    m.Duration,
		Artist:      m.Artist,
		Views:       m.Views,
		Status:      m.Status,
		CategoryID:  m.CategoryID,
		CreatedAt:   util.FormatISO(m.CreatedAt),
		UpdatedAt:   util.FormatISO(m.UpdatedAt),
	}
	if m.Category != nil {
		resp.Category = &dto.CategoryResponse{
			ID:        m.Category.ID,
			Name:      m.Category.Name,
			Slug:      m.Category.Slug,
			PosterURL: m.Category.PosterURL,
			CreatedAt: util.FormatISO(m.Category.CreatedAt),
			UpdatedAt: util.FormatISO(m.Category.UpdatedAt),
		}
	}
	if len(m.Tags) > 0 {
		resp.Tags = make([]dto.TagResponse, 0, len(m.Tags))
		for _, mt := range m.Tags {
			resp.Tags = append(resp.Tags, dto.TagResponse{
				ID:        mt.Tag.ID,
				Name:      mt.Tag.Name,
				CreatedAt: util.FormatISO(mt.Tag.CreatedAt),
				UpdatedAt: util.FormatISO(mt.Tag.UpdatedAt),
			})
		}
	}
	return resp
}

func serializeMediaList(rows []model.Media) []dto.MediaResponse {
	out := make([]dto.MediaResponse, 0, len(rows))
	for i := range rows {
		out = append(out, serializeMedia(&rows[i]))
	}
	return out
}
