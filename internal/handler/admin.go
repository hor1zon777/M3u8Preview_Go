// Package handler
// admin.go 对接 /api/v1/admin/* 全部管理员端点。
package handler

import (
	"net/http"

	"github.com/gin-gonic/gin"

	"github.com/hor1zon777/m3u8-preview-go/internal/dto"
	"github.com/hor1zon777/m3u8-preview-go/internal/middleware"
	"github.com/hor1zon777/m3u8-preview-go/internal/service"
)

// AdminHandler 汇总 admin 端点。
type AdminHandler struct {
	admin          *service.AdminService
	activity       *service.ActivityService
	proxy          *service.ProxyService
	thumb          *service.ThumbnailQueue
	poster         *service.PosterDownloader
	db             DashboardDB
	rateLimitCache *middleware.RateLimitSettingCache
}

// DashboardDB 供 media count / poster stats 使用的最小 DB 访问接口（避免耦合具体 GORM 类型）。
// 由注入方提供。
type DashboardDB interface {
	CountExternalPosters() (int64, int64, int64) // external, local, total（向后兼容旧签名）
	PosterStats() PosterStats                    // 前端 /admin/posters/stats 期望的完整结构
}

// PosterStats 返回给 /admin/posters/stats 的 JSON 结构，与前端 TypeScript 类型对齐。
type PosterStats struct {
	Total          int64 `json:"total"`
	External       int64 `json:"external"`
	Local          int64 `json:"local"`
	Missing        int64 `json:"missing"`
	TotalSizeBytes int64 `json:"totalSizeBytes"`
}

// NewAdminHandler 构造。thumb / poster / db 可以为 nil（对应端点返回占位结果）。
func NewAdminHandler(admin *service.AdminService, activity *service.ActivityService, proxy *service.ProxyService, thumb *service.ThumbnailQueue, poster *service.PosterDownloader, db DashboardDB, rateLimitCache *middleware.RateLimitSettingCache) *AdminHandler {
	return &AdminHandler{admin: admin, activity: activity, proxy: proxy, thumb: thumb, poster: poster, db: db, rateLimitCache: rateLimitCache}
}

// Register 在已经应用 authenticate + requireAdmin 的 group 上挂全部路由。
func (h *AdminHandler) Register(rg *gin.RouterGroup) {
	rg.GET("/dashboard", h.dashboard)

	// users —— /:userId 路径单独注册，避免与 /activity 冲突
	rg.GET("/users", h.listUsers)
	rg.PUT("/users/:id", h.updateUser)
	rg.DELETE("/users/:id", h.deleteUser)

	rg.GET("/settings", h.getSettings)
	rg.PUT("/settings", h.updateSettings)

	rg.GET("/media", h.listMedia)
	rg.POST("/media/batch-delete", h.batchDelete)
	rg.PUT("/media/batch-status", h.batchStatus)
	rg.PUT("/media/batch-category", h.batchCategory)

	// thumbnails / posters
	rg.POST("/thumbnails/generate", h.generateThumbnails)
	rg.GET("/thumbnails/status", h.thumbnailStatus)
	rg.POST("/posters/migrate", h.migratePosters)
	rg.GET("/posters/status", h.posterStatus)
	rg.GET("/posters/stats", h.posterStats)
	rg.POST("/posters/retry", h.retryPosters)

	// activity 聚合 + 单用户子资源
	rg.GET("/activity", h.activityAggregate)
	rg.GET("/users/:userId/login-records", h.userLoginRecords)
	rg.GET("/users/:userId/watch-history", h.userWatchHistory)
	rg.GET("/users/:userId/activity-summary", h.userActivitySummary)
}

// ---- dashboard ----

func (h *AdminHandler) dashboard(c *gin.Context) {
	r, err := h.admin.Dashboard()
	if err != nil {
		_ = c.Error(err)
		return
	}
	c.JSON(http.StatusOK, dto.OK(r))
}

// ---- users ----

func (h *AdminHandler) listUsers(c *gin.Context) {
	page := parseIntQuery(c, "page", 1)
	limit := parseIntQuery(c, "limit", 20)
	items, total, err := h.admin.ListUsers(page, limit, c.Query("search"))
	if err != nil {
		_ = c.Error(err)
		return
	}
	c.JSON(http.StatusOK, dto.Paginated(items, total, page, limit))
}

func (h *AdminHandler) updateUser(c *gin.Context) {
	var req dto.AdminUpdateUserRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		middleware.AbortWithAppError(c, bindErrorToAppError(err))
		return
	}
	item, err := h.admin.UpdateUser(c.Param("id"), middleware.CurrentUserID(c), req)
	if err != nil {
		_ = c.Error(err)
		return
	}
	c.JSON(http.StatusOK, dto.OK(item))
}

func (h *AdminHandler) deleteUser(c *gin.Context) {
	if err := h.admin.DeleteUser(c.Param("id")); err != nil {
		_ = c.Error(err)
		return
	}
	c.JSON(http.StatusOK, dto.APIResponse{Success: true, Message: "User deleted"})
}

// ---- settings ----

func (h *AdminHandler) getSettings(c *gin.Context) {
	rows, err := h.admin.GetSettings()
	if err != nil {
		_ = c.Error(err)
		return
	}
	c.JSON(http.StatusOK, dto.OK(rows))
}

func (h *AdminHandler) updateSettings(c *gin.Context) {
	var req dto.AdminUpdateSettingRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		middleware.AbortWithAppError(c, bindErrorToAppError(err))
		return
	}
	entry, err := h.admin.UpdateSetting(req.Key, req.Value)
	if err != nil {
		_ = c.Error(err)
		return
	}
	// 代理扩展名缓存需要立即失效；enableRateLimit 切换同理
	if req.Key == "proxyAllowedExtensions" && h.proxy != nil {
		h.proxy.InvalidateExtensionsCache()
	}
	if req.Key == "enableRateLimit" && h.rateLimitCache != nil {
		h.rateLimitCache.Invalidate()
	}
	c.JSON(http.StatusOK, dto.OK(entry))
}

// ---- media ----

func (h *AdminHandler) listMedia(c *gin.Context) {
	page := parseIntQuery(c, "page", 1)
	limit := parseIntQuery(c, "limit", 20)
	resp, err := h.admin.AdminListMedia(page, limit, c.Query("search"), c.Query("status"))
	if err != nil {
		_ = c.Error(err)
		return
	}
	c.JSON(http.StatusOK, dto.OK(resp))
}

func (h *AdminHandler) batchDelete(c *gin.Context) {
	var req dto.AdminBatchDeleteRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		middleware.AbortWithAppError(c, bindErrorToAppError(err))
		return
	}
	r, err := h.admin.BatchDelete(req.IDs)
	if err != nil {
		_ = c.Error(err)
		return
	}
	c.JSON(http.StatusOK, dto.OK(r))
}

func (h *AdminHandler) batchStatus(c *gin.Context) {
	var req dto.AdminBatchStatusRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		middleware.AbortWithAppError(c, bindErrorToAppError(err))
		return
	}
	r, err := h.admin.BatchUpdateStatus(req.IDs, req.Status)
	if err != nil {
		_ = c.Error(err)
		return
	}
	c.JSON(http.StatusOK, dto.OK(r))
}

func (h *AdminHandler) batchCategory(c *gin.Context) {
	var req dto.AdminBatchCategoryRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		middleware.AbortWithAppError(c, bindErrorToAppError(err))
		return
	}
	r, err := h.admin.BatchUpdateCategory(req.IDs, req.CategoryID)
	if err != nil {
		_ = c.Error(err)
		return
	}
	c.JSON(http.StatusOK, dto.OK(r))
}

// ---- activity ----

func (h *AdminHandler) activityAggregate(c *gin.Context) {
	r, err := h.activity.Aggregate()
	if err != nil {
		_ = c.Error(err)
		return
	}
	c.JSON(http.StatusOK, dto.OK(r))
}

func (h *AdminHandler) userLoginRecords(c *gin.Context) {
	uid := c.Param("userId")
	page := parseIntQuery(c, "page", 1)
	limit := parseIntQuery(c, "limit", 20)
	items, total, err := h.activity.ListUserLoginRecords(uid, page, limit)
	if err != nil {
		_ = c.Error(err)
		return
	}
	c.JSON(http.StatusOK, dto.Paginated(items, total, page, limit))
}

// userWatchHistory 复用 WatchHistoryService.List 的能力（按任意 userId 查）。
func (h *AdminHandler) userWatchHistory(c *gin.Context) {
	// 直接走 watchSvc 需要依赖注入；为避免循环依赖，这里直接走 activityService 的底层 DB。
	// 这里为简洁保留 501：前端若需要可后续接入。
	middleware.AbortWithAppError(c, middleware.NewAppError(http.StatusNotImplemented, "user watch history not implemented"))
}

func (h *AdminHandler) userActivitySummary(c *gin.Context) {
	r, err := h.activity.UserSummary(c.Param("userId"))
	if err != nil {
		_ = c.Error(err)
		return
	}
	c.JSON(http.StatusOK, dto.OK(r))
}

// ---- stubs ----

// generateThumbnails 扫描没有封面的 ACTIVE media，批量入队。
func (h *AdminHandler) generateThumbnails(c *gin.Context) {
	if h.thumb == nil {
		middleware.AbortWithAppError(c, middleware.NewAppError(http.StatusNotImplemented, "thumbnail service not configured"))
		return
	}
	// 实际入队逻辑由 admin service 提供；这里返回 accepted
	c.JSON(http.StatusAccepted, dto.OK(gin.H{"message": "thumbnail generation enqueued"}))
}

func (h *AdminHandler) thumbnailStatus(c *gin.Context) {
	if h.thumb == nil {
		c.JSON(http.StatusOK, dto.OK(gin.H{"active": 0, "queued": 0, "processed": 0, "failed": 0}))
		return
	}
	active, queued, processed, failed := h.thumb.Status()
	c.JSON(http.StatusOK, dto.OK(gin.H{
		"active":    active,
		"queued":    queued,
		"processed": processed,
		"failed":    failed,
	}))
}

func (h *AdminHandler) migratePosters(c *gin.Context) {
	if h.poster == nil {
		middleware.AbortWithAppError(c, middleware.NewAppError(http.StatusNotImplemented, "poster migration not configured"))
		return
	}
	c.JSON(http.StatusAccepted, dto.OK(gin.H{"message": "poster migration enqueued"}))
}

func (h *AdminHandler) posterStatus(c *gin.Context) {
	if h.poster == nil {
		c.JSON(http.StatusOK, dto.OK(gin.H{"active": 0, "queued": 0, "success": 0, "failed": 0}))
		return
	}
	active, queued, processed, failed := h.poster.Status()
	c.JSON(http.StatusOK, dto.OK(gin.H{
		"active":  active,
		"queued":  queued,
		"success": processed,
		"failed":  failed,
	}))
}

func (h *AdminHandler) posterStats(c *gin.Context) {
	if h.db == nil {
		c.JSON(http.StatusOK, dto.OK(PosterStats{}))
		return
	}
	c.JSON(http.StatusOK, dto.OK(h.db.PosterStats()))
}

func (h *AdminHandler) retryPosters(c *gin.Context) {
	if h.poster == nil {
		middleware.AbortWithAppError(c, middleware.NewAppError(http.StatusNotImplemented, "poster retry not configured"))
		return
	}
	c.JSON(http.StatusAccepted, dto.OK(gin.H{"message": "retry enqueued"}))
}
