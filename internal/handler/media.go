// Package handler
// media.go 对接 /api/v1/media/* 路由。
package handler

import (
	"net/http"
	"strconv"

	"github.com/gin-gonic/gin"

	"github.com/hor1zon777/m3u8-preview-go/internal/dto"
	"github.com/hor1zon777/m3u8-preview-go/internal/middleware"
	"github.com/hor1zon777/m3u8-preview-go/internal/service"
)

// MediaHandler 汇总 media 端点。
type MediaHandler struct {
	svc *service.MediaService
}

// NewMediaHandler 构造。
func NewMediaHandler(svc *service.MediaService) *MediaHandler {
	return &MediaHandler{svc: svc}
}

// RegisterPublic 挂载公开查询端点（匿名可访问）。
func (h *MediaHandler) RegisterPublic(rg *gin.RouterGroup) {
	rg.GET("", h.findAll)
	rg.GET("/recent", h.getRecent)
	rg.GET("/random", h.getRandom)
	rg.GET("/:id", h.findByID)
}

// RegisterAuthed 挂载需要 Authenticate 中间件的端点。
func (h *MediaHandler) RegisterAuthed(rg *gin.RouterGroup) {
	rg.GET("/artists", h.getArtists)
}

// RegisterViews 挂载 views 端点（需 optionalAuth + viewsLimiter）。
func (h *MediaHandler) RegisterViews(rg *gin.RouterGroup) {
	rg.POST("/:id/views", h.incrementViews)
}

// RegisterAdmin 挂载管理员端点。
func (h *MediaHandler) RegisterAdmin(rg *gin.RouterGroup) {
	rg.POST("", h.create)
	rg.PUT("/:id", h.update)
	rg.DELETE("/:id", h.delete)
	rg.POST("/:id/thumbnail", h.regenerateThumbnail)
}

// --- handlers ---

func (h *MediaHandler) findAll(c *gin.Context) {
	var q dto.MediaQuery
	if err := c.ShouldBindQuery(&q); err != nil {
		middleware.AbortWithAppError(c, bindErrorToAppError(err))
		return
	}
	result, err := h.svc.FindAll(q)
	if err != nil {
		_ = c.Error(err)
		return
	}
	c.JSON(http.StatusOK, dto.OK(result))
}

func (h *MediaHandler) findByID(c *gin.Context) {
	m, err := h.svc.FindByID(c.Param("id"))
	if err != nil {
		_ = c.Error(err)
		return
	}
	c.JSON(http.StatusOK, dto.OK(m))
}

func (h *MediaHandler) create(c *gin.Context) {
	var req dto.MediaCreateRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		middleware.AbortWithAppError(c, bindErrorToAppError(err))
		return
	}
	m, err := h.svc.Create(req)
	if err != nil {
		_ = c.Error(err)
		return
	}
	c.JSON(http.StatusCreated, dto.OK(m))
}

func (h *MediaHandler) update(c *gin.Context) {
	var req dto.MediaUpdateRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		middleware.AbortWithAppError(c, bindErrorToAppError(err))
		return
	}
	m, err := h.svc.Update(c.Param("id"), req)
	if err != nil {
		_ = c.Error(err)
		return
	}
	c.JSON(http.StatusOK, dto.OK(m))
}

func (h *MediaHandler) delete(c *gin.Context) {
	if err := h.svc.Delete(c.Param("id")); err != nil {
		_ = c.Error(err)
		return
	}
	c.JSON(http.StatusOK, dto.APIResponse{Success: true, Message: "Media deleted successfully"})
}

func (h *MediaHandler) incrementViews(c *gin.Context) {
	if err := h.svc.IncrementViews(c.Param("id")); err != nil {
		_ = c.Error(err)
		return
	}
	c.JSON(http.StatusOK, dto.APIResponse{Success: true})
}

func (h *MediaHandler) getRandom(c *gin.Context) {
	items, err := h.svc.GetRandom(parseCount(c, 10))
	if err != nil {
		_ = c.Error(err)
		return
	}
	c.JSON(http.StatusOK, dto.OK(items))
}

func (h *MediaHandler) getRecent(c *gin.Context) {
	items, err := h.svc.GetRecent(parseCount(c, 10))
	if err != nil {
		_ = c.Error(err)
		return
	}
	c.JSON(http.StatusOK, dto.OK(items))
}

func (h *MediaHandler) getArtists(c *gin.Context) {
	items, err := h.svc.GetArtists()
	if err != nil {
		_ = c.Error(err)
		return
	}
	c.JSON(http.StatusOK, dto.OK(items))
}

// regenerateThumbnail 阶段 I 会接真正的 ffmpeg；此处返回 501 以明示未实现。
func (h *MediaHandler) regenerateThumbnail(c *gin.Context) {
	middleware.AbortWithAppError(c, middleware.NewAppError(http.StatusNotImplemented, "thumbnail generation not implemented yet"))
}

// parseCount 从 query 读 count，限制 1..50，解析失败走 defaultCount。
func parseCount(c *gin.Context, defaultCount int) int {
	raw := c.Query("count")
	if raw == "" {
		return defaultCount
	}
	n, err := strconv.Atoi(raw)
	if err != nil {
		return defaultCount
	}
	if n < 1 {
		return 1
	}
	if n > 50 {
		return 50
	}
	return n
}
