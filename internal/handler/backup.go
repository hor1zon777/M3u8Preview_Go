// Package handler
// backup.go 对接 /api/v1/admin/backup/*：导出（直接 / SSE）、下载、上传、恢复（同步 / SSE）。
package handler

import (
	"encoding/json"
	"io"
	"net/http"
	"os"

	"github.com/gin-gonic/gin"

	"github.com/hor1zon777/m3u8-preview-go/internal/dto"
	"github.com/hor1zon777/m3u8-preview-go/internal/middleware"
	"github.com/hor1zon777/m3u8-preview-go/internal/service"
)

// BackupHandler 汇总 backup 端点。
type BackupHandler struct {
	svc *service.BackupService
}

// NewBackupHandler 构造。
func NewBackupHandler(svc *service.BackupService) *BackupHandler {
	return &BackupHandler{svc: svc}
}

// Register 在已经应用 authenticate + requireAdmin 的 group 上挂全部路由。
func (h *BackupHandler) Register(rg *gin.RouterGroup) {
	rg.GET("/export", h.exportDirect)
	rg.GET("/export/stream", h.exportStream)
	rg.GET("/download/:id", h.download)
	rg.POST("/import", h.importSync)
	rg.POST("/import/upload", h.importUpload)
	rg.GET("/import/stream/:id", h.importStream)
}

// exportDirect 同步打包 ZIP 到响应流。
func (h *BackupHandler) exportDirect(c *gin.Context) {
	includePosters := c.Query("includePosters") != "false"
	timestamp := nowISO()
	filename := "backup-" + timestamp + ".zip"
	c.Header("Content-Type", "application/zip")
	c.Header("Content-Disposition", `attachment; filename="`+filename+`"`)
	if err := h.svc.ExportToStream(c.Writer, includePosters); err != nil {
		_ = c.Error(err)
		return
	}
}

// exportStream SSE：按阶段推送进度，完成时带 downloadId。
func (h *BackupHandler) exportStream(c *gin.Context) {
	includePosters := c.Query("includePosters") != "false"
	setupSSEHeaders(c)

	send := func(v any) {
		raw, _ := json.Marshal(v)
		_, _ = c.Writer.Write([]byte("data: "))
		_, _ = c.Writer.Write(raw)
		_, _ = c.Writer.Write([]byte("\n\n"))
		c.Writer.Flush()
	}

	_, _, err := h.svc.ExportToFile(includePosters, func(p service.ExportProgress) {
		send(p)
	})
	if err != nil {
		send(service.ExportProgress{Phase: "error", Message: err.Error()})
		return
	}
}

// download 用 downloadId 取文件并流回；成功后清理。
func (h *BackupHandler) download(c *gin.Context) {
	id := c.Param("id")
	item, ok := h.svc.ConsumeDownload(id)
	if !ok {
		middleware.AbortWithAppError(c, middleware.NewAppError(http.StatusNotFound, "下载链接已过期或不存在"))
		return
	}
	f, err := os.Open(item.FilePath)
	if err != nil {
		middleware.AbortWithAppError(c, middleware.WrapAppError(http.StatusInternalServerError, "打开临时文件失败", err))
		return
	}
	defer func() { _ = f.Close() }()
	st, err := f.Stat()
	if err == nil {
		c.Header("Content-Length", int64ToStr(st.Size()))
	}
	c.Header("Content-Type", "application/zip")
	c.Header("Content-Disposition", `attachment; filename="`+item.Filename+`"`)
	if _, err := io.Copy(c.Writer, f); err != nil {
		// 客户端断开；仍尝试清理
	}
	h.svc.DeleteDownload(id)
}

// importSync 一步式：multipart 上传 → 立即恢复 → 返回结果（阻塞到完成）。
func (h *BackupHandler) importSync(c *gin.Context) {
	fh, err := c.FormFile("file")
	if err != nil {
		middleware.AbortWithAppError(c, middleware.NewAppError(http.StatusBadRequest, "请上传 ZIP 备份文件"))
		return
	}
	src, err := fh.Open()
	if err != nil {
		middleware.AbortWithAppError(c, middleware.WrapAppError(http.StatusBadRequest, "打开上传文件失败", err))
		return
	}
	tmp, err := os.CreateTemp("", "m3u8-restore-sync-*.zip")
	if err != nil {
		_ = src.Close()
		middleware.AbortWithAppError(c, middleware.WrapAppError(http.StatusInternalServerError, "创建临时文件失败", err))
		return
	}
	_, _ = io.Copy(tmp, src)
	_ = src.Close()
	_ = tmp.Close()
	defer func() { _ = os.Remove(tmp.Name()) }()

	res, err := h.svc.ImportFromFile(tmp.Name(), nil)
	if err != nil {
		_ = c.Error(err)
		return
	}
	c.JSON(http.StatusOK, dto.OK(res))
}

// importUpload 上传 ZIP 并返回 restoreId 供 SSE 触发。
func (h *BackupHandler) importUpload(c *gin.Context) {
	fh, err := c.FormFile("file")
	if err != nil {
		middleware.AbortWithAppError(c, middleware.NewAppError(http.StatusBadRequest, "请上传 ZIP 备份文件"))
		return
	}
	src, err := fh.Open()
	if err != nil {
		middleware.AbortWithAppError(c, middleware.WrapAppError(http.StatusBadRequest, "打开上传文件失败", err))
		return
	}
	defer func() { _ = src.Close() }()

	id, err := h.svc.SaveUploadedBackup(src)
	if err != nil {
		_ = c.Error(err)
		return
	}
	c.JSON(http.StatusOK, dto.OK(gin.H{"restoreId": id}))
}

// importStream 用 restoreId 触发恢复并发送 SSE 进度。
func (h *BackupHandler) importStream(c *gin.Context) {
	id := c.Param("id")
	path, ok := h.svc.ConsumeRestore(id)
	if !ok {
		middleware.AbortWithAppError(c, middleware.NewAppError(http.StatusNotFound, "恢复任务不存在或已过期"))
		return
	}
	defer func() { _ = os.Remove(path) }()
	setupSSEHeaders(c)

	send := func(v any) {
		raw, _ := json.Marshal(v)
		_, _ = c.Writer.Write([]byte("data: "))
		_, _ = c.Writer.Write(raw)
		_, _ = c.Writer.Write([]byte("\n\n"))
		c.Writer.Flush()
	}

	if _, err := h.svc.ImportFromFile(path, func(p service.BackupProgress) {
		send(p)
	}); err != nil {
		send(service.BackupProgress{Phase: "error", Message: err.Error()})
		return
	}
}

// ---- helpers ----

func setupSSEHeaders(c *gin.Context) {
	c.Header("Content-Type", "text/event-stream")
	c.Header("Cache-Control", "no-cache")
	c.Header("Connection", "keep-alive")
	c.Header("X-Accel-Buffering", "no")
	c.Writer.WriteHeader(http.StatusOK)
	c.Writer.Flush()
}

func int64ToStr(n int64) string {
	buf := make([]byte, 0, 20)
	if n == 0 {
		return "0"
	}
	neg := n < 0
	if neg {
		n = -n
	}
	for n > 0 {
		buf = append([]byte{byte('0' + n%10)}, buf...)
		n /= 10
	}
	if neg {
		buf = append([]byte{'-'}, buf...)
	}
	return string(buf)
}

func nowISO() string {
	return timeReplacer.Replace(nowRFCSeconds())
}
