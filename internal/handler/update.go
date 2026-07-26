// update.go 实现应用内自更新端点（/api/v1/admin/update/*）。
//
// 这组路由与 /admin/ha 一样刻意不挂在 v1 组下（见 app.go）：滚动升级要求
// 先在 replica 节点上执行更新（apply 只写本机 /data 的暂存目录，不写 SQLite），
// 不能被 RequirePrimary 写闸门拦成 503，也不能被前端 api.ts 对 NODE_READ_ONLY
// 的自动重试静默重放。
package handler

import (
	"errors"
	"net/http"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/hor1zon777/m3u8-preview-go/internal/dto"
	"github.com/hor1zon777/m3u8-preview-go/internal/middleware"
	"github.com/hor1zon777/m3u8-preview-go/internal/update"
)

// 自更新的机器可读错误码。
const (
	CodeUpdateDisabled        = "UPDATE_DISABLED"
	CodeUpdateDevBuild        = "UPDATE_DEV_BUILD"
	CodeUpdateAlreadyRunning  = "UPDATE_ALREADY_RUNNING"
	CodeUpdateNoUpdate        = "UPDATE_NO_UPDATE"
	CodeUpdateVersionMismatch = "UPDATE_VERSION_MISMATCH"
	CodeUpdateRateLimited     = "UPDATE_RATE_LIMITED"
	CodeUpdateCheckFailed     = "UPDATE_CHECK_FAILED"
)

// UpdateHandler 自更新端点。
type UpdateHandler struct {
	mgr *update.Manager
}

// NewUpdateHandler 构造。
func NewUpdateHandler(mgr *update.Manager) *UpdateHandler {
	return &UpdateHandler{mgr: mgr}
}

// Register 挂载路由（调用方需已应用 Authenticate + RequireRole("ADMIN")）。
func (h *UpdateHandler) Register(rg *gin.RouterGroup) {
	rg.GET("/status", h.status)
	rg.POST("/check", h.check)
	rg.POST("/apply", h.apply)
}

// status 返回当前更新状态快照。
func (h *UpdateHandler) status(c *gin.Context) {
	c.JSON(http.StatusOK, dto.OK(buildUpdateStatus(h.mgr.Status())))
}

// check 强制检查一次更新（内部有 10s 节流），返回最新快照。
func (h *UpdateHandler) check(c *gin.Context) {
	snap, err := h.mgr.Check(c.Request.Context(), true)
	if err != nil {
		// 检查失败也返回快照（error 字段带原因），让前端一次拿全；
		// 但 disabled/dev 这类"根本不可用"仍走错误响应。
		if errors.Is(err, update.ErrDisabled) || errors.Is(err, update.ErrDevBuild) {
			middleware.AbortWithAppError(c, updateError(err))
			return
		}
	}
	c.JSON(http.StatusOK, dto.OK(buildUpdateStatus(snap)))
}

// apply 开始下载并暂存指定版本；全程异步，进度经 status 轮询获取。
func (h *UpdateHandler) apply(c *gin.Context) {
	var req dto.UpdateApplyRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		middleware.AbortWithAppError(c, middleware.NewAppError(http.StatusBadRequest, "缺少 version 字段"))
		return
	}
	if err := h.mgr.Apply(req.Version); err != nil {
		middleware.AbortWithAppError(c, updateError(err))
		return
	}
	c.JSON(http.StatusAccepted, dto.OK(buildUpdateStatus(h.mgr.Status())))
}

// updateError 把 update 包的前置校验错误映射为带 code 的 HTTP 错误。
func updateError(err error) *middleware.AppError {
	switch {
	case errors.Is(err, update.ErrDisabled):
		return middleware.NewAppErrorWithCode(http.StatusBadRequest, CodeUpdateDisabled, "自更新已被 UPDATE_DISABLED 关闭")
	case errors.Is(err, update.ErrDevBuild):
		return middleware.NewAppErrorWithCode(http.StatusBadRequest, CodeUpdateDevBuild, "开发构建不支持在线更新")
	case errors.Is(err, update.ErrAlreadyRunning):
		return middleware.NewAppErrorWithCode(http.StatusConflict, CodeUpdateAlreadyRunning, "更新已在进行中")
	case errors.Is(err, update.ErrNoUpdate):
		return middleware.NewAppErrorWithCode(http.StatusConflict, CodeUpdateNoUpdate, "没有可应用的新版本，请先检查更新")
	case errors.Is(err, update.ErrVersionMismatch):
		return middleware.NewAppErrorWithCode(http.StatusConflict, CodeUpdateVersionMismatch, "版本已变化，请重新检查后再试")
	case errors.Is(err, update.ErrRateLimited):
		return middleware.NewAppErrorWithCode(http.StatusTooManyRequests, CodeUpdateRateLimited, "GitHub API 限流，请稍后再试")
	default:
		return middleware.WrapAppError(http.StatusInternalServerError, "更新操作失败", err)
	}
}

// buildUpdateStatus 把内部快照转成响应 DTO。
func buildUpdateStatus(s update.Snapshot) dto.UpdateStatusResponse {
	out := dto.UpdateStatusResponse{
		CurrentVersion: s.CurrentVersion,
		Commit:         s.Commit,
		Enabled:        s.Enabled,
		DisabledReason: s.DisabledReason,
		State:          string(s.State),
	}
	if !s.LastCheckedAt.IsZero() {
		out.LastCheckedAt = s.LastCheckedAt.UTC().Format(time.RFC3339)
	}
	if s.Latest != nil {
		out.Latest = &dto.UpdateReleaseInfo{
			Version:   s.Latest.Version,
			Notes:     s.Latest.Notes,
			AssetSize: s.Latest.AssetSize,
		}
		if !s.Latest.PublishedAt.IsZero() {
			out.Latest.PublishedAt = s.Latest.PublishedAt.UTC().Format(time.RFC3339)
		}
	}
	if s.State == update.StateDownloading || s.State == update.StateVerifying {
		out.Progress = &dto.UpdateProgress{
			DownloadedBytes: s.Progress.Downloaded,
			TotalBytes:      s.Progress.Total,
		}
	}
	if s.Staged != nil {
		out.Staged = &dto.UpdateStagedInfo{
			Version:  s.Staged.Version,
			StagedAt: s.Staged.StagedAt.UTC().Format(time.RFC3339),
		}
	}
	if s.Err != nil {
		out.Error = &dto.UpdateErrorInfo{Code: s.Err.Code, Message: s.Err.Message}
	}
	return out
}
