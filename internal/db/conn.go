// Package db 负责 GORM 连接、PRAGMA 设置、AutoMigrate 与种子。
package db

import (
	"fmt"
	"log"
	"os"
	"path/filepath"
	"time"

	"github.com/glebarez/sqlite"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"

	"github.com/hor1zon777/m3u8-preview-go/internal/config"
)

// Open 建立 GORM 连接并设置 SQLite 关键 PRAGMA。
// foreign_keys=ON 对齐 Prisma 默认行为（SQLite 原生不强制外键，必须显式打开）；
// WAL 模式提升并发读性能，对备份恢复场景更友好。
func Open(cfg *config.Config) (*gorm.DB, error) {
	dbPath := cfg.SQLitePath()
	// 确保数据库文件目录存在
	if err := os.MkdirAll(filepath.Dir(dbPath), 0o755); err != nil {
		return nil, fmt.Errorf("mkdir data dir: %w", err)
	}

	gormLogLevel := logger.Warn
	if cfg.NodeEnv == "development" {
		gormLogLevel = logger.Info
	}

	gormCfg := &gorm.Config{
		Logger: logger.New(log.New(os.Stdout, "\r\n", log.LstdFlags), logger.Config{
			SlowThreshold:             200 * time.Millisecond,
			LogLevel:                  gormLogLevel,
			IgnoreRecordNotFoundError: true,
			ParameterizedQueries:      cfg.NodeEnv == "production",
			Colorful:                  false,
		}),
		NowFunc: func() time.Time { return time.Now().UTC() },
	}

	db, err := gorm.Open(sqlite.Open(dbPath), gormCfg)
	if err != nil {
		return nil, fmt.Errorf("open sqlite: %w", err)
	}

	sqlDB, err := db.DB()
	if err != nil {
		return nil, fmt.Errorf("get sql.DB: %w", err)
	}
	// SQLite 单写入 / 多读取；连接池过大没意义，且写入时会等锁
	sqlDB.SetMaxOpenConns(1)
	sqlDB.SetMaxIdleConns(1)
	sqlDB.SetConnMaxLifetime(time.Hour)

	// 开启外键（SQLite 默认关）
	if err := db.Exec("PRAGMA foreign_keys = ON").Error; err != nil {
		return nil, fmt.Errorf("enable foreign_keys: %w", err)
	}
	// WAL + NORMAL 组合：读写并发更好，崩溃安全性与 FULL 接近
	if err := db.Exec("PRAGMA journal_mode = WAL").Error; err != nil {
		return nil, fmt.Errorf("set journal_mode: %w", err)
	}
	if err := db.Exec("PRAGMA synchronous = NORMAL").Error; err != nil {
		return nil, fmt.Errorf("set synchronous: %w", err)
	}
	// 忙等待 5s 再报错，减少并发写冲突
	if err := db.Exec("PRAGMA busy_timeout = 5000").Error; err != nil {
		return nil, fmt.Errorf("set busy_timeout: %w", err)
	}

	return db, nil
}

// Close 统一关闭 *gorm.DB 底层 *sql.DB。
func Close(db *gorm.DB) error {
	sqlDB, err := db.DB()
	if err != nil {
		return err
	}
	return sqlDB.Close()
}
