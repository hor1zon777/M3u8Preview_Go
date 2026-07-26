// Package main 是 m3u8-preview-go 的启动入口。
// 对齐 packages/server/src/index.ts：加载配置 → 连接 DB → 迁移 → 种子
// → ensureDefaultSettings → 监听端口 → 优雅关闭。
package main

import (
	"context"
	"errors"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"

	"github.com/hor1zon777/m3u8-preview-go/internal/app"
	"github.com/hor1zon777/m3u8-preview-go/internal/config"
	"github.com/hor1zon777/m3u8-preview-go/internal/db"
	"github.com/hor1zon777/m3u8-preview-go/internal/ha"
	"github.com/hor1zon777/m3u8-preview-go/internal/middleware"
	"github.com/hor1zon777/m3u8-preview-go/internal/service"
	"github.com/hor1zon777/m3u8-preview-go/internal/version"
)

// resolveRoleTimeout 是开机角色决议的总超时。
// 需要覆盖首次部署时非首选节点的等让时长（30s）加上几次 API 往返。
const resolveRoleTimeout = 2 * time.Minute

func main() {
	projectRoot, err := os.Getwd()
	if err != nil {
		log.Fatalf("getwd: %v", err)
	}
	if filepath.Base(projectRoot) == "server" {
		projectRoot = filepath.Dir(filepath.Dir(projectRoot))
	}

	// 子命令必须在连接数据库之前处理：ha-resolve-role 由 docker-entrypoint.sh
	// 在 LiteFS 挂载**之前**调用，此时数据库文件还不存在。
	if len(os.Args) > 1 && os.Args[1] == "ha-resolve-role" {
		runResolveRole(projectRoot)
		return
	}

	cfg := config.MustLoad(projectRoot)

	// 注册自定义 validator（username_chars / password_complex）
	if err := middleware.RegisterCustomValidators(); err != nil {
		log.Fatalf("register validators: %v", err)
	}

	// DB 连接 + 迁移 + 种子 + 默认设置补全
	gdb, err := db.Open(cfg)
	if err != nil {
		log.Fatalf("db open: %v", err)
	}
	defer func() {
		if cerr := db.Close(gdb); cerr != nil {
			log.Printf("db close: %v", cerr)
		}
	}()
	log.Println("Database connected")

	if err := db.AutoMigrate(gdb); err != nil {
		log.Fatalf("db migrate: %v", err)
	}
	if err := db.EnsureDefaultSettings(gdb); err != nil {
		log.Fatalf("ensure default settings: %v", err)
	}
	if err := db.Seed(gdb, cfg); err != nil {
		log.Fatalf("db seed: %v", err)
	}

	engine, deps := app.Build(cfg, gdb)

	// 主备领导权仲裁。未启用时 haAgent 为 nil，其方法与通道都能安全地零值使用。
	haAgent, err := ha.New(cfg.HA, deps.LiteFS, func() int {
		if deps.SubtitleSvc == nil {
			return 0
		}
		if b := deps.SubtitleSvc.AudioBroker(); b != nil {
			return b.PendingFetches()
		}
		return 0
	})
	if err != nil {
		log.Fatalf("ha agent: %v", err)
	}
	if haAgent != nil {
		// 两个钩子都必须在 Start 之前注入：
		// 自动回切闸门查 system_settings（管理员手动切走 preferred 后置 false），
		// 让位钩子在 preferred 节点进入停写前把闸门写成关闭（经 LiteFS 复制到对端）。
		haAgent.SetAutoFailbackGate(func() bool { return service.HAAutoFailbackEnabled(gdb) })
		haAgent.SetPreferredYieldHook(func() error { return service.DisableHAAutoFailback(gdb) })
		haAgent.Start()
	}
	deps.HAAgent = haAgent

	addr := fmt.Sprintf("%s:%d", cfg.BindAddress, cfg.Port)
	srv := &http.Server{
		Addr:              addr,
		Handler:           engine,
		ReadHeaderTimeout: 15 * time.Second,
	}

	serverErr := make(chan error, 1)
	go func() {
		log.Printf("Server running on http://%s", addr)
		log.Printf("Environment: %s", cfg.NodeEnv)
		log.Printf("Version: %s (built %s)", version.String(), version.BuildTime)
		if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			serverErr <- err
		}
	}()

	quit := make(chan os.Signal, 1)
	signal.Notify(quit, os.Interrupt, syscall.SIGTERM)

	shutdown := func() {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		if err := srv.Shutdown(ctx); err != nil {
			log.Printf("forced shutdown: %v", err)
		}
		haAgent.Close()
		// 停止字幕 worker（取消运行中的 ffmpeg/whisper 子进程）
		if deps != nil && deps.SubtitleSvc != nil {
			deps.SubtitleSvc.Stop()
		}
		if deps != nil && deps.LiteFS != nil {
			deps.LiteFS.Close()
		}
		log.Println("HTTP server closed")
	}

	select {
	case err := <-serverErr:
		log.Fatalf("server error: %v", err)
	case sig := <-quit:
		log.Printf("%s received, shutting down gracefully...", sig)
		shutdown()
	case reason := <-haAgent.SwitchRequested():
		// LiteFS 的 static 租约在 litefs.yml 里静态声明 candidate，换角色只能重新
		// mount。因此这里主动退出，由 litefs 跟随退出、容器 restart 策略拉起，
		// entrypoint 重新决议角色后以新身份挂载。
		log.Printf("[ha] %s；退出进程以新角色重启", reason)
		shutdown()
	}
}

// runResolveRole 执行开机角色决议并把结果写成 shell 可 source 的 env 文件。
//
// 失败时以非零码退出，由 docker-entrypoint.sh 决定兜底策略（保守起为 replica）。
func runResolveRole(projectRoot string) {
	cfg, err := config.Load(projectRoot)
	if err != nil {
		log.Printf("[ha] 加载配置失败: %v", err)
		os.Exit(1)
	}

	ctx, cancel := context.WithTimeout(context.Background(), resolveRoleTimeout)
	defer cancel()

	res := ha.Resolve(ctx, cfg.HA)
	log.Printf("[ha] 开机角色决议: role=%s epoch=%d 依据=%s", res.Role, res.Epoch, res.Reason)

	if err := ha.WriteRoleEnv(cfg.HA.RoleFilePath, res); err != nil {
		log.Printf("[ha] %v", err)
		os.Exit(1)
	}
}
