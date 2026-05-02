// Package app 组装 Gin engine 与全部跨模块依赖 singleton。
// 对齐 packages/server/src/app.ts 的中间件顺序与路由前缀。
package app

import (
	"fmt"
	"log"
	"net/http"
	"net/url"
	"os"
	"path"
	"path/filepath"
	"strings"
	"sync"
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

	// 字幕模块（可选；cfg.Subtitle.Enabled=false 时为 nil）
	SubtitleSvc *service.SubtitleService
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

	captchaSvc := service.NewCaptchaService(db, extractHostnames(cfg.CORS.Origins))
	r.Use(secureHeaders(captchaSvc))

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

	// ErrorHandler 必须放在 gzip 之后注册：post-Next 顺序与注册顺序相反，
	// 这样 ErrorHandler 的"读 c.Errors → 写 429 JSON"在 gzip 的 defer 之前执行。
	// 否则 gzip@v1.2.6 的 defer 会在 buffer 为空时调用
	// gw.ResponseWriter.Write(empty) → 触发 WriteHeaderNow 把默认 200 状态码刷出去，
	// ErrorHandler 再检查 c.Writer.Written() 时已是 true，被迫跳过写响应，
	// 导致限流 / 业务错误最终被吞成 200 空 body（前端解析失败显示"无法获取登录挑战"等）。
	r.Use(middleware.ErrorHandler(cfg.NodeEnv == "production"))

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
	authH := handler.NewAuthHandler(authSvc, captchaSvc, deps.Ticket, cfg, deps.ECDH, deps.Chal)
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
	posterDL := service.NewPosterDownloader(cfg.UploadsDir, cfg.PosterConcurrency, func(mediaID, localPath, originalURL string) {
		db.Model(&model.Media{}).Where("id = ?", mediaID).Updates(map[string]any{"poster_url": localPath, "original_poster_url": originalURL})
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

	// 让批量删除复用单条 Delete 的文件清理 + 生命周期钩子（封面、缩略图、字幕 VTT）
	adminSvc.SetMediaDeleter(mediaSvc)

	adminGroup := v1.Group("/admin")
	adminGroup.Use(middleware.Authenticate(authDeps), requireAdmin)
	adminH.Register(adminGroup)

	// ---- Backup 模块（阶段 I-2）----
	// 传入 SubtitlesDir 让 backup 服务能定位 VTT 文件（用户可能配置 SUBTITLE_DIR 指向 uploadsDir 外部）。
	backupSvc := service.NewBackupService(db, cfg.UploadsDir, cfg.Subtitle.SubtitlesDir)
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

	// ---- 字幕模块（可选）----
	// 仅在 cfg.Subtitle.Enabled=true 时启动 worker。
	// 即使禁用，路由也注册（返回 disabled 占位响应），方便前端做 UI 一致性。
	//
	// 配置加载顺序：
	//   1. .env 提供基线（兼容旧部署）
	//   2. system_settings 中以 "subtitle.*" 前缀存储的覆盖值（admin 网页修改的运行时配置）
	//   3. admin 后续通过 PUT /admin/subtitle/settings 修改时再次落 DB 并同步 in-memory
	if err := service.LoadSubtitleOverrides(db, &cfg.Subtitle); err != nil {
		log.Fatalf("[subtitle] load overrides: %v", err)
	}

	// asrFactory / translatorFactory 接受当前 cfg 快照按需构造客户端，让 admin 修改的
	// whisper bin / model / 翻译 baseURL / API Key 立即对下一个 job 生效，无需重启服务。
	asrFactory := func(snap config.SubtitleConfig) service.ASRClient {
		return service.NewWhisperCppASR(snap.WhisperBin, snap.WhisperModel, snap.WhisperLanguage, snap.WhisperThreads)
	}
	translatorFactory := func(snap config.SubtitleConfig) service.Translator {
		return service.NewOpenAICompatibleTranslator(snap.TranslateBaseURL, snap.TranslateAPIKey, snap.TranslateModel, snap.MaxRetries)
	}
	subtitleSvc := service.NewSubtitleService(db, &cfg.Subtitle, asrFactory, translatorFactory, deps.Proxy)
	if err := subtitleSvc.Start(); err != nil {
		// 启动失败不阻断整个 server；handler 仍会返回 disabled
		log.Printf("[subtitle] start failed: %v (feature disabled)", err)
	}
	deps.SubtitleSvc = subtitleSvc

	// 把字幕生命周期钩子注入 MediaService（创建/删除媒体时联动）
	mediaSvc.SetLifecycleHooks(subtitleSvc.HookOnMediaCreated, subtitleSvc.HookOnMediaDeleted)

	subtitleH := handler.NewSubtitleHandler(subtitleSvc)

	// 公开 VTT 端点：不挂 Authenticate（<track> 请求不携带 Bearer），仅靠 HMAC 签名鉴权
	subtitleVTT := v1.Group("/subtitle")
	subtitleH.RegisterPublic(subtitleVTT)

	// 状态查询端点：需登录
	subtitlePublic := v1.Group("/subtitle")
	subtitlePublic.Use(middleware.Authenticate(authDeps))
	subtitleH.RegisterAuthed(subtitlePublic)

	// admin 端点
	subtitleAdmin := v1.Group("/admin/subtitle")
	subtitleAdmin.Use(middleware.Authenticate(authDeps), requireAdmin)
	subtitleH.RegisterAdmin(subtitleAdmin)

	// 远程字幕 worker 端点：独立鉴权（mwt_xxx Bearer token），与 user JWT 解耦
	subtitleWorkerH := handler.NewSubtitleWorkerHandler(subtitleSvc)
	workerGroup := v1.Group("/worker")
	workerGroup.Use(middleware.RequireWorkerAuth(db))
	subtitleWorkerH.Register(workerGroup)

	r.NoRoute(spaFallback(cfg))

	return r, deps
}

// secureHeaders 写应用层安全响应头，CSP 根据验证码服务地址动态生成。
//
// 指令选择依据:
//   - default-src 'self'：兜底，不落在具体指令的资源一律本站
//   - script-src 'self' 'wasm-unsafe-eval' [+captcha]：WASM 加密核心依赖 wasm-unsafe-eval
//   - style-src 'self' 'unsafe-inline'：React / shadcn 运行时 style 属性需要
//   - img-src/media-src 允许 https: 与 data:/blob:（m3u8 预览）
//   - connect-src 加 captcha origin 覆盖 widget XHR；frame-src 同样加入以覆盖未来 iframe 版 widget
//   - font-src 'self'（无 Google Fonts）
//   - base-uri 'self'：防止 <base href> 注入改写相对链接到攻击者域
//   - form-action 'self'：防止被注入的 <form action="evil"> 提交到外站
//   - frame-ancestors 'none'：等价于 X-Frame-Options: DENY，禁止被 iframe 嵌入
//   - object-src 'none'：禁止 <object>/<embed>/<applet>，关闭 legacy 插件攻击面
func secureHeaders(captcha *service.CaptchaService) gin.HandlerFunc {
	const baseCSP = "default-src 'self'; " +
		"script-src 'self' 'wasm-unsafe-eval'%s; " +
		"style-src 'self' 'unsafe-inline'; " +
		"img-src 'self' data: blob: https:; " +
		"media-src 'self' blob: https:; " +
		"connect-src 'self'%s; " +
		"frame-src 'self'%s; " +
		"font-src 'self'; " +
		"base-uri 'self'; " +
		"form-action 'self'; " +
		"frame-ancestors 'none'; " +
		"object-src 'none'"

	return func(c *gin.Context) {
		c.Header("X-Content-Type-Options", "nosniff")
		c.Header("X-Frame-Options", "SAMEORIGIN")
		c.Header("Referrer-Policy", "strict-origin-when-cross-origin")

		extra := ""
		if origin := captcha.CSPOrigin(); origin != "" {
			extra = " " + origin
		}
		c.Header("Content-Security-Policy", fmt.Sprintf(baseCSP, extra, extra, extra))
		c.Next()
	}
}

// staticAssetExts 白名单：路径带这些扩展名的请求视为静态资源，未命中 dist 直接 404；
// 其它一律 fallback 到 index.html 让 React Router 处理。
// 白名单策略（而非"任意扩展名都 404"）可以正确处理 /tags/sci-fi.v2、/media/movie.2024 这类
// 合法 SPA 路由，避免它们被错误地当作静态资源请求而看不到前端页面。
var staticAssetExts = map[string]struct{}{
	".js": {}, ".mjs": {}, ".cjs": {}, ".map": {},
	".css": {}, ".json": {}, ".xml": {}, ".txt": {}, ".svg": {},
	".png": {}, ".jpg": {}, ".jpeg": {}, ".gif": {}, ".webp": {}, ".avif": {}, ".ico": {},
	".woff": {}, ".woff2": {}, ".ttf": {}, ".otf": {}, ".eot": {},
	".mp3": {}, ".mp4": {}, ".webm": {}, ".ogg": {}, ".wav": {},
	".wasm": {}, ".pdf": {},
}

// spaFallback 尝试用构建好的 index.html 响应前端路由；
// 若 dist 不存在（本地开发走 Vite DevServer），退回 404 JSON。
// 使用 sync.Once 延迟到首次请求时查找，确保 Docker volume 已就绪。
func spaFallback(cfg *config.Config) gin.HandlerFunc {
	candidates := []string{
		"/app/web/dist/index.html",
		filepath.Join(cfg.DataDir, "..", "web", "dist", "index.html"),
		filepath.Join("web", "dist", "index.html"),
		filepath.Join("web", "client", "dist", "index.html"),
	}

	var indexPath string
	var once sync.Once

	findIndex := func() {
		for _, p := range candidates {
			if info, err := os.Stat(p); err == nil && !info.IsDir() {
				indexPath = p
				log.Printf("[spa] index.html found at %s", p)
				return
			}
		}
		log.Printf("[spa] index.html not found in any candidate: %v", candidates)
	}

	return func(c *gin.Context) {
		once.Do(findIndex)
		reqPath := c.Request.URL.Path
		if indexPath == "" || strings.HasPrefix(reqPath, "/api/") || isStaticAssetPath(reqPath) {
			c.JSON(http.StatusNotFound, gin.H{"success": false, "error": "Route not found"})
			return
		}
		c.File(indexPath)
	}
}

// isStaticAssetPath 按扩展名白名单判断 URL 路径是否是静态资源请求。
// 用 path.Ext（URL 语义）而不是 filepath.Ext（OS 路径分隔符）；
// Windows 下后者会把 `\\` 当分隔符，导致含反斜杠的 URL 判错。
func isStaticAssetPath(p string) bool {
	ext := strings.ToLower(path.Ext(p))
	if ext == "" {
		return false
	}
	_, ok := staticAssetExts[ext]
	return ok
}

// extractHostnames 从 CORS 原点 URL 列表中提取 host 部分（小写、去端口也保留）用于 captcha hostname 等值校验。
// 解析失败的条目跳过；重复项由调用方（NewCaptchaService）去重。
func extractHostnames(origins []string) []string {
	out := make([]string, 0, len(origins))
	for _, o := range origins {
		u, err := url.Parse(strings.TrimSpace(o))
		if err != nil || u.Hostname() == "" {
			continue
		}
		out = append(out, strings.ToLower(u.Hostname()))
	}
	return out
}
