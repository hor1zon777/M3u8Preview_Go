// Package handler
// playlist.go 对接 /api/v1/playlists/*：
//   - GET /public、GET /:id/items 匿名或登录均可访问（公开 playlist 无需登录）
//   - GET /、GET /:id 需登录（返回 owner 或 public 的 playlist）
//   - POST /、PUT /:id、DELETE /:id 仅 ADMIN（且必须是 owner）
//   - POST /:id/items、DELETE /:id/items/:mediaId、PUT /:id/reorder 需登录且为 owner
package handler

import (
	"net/http"

	"github.com/gin-gonic/gin"

	"github.com/hor1zon777/m3u8-preview-go/internal/dto"
	"github.com/hor1zon777/m3u8-preview-go/internal/middleware"
	"github.com/hor1zon777/m3u8-preview-go/internal/service"
)

// PlaylistHandler 汇总 playlist 端点。
type PlaylistHandler struct {
	svc *service.PlaylistService
}

// NewPlaylistHandler 构造。
func NewPlaylistHandler(svc *service.PlaylistService) *PlaylistHandler {
	return &PlaylistHandler{svc: svc}
}

// RegisterPublic 挂载公开端点（GET /public 匿名可访问）。
func (h *PlaylistHandler) RegisterPublic(rg *gin.RouterGroup) {
	rg.GET("/public", h.getPublic)
}

// RegisterOptional 挂载 optionalAuth 下可访问的端点（/:id/items 公开 playlist 不登录也能看）。
func (h *PlaylistHandler) RegisterOptional(rg *gin.RouterGroup) {
	rg.GET("/:id/items", h.getItems)
}

// RegisterAuthed 挂载需登录的查询与 items 操作端点。
func (h *PlaylistHandler) RegisterAuthed(rg *gin.RouterGroup) {
	rg.GET("", h.findAll)
	rg.GET("/:id", h.findByID)
	rg.POST("/:id/items", h.addItem)
	rg.DELETE("/:id/items/:mediaId", h.removeItem)
	rg.PUT("/:id/reorder", h.reorder)
}

// RegisterAdmin 挂载 ADMIN 专属端点（创建 / 更新 / 删除 playlist 本体）。
func (h *PlaylistHandler) RegisterAdmin(rg *gin.RouterGroup) {
	rg.POST("", h.create)
	rg.PUT("/:id", h.update)
	rg.DELETE("/:id", h.delete)
}

// ---- handlers ----

func (h *PlaylistHandler) getPublic(c *gin.Context) {
	page := parseIntQuery(c, "page", 1)
	limit := parseIntQuery(c, "limit", 20)
	items, total, err := h.svc.GetPublic(page, limit)
	if err != nil {
		_ = c.Error(err)
		return
	}
	c.JSON(http.StatusOK, dto.OK(gin.H{
		"items":      items,
		"total":      total,
		"page":       page,
		"limit":      limit,
		"totalPages": totalPages(total, limit),
	}))
}

func (h *PlaylistHandler) findAll(c *gin.Context) {
	uid := middleware.CurrentUserID(c)
	page := parseIntQuery(c, "page", 1)
	limit := parseIntQuery(c, "limit", 20)
	items, _, err := h.svc.GetOwned(uid, page, limit)
	if err != nil {
		_ = c.Error(err)
		return
	}
	// 对齐 TS：findAll 不带 meta，直接返回数组
	c.JSON(http.StatusOK, dto.OK(items))
}

func (h *PlaylistHandler) findByID(c *gin.Context) {
	uid := middleware.CurrentUserID(c)
	r, err := h.svc.GetByID(c.Param("id"), uid)
	if err != nil {
		_ = c.Error(err)
		return
	}
	c.JSON(http.StatusOK, dto.OK(r))
}

func (h *PlaylistHandler) getItems(c *gin.Context) {
	uid := middleware.CurrentUserID(c) // optionalAuth：未登录时为 ""
	items, err := h.svc.GetItems(c.Param("id"), uid)
	if err != nil {
		_ = c.Error(err)
		return
	}
	// TS 原版 getPlaylistItems 返回 { items, total, page, limit, totalPages }
	total := int64(len(items))
	c.JSON(http.StatusOK, dto.OK(gin.H{
		"items":      items,
		"total":      total,
		"page":       1,
		"limit":      total,
		"totalPages": 1,
	}))
}

func (h *PlaylistHandler) create(c *gin.Context) {
	var req dto.PlaylistCreateRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		middleware.AbortWithAppError(c, bindErrorToAppError(err))
		return
	}
	uid := middleware.CurrentUserID(c)
	r, err := h.svc.Create(uid, req)
	if err != nil {
		_ = c.Error(err)
		return
	}
	c.JSON(http.StatusCreated, dto.OK(r))
}

func (h *PlaylistHandler) update(c *gin.Context) {
	var req dto.PlaylistUpdateRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		middleware.AbortWithAppError(c, bindErrorToAppError(err))
		return
	}
	uid := middleware.CurrentUserID(c)
	r, err := h.svc.Update(c.Param("id"), uid, req)
	if err != nil {
		_ = c.Error(err)
		return
	}
	c.JSON(http.StatusOK, dto.OK(r))
}

func (h *PlaylistHandler) delete(c *gin.Context) {
	uid := middleware.CurrentUserID(c)
	if err := h.svc.Delete(c.Param("id"), uid); err != nil {
		_ = c.Error(err)
		return
	}
	c.JSON(http.StatusOK, dto.APIResponse{Success: true, Message: "Playlist deleted successfully"})
}

func (h *PlaylistHandler) addItem(c *gin.Context) {
	var req dto.PlaylistAddItemRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		middleware.AbortWithAppError(c, bindErrorToAppError(err))
		return
	}
	uid := middleware.CurrentUserID(c)
	r, err := h.svc.AddItem(c.Param("id"), uid, req.MediaID)
	if err != nil {
		_ = c.Error(err)
		return
	}
	c.JSON(http.StatusCreated, dto.OK(r))
}

func (h *PlaylistHandler) removeItem(c *gin.Context) {
	uid := middleware.CurrentUserID(c)
	if err := h.svc.RemoveItem(c.Param("id"), uid, c.Param("mediaId")); err != nil {
		_ = c.Error(err)
		return
	}
	c.JSON(http.StatusOK, dto.APIResponse{Success: true, Message: "Item removed from playlist"})
}

func (h *PlaylistHandler) reorder(c *gin.Context) {
	var req dto.PlaylistReorderRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		middleware.AbortWithAppError(c, bindErrorToAppError(err))
		return
	}
	uid := middleware.CurrentUserID(c)
	if err := h.svc.Reorder(c.Param("id"), uid, req.ItemIDs); err != nil {
		_ = c.Error(err)
		return
	}
	c.JSON(http.StatusOK, dto.APIResponse{Success: true, Message: "Items reordered successfully"})
}

func totalPages(total int64, limit int) int {
	if limit <= 0 {
		return 0
	}
	return int((total + int64(limit) - 1) / int64(limit))
}
