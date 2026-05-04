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
	"time"

	"github.com/gin-gonic/gin"

	"github.com/hor1zon777/m3u8-preview-go/internal/dto"
	"github.com/hor1zon777/m3u8-preview-go/internal/middleware"
	"github.com/hor1zon777/m3u8-preview-go/internal/service"
)

// VTT 上传大小上限：10MB（半小时密集字幕约 100KB 量级，留足余量）
const maxVTTUploadBytes = 10 * 1024 * 1024

// audio-stream 上传大小上限：1 GB（覆盖 4 小时无损 FLAC）。
// 配合 broker 流式转发；body 直接是二进制 FLAC，不是 multipart。
const maxAudioStreamBytes = 1 * 1024 * 1024 * 1024

// audio-fetch-poll 客户端可指定的最大 hold 时长（秒）。
const (
	audioPollMinSec = 5
	audioPollMaxSec = 60
)

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
	// v3 分布式 worker broker 端点（替换 v2 落盘版）：
	//  - audio-ready：audio worker 完成本地 FLAC 后注册元数据（不传文件）
	//  - audio-fetch-poll：audio worker long-poll 等待 fetch / cleanup 指令
	//  - audio-stream：audio worker 收到 fetch 通知后流式上传 FLAC（broker 实时转发）
	//  - audio (GET)：subtitle worker 拉 FLAC，服务端 broker 桥接
	rg.POST("/jobs/:jobId/audio-ready", h.audioReady)
	rg.POST("/jobs/:jobId/audio-lost", h.audioLost)
	rg.POST("/audio-fetch-poll", h.audioFetchPoll)
	rg.POST("/jobs/:jobId/audio-stream", h.audioStream)
	rg.GET("/jobs/:jobId/audio", h.audioFetch)
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

// audioReady 是 v3 audio worker 完成本地 FLAC 后调用的端点。
//
// 与 v2 audio-complete 的差别：
//   - 不接收文件 body，只接收 meta JSON（workerId / size / sha256 / format / durationMs）
//   - audio worker 把 FLAC 留在本地，subtitle worker 拉取时通过 broker 实时桥接
//
// 错误响应：
//   - 400 invalid JSON / missing fields
//   - 409 stage 不允许（WORKER_AUDIO_NOT_READY）
//   - 410 ownership 不匹配（WORKER_JOB_NOT_OWNED）
func (h *SubtitleWorkerHandler) audioReady(c *gin.Context) {
	jobID := c.Param("jobId")
	if jobID == "" {
		middleware.AbortWithAppError(c, middleware.NewAppError(http.StatusBadRequest, "missing jobId"))
		return
	}
	var meta dto.WorkerAudioReadyMeta
	if err := c.ShouldBindJSON(&meta); err != nil {
		middleware.AbortWithAppError(c, middleware.NewAppError(http.StatusBadRequest, "invalid audio-ready payload: "+err.Error()))
		return
	}
	if err := h.svc.WorkerAudioReady(jobID, meta); err != nil {
		_ = c.Error(err)
		return
	}
	c.JSON(http.StatusOK, dto.APIResponse{Success: true})
}

// audioLost 是 v3.1 audio worker 在 long-poll 收到 broker fetch 通知后，发现本地
// FLAC 实际不在（被误删 / storage_dir 改动 / 索引损坏）时主动声明的端点。
//
// 与 audio-fail（fail 端点的复用）的差别：
//   - fail 端点要求 claimed_by == workerId，但 audio_ready 之后 claimed_by 已清空
//     （随即被 subtitle worker 重新占用），audio worker 无法走 fail 端点反馈丢失
//   - 本端点校验 audio_worker_id == workerId，让 FLAC owner 显式声明丢失
//
// 服务端响应：清空音频元数据 + claimed_by + subtitle_worker_id，stage 回 queued，
// 让任意 audio worker 重新跑全流程；subtitle worker 后续 fail/heartbeat 调用会因
// claimed_by 已清空被 410 优雅退出。
//
// 错误响应：
//   - 400 invalid JSON / missing fields
//   - 404 job not found
//   - 409 stage 不允许（必须是 audio_uploaded / asr / translate / writing 之一，
//         详见 service.audioLostAllowedStages）
//   - 410 audio_worker_id 不匹配请求方（WORKER_AUDIO_LOST_NOT_OWNED）
func (h *SubtitleWorkerHandler) audioLost(c *gin.Context) {
	jobID := c.Param("jobId")
	if jobID == "" {
		middleware.AbortWithAppError(c, middleware.NewAppError(http.StatusBadRequest, "missing jobId"))
		return
	}
	var req dto.WorkerAudioLostRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		middleware.AbortWithAppError(c, middleware.NewAppError(http.StatusBadRequest, "invalid audio-lost payload: "+err.Error()))
		return
	}
	if err := h.svc.WorkerAudioLost(jobID, req.WorkerID, req.ErrorMsg); err != nil {
		_ = c.Error(err)
		return
	}
	c.JSON(http.StatusOK, dto.APIResponse{Success: true})
}

// audioFetchPoll 是 audio worker 长轮询：阻塞 25s 等待服务端下发 fetch / cleanup 指令。
//
// 客户端 timeout 通过请求体的 timeoutSec 字段传入；服务端 clamp 到 [5, 60]。
// 没任务时返回 204 No Content；有任务时返回 200 + WorkerAudioFetchTask。
func (h *SubtitleWorkerHandler) audioFetchPoll(c *gin.Context) {
	var req dto.WorkerAudioFetchPollRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		middleware.AbortWithAppError(c, middleware.NewAppError(http.StatusBadRequest, "invalid poll payload"))
		return
	}
	timeout := time.Duration(req.TimeoutSec) * time.Second
	if req.TimeoutSec < audioPollMinSec {
		timeout = 0 // service 层会按默认 25s 处理
	} else if req.TimeoutSec > audioPollMaxSec {
		timeout = time.Duration(audioPollMaxSec) * time.Second
	}

	task, err := h.svc.WorkerAudioFetchPoll(req.WorkerID, timeout)
	if err != nil {
		_ = c.Error(err)
		return
	}
	if task == nil {
		c.Status(http.StatusNoContent)
		return
	}
	c.JSON(http.StatusOK, dto.OK(dto.WorkerAudioFetchTask{
		Action: task.Action,
		JobID:  task.JobID,
	}))
}

// audioStream 接收 audio worker 收到 fetch 通知后的二进制 FLAC 上传。
//
// Body 是裸 FLAC 字节流（不是 multipart）。服务端把 body 流式 io.Copy 到等待中的
// fetch coupling 的 pipe writer，subtitle worker 那侧的 GET response 实时收到流。
//
// Headers：
//   - X-Worker-Id：上传方 audio worker id（用于 ownership 校验）
//   - Content-Length：可选，服务端用作 LimitReader 的硬上限
//
// 错误响应：
//   - 400 missing workerId
//   - 403 不是该任务的 owner audio worker
//   - 410 没人在等这个 jobId 的 fetch
//   - 413 body > 1 GB
func (h *SubtitleWorkerHandler) audioStream(c *gin.Context) {
	jobID := c.Param("jobId")
	if jobID == "" {
		middleware.AbortWithAppError(c, middleware.NewAppError(http.StatusBadRequest, "missing jobId"))
		return
	}
	workerID := c.GetHeader("X-Worker-Id")
	if workerID == "" {
		// 兼容用 query string 传递的客户端
		workerID = c.Query("workerId")
	}
	if workerID == "" {
		middleware.AbortWithAppError(c, middleware.NewAppError(http.StatusBadRequest, "missing X-Worker-Id"))
		return
	}

	// 限制 body 总大小防 DoS（即使 audio worker 异常上传巨型 body 也不至于撑爆服务端）
	c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, maxAudioStreamBytes)

	if err := h.svc.WorkerAudioStreamReceive(jobID, workerID, c.Request.Body); err != nil {
		_ = c.Error(err)
		return
	}
	c.JSON(http.StatusOK, dto.APIResponse{Success: true})
}

// audioFetch 是 v3 subtitle worker GET /audio 的实现：
// 通过 broker 通知 owner audio worker 上传，把上传流实时 pipe 到当前 GET response。
//
// 与 v2 ServeFile 行为差别：
//   - 不再返回 Content-Length（流式 chunked）
//   - 不支持 Range（broker 一次性流式转发）
//   - audio worker 离线时 30s 内返回 503 / WORKER_AUDIO_OWNER_OFFLINE
//
// 错误响应：
//   - 400 missing workerId
//   - 403 not job owner
//   - 410 audio_worker_id 字段为空（任务还未进入 audio_uploaded）
//   - 503 audio worker offline
//   - 504 audio worker 收到通知但没及时上传
func (h *SubtitleWorkerHandler) audioFetch(c *gin.Context) {
	jobID := c.Param("jobId")
	if jobID == "" {
		middleware.AbortWithAppError(c, middleware.NewAppError(http.StatusBadRequest, "missing jobId"))
		return
	}
	workerID := c.Query("workerId")
	if workerID == "" {
		middleware.AbortWithAppError(c, middleware.NewAppError(http.StatusBadRequest, "missing workerId query"))
		return
	}

	// 提前设 header（broker pipe 开始前就发出 200 OK，避免客户端误把超时当 ECONN）
	c.Header("Content-Type", "audio/flac")
	c.Header("Cache-Control", "no-store")
	c.Header("Transfer-Encoding", "chunked")
	c.Status(http.StatusOK)
	// flush header 让客户端看到 200，避免客户端 reqwest timeout 在 broker 等 30s 时误以为连接挂了
	if flusher, ok := c.Writer.(http.Flusher); ok {
		flusher.Flush()
	}

	sha, err := h.svc.WorkerAudioFetchBroker(jobID, workerID, c.Writer)
	if err != nil {
		// 已经发了 200 + headers，无法再写 JSON 错误响应；只能记日志 + 直接关
		// 客户端会看到 EOF + 没有 ETag，自己处理重试
		_ = c.Error(err)
		return
	}
	// 全部传完才知道 SHA：broker.RequestFetch 返回前已经把 body io.Copy 完
	// 这里设 ETag 没用了（客户端已经读完 body 才看到 trailer）；如果未来需要可以改成 trailers 头
	_ = sha
}
