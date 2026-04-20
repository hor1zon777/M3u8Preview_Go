// Package handler
// import.go 对接 /api/v1/import/*：preview / execute / logs / template/:format。
package handler

import (
	"io"
	"net/http"
	"strconv"

	"github.com/gin-gonic/gin"

	"github.com/hor1zon777/m3u8-preview-go/internal/dto"
	"github.com/hor1zon777/m3u8-preview-go/internal/middleware"
	"github.com/hor1zon777/m3u8-preview-go/internal/service"
)

// 最大 preview / execute 接收的上传体积。
const maxImportFileSize = 10 * 1024 * 1024

// ImportHandler 汇总 import 端点。
type ImportHandler struct {
	svc *service.ImportService
}

// NewImportHandler 构造。
func NewImportHandler(svc *service.ImportService) *ImportHandler {
	return &ImportHandler{svc: svc}
}

// RegisterPublic 挂载模板下载（不需要登录，与 TS 原版一致）。
func (h *ImportHandler) RegisterPublic(rg *gin.RouterGroup) {
	rg.GET("/template/:format", h.template)
}

// RegisterAdmin 挂载管理员端点。
func (h *ImportHandler) RegisterAdmin(rg *gin.RouterGroup) {
	rg.POST("/preview", h.preview)
	rg.POST("/execute", h.execute)
	rg.GET("/logs", h.logs)
}

// preview 支持两种输入：multipart 文件 或 JSON body（{format, content}）。
func (h *ImportHandler) preview(c *gin.Context) {
	// multipart 优先
	if fh, err := c.FormFile("file"); err == nil && fh != nil {
		if fh.Size > maxImportFileSize {
			middleware.AbortWithAppError(c, middleware.NewAppError(http.StatusRequestEntityTooLarge, "文件超出最大大小"))
			return
		}
		src, err := fh.Open()
		if err != nil {
			middleware.AbortWithAppError(c, middleware.WrapAppError(http.StatusBadRequest, "打开上传文件失败", err))
			return
		}
		defer func() { _ = src.Close() }()
		data, err := io.ReadAll(io.LimitReader(src, maxImportFileSize+1))
		if err != nil {
			middleware.AbortWithAppError(c, middleware.WrapAppError(http.StatusBadRequest, "读取上传文件失败", err))
			return
		}
		if int64(len(data)) > maxImportFileSize {
			middleware.AbortWithAppError(c, middleware.NewAppError(http.StatusRequestEntityTooLarge, "文件超出最大大小"))
			return
		}
		items, format, err := h.svc.DetectAndParseFile(fh.Filename, data)
		if err != nil {
			_ = c.Error(err)
			return
		}
		if len(items) > 1000 {
			middleware.AbortWithAppError(c, middleware.NewAppError(http.StatusBadRequest, "Maximum 1000 items per import"))
			return
		}
		resp := h.svc.Preview(items)
		resp.Format = format
		resp.FileName = fh.Filename
		c.JSON(http.StatusOK, dto.OK(resp))
		return
	}

	// 无 file：读 JSON body
	var body struct {
		Format  string `json:"format"`
		Content string `json:"content"`
	}
	if err := c.ShouldBindJSON(&body); err != nil {
		middleware.AbortWithAppError(c, middleware.NewAppError(http.StatusBadRequest, "No file or content provided"))
		return
	}
	items, format, err := h.svc.DetectAndParseBody(body.Format, body.Content)
	if err != nil {
		_ = c.Error(err)
		return
	}
	if len(items) > 1000 {
		middleware.AbortWithAppError(c, middleware.NewAppError(http.StatusBadRequest, "Maximum 1000 items per import"))
		return
	}
	resp := h.svc.Preview(items)
	resp.Format = format
	c.JSON(http.StatusOK, dto.OK(resp))
}

func (h *ImportHandler) execute(c *gin.Context) {
	var req dto.ImportExecuteRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		middleware.AbortWithAppError(c, bindErrorToAppError(err))
		return
	}
	if len(req.Items) == 0 {
		middleware.AbortWithAppError(c, middleware.NewAppError(http.StatusBadRequest, "Items array is required"))
		return
	}
	if len(req.Items) > 1000 {
		middleware.AbortWithAppError(c, middleware.NewAppError(http.StatusBadRequest, "Maximum 1000 items per import"))
		return
	}

	uid := middleware.CurrentUserID(c)
	format := req.Format
	if format == "" {
		format = "TEXT"
	}
	result, err := h.svc.Execute(uid, req.Items, format, req.FileName)
	if err != nil {
		_ = c.Error(err)
		return
	}
	c.JSON(http.StatusOK, dto.OK(result))
}

func (h *ImportHandler) logs(c *gin.Context) {
	limit := 50
	if raw := c.Query("limit"); raw != "" {
		if n, err := strconv.Atoi(raw); err == nil {
			limit = n
		}
	}
	logs, err := h.svc.GetLogs(limit)
	if err != nil {
		_ = c.Error(err)
		return
	}
	c.JSON(http.StatusOK, dto.OK(logs))
}

// template 仅支持 csv / json。
func (h *ImportHandler) template(c *gin.Context) {
	switch c.Param("format") {
	case "csv":
		c.Header("Content-Type", "text/csv; charset=utf-8")
		c.Header("Content-Disposition", `attachment; filename="import-template.csv"`)
		c.String(http.StatusOK, h.svc.TemplateCSV())
	case "json":
		c.Header("Content-Type", "application/json; charset=utf-8")
		c.Header("Content-Disposition", `attachment; filename="import-template.json"`)
		c.String(http.StatusOK, h.svc.TemplateJSON())
	default:
		middleware.AbortWithAppError(c, middleware.NewAppError(http.StatusBadRequest, "Unsupported template format. Use csv or json."))
	}
}
