// Package handler
// subtitle.go 对接 /api/v1/subtitle/* 与 /api/v1/admin/subtitle/* 端点。
//
// 端点列表：
//
//	公开（需登录）:
//	  GET  /api/v1/subtitle/:mediaId/status        查询字幕生成状态
//	  GET  /api/v1/subtitle/:mediaId.vtt           带 HMAC 签名拉取 VTT
//
//	Admin（需 ADMIN 角色）:
//	  GET    /api/v1/admin/subtitle/jobs                  分页列表
//	  GET    /api/v1/admin/subtitle/jobs/:mediaId         详情
//	  POST   /api/v1/admin/subtitle/jobs/:mediaId/retry   重新生成
//	  DELETE /api/v1/admin/subtitle/jobs/:mediaId         删除任务
//	  PUT    /api/v1/admin/subtitle/jobs/:mediaId/disabled?value=true|false
//	  POST   /api/v1/admin/subtitle/jobs/batch-regenerate
//	  GET    /api/v1/admin/subtitle/queue                 队列概况
//	  GET    /api/v1/admin/subtitle/settings              配置回显（脱敏）
package handler

import (
	"errors"
	"net/http"
	"strconv"

	"github.com/gin-gonic/gin"

	"github.com/hor1zon777/m3u8-preview-go/internal/dto"
	"github.com/hor1zon777/m3u8-preview-go/internal/middleware"
	"github.com/hor1zon777/m3u8-preview-go/internal/service"
)

// SubtitleHandler 汇总字幕端点。
type SubtitleHandler struct {
	svc *service.SubtitleService
}

// NewSubtitleHandler 构造。
func NewSubtitleHandler(svc *service.SubtitleService) *SubtitleHandler {
	return &SubtitleHandler{svc: svc}
}

// RegisterAuthed 挂登录用户可用的端点（在已应用 Authenticate 中间件的 RouterGroup 上）。
func (h *SubtitleHandler) RegisterAuthed(rg *gin.RouterGroup) {
	rg.GET("/:mediaId/status", h.getStatus)
}

// RegisterPublic 挂无需 Bearer token 的端点（VTT 文件由 <track> 请求加载，
// 浏览器不会带 Authorization；改用 HMAC 签名 URL 鉴权）。
func (h *SubtitleHandler) RegisterPublic(rg *gin.RouterGroup) {
	rg.GET("/vtt/:mediaId", h.getVTT)
}

// RegisterAdmin 挂 admin 端点。
func (h *SubtitleHandler) RegisterAdmin(rg *gin.RouterGroup) {
	rg.GET("/jobs", h.listJobs)
	rg.GET("/queue", h.queueStatus)
	rg.GET("/settings", h.settings)
	rg.PUT("/settings", h.updateSettings)
	rg.POST("/jobs/batch-regenerate", h.batchRegenerate)
	rg.GET("/jobs/:mediaId", h.getJob)
	rg.POST("/jobs/:mediaId/retry", h.retry)
	rg.DELETE("/jobs/:mediaId", h.deleteJob)
	rg.PUT("/jobs/:mediaId/disabled", h.setDisabled)

	// 远程 worker 管理
	rg.GET("/workers", h.listWorkers)
	rg.GET("/worker-tokens", h.listWorkerTokens)
	rg.POST("/worker-tokens", h.createWorkerToken)
	rg.PUT("/worker-tokens/:id", h.updateWorkerToken)
	rg.DELETE("/worker-tokens/:id", h.revokeWorkerToken)
}

// getStatus 公开（需登录）状态查询。
func (h *SubtitleHandler) getStatus(c *gin.Context) {
	if !h.svc.Enabled() {
		c.JSON(http.StatusOK, dto.OK(&dto.SubtitleStatusResponse{
			MediaID: c.Param("mediaId"),
			Status:  "DISABLED",
		}))
		return
	}
	mediaID := c.Param("mediaId")
	if mediaID == "" {
		middleware.AbortWithAppError(c, middleware.NewAppError(http.StatusBadRequest, "缺少 mediaId"))
		return
	}
	uid := middleware.CurrentUserID(c)
	st, err := h.svc.GetStatus(mediaID, uid)
	if err != nil {
		_ = c.Error(err)
		return
	}
	c.JSON(http.StatusOK, dto.OK(st))
}

// getVTT 输出 WebVTT 文本，受 HMAC 签名保护。
func (h *SubtitleHandler) getVTT(c *gin.Context) {
	if !h.svc.Enabled() {
		c.Status(http.StatusServiceUnavailable)
		return
	}
	mediaID := c.Param("mediaId")
	uid := c.Query("u")
	expires := c.Query("expires")
	sig := c.Query("sig")
	if mediaID == "" || uid == "" || expires == "" || sig == "" {
		middleware.AbortWithAppError(c, middleware.NewAppError(http.StatusBadRequest, "缺少必要参数"))
		return
	}

	c.Header("Content-Type", "text/vtt; charset=utf-8")
	c.Header("Cache-Control", "private, max-age=600")
	c.Header("X-Content-Type-Options", "nosniff")

	code, err := h.svc.ServeVTT(mediaID, uid, expires, sig, c.Writer)
	if err != nil && code != http.StatusOK {
		// 已经写了 header，但还没写 body 的场景：用 AbortWithStatus 即可
		c.AbortWithStatus(code)
		return
	}
}

// ---- admin handlers ----

func (h *SubtitleHandler) listJobs(c *gin.Context) {
	page, _ := strconv.Atoi(c.DefaultQuery("page", "1"))
	limit, _ := strconv.Atoi(c.DefaultQuery("limit", "20"))
	statusFilter := c.Query("status")
	search := c.Query("search")
	categoryID := c.Query("categoryId")

	items, total, err := h.svc.ListJobs(page, limit, statusFilter, search, categoryID)
	if err != nil {
		_ = c.Error(err)
		return
	}
	c.JSON(http.StatusOK, dto.Paginated(items, total, page, limit))
}

func (h *SubtitleHandler) queueStatus(c *gin.Context) {
	st, err := h.svc.QueueStatus()
	if err != nil {
		_ = c.Error(err)
		return
	}
	c.JSON(http.StatusOK, dto.OK(st))
}

func (h *SubtitleHandler) settings(c *gin.Context) {
	c.JSON(http.StatusOK, dto.OK(h.svc.CurrentSettings()))
}

// updateSettings 接受 admin 提交的字幕配置 patch；
// 校验失败返回 400，写库失败返回 500，成功回显新配置（脱敏 api key）。
func (h *SubtitleHandler) updateSettings(c *gin.Context) {
	var req dto.SubtitleSettingsUpdateRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		middleware.AbortWithAppError(c, middleware.NewAppError(http.StatusBadRequest, "请求格式错误"))
		return
	}
	resp, err := h.svc.UpdateSettings(req)
	if err != nil {
		// applySubtitlePatch 的校验错误是 user-facing，直接 400 回显
		middleware.AbortWithAppError(c, middleware.NewAppError(http.StatusBadRequest, err.Error()))
		return
	}
	c.JSON(http.StatusOK, dto.OK(resp))
}

func (h *SubtitleHandler) getJob(c *gin.Context) {
	mediaID := c.Param("mediaId")
	job, err := h.svc.GetJob(mediaID)
	if err != nil {
		middleware.AbortWithAppError(c, middleware.NewAppError(http.StatusNotFound, "任务不存在"))
		return
	}
	c.JSON(http.StatusOK, dto.OK(job))
}

func (h *SubtitleHandler) retry(c *gin.Context) {
	mediaID := c.Param("mediaId")
	if err := h.svc.Retry(mediaID); err != nil {
		if errors.Is(err, service.ErrSubtitleDisabled) {
			middleware.AbortWithAppError(c, middleware.NewAppError(http.StatusServiceUnavailable, "字幕功能未启用"))
			return
		}
		_ = c.Error(err)
		return
	}
	c.JSON(http.StatusOK, dto.APIResponse{Success: true, Message: "已重新入队"})
}

func (h *SubtitleHandler) deleteJob(c *gin.Context) {
	mediaID := c.Param("mediaId")
	if err := h.svc.Delete(mediaID); err != nil {
		_ = c.Error(err)
		return
	}
	c.JSON(http.StatusOK, dto.APIResponse{Success: true, Message: "已删除"})
}

func (h *SubtitleHandler) setDisabled(c *gin.Context) {
	mediaID := c.Param("mediaId")
	value := c.Query("value")
	disabled := value == "true" || value == "1"
	if err := h.svc.SetDisabled(mediaID, disabled); err != nil {
		_ = c.Error(err)
		return
	}
	c.JSON(http.StatusOK, dto.APIResponse{Success: true})
}

func (h *SubtitleHandler) batchRegenerate(c *gin.Context) {
	var req dto.SubtitleBatchRegenerateRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		middleware.AbortWithAppError(c, middleware.NewAppError(http.StatusBadRequest, "请求格式错误"))
		return
	}
	resp, err := h.svc.BatchRegenerate(req)
	if err != nil {
		if errors.Is(err, service.ErrSubtitleDisabled) {
			middleware.AbortWithAppError(c, middleware.NewAppError(http.StatusServiceUnavailable, "字幕功能未启用"))
			return
		}
		_ = c.Error(err)
		return
	}
	c.JSON(http.StatusOK, dto.OK(resp))
}

// ---- 远程 worker 管理 ----

func (h *SubtitleHandler) listWorkers(c *gin.Context) {
	items, err := h.svc.ListOnlineWorkers()
	if err != nil {
		_ = c.Error(err)
		return
	}
	c.JSON(http.StatusOK, dto.OK(items))
}

func (h *SubtitleHandler) listWorkerTokens(c *gin.Context) {
	items, err := h.svc.ListWorkerTokens()
	if err != nil {
		_ = c.Error(err)
		return
	}
	c.JSON(http.StatusOK, dto.OK(items))
}

// createWorkerToken 仅本次返回明文 token；面板必须立即让用户复制。
func (h *SubtitleHandler) createWorkerToken(c *gin.Context) {
	var req dto.SubtitleWorkerTokenCreateRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		middleware.AbortWithAppError(c, middleware.NewAppError(http.StatusBadRequest, "请求格式错误"))
		return
	}
	plaintext, rec, err := h.svc.CreateWorkerToken(req.Name, req.MaxConcurrency)
	if err != nil {
		_ = c.Error(err)
		return
	}
	c.JSON(http.StatusOK, dto.OK(dto.SubtitleWorkerTokenCreateResponse{
		Token:  plaintext,
		Record: *rec,
	}))
}

// updateWorkerToken 修改 token 字段（目前仅 maxConcurrency）。
func (h *SubtitleHandler) updateWorkerToken(c *gin.Context) {
	id := c.Param("id")
	if id == "" {
		middleware.AbortWithAppError(c, middleware.NewAppError(http.StatusBadRequest, "missing id"))
		return
	}
	var req dto.SubtitleWorkerTokenUpdateRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		middleware.AbortWithAppError(c, middleware.NewAppError(http.StatusBadRequest, "请求格式错误"))
		return
	}
	rec, err := h.svc.UpdateWorkerToken(id, req)
	if err != nil {
		switch {
		case errors.Is(err, service.ErrWorkerTokenNotFound):
			middleware.AbortWithAppError(c, middleware.NewAppError(http.StatusNotFound, "token 不存在"))
		case errors.Is(err, service.ErrWorkerTokenAlreadyRev):
			middleware.AbortWithAppError(c, middleware.NewAppError(http.StatusConflict, "token 已吊销，不可编辑"))
		default:
			_ = c.Error(err)
		}
		return
	}
	c.JSON(http.StatusOK, dto.OK(rec))
}

func (h *SubtitleHandler) revokeWorkerToken(c *gin.Context) {
	id := c.Param("id")
	if id == "" {
		middleware.AbortWithAppError(c, middleware.NewAppError(http.StatusBadRequest, "missing id"))
		return
	}
	if err := h.svc.RevokeWorkerToken(id); err != nil {
		switch {
		case errors.Is(err, service.ErrWorkerTokenNotFound):
			middleware.AbortWithAppError(c, middleware.NewAppError(http.StatusNotFound, "token 不存在"))
		case errors.Is(err, service.ErrWorkerTokenAlreadyRev):
			middleware.AbortWithAppError(c, middleware.NewAppError(http.StatusConflict, "token 已吊销"))
		default:
			_ = c.Error(err)
		}
		return
	}
	c.JSON(http.StatusOK, dto.APIResponse{Success: true, Message: "已吊销"})
}
