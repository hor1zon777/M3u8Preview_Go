// Package dto
// common.go 汇总 category / tag / favorite / watch / playlist 的轻量 DTO。
package dto

// CategoryCreateRequest / CategoryUpdateRequest 对齐 categoryCreateSchema。
type CategoryCreateRequest struct {
	Name      string  `json:"name" binding:"required,min=1,max=50"`
	Slug      string  `json:"slug" binding:"required,min=1,max=50"`
	PosterURL *string `json:"posterUrl,omitempty"`
}

// CategoryUpdateRequest 所有字段可选。
type CategoryUpdateRequest struct {
	Name      *string `json:"name,omitempty" binding:"omitempty,min=1,max=50"`
	Slug      *string `json:"slug,omitempty" binding:"omitempty,min=1,max=50"`
	PosterURL *string `json:"posterUrl,omitempty"`
}

// TagCreateRequest 对齐 tagCreateSchema。
type TagCreateRequest struct {
	Name string `json:"name" binding:"required,min=1,max=50"`
}

// TagUpdateRequest 只允许改 name。
type TagUpdateRequest struct {
	Name string `json:"name" binding:"required,min=1,max=50"`
}

// FavoriteCheckResponse GET /favorites/:mediaId/check
type FavoriteCheckResponse struct {
	Favorited bool `json:"favorited"`
}

// FavoriteToggleResponse POST /favorites/:mediaId
type FavoriteToggleResponse struct {
	Favorited bool `json:"favorited"`
}

// WatchProgressRequest POST /history/progress
type WatchProgressRequest struct {
	MediaID    string  `json:"mediaId" binding:"required"`
	Progress   float64 `json:"progress" binding:"min=0"`
	Duration   float64 `json:"duration" binding:"min=0"`
	Percentage float64 `json:"percentage" binding:"min=0,max=100"`
	Completed  bool    `json:"completed"`
}

// WatchHistoryResponse 单条观看记录。
type WatchHistoryResponse struct {
	ID         string         `json:"id"`
	UserID     string         `json:"userId"`
	MediaID    string         `json:"mediaId"`
	Progress   float64        `json:"progress"`
	Duration   float64        `json:"duration"`
	Percentage float64        `json:"percentage"`
	Completed  bool           `json:"completed"`
	CreatedAt  string         `json:"createdAt"`
	UpdatedAt  string         `json:"updatedAt"`
	Media      *MediaResponse `json:"media,omitempty"`
}

// ProgressMapRequest POST /history/progress-map（前端批量查）
// GET 语义时前端传 ?ids=a,b,c；POST 语义时传 JSON 数组。两种我们都支持。
type ProgressMapRequest struct {
	IDs []string `json:"ids" binding:"required,min=1,max=200,dive,required"`
}

// PlaylistCreateRequest POST /playlists
type PlaylistCreateRequest struct {
	Name        string  `json:"name" binding:"required,min=1,max=100"`
	Description *string `json:"description,omitempty"`
	PosterURL   *string `json:"posterUrl,omitempty"`
	IsPublic    bool    `json:"isPublic"`
}

// PlaylistUpdateRequest PUT /playlists/:id
type PlaylistUpdateRequest struct {
	Name        *string `json:"name,omitempty" binding:"omitempty,min=1,max=100"`
	Description *string `json:"description,omitempty"`
	PosterURL   *string `json:"posterUrl,omitempty"`
	IsPublic    *bool   `json:"isPublic,omitempty"`
}

// PlaylistAddItemRequest POST /playlists/:id/items
type PlaylistAddItemRequest struct {
	MediaID string `json:"mediaId" binding:"required"`
}

// PlaylistReorderRequest PUT /playlists/:id/items/reorder
type PlaylistReorderRequest struct {
	ItemIDs []string `json:"itemIds" binding:"required,min=1,dive,required"`
}

// PlaylistResponse Playlist 详情 / 列表元素。
type PlaylistResponse struct {
	ID          string  `json:"id"`
	Name        string  `json:"name"`
	Description *string `json:"description,omitempty"`
	PosterURL   *string `json:"posterUrl,omitempty"`
	UserID      string  `json:"userId"`
	IsPublic    bool    `json:"isPublic"`
	CreatedAt   string  `json:"createdAt"`
	UpdatedAt   string  `json:"updatedAt"`
	ItemCount   int64   `json:"itemCount"`
}

// PlaylistItemResponse /playlists/:id/items 元素。
type PlaylistItemResponse struct {
	ID        string         `json:"id"`
	MediaID   string         `json:"mediaId"`
	Position  int            `json:"position"`
	CreatedAt string         `json:"createdAt"`
	Media     *MediaResponse `json:"media,omitempty"`
}

// FavoriteResponse /favorites 列表元素。
type FavoriteResponse struct {
	ID        string         `json:"id"`
	MediaID   string         `json:"mediaId"`
	CreatedAt string         `json:"createdAt"`
	Media     *MediaResponse `json:"media,omitempty"`
}
