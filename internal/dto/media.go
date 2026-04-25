// Package dto
// media.go 定义 Media 相关请求/响应结构。
// 对齐 shared/validation.ts 的 mediaCreateSchema / mediaUpdateSchema / mediaQuerySchema。
package dto

// MediaQuery /media 列表的查询参数。
// sortBy 白名单：createdAt | updatedAt | title | rating | year | views；sortOrder: asc | desc
type MediaQuery struct {
	Page       int    `form:"page,default=1" binding:"min=1"`
	Limit      int    `form:"limit,default=20" binding:"min=1,max=100"`
	Search     string `form:"search"`
	CategoryID string `form:"categoryId"`
	TagID      string `form:"tagId"`
	Artist     string `form:"artist"`
	Status     string `form:"status" binding:"omitempty,oneof=ACTIVE INACTIVE ERROR"`
	SortBy     string `form:"sortBy,default=createdAt" binding:"omitempty,oneof=createdAt updatedAt title rating year views"`
	SortOrder  string `form:"sortOrder,default=desc" binding:"omitempty,oneof=asc desc"`
}

// MediaCreateRequest POST /media
// m3u8Url 必须 https?:// + 包含 ".m3u8" 子串（在 service 层补充）
type MediaCreateRequest struct {
	Title       string   `json:"title" binding:"required,min=1,max=200"`
	M3u8URL     string   `json:"m3u8Url" binding:"required,url"`
	PosterURL   *string  `json:"posterUrl,omitempty" binding:"omitempty"`
	Description *string  `json:"description,omitempty"`
	Year        *int     `json:"year,omitempty" binding:"omitempty,min=1900,max=2100"`
	Rating      *float64 `json:"rating,omitempty" binding:"omitempty,min=0,max=10"`
	Duration    *int     `json:"duration,omitempty" binding:"omitempty,min=0"`
	Artist      *string  `json:"artist,omitempty"`
	Status      *string  `json:"status,omitempty" binding:"omitempty,oneof=ACTIVE INACTIVE ERROR"`
	CategoryID  *string  `json:"categoryId,omitempty"`
	TagIDs      []string `json:"tagIds,omitempty"`
}

// MediaUpdateRequest PUT /media/:id
// 所有字段均可选；显式 null 在 JSON 解析时呈现为 *T=nil。
type MediaUpdateRequest struct {
	Title       *string  `json:"title,omitempty" binding:"omitempty,min=1,max=200"`
	M3u8URL     *string  `json:"m3u8Url,omitempty" binding:"omitempty,url"`
	PosterURL   *string  `json:"posterUrl,omitempty"`
	Description *string  `json:"description,omitempty"`
	Year        *int     `json:"year,omitempty" binding:"omitempty,min=1900,max=2100"`
	Rating      *float64 `json:"rating,omitempty" binding:"omitempty,min=0,max=10"`
	Duration    *int     `json:"duration,omitempty" binding:"omitempty,min=0"`
	Artist      *string  `json:"artist,omitempty"`
	Status      *string  `json:"status,omitempty" binding:"omitempty,oneof=ACTIVE INACTIVE ERROR"`
	CategoryID  *string  `json:"categoryId,omitempty"`
	TagIDs      *[]string `json:"tagIds,omitempty"` // 指针包装以区分 "未传" vs "传入空数组"
}

// CategoryResponse 嵌入在 Media 响应里。
type CategoryResponse struct {
	ID        string `json:"id"`
	Name      string `json:"name"`
	Slug      string `json:"slug"`
	PosterURL *string `json:"posterUrl,omitempty"`
	CreatedAt string `json:"createdAt"`
	UpdatedAt string `json:"updatedAt"`
}

// TagResponse 嵌入在 Media 响应里。
type TagResponse struct {
	ID        string `json:"id"`
	Name      string `json:"name"`
	CreatedAt string `json:"createdAt"`
	UpdatedAt string `json:"updatedAt"`
}

// MediaResponse 是详情 / 列表的统一响应结构。
type MediaResponse struct {
	ID          string            `json:"id"`
	Title       string            `json:"title"`
	M3u8URL           string            `json:"m3u8Url"`
	PosterURL         *string           `json:"posterUrl,omitempty"`
	OriginalPosterURL *string           `json:"originalPosterUrl,omitempty"`
	Description       *string           `json:"description,omitempty"`
	Year        *int              `json:"year,omitempty"`
	Rating      *float64          `json:"rating,omitempty"`
	Duration    *int              `json:"duration,omitempty"`
	Artist      *string           `json:"artist,omitempty"`
	Views       int               `json:"views"`
	Status      string            `json:"status"`
	CategoryID  *string           `json:"categoryId,omitempty"`
	Category    *CategoryResponse `json:"category,omitempty"`
	Tags        []TagResponse     `json:"tags,omitempty"`
	CreatedAt   string            `json:"createdAt"`
	UpdatedAt   string            `json:"updatedAt"`
}

// MediaListResponse /media 列表端点的统一响应（items + 分页）。
type MediaListResponse struct {
	Items      []MediaResponse `json:"items"`
	Total      int64           `json:"total"`
	Page       int             `json:"page"`
	Limit      int             `json:"limit"`
	TotalPages int             `json:"totalPages"`
}

// ArtistInfo GET /media/artists 响应元素。
type ArtistInfo struct {
	Name       string `json:"name"`
	VideoCount int64  `json:"videoCount"`
}
