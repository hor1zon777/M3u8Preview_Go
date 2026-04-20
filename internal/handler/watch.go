// Package handler
// watch.go 对接 /api/v1/history/*：进度 upsert / 列表 / 继续观看 / 批量进度 / 单媒体进度 / 清空 / 删单条。
// 路由对齐 Express watchHistoryRoutes：`DELETE /clear` 必须排在 `DELETE /:id` 前。
package handler

import (
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"

	"github.com/hor1zon777/m3u8-preview-go/internal/dto"
	"github.com/hor1zon777/m3u8-preview-go/internal/middleware"
	"github.com/hor1zon777/m3u8-preview-go/internal/service"
)

// WatchHistoryHandler 汇总 watch history 端点。
type WatchHistoryHandler struct {
	svc *service.WatchHistoryService
}

// NewWatchHistoryHandler 构造。
func NewWatchHistoryHandler(svc *service.WatchHistoryService) *WatchHistoryHandler {
	return &WatchHistoryHandler{svc: svc}
}

// RegisterAuthed 挂载端点（路由组已带 Authenticate 中间件）。
func (h *WatchHistoryHandler) RegisterAuthed(rg *gin.RouterGroup) {
	rg.POST("/progress", h.updateProgress)
	rg.GET("", h.list)
	rg.GET("/continue", h.continueWatching)
	rg.GET("/progress-map", h.progressMap)
	// /clear 需要排在 /:id 前，否则会被当成 id 参数吞掉。
	rg.DELETE("/clear", h.clear)
	rg.GET("/:mediaId", h.getByMedia)
	rg.DELETE("/:id", h.deleteOne)
}

func (h *WatchHistoryHandler) updateProgress(c *gin.Context) {
	var req dto.WatchProgressRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		middleware.AbortWithAppError(c, bindErrorToAppError(err))
		return
	}
	uid := middleware.CurrentUserID(c)
	r, err := h.svc.UpdateProgress(uid, req)
	if err != nil {
		_ = c.Error(err)
		return
	}
	c.JSON(http.StatusOK, dto.OK(r))
}

func (h *WatchHistoryHandler) list(c *gin.Context) {
	uid := middleware.CurrentUserID(c)
	page := parseIntQuery(c, "page", 1)
	limit := parseIntQuery(c, "limit", 20)

	items, total, err := h.svc.List(uid, page, limit)
	if err != nil {
		_ = c.Error(err)
		return
	}
	c.JSON(http.StatusOK, dto.Paginated(items, total, page, limit))
}

func (h *WatchHistoryHandler) continueWatching(c *gin.Context) {
	uid := middleware.CurrentUserID(c)
	limit := parseIntQuery(c, "limit", 20)
	items, err := h.svc.Continue(uid, limit)
	if err != nil {
		_ = c.Error(err)
		return
	}
	c.JSON(http.StatusOK, dto.OK(items))
}

// progressMap 支持 GET ?ids=a,b,c（前端批量查询使用逗号分隔）。
func (h *WatchHistoryHandler) progressMap(c *gin.Context) {
	uid := middleware.CurrentUserID(c)
	raw := c.Query("ids")
	if raw == "" {
		c.JSON(http.StatusOK, dto.OK(map[string]dto.WatchHistoryResponse{}))
		return
	}
	ids := strings.Split(raw, ",")
	cleaned := ids[:0]
	for _, id := range ids {
		trimmed := strings.TrimSpace(id)
		if trimmed != "" {
			cleaned = append(cleaned, trimmed)
		}
	}
	result, err := h.svc.ProgressMap(uid, cleaned)
	if err != nil {
		_ = c.Error(err)
		return
	}
	c.JSON(http.StatusOK, dto.OK(result))
}

func (h *WatchHistoryHandler) getByMedia(c *gin.Context) {
	uid := middleware.CurrentUserID(c)
	r, err := h.svc.GetByMedia(uid, c.Param("mediaId"))
	if err != nil {
		_ = c.Error(err)
		return
	}
	c.JSON(http.StatusOK, dto.OK(r))
}

func (h *WatchHistoryHandler) clear(c *gin.Context) {
	uid := middleware.CurrentUserID(c)
	if err := h.svc.Clear(uid); err != nil {
		_ = c.Error(err)
		return
	}
	c.JSON(http.StatusOK, dto.APIResponse{Success: true, Message: "History cleared"})
}

func (h *WatchHistoryHandler) deleteOne(c *gin.Context) {
	uid := middleware.CurrentUserID(c)
	if err := h.svc.DeleteOne(uid, c.Param("id")); err != nil {
		_ = c.Error(err)
		return
	}
	c.JSON(http.StatusOK, dto.APIResponse{Success: true, Message: "History deleted"})
}
