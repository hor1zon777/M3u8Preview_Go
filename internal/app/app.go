// Package app 组装 Gin engine 与全部跨模块依赖 singleton。
// 对齐 packages/server/src/app.ts 的中间件顺序与路由前缀。
package app

import (
	"log"
	"net/http"
	"path/filepath"
	"strings"
	"time"

	"github.com/gin-contrib/cors"
	"github.com/gin-contrib/gzip"
	"github.com/gin-gonic/gin"
	"gorm.io/gorm"

	"github.com/hor1zon777/m3u8-preview-go/internal/config"
	"github.com/hor1zon777/m3u8-preview-go/internal/handler"
	"github.com/hor1zon777/m3u8-preview-go/internal/middleware"
	"github.com/hor1zon777/m3u8-preview-go/internal/model"
	"github.com/hor1zon777/m3u8-preview-go/internal/service"
	"github.com/hor1zon777/m3u8-preview-go/internal/util"
)

// Deps 是跨请求复用的 singleton 集合。
// 所有 handler / 后续阶段 service 都应该从 Deps 取依赖，禁止使用包级全局变量。
type Deps struct {
	Cfg    *config.Config
	DB     *gorm.DB
	JWT    *util.JWTService
	Ticket *util.SSETicketStore
	Proxy  *util.ProxySigner
	ECDH   *util.ECDHService
	Chal   *util.ChallengeStore

	// 限流
	RateLimitCache *middleware.RateLimitSettingCache
	AuthLimiter    *middleware.WindowLimiter // 登录/注册/刷新共用：15m/50
	GlobalLimiter  *middleware.WindowLimiter // /api/v1/*：15m/200
	ViewsLimiter   *middleware.WindowLimiter // /media/:id/views：15m/100
	ProxyLimiter   *middleware.WindowLimiter // /proxy/*：15m/1500
	SignLimiter    *middleware.WindowLimiter // /proxy/sign：15m/60

	// 阶段 H 后注入
	ProxySvc *service.ProxyService
}

// NewDeps 构造跨请求 singleton。
// ECDH 密钥加载失败会 log.Fatal —— 这是启动必备资源，缺失时直接阻断启动比静默降级更安全。
func NewDeps(cfg *config.Config, db *gorm.DB) *Deps {
	ecdhSvc, err := util.LoadOrGenerateECDH(cfg.ECDHPrivateKeyPath)
	if err != nil {
		log.Fatalf("[app] load ecdh private key: %v", err)
	}
	return &Deps{
		Cfg:            cfg,
		DB:             db,
		JWT:            util.NewJWTService(&cfg.JWT),
		Ticket:         util.NewSSETicketStore(),
		Proxy:          util.NewProxySigner(cfg.Proxy.Secret, cfg.Proxy.SignatureTTL),
		ECDH:           ecdhSvc,
		Chal:           util.NewChallengeStore(),
		RateLimitCache: middleware.NewRateLimitSettingCache(db),
		AuthLimiter:    middleware.NewWindowLimiter(50, 15*time.Minute),
		GlobalLimiter:  middleware.NewWindowLimiter(200, 15*time.Minute),
		ViewsLimiter:   middleware.NewWindowLimiter(100, 15*time.Minute),
		ProxyLimiter:   middleware.NewWindowLimiter(1500, 15*time.Minute),
		SignLimiter:    middleware.NewWindowLimiter(60, 15*time.Minute),
	}
}

// Build 构造一个完整配置好的 gin.Engine。
// 返回值还暴露 Deps 供测试注入或后续阶段扩展路由。
func Build(cfg *config.Config, db *gorm.DB) (*gin.Engine, *Deps) {
	deps := NewDeps(cfg, db)

	if cfg.NodeEnv == "production" {
		gin.SetMode(gin.ReleaseMode)
	}

	r := gin.New()
	_ = r.SetTrustedProxies([]string{"127.0.0.1", "::1"})

	r.Use(gin.Logger())
	r.Use(middleware.Recovery())
	r.Use(middleware.ErrorHandler(cfg.NodeEnv == "production"))
	r.Use(secureHeaders())

	// gzip：代理与 SSE 路由跳过（前者是二进制流、后者必须实时 flush）
	r.Use(gzip.Gzip(gzip.DefaultCompression, gzip.WithExcludedPathsRegexs([]string{
		`^/api/v1/proxy/.*`,
		`^/api/v1/admin/backup/.*/stream.*`,
	})))

	r.Use(cors.New(cors.Config{
		AllowOrigins:     cfg.CORS.Origins,
		AllowMethods:     []string{"GET", "POST", "PUT", "DELETE", "OPTIONS", "PATCH", "HEAD"},
		AllowHeaders:     []string{"Origin", "Content-Type", "Authorization", "Cookie", "Accept", "X-Requested-With"},
		ExposeHeaders:    []string{"Content-Length", "Content-Range"},
		AllowCredentials: true,
		MaxAge:           12 * time.Hour,
	}))

	// /uploads 静态文件服务
	r.GET("/uploads/*filepath", func(c *gin.Context) {
		c.Header("X-Content-Type-Options", "nosniff")
		c.Header("Cache-Control", "public, max-age=300, must-revalidate")
		cleaned := filepath.Clean("/" + c.Param("filepath"))
		fullPath := filepath.Join(cfg.UploadsDir, cleaned)
		if !strings.HasPrefix(fullPath, filepath.Clean(cfg.UploadsDir)) {
			c.Status(http.StatusForbidden)
			return
		}
		c.File(fullPath)
	})

	r.GET("/api/health", handler.Health)

	// /api/v1 路由组 + 全局限流（enableRateLimit=true 时才起作用；管理员 settings 请求 bypass）
	v1 := r.Group("/api/v1")
	globalLimiterMW := middleware.RateLimit(deps.GlobalLimiter, "", nil)
	v1.Use(middleware.ConditionalRateLimit(
		deps.RateLimitCache,
		globalLimiterMW,
		middleware.IsRateLimitToggleRequest,
	))

	// ---- Auth 模块 ----
	authSvc := service.NewAuthService(db, deps.JWT, cfg)
	authH := handler.NewAuthHandler(authSvc, deps.Ticket, cfg, deps.ECDH, deps.Chal)
	authDeps := &middleware.AuthDeps{JWT: deps.JWT, Ticket: deps.Ticket}

	// 公开 auth 路由：register/login/refresh/logout/register-status
	authPublic := v1.Group("/auth")
	authPublic.Use(middleware.ConditionalRateLimit(
		deps.RateLimitCache,
		middleware.RateLimit(deps.AuthLimiter, "请求过于频繁，请稍后再试", nil),
		nil,
	))
	authH.Register(authPublic)

	// 需登录的 auth 路由：me / change-password / sse-ticket
	authAuthed := v1.Group("/auth")
	authAuthed.Use(middleware.Authenticate(authDeps))
	authH.RegisterAuthed(authAuthed)

	// ---- 队列服务（需要在 media/admin handler 之前初始化）----
	thumbQueue := service.NewThumbnailQueue(cfg.ThumbnailConcurrency, service.NewFFmpegProcessor(cfg.UploadsDir, db))
	posterDL := service.NewPosterDownloader(cfg.UploadsDir, cfg.PosterConcurrency, func(mediaID, localPath string) {
		db.Model(&model.Media{}).Where("id = ?", mediaID).Update("poster_url", localPath)
	})

	// ---- 核心业务模块（阶段 F）----
	// thumbQueue / posterDL 已在上方构造，这里注入给 MediaService：
	//   - PosterResolver：admin 创建/更新时若 poster_url 为外部 http(s)，同步下载到本地
	//   - ThumbnailEnqueuer：新建媒体且未显式指定封面时异步生成缩略图
	mediaSvc := service.NewMediaService(db, cfg.UploadsDir, thumbQueue, posterDL)
	mediaH := handler.NewMediaHandler(mediaSvc, thumbQueue)
	categorySvc := service.NewCategoryService(db)
	categoryH := handler.NewCategoryHandler(categorySvc)
	tagSvc := service.NewTagService(db)
	tagH := handler.NewTagHandler(tagSvc)
	favoriteSvc := service.NewFavoriteService(db)
	favoriteH := handler.NewFavoriteHandler(favoriteSvc)
	watchSvc := service.NewWatchHistoryService(db)
	watchH := handler.NewWatchHistoryHandler(watchSvc)
	playlistSvc := service.NewPlaylistService(db)
	playlistH := handler.NewPlaylistHandler(playlistSvc)

	// 公共帮助器
	requireAdmin := middleware.RequireRole("ADMIN")
	viewsLimiterMW := middleware.RateLimit(deps.ViewsLimiter, "Too many view requests", func(c *gin.Context) string {
		if uid := middleware.CurrentUserID(c); uid != "" {
			return "u:" + uid
		}
		return "ip:" + c.ClientIP()
	})

	// /media —— 公开查询 + admin 写入
	mediaPublic := v1.Group("/media")
	mediaH.RegisterPublic(mediaPublic)

	mediaAuthed := v1.Group("/media")
	mediaAuthed.Use(middleware.Authenticate(authDeps))
	mediaH.RegisterAuthed(mediaAuthed)

	mediaViews := v1.Group("/media")
	mediaViews.Use(middleware.OptionalAuth(authDeps))
	mediaViews.Use(middleware.ConditionalRateLimit(deps.RateLimitCache, viewsLimiterMW, nil))
	mediaH.RegisterViews(mediaViews)

	mediaAdmin := v1.Group("/media")
	mediaAdmin.Use(middleware.Authenticate(authDeps), requireAdmin)
	mediaH.RegisterAdmin(mediaAdmin)

	// /categories —— 公开 + admin
	categoryPublic := v1.Group("/categories")
	categoryH.RegisterPublic(categoryPublic)
	categoryAdmin := v1.Group("/categories")
	categoryAdmin.Use(middleware.Authenticate(authDeps), requireAdmin)
	categoryH.RegisterAdmin(categoryAdmin)

	// /tags —— 公开 + admin
	tagPublic := v1.Group("/tags")
	tagH.RegisterPublic(tagPublic)
	tagAdmin := v1.Group("/tags")
	tagAdmin.Use(middleware.Authenticate(authDeps), requireAdmin)
	tagH.RegisterAdmin(tagAdmin)

	// /favorites —— 全部需登录
	favAuthed := v1.Group("/favorites")
	favAuthed.Use(middleware.Authenticate(authDeps))
	favoriteH.RegisterAuthed(favAuthed)

	// /history —— 全部需登录
	historyAuthed := v1.Group("/history")
	historyAuthed.Use(middleware.Authenticate(authDeps))
	watchH.RegisterAuthed(historyAuthed)

	// /playlists —— 混合：public + optionalAuth + authed + admin
	playlistPublic := v1.Group("/playlists")
	playlistH.RegisterPublic(playlistPublic)

	playlistOptional := v1.Group("/playlists")
	playlistOptional.Use(middleware.OptionalAuth(authDeps))
	playlistH.RegisterOptional(playlistOptional)

	playlistAuthed := v1.Group("/playlists")
	playlistAuthed.Use(middleware.Authenticate(authDeps))
	playlistH.RegisterAuthed(playlistAuthed)

	playlistAdmin := v1.Group("/playlists")
	playlistAdmin.Use(middleware.Authenticate(authDeps), requireAdmin)
	playlistH.RegisterAdmin(playlistAdmin)

	// ---- Upload / Import 模块（阶段 G）----
	uploadSvc := service.NewUploadService(cfg.UploadsDir, cfg.Upload.MaxFileSize, cfg.Upload.AllowedMimeTypes)
	uploadH := handler.NewUploadHandler(uploadSvc)
	importSvc := service.NewImportService(db, thumbQueue, posterDL)
	importH := handler.NewImportHandler(importSvc)

	// /upload/poster —— 仅 admin
	uploadAdmin := v1.Group("/upload")
	uploadAdmin.Use(middleware.Authenticate(authDeps), requireAdmin)
	uploadH.RegisterAdmin(uploadAdmin)

	// /import/template/:format —— 匿名可下载（对齐 TS 原版）
	importPublic := v1.Group("/import")
	importH.RegisterPublic(importPublic)

	// /import/preview、/execute、/logs —— 仅 admin
	importAdmin := v1.Group("/import")
	importAdmin.Use(middleware.Authenticate(authDeps), requireAdmin)
	importH.RegisterAdmin(importAdmin)

	// ---- Proxy 模块（阶段 H）----
	proxySvc := service.NewProxyService(db)
	proxyH := handler.NewProxyHandler(proxySvc, deps.Proxy)

	// /proxy/sign —— authenticate + signLimiter(15m/60)，受 enableRateLimit 开关
	signGroup := v1.Group("/proxy")
	signGroup.Use(middleware.ConditionalRateLimit(
		deps.RateLimitCache,
		middleware.RateLimit(deps.SignLimiter, "签名请求过于频繁，请稍后再试", nil),
		nil,
	))
	signGroup.Use(middleware.Authenticate(authDeps))
	proxyH.RegisterSign(signGroup)

	// /proxy/m3u8 —— authenticate + proxyLimiter(15m/1500)
	m3u8Group := v1.Group("/proxy")
	m3u8Group.Use(middleware.ConditionalRateLimit(
		deps.RateLimitCache,
		middleware.RateLimit(deps.ProxyLimiter, "代理请求过于频繁，请稍后再试", nil),
		nil,
	))
	m3u8Group.Use(middleware.Authenticate(authDeps))
	proxyH.RegisterM3U8(m3u8Group)

	// 把 proxy service 暴露到 Deps，供 admin settings handler 改扩展名时清缓存
	deps.ProxySvc = proxySvc

	// ---- Admin 模块（阶段 I-1 + I-3 注入队列）----
	adminSvc := service.NewAdminService(db)
	activitySvc := service.NewActivityService(db)
	adminH := handler.NewAdminHandler(adminSvc, activitySvc, proxySvc, thumbQueue, posterDL, watchSvc, newPosterStatsDB(db, cfg.UploadsDir), deps.RateLimitCache)

	// 注入真正的 poster resolver 到 media service（替换占位实现）
	mediaSvc.SetPosterResolver(posterDL)
	mediaSvc.SetThumbnailEnqueuer(thumbQueue)

	adminGroup := v1.Group("/admin")
	adminGroup.Use(middleware.Authenticate(authDeps), requireAdmin)
	adminH.Register(adminGroup)

	// ---- Backup 模块（阶段 I-2）----
	backupSvc := service.NewBackupService(db, cfg.UploadsDir)
	backupSvc.RegisterInvalidator(deps.RateLimitCache.Invalidate)
	backupSvc.RegisterInvalidator(proxySvc.InvalidateExtensionsCache)
	backupH := handler.NewBackupHandler(backupSvc)

	backupGroup := v1.Group("/admin/backup")
	backupGroup.Use(middleware.Authenticate(authDeps), requireAdmin)
	backupH.Register(backupGroup)

	// SSE 路由单独挂载：使用 AuthenticateSSE 识别 ?ticket=
	backupSSE := v1.Group("/admin/backup")
	backupSSE.Use(middleware.AuthenticateSSE(authDeps), requireAdmin)
	backupH.RegisterSSE(backupSSE)

	r.NoRoute(func(c *gin.Context) {
		c.JSON(404, gin.H{"success": false, "error": "Route not found"})
	})

	return r, deps
}

// secureHeaders 写应用层安全响应头；nginx 层还会再覆盖一轮 CSP。
func secureHeaders() gin.HandlerFunc {
	return func(c *gin.Context) {
		c.Header("X-Content-Type-Options", "nosniff")
		c.Header("X-Frame-Options", "SAMEORIGIN")
		c.Header("Referrer-Policy", "strict-origin-when-cross-origin")
		c.Next()
	}
}
