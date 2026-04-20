// Package service
// category.go + tag.go 对齐 categoryService.ts / tagService.ts。
// 两者模式一致：唯一约束（slug/name）、管理员 CRUD、公开列表/详情。
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

// CategoryService 负责 categories 表操作。
type CategoryService struct{ db *gorm.DB }

// NewCategoryService 构造。
func NewCategoryService(db *gorm.DB) *CategoryService { return &CategoryService{db: db} }

// FindAll 按名称升序返回全部分类。前端分类很少，全量返回不分页。
func (s *CategoryService) FindAll() ([]dto.CategoryResponse, error) {
	var rows []model.Category
	if err := s.db.Order("name ASC").Find(&rows).Error; err != nil {
		return nil, middleware.WrapAppError(http.StatusInternalServerError, "查询失败", err)
	}
	out := make([]dto.CategoryResponse, 0, len(rows))
	for i := range rows {
		out = append(out, serializeCategory(&rows[i]))
	}
	return out, nil
}

// FindByID 详情。
func (s *CategoryService) FindByID(id string) (*dto.CategoryResponse, error) {
	var c model.Category
	if err := s.db.Take(&c, "id = ?", id).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, middleware.NewAppError(http.StatusNotFound, "Category not found")
		}
		return nil, middleware.WrapAppError(http.StatusInternalServerError, "查询失败", err)
	}
	r := serializeCategory(&c)
	return &r, nil
}

// Create 新建。name 或 slug 唯一冲突返回 409。
func (s *CategoryService) Create(req dto.CategoryCreateRequest) (*dto.CategoryResponse, error) {
	c := model.Category{Name: req.Name, Slug: req.Slug, PosterURL: req.PosterURL}
	if err := s.db.Create(&c).Error; err != nil {
		return nil, mapUniqueErr(err, "分类名称或 slug 已存在")
	}
	return s.FindByID(c.ID)
}

// Update 部分更新。
func (s *CategoryService) Update(id string, req dto.CategoryUpdateRequest) (*dto.CategoryResponse, error) {
	updates := map[string]any{}
	if req.Name != nil {
		updates["name"] = *req.Name
	}
	if req.Slug != nil {
		updates["slug"] = *req.Slug
	}
	if req.PosterURL != nil {
		updates["poster_url"] = *req.PosterURL
	}
	if len(updates) == 0 {
		return s.FindByID(id)
	}
	if err := s.db.Model(&model.Category{}).Where("id = ?", id).Updates(updates).Error; err != nil {
		return nil, mapUniqueErr(err, "分类名称或 slug 已存在")
	}
	return s.FindByID(id)
}

// Delete 删除（关联 media.category_id 会被设为 NULL，由 GORM constraint 处理）。
func (s *CategoryService) Delete(id string) error {
	tx := s.db.Delete(&model.Category{}, "id = ?", id)
	if tx.Error != nil {
		return middleware.WrapAppError(http.StatusInternalServerError, "删除失败", tx.Error)
	}
	if tx.RowsAffected == 0 {
		return middleware.NewAppError(http.StatusNotFound, "Category not found")
	}
	return nil
}

func serializeCategory(c *model.Category) dto.CategoryResponse {
	return dto.CategoryResponse{
		ID:        c.ID,
		Name:      c.Name,
		Slug:      c.Slug,
		PosterURL: c.PosterURL,
		CreatedAt: util.FormatISO(c.CreatedAt),
		UpdatedAt: util.FormatISO(c.UpdatedAt),
	}
}

// TagService 负责 tags 表操作。
type TagService struct{ db *gorm.DB }

// NewTagService 构造。
func NewTagService(db *gorm.DB) *TagService { return &TagService{db: db} }

// FindAll 全量标签按 name 升序。
func (s *TagService) FindAll() ([]dto.TagResponse, error) {
	var rows []model.Tag
	if err := s.db.Order("name ASC").Find(&rows).Error; err != nil {
		return nil, middleware.WrapAppError(http.StatusInternalServerError, "查询失败", err)
	}
	out := make([]dto.TagResponse, 0, len(rows))
	for i := range rows {
		out = append(out, serializeTag(&rows[i]))
	}
	return out, nil
}

// FindByID 详情。
func (s *TagService) FindByID(id string) (*dto.TagResponse, error) {
	var t model.Tag
	if err := s.db.Take(&t, "id = ?", id).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, middleware.NewAppError(http.StatusNotFound, "Tag not found")
		}
		return nil, middleware.WrapAppError(http.StatusInternalServerError, "查询失败", err)
	}
	r := serializeTag(&t)
	return &r, nil
}

// Create 新建 tag。
func (s *TagService) Create(req dto.TagCreateRequest) (*dto.TagResponse, error) {
	t := model.Tag{Name: req.Name}
	if err := s.db.Create(&t).Error; err != nil {
		return nil, mapUniqueErr(err, "标签名已存在")
	}
	return s.FindByID(t.ID)
}

// Update 更新。
func (s *TagService) Update(id string, req dto.TagUpdateRequest) (*dto.TagResponse, error) {
	if err := s.db.Model(&model.Tag{}).Where("id = ?", id).Update("name", req.Name).Error; err != nil {
		return nil, mapUniqueErr(err, "标签名已存在")
	}
	return s.FindByID(id)
}

// Delete 删除。
func (s *TagService) Delete(id string) error {
	tx := s.db.Delete(&model.Tag{}, "id = ?", id)
	if tx.Error != nil {
		return middleware.WrapAppError(http.StatusInternalServerError, "删除失败", tx.Error)
	}
	if tx.RowsAffected == 0 {
		return middleware.NewAppError(http.StatusNotFound, "Tag not found")
	}
	return nil
}

func serializeTag(t *model.Tag) dto.TagResponse {
	return dto.TagResponse{
		ID:        t.ID,
		Name:      t.Name,
		CreatedAt: util.FormatISO(t.CreatedAt),
		UpdatedAt: util.FormatISO(t.UpdatedAt),
	}
}

// mapUniqueErr 把 SQLite UNIQUE 约束失败映射为 409。
// glebarez 驱动没有稳定的错误类型，这里用字符串匹配（够用但非最佳）。
func mapUniqueErr(err error, msg string) error {
	if err == nil {
		return nil
	}
	if isUniqueViolation(err) {
		return middleware.NewAppError(http.StatusConflict, msg)
	}
	return middleware.WrapAppError(http.StatusInternalServerError, "写入失败", err)
}

func isUniqueViolation(err error) bool {
	s := err.Error()
	return contains(s, "UNIQUE constraint failed") || contains(s, "constraint failed")
}

func contains(s, sub string) bool {
	return len(sub) == 0 || indexOfSub(s, sub) >= 0
}

func indexOfSub(s, sub string) int {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return i
		}
	}
	return -1
}
