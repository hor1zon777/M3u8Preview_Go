// Package handler
// category.go 对接 /api/v1/categories/* 路由（public 查询 + admin CRUD）。
package handler

import (
	"net/http"

	"github.com/gin-gonic/gin"

	"github.com/hor1zon777/m3u8-preview-go/internal/dto"
	"github.com/hor1zon777/m3u8-preview-go/internal/middleware"
	"github.com/hor1zon777/m3u8-preview-go/internal/service"
)

// CategoryHandler 汇总 category 端点。
type CategoryHandler struct {
	svc *service.CategoryService
}

// NewCategoryHandler 构造。
func NewCategoryHandler(svc *service.CategoryService) *CategoryHandler {
	return &CategoryHandler{svc: svc}
}

// RegisterPublic 挂载公开查询端点。
func (h *CategoryHandler) RegisterPublic(rg *gin.RouterGroup) {
	rg.GET("", h.findAll)
	rg.GET("/:id", h.findByID)
}

// RegisterAdmin 挂载管理员写入端点。
func (h *CategoryHandler) RegisterAdmin(rg *gin.RouterGroup) {
	rg.POST("", h.create)
	rg.PUT("/:id", h.update)
	rg.DELETE("/:id", h.delete)
}

func (h *CategoryHandler) findAll(c *gin.Context) {
	items, err := h.svc.FindAll()
	if err != nil {
		_ = c.Error(err)
		return
	}
	c.JSON(http.StatusOK, dto.OK(items))
}

func (h *CategoryHandler) findByID(c *gin.Context) {
	r, err := h.svc.FindByID(c.Param("id"))
	if err != nil {
		_ = c.Error(err)
		return
	}
	c.JSON(http.StatusOK, dto.OK(r))
}

func (h *CategoryHandler) create(c *gin.Context) {
	var req dto.CategoryCreateRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		middleware.AbortWithAppError(c, bindErrorToAppError(err))
		return
	}
	r, err := h.svc.Create(req)
	if err != nil {
		_ = c.Error(err)
		return
	}
	c.JSON(http.StatusCreated, dto.OK(r))
}

func (h *CategoryHandler) update(c *gin.Context) {
	var req dto.CategoryUpdateRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		middleware.AbortWithAppError(c, bindErrorToAppError(err))
		return
	}
	r, err := h.svc.Update(c.Param("id"), req)
	if err != nil {
		_ = c.Error(err)
		return
	}
	c.JSON(http.StatusOK, dto.OK(r))
}

func (h *CategoryHandler) delete(c *gin.Context) {
	if err := h.svc.Delete(c.Param("id")); err != nil {
		_ = c.Error(err)
		return
	}
	c.JSON(http.StatusOK, dto.APIResponse{Success: true, Message: "Category deleted successfully"})
}
