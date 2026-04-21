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
//
// 注意：PRAGMA 在 SQLite 中是"连接级"配置。
// glebarez/sqlite 以 DSN 查询参数 `_pragma=foreign_keys(1)` 形式传递的 PRAGMA 会在每次建连时执行，
// 因此即便 SetConnMaxLifetime 触发连接重建，新连接也会自动带上外键 / busy_timeout / WAL 设置。
// 旧版只用 db.Exec("PRAGMA ...") 对当前连接生效，连接回收后这些约束静默失效。
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

	// DSN 里挂 PRAGMA：glebarez/sqlite 驱动在每次建连时会执行这些 PRAGMA，
	// 因此连接池重建连接后外键 / busy_timeout / WAL 等仍然生效，不再受 ConnMaxLifetime 影响。
	dsn := dbPath + "?_pragma=foreign_keys(1)&_pragma=busy_timeout(5000)&_pragma=journal_mode(WAL)&_pragma=synchronous(NORMAL)"
	db, err := gorm.Open(sqlite.Open(dsn), gormCfg)
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

	// 首连接上再执行一次 PRAGMA 校验，确保 DSN 被驱动正确解析（防御性）。
	if err := db.Exec("PRAGMA foreign_keys = ON").Error; err != nil {
		return nil, fmt.Errorf("enable foreign_keys: %w", err)
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
