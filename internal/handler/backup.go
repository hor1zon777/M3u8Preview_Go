// Package handler
// backup.go 对接 /api/v1/admin/backup/*：导出（直接 / SSE）、下载、上传、恢复（同步 / SSE）。
package handler

import (
	"encoding/json"
	"io"
	"log"
	"net/http"
	"os"

	"github.com/gin-gonic/gin"

	"github.com/hor1zon777/m3u8-preview-go/internal/dto"
	"github.com/hor1zon777/m3u8-preview-go/internal/middleware"
	"github.com/hor1zon777/m3u8-preview-go/internal/service"
)

// maxBackupUploadBytes 限制上传备份 ZIP 的大小，防止磁盘填满 DoS。
// 对齐典型 admin 备份规模（数百 MB 级别）。如需更大可设 ENV，这里先硬编码 2 GiB。
const maxBackupUploadBytes int64 = 2 << 30

// BackupHandler 汇总 backup 端点。
type BackupHandler struct {
	svc *service.BackupService
}

// NewBackupHandler 构造。
func NewBackupHandler(svc *service.BackupService) *BackupHandler {
	return &BackupHandler{svc: svc}
}

// Register 在已经应用 authenticate + requireAdmin 的 group 上挂非 SSE 路由。
// SSE 路由由 RegisterSSE 注册，须走 AuthenticateSSE 中间件以识别 ?ticket=。
func (h *BackupHandler) Register(rg *gin.RouterGroup) {
	rg.GET("/export", h.exportDirect)
	rg.GET("/download/:id", h.download)
	rg.POST("/import", h.importSync)
	rg.POST("/import/upload", h.importUpload)
}

// RegisterSSE 注册需要 ?ticket= 认证的 EventSource 路由。
// 调用方应对此 group 使用 middleware.AuthenticateSSE + RequireRole("ADMIN")。
func (h *BackupHandler) RegisterSSE(rg *gin.RouterGroup) {
	rg.GET("/export/stream", h.exportStream)
	rg.GET("/import/stream/:id", h.importStream)
}

// exportDirect 同步打包 ZIP 到临时文件再返回，确保出错时不返回损坏数据。
func (h *BackupHandler) exportDirect(c *gin.Context) {
	includePosters := c.Query("includePosters") != "false"
	timestamp := nowISO()
	filename := "backup-" + timestamp + ".zip"

	tmp, err := os.CreateTemp("", "m3u8-export-*.zip")
	if err != nil {
		_ = c.Error(middleware.WrapAppError(http.StatusInternalServerError, "创建临时文件失败", err))
		return
	}
	defer func() { _ = os.Remove(tmp.Name()) }()

	if err := h.svc.ExportToStream(tmp, includePosters); err != nil {
		_ = tmp.Close()
		_ = c.Error(err)
		return
	}
	_ = tmp.Close()

	c.Header("Content-Disposition", `attachment; filename="`+filename+`"`)
	c.File(tmp.Name())
}

// exportStream SSE：按阶段推送进度，完成时带 downloadId。
// 客户端断开时 (c.Request.Context().Done()) 立即中止，避免白跑完整 DB 扫描 + ZIP 打包。
func (h *BackupHandler) exportStream(c *gin.Context) {
	includePosters := c.Query("includePosters") != "false"
	setupSSEHeaders(c)
	ctx := c.Request.Context()

	cancelled := false
	send := func(v any) {
		if cancelled {
			return
		}
		select {
		case <-ctx.Done():
			cancelled = true
			return
		default:
		}
		raw, _ := json.Marshal(v)
		if _, err := c.Writer.Write([]byte("data: ")); err != nil {
			cancelled = true
			return
		}
		if _, err := c.Writer.Write(raw); err != nil {
			cancelled = true
			return
		}
		if _, err := c.Writer.Write([]byte("\n\n")); err != nil {
			cancelled = true
			return
		}
		c.Writer.Flush()
	}

	_, _, err := h.svc.ExportToFile(includePosters, func(p service.ExportProgress) {
		send(p)
	})
	if cancelled {
		log.Printf("[backup] exportStream client disconnected, abort")
		return
	}
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
	// 无论后续 open/copy 是否成功，都必须清理 DB 中的 pendingDownload + 磁盘文件，
	// 否则 Open 失败的路径会导致临时 ZIP 永久残留。
	defer h.svc.DeleteDownload(id)
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
		_ = err
	}
}

// importSync 一步式：multipart 上传 → 立即恢复 → 返回结果（阻塞到完成）。
// 前置 size 校验 + LimitReader 双重防护，防止 100GB 恶意上传。
func (h *BackupHandler) importSync(c *gin.Context) {
	fh, err := c.FormFile("file")
	if err != nil {
		middleware.AbortWithAppError(c, middleware.NewAppError(http.StatusBadRequest, "请上传 ZIP 备份文件"))
		return
	}
	if fh.Size > maxBackupUploadBytes {
		middleware.AbortWithAppError(c, middleware.NewAppError(http.StatusRequestEntityTooLarge,
			"备份文件超过最大允许大小"))
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
	// LimitReader 拦截篡改 Content-Length 的攻击；+1 用于识别超限情况
	n, err := io.Copy(tmp, io.LimitReader(src, maxBackupUploadBytes+1))
	_ = src.Close()
	if err != nil {
		_ = tmp.Close()
		_ = os.Remove(tmp.Name())
		middleware.AbortWithAppError(c, middleware.WrapAppError(http.StatusInternalServerError, "写入临时文件失败", err))
		return
	}
	if n > maxBackupUploadBytes {
		_ = tmp.Close()
		_ = os.Remove(tmp.Name())
		middleware.AbortWithAppError(c, middleware.NewAppError(http.StatusRequestEntityTooLarge,
			"备份文件超过最大允许大小"))
		return
	}
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
	if fh.Size > maxBackupUploadBytes {
		middleware.AbortWithAppError(c, middleware.NewAppError(http.StatusRequestEntityTooLarge,
			"备份文件超过最大允许大小"))
		return
	}
	src, err := fh.Open()
	if err != nil {
		middleware.AbortWithAppError(c, middleware.WrapAppError(http.StatusBadRequest, "打开上传文件失败", err))
		return
	}
	defer func() { _ = src.Close() }()

	id, err := h.svc.SaveUploadedBackup(io.LimitReader(src, maxBackupUploadBytes+1))
	if err != nil {
		_ = c.Error(err)
		return
	}
	c.JSON(http.StatusOK, dto.OK(gin.H{"restoreId": id}))
}

// importStream 用 restoreId 触发恢复并发送 SSE 进度。
// 客户端断开时立即中止，避免白跑完整恢复流程。
func (h *BackupHandler) importStream(c *gin.Context) {
	id := c.Param("id")
	path, ok := h.svc.ConsumeRestore(id)
	if !ok {
		middleware.AbortWithAppError(c, middleware.NewAppError(http.StatusNotFound, "恢复任务不存在或已过期"))
		return
	}
	defer func() { _ = os.Remove(path) }()
	setupSSEHeaders(c)
	ctx := c.Request.Context()

	cancelled := false
	send := func(v any) {
		if cancelled {
			return
		}
		select {
		case <-ctx.Done():
			cancelled = true
			return
		default:
		}
		raw, _ := json.Marshal(v)
		if _, err := c.Writer.Write([]byte("data: ")); err != nil {
			cancelled = true
			return
		}
		if _, err := c.Writer.Write(raw); err != nil {
			cancelled = true
			return
		}
		if _, err := c.Writer.Write([]byte("\n\n")); err != nil {
			cancelled = true
			return
		}
		c.Writer.Flush()
	}

	if _, err := h.svc.ImportFromFile(path, func(p service.BackupProgress) {
		send(p)
	}); err != nil {
		if !cancelled {
			send(service.BackupProgress{Phase: "error", Message: err.Error()})
		}
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
