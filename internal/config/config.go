// Package config 加载运行时配置并做强度校验。
// 对齐 packages/server/src/config.ts：先加载根目录 .env，再用本地 .env 覆盖；
// 生产环境强制检查 JWT/PROXY 密钥非默认值且长度 >= 32，CORS_ORIGIN 必须显式配置。
package config

import (
	"fmt"
	"log"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/joho/godotenv"
)

type JWTConfig struct {
	Secret            string
	RefreshSecret     string
	AccessExpiresIn   time.Duration
	RefreshExpiresIn  time.Duration
	Kid               string
	KidPrev           string
	SecretPrev        string
	RefreshSecretPrev string
}

type CORSConfig struct {
	Origin string
}

type UploadConfig struct {
	MaxFileSize      int64
	AllowedMimeTypes []string
}

type ProxyConfig struct {
	Secret       string
	SignatureTTL time.Duration
}

type BcryptConfig struct {
	SaltRounds int
}

type Config struct {
	Port         int
	BindAddress  string
	NodeEnv      string
	DatabaseURL  string
	JWT          JWTConfig
	CORS         CORSConfig
	Upload       UploadConfig
	Proxy        ProxyConfig
	Bcrypt       BcryptConfig
	TrustCDN     bool
	UploadsDir   string
	DataDir      string
	ThumbnailConcurrency int
	PosterConcurrency    int
}

// 已知的弱默认值：这些值出现在生产必须 fatal
var weakDefaults = map[string]bool{
	"change-me-in-production":                             true,
	"change-me-in-production-refresh":                     true,
	"change-me-proxy-secret-in-production":                true,
	"dev-jwt-secret":                                      true,
	"dev-jwt-refresh-secret":                              true,
	"dev-proxy-secret":                                    true,
	"m3u8preview-docker-default-secret-key-change-me":     true,
	"m3u8preview-docker-default-refresh-key-change-me":    true,
}

// Load 读取 .env 并返回 Config。projectRoot 用来定位 .env 文件；传空时取可执行文件所在目录的上级。
func Load(projectRoot string) (*Config, error) {
	if projectRoot == "" {
		exe, err := os.Executable()
		if err == nil {
			projectRoot = filepath.Dir(exe)
		} else {
			projectRoot, _ = os.Getwd()
		}
	}

	// 两层加载：先根目录 .env（不存在不报错），后项目本地 .env override
	_ = godotenv.Load(filepath.Join(projectRoot, ".env"))
	_ = godotenv.Overload(filepath.Join(projectRoot, ".env.local"))

	nodeEnv := getenv("NODE_ENV", "development")

	cfg := &Config{
		Port:        atoiDefault(os.Getenv("PORT"), 3000),
		BindAddress: os.Getenv("BIND_ADDRESS"),
		NodeEnv:     nodeEnv,
		DatabaseURL: getenv("DATABASE_URL", "file:./data/m3u8preview.db"),
		JWT: JWTConfig{
			Secret:            getJWTSecret("JWT_SECRET", "dev-jwt-secret", nodeEnv),
			RefreshSecret:     getJWTSecret("JWT_REFRESH_SECRET", "dev-jwt-refresh-secret", nodeEnv),
			AccessExpiresIn:   15 * time.Minute,
			RefreshExpiresIn:  7 * 24 * time.Hour,
			Kid:               getenv("JWT_KID", "v1"),
			KidPrev:           os.Getenv("JWT_KID_PREV"),
			SecretPrev:        os.Getenv("JWT_SECRET_PREV"),
			RefreshSecretPrev: os.Getenv("JWT_REFRESH_SECRET_PREV"),
		},
		CORS: CORSConfig{
			Origin: getenv("CORS_ORIGIN", "http://localhost:5173"),
		},
		Upload: UploadConfig{
			MaxFileSize:      10 * 1024 * 1024,
			AllowedMimeTypes: []string{"image/jpeg", "image/png", "image/gif", "image/webp"},
		},
		Proxy: ProxyConfig{
			Secret:       getJWTSecret("PROXY_SECRET", "dev-proxy-secret", nodeEnv),
			SignatureTTL: 4 * time.Hour,
		},
		Bcrypt: BcryptConfig{
			SaltRounds: 12,
		},
		TrustCDN:             parseBoolDefault(os.Getenv("TRUST_CDN"), true),
		UploadsDir:           getenv("UPLOADS_DIR", filepath.Join(projectRoot, "uploads")),
		DataDir:              getenv("DATA_DIR", filepath.Join(projectRoot, "data")),
		ThumbnailConcurrency: clamp(atoiDefault(os.Getenv("THUMBNAIL_CONCURRENCY"), 5), 1, 20),
		PosterConcurrency:    clamp(atoiDefault(os.Getenv("POSTER_MIGRATION_CONCURRENCY"), 2), 1, 10),
	}

	// 默认绑定地址：生产 127.0.0.1，开发 0.0.0.0
	if cfg.BindAddress == "" {
		if nodeEnv == "production" {
			cfg.BindAddress = "127.0.0.1"
		} else {
			cfg.BindAddress = "0.0.0.0"
		}
	}

	if err := cfg.validate(); err != nil {
		return nil, err
	}
	return cfg, nil
}

func (c *Config) validate() error {
	if c.NodeEnv != "production" {
		return nil
	}
	// 生产环境强制校验
	if len(c.JWT.Secret) < 32 || weakDefaults[c.JWT.Secret] {
		return fmt.Errorf("FATAL: JWT_SECRET must be >= 32 chars and not a known default")
	}
	if len(c.JWT.RefreshSecret) < 32 || weakDefaults[c.JWT.RefreshSecret] {
		return fmt.Errorf("FATAL: JWT_REFRESH_SECRET must be >= 32 chars and not a known default")
	}
	if len(c.Proxy.Secret) < 32 || weakDefaults[c.Proxy.Secret] {
		return fmt.Errorf("FATAL: PROXY_SECRET must be >= 32 chars and not a known default")
	}
	if c.CORS.Origin == "" || c.CORS.Origin == "*" {
		return fmt.Errorf("FATAL: CORS_ORIGIN must be explicitly configured in production and cannot be *")
	}
	if _, err := url.Parse(c.CORS.Origin); err != nil {
		return fmt.Errorf("FATAL: CORS_ORIGIN must be a valid URL: %w", err)
	}
	return nil
}

// SQLitePath 将 DatabaseURL（file:./... 或 file:/abs/...）转成普通文件系统路径。
func (c *Config) SQLitePath() string {
	s := c.DatabaseURL
	s = strings.TrimPrefix(s, "file:")
	if !filepath.IsAbs(s) {
		// 相对路径基于 DataDir 的上级（和 Prisma 行为一致）
		abs, err := filepath.Abs(s)
		if err == nil {
			return abs
		}
	}
	return s
}

// MustLoad 在加载失败时 log.Fatal。
func MustLoad(projectRoot string) *Config {
	cfg, err := Load(projectRoot)
	if err != nil {
		log.Fatal(err)
	}
	return cfg
}

// ---- helpers ----

func getenv(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}

func getJWTSecret(key, devFallback, nodeEnv string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	if nodeEnv == "production" {
		return ""
	}
	return devFallback
}

func atoiDefault(s string, def int) int {
	if s == "" {
		return def
	}
	n, err := strconv.Atoi(s)
	if err != nil {
		return def
	}
	return n
}

func parseBoolDefault(s string, def bool) bool {
	if s == "" {
		return def
	}
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "1", "true", "yes":
		return true
	case "0", "false", "no":
		return false
	}
	return def
}

func clamp(n, lo, hi int) int {
	if n < lo {
		return lo
	}
	if n > hi {
		return hi
	}
	return n
}
