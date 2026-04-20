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
	"github.com/hor1zon777/m3u8-preview-go/internal/middleware"
)

func main() {
	projectRoot, err := os.Getwd()
	if err != nil {
		log.Fatalf("getwd: %v", err)
	}
	if filepath.Base(projectRoot) == "server" {
		projectRoot = filepath.Dir(filepath.Dir(projectRoot))
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

	engine, _ := app.Build(cfg, gdb)

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
		if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			serverErr <- err
		}
	}()

	quit := make(chan os.Signal, 1)
	signal.Notify(quit, os.Interrupt, syscall.SIGTERM)

	select {
	case err := <-serverErr:
		log.Fatalf("server error: %v", err)
	case sig := <-quit:
		log.Printf("%s received, shutting down gracefully...", sig)
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		if err := srv.Shutdown(ctx); err != nil {
			log.Fatalf("forced shutdown: %v", err)
		}
		log.Println("HTTP server closed")
	}
}
