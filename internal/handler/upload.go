// Package handler
// upload.go 对接 POST /api/v1/upload/poster（管理员上传封面）。
package handler

import (
	"net/http"

	"github.com/gin-gonic/gin"

	"github.com/hor1zon777/m3u8-preview-go/internal/dto"
	"github.com/hor1zon777/m3u8-preview-go/internal/middleware"
	"github.com/hor1zon777/m3u8-preview-go/internal/service"
)

// UploadHandler 汇总 upload 端点。
type UploadHandler struct {
	svc *service.UploadService
}

// NewUploadHandler 构造。
func NewUploadHandler(svc *service.UploadService) *UploadHandler {
	return &UploadHandler{svc: svc}
}

// RegisterAdmin 挂 /upload/poster。
func (h *UploadHandler) RegisterAdmin(rg *gin.RouterGroup) {
	rg.POST("/poster", h.uploadPoster)
}

func (h *UploadHandler) uploadPoster(c *gin.Context) {
	fh, err := c.FormFile("poster")
	if err != nil {
		middleware.AbortWithAppError(c, middleware.NewAppError(http.StatusBadRequest, "No file uploaded"))
		return
	}
	url, err := h.svc.SavePoster(fh)
	if err != nil {
		_ = c.Error(err)
		return
	}
	c.JSON(http.StatusCreated, dto.OK(dto.UploadPosterResponse{URL: url}))
}
