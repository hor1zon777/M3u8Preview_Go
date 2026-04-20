// Package handler
// favorite.go 对接 /api/v1/favorites/*：toggle / check / list（全部需登录）。
package handler

import (
	"net/http"
	"strconv"

	"github.com/gin-gonic/gin"

	"github.com/hor1zon777/m3u8-preview-go/internal/dto"
	"github.com/hor1zon777/m3u8-preview-go/internal/middleware"
	"github.com/hor1zon777/m3u8-preview-go/internal/service"
)

// FavoriteHandler 汇总 favorite 端点。
type FavoriteHandler struct {
	svc *service.FavoriteService
}

// NewFavoriteHandler 构造。
func NewFavoriteHandler(svc *service.FavoriteService) *FavoriteHandler {
	return &FavoriteHandler{svc: svc}
}

// RegisterAuthed 挂载需登录端点。
func (h *FavoriteHandler) RegisterAuthed(rg *gin.RouterGroup) {
	rg.POST("/:mediaId", h.toggle)
	rg.GET("/:mediaId/check", h.check)
	rg.GET("", h.list)
}

func (h *FavoriteHandler) toggle(c *gin.Context) {
	uid := middleware.CurrentUserID(c)
	favorited, err := h.svc.Toggle(uid, c.Param("mediaId"))
	if err != nil {
		_ = c.Error(err)
		return
	}
	c.JSON(http.StatusOK, dto.OK(dto.FavoriteToggleResponse{Favorited: favorited}))
}

func (h *FavoriteHandler) check(c *gin.Context) {
	uid := middleware.CurrentUserID(c)
	ok, err := h.svc.Check(uid, c.Param("mediaId"))
	if err != nil {
		_ = c.Error(err)
		return
	}
	c.JSON(http.StatusOK, dto.OK(dto.FavoriteCheckResponse{Favorited: ok}))
}

func (h *FavoriteHandler) list(c *gin.Context) {
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

// parseIntQuery 解析 query 整型参数，失败回退到默认值。
func parseIntQuery(c *gin.Context, key string, def int) int {
	raw := c.Query(key)
	if raw == "" {
		return def
	}
	n, err := strconv.Atoi(raw)
	if err != nil {
		return def
	}
	return n
}
