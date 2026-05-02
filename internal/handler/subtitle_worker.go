// Package handler
// subtitle_worker.go 暴露 /api/v1/worker/* 端点给远程字幕 worker。
//
// 端点契约：
//
//	所有端点均需 Authorization: Bearer mwt_xxx（通过 RequireWorkerAuth 中间件）
//
//	POST /api/v1/worker/register
//	  Body: {workerId, name, version?, gpu?}
//	  Resp: {workerId, serverTime, workerStaleThreshold}
//
//	POST /api/v1/worker/claim
//	  Body: {workerId}
//	  Resp: 200 + ClaimedJob | 204 No Content
//
//	POST /api/v1/worker/jobs/:jobId/heartbeat
//	  Body: {workerId, stage, progress}
//	  Resp: {success: true}
//
//	POST /api/v1/worker/jobs/:jobId/complete  (multipart)
//	  Form: meta=<json string>, vtt=<file>
//	  Resp: {success: true}
//
//	POST /api/v1/worker/jobs/:jobId/fail
//	  Body: {workerId, errorMsg}
//	  Resp: {success: true}
//
//	POST /api/v1/worker/jobs/:mediaId/retry
//	  Body: 空
//	  Resp: {success: true}
//	  作用：把 failed/done 任务重置为 PENDING；不存在则按 EnsureJob 创建。
//	  注意 path 用 mediaId 而非 jobId（与 admin 接口对齐 Retry 语义）。
package handler

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"

	"github.com/gin-gonic/gin"

	"github.com/hor1zon777/m3u8-preview-go/internal/dto"
	"github.com/hor1zon777/m3u8-preview-go/internal/middleware"
	"github.com/hor1zon777/m3u8-preview-go/internal/service"
)

// VTT 上传大小上限：10MB（半小时密集字幕约 100KB 量级，留足余量）
const maxVTTUploadBytes = 10 * 1024 * 1024

// SubtitleWorkerHandler 远程字幕 worker 端点处理器。
type SubtitleWorkerHandler struct {
	svc *service.SubtitleService
}

// NewSubtitleWorkerHandler 构造。
func NewSubtitleWorkerHandler(svc *service.SubtitleService) *SubtitleWorkerHandler {
	return &SubtitleWorkerHandler{svc: svc}
}

// Register 挂全部端点到已应用 RequireWorkerAuth 的 RouterGroup。
func (h *SubtitleWorkerHandler) Register(rg *gin.RouterGroup) {
	rg.POST("/register", h.register)
	rg.POST("/claim", h.claim)
	rg.POST("/jobs/:jobId/heartbeat", h.heartbeat)
	rg.POST("/jobs/:jobId/complete", h.complete)
	rg.POST("/jobs/:jobId/fail", h.fail)
	// retry 用 mediaId 维度，跟 admin /jobs/:mediaId/retry 一致
	rg.POST("/media/:mediaId/retry", h.retry)
}

func (h *SubtitleWorkerHandler) register(c *gin.Context) {
	tokenID := middleware.CurrentWorkerTokenID(c)
	if tokenID == "" {
		middleware.AbortWithAppError(c, middleware.NewAppError(http.StatusUnauthorized, "missing worker token"))
		return
	}

	var req dto.WorkerRegisterRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		middleware.AbortWithAppError(c, middleware.NewAppError(http.StatusBadRequest, "invalid register payload"))
		return
	}

	resp, err := h.svc.RegisterWorker(tokenID, req)
	if err != nil {
		_ = c.Error(err)
		return
	}
	c.JSON(http.StatusOK, dto.OK(resp))
}

func (h *SubtitleWorkerHandler) claim(c *gin.Context) {
	var req dto.WorkerClaimRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		middleware.AbortWithAppError(c, middleware.NewAppError(http.StatusBadRequest, "invalid claim payload"))
		return
	}

	job, err := h.svc.ClaimNextJob(req.WorkerID)
	if err != nil {
		_ = c.Error(err)
		return
	}
	if job == nil {
		// 没有 PENDING 任务：204 让 worker 直接 sleep
		c.Status(http.StatusNoContent)
		return
	}
	c.JSON(http.StatusOK, dto.OK(job))
}

func (h *SubtitleWorkerHandler) heartbeat(c *gin.Context) {
	jobID := c.Param("jobId")
	if jobID == "" {
		middleware.AbortWithAppError(c, middleware.NewAppError(http.StatusBadRequest, "missing jobId"))
		return
	}

	var req dto.WorkerHeartbeatRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		middleware.AbortWithAppError(c, middleware.NewAppError(http.StatusBadRequest, "invalid heartbeat payload"))
		return
	}

	if err := h.svc.WorkerHeartbeat(jobID, req.WorkerID, req.Stage, req.Progress); err != nil {
		// ownership 不匹配返 410 Gone（任务已被 stale 回收 / 别的 worker 接走）
		if errors.Is(err, service.ErrWorkerJobNotOwned) {
			middleware.AbortWithAppError(c, middleware.NewAppError(http.StatusGone, "job no longer owned by this worker"))
			return
		}
		_ = c.Error(err)
		return
	}
	c.JSON(http.StatusOK, dto.APIResponse{Success: true})
}

// complete 接收 multipart：
//   - 字段 "meta"：WorkerCompleteMeta JSON 字符串
//   - 字段 "vtt"：VTT 文件（<= 10MB）
func (h *SubtitleWorkerHandler) complete(c *gin.Context) {
	jobID := c.Param("jobId")
	if jobID == "" {
		middleware.AbortWithAppError(c, middleware.NewAppError(http.StatusBadRequest, "missing jobId"))
		return
	}

	// 限制 multipart 总大小
	c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, maxVTTUploadBytes+1024)

	metaStr := c.PostForm("meta")
	if metaStr == "" {
		middleware.AbortWithAppError(c, middleware.NewAppError(http.StatusBadRequest, "missing meta field"))
		return
	}
	var meta dto.WorkerCompleteMeta
	if err := json.Unmarshal([]byte(metaStr), &meta); err != nil {
		middleware.AbortWithAppError(c, middleware.NewAppError(http.StatusBadRequest, "invalid meta json"))
		return
	}
	if meta.WorkerID == "" {
		middleware.AbortWithAppError(c, middleware.NewAppError(http.StatusBadRequest, "meta.workerId required"))
		return
	}

	fileHeader, err := c.FormFile("vtt")
	if err != nil {
		middleware.AbortWithAppError(c, middleware.NewAppError(http.StatusBadRequest, "missing vtt file"))
		return
	}
	if fileHeader.Size > maxVTTUploadBytes {
		middleware.AbortWithAppError(c, middleware.NewAppError(http.StatusRequestEntityTooLarge, "vtt file too large"))
		return
	}
	f, err := fileHeader.Open()
	if err != nil {
		_ = c.Error(err)
		return
	}
	defer func() { _ = f.Close() }()
	body, err := io.ReadAll(io.LimitReader(f, maxVTTUploadBytes+1))
	if err != nil {
		_ = c.Error(err)
		return
	}
	if int64(len(body)) > maxVTTUploadBytes {
		middleware.AbortWithAppError(c, middleware.NewAppError(http.StatusRequestEntityTooLarge, "vtt file too large"))
		return
	}

	if err := h.svc.WorkerComplete(jobID, meta, body); err != nil {
		switch {
		case errors.Is(err, service.ErrWorkerJobNotOwned):
			middleware.AbortWithAppError(c, middleware.NewAppError(http.StatusGone, "job no longer owned by this worker"))
		case errors.Is(err, service.ErrWorkerJobNotRunning):
			middleware.AbortWithAppError(c, middleware.NewAppError(http.StatusConflict, "job not in RUNNING state"))
		default:
			_ = c.Error(err)
		}
		return
	}
	c.JSON(http.StatusOK, dto.APIResponse{Success: true})
}

func (h *SubtitleWorkerHandler) fail(c *gin.Context) {
	jobID := c.Param("jobId")
	if jobID == "" {
		middleware.AbortWithAppError(c, middleware.NewAppError(http.StatusBadRequest, "missing jobId"))
		return
	}

	var req dto.WorkerFailRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		middleware.AbortWithAppError(c, middleware.NewAppError(http.StatusBadRequest, "invalid fail payload"))
		return
	}

	if err := h.svc.WorkerFail(jobID, req.WorkerID, req.ErrorMsg); err != nil {
		if errors.Is(err, service.ErrWorkerJobNotOwned) {
			middleware.AbortWithAppError(c, middleware.NewAppError(http.StatusGone, "job no longer owned by this worker"))
			return
		}
		_ = c.Error(err)
		return
	}
	c.JSON(http.StatusOK, dto.APIResponse{Success: true})
}

// retry 把 failed / done job 重置为 PENDING；不存在则用 EnsureJob 创建。
// 复用 admin Retry 逻辑（worker token 已经是高权限凭据，允许触发重试）。
func (h *SubtitleWorkerHandler) retry(c *gin.Context) {
	mediaID := c.Param("mediaId")
	if mediaID == "" {
		middleware.AbortWithAppError(c, middleware.NewAppError(http.StatusBadRequest, "missing mediaId"))
		return
	}
	if err := h.svc.Retry(mediaID); err != nil {
		if errors.Is(err, service.ErrSubtitleDisabled) {
			middleware.AbortWithAppError(c, middleware.NewAppError(http.StatusServiceUnavailable, "字幕功能未启用"))
			return
		}
		_ = c.Error(err)
		return
	}
	c.JSON(http.StatusOK, dto.APIResponse{Success: true})
}
