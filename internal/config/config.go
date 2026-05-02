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
	// Origins 允许的前端来源列表。
	// 支持逗号分隔多个（常见场景：localhost + 127.0.0.1 + 生产域名），
	// 空白会被 trim。单值和多值配置都工作。
	Origins []string
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

// SubtitleConfig 控制字幕自动生成（日语 ASR + LLM 翻译为中文）功能。
//
// 部署要求（CPU 环境）：
//   - whisper.cpp 二进制（whisper-cli）需在 PATH 或显式指定 WhisperBin
//   - GGML 模型文件（推荐 ggml-medium-q5_0.bin / ggml-large-v3-q5_0.bin）
//   - ffmpeg 已在 PATH（项目其它模块已要求）
//
// 翻译走 OpenAI 兼容 API（DeepSeek / Qwen / OpenAI / 智谱 / 自建网关）：
//   - TranslateBaseURL 形如 https://api.deepseek.com（不含 /v1）
//   - 实际调用 <BaseURL>/v1/chat/completions
type SubtitleConfig struct {
	// Enabled 关闭后所有 subtitle 端点返回 503，worker 不启动
	Enabled bool
	// AutoGenerate 为 true 时：启动扫描 ACTIVE media 入队 + 新建 media 钩子入队
	AutoGenerate bool
	// WhisperBin 默认 "whisper-cli"（whisper.cpp 官方编译产物）
	WhisperBin string
	// WhisperModel GGML 模型文件绝对路径（如 /opt/whisper-models/ggml-medium-q5_0.bin）
	WhisperModel string
	// WhisperLanguage 源语言 ISO-639-1（默认 "ja"）
	WhisperLanguage string
	// WhisperThreads CPU 线程数（默认 0=自动按 NumCPU）
	WhisperThreads int
	// TranslateBaseURL OpenAI 兼容服务 base URL（不含 /v1）
	TranslateBaseURL string
	// TranslateAPIKey
	TranslateAPIKey string
	// TranslateModel 如 "deepseek-chat" / "qwen2.5-7b-instruct" / "gpt-4o-mini"
	TranslateModel string
	// TargetLang 目标语言（默认 "zh"）
	TargetLang string
	// BatchSize 一次发给 LLM 的字幕条数（默认 8）
	BatchSize int
	// MaxRetries 翻译失败重试次数（默认 2）
	MaxRetries int
	// SubtitlesDir 字幕文件目录（默认 <UploadsDir>/subtitles）
	SubtitlesDir string
	// SignatureTTL 签名 VTT URL 有效期（默认 4h，复用 Proxy 风格）
	SignatureTTL time.Duration

	// LocalWorkerEnabled 控制是否启动 in-process whisper.cpp worker。
	//   - true：进程内 ASR worker（单 CPU 串行，慢但自包含；老部署兜底）
	//   - false（默认）：仅接受远程 GPU worker 通过 /api/v1/worker/* pull 任务
	// 切到远程 worker 模式后，admin 重试 / 自动入队等行为不变；任务停留在 PENDING
	// 直到远程 worker 上线 claim。
	LocalWorkerEnabled bool

	// WorkerStaleThreshold 远程 worker 心跳超时阈值。
	// claimed_at 之后超过此时长仍无 last_heartbeat_at 更新，
	// RecoverStaleJobs 会把 RUNNING 重置回 PENDING 让其它 worker 重新认领。
	// 默认 10 分钟（覆盖正常 ASR + 翻译耗时；过短会误杀长视频）。
	WorkerStaleThreshold time.Duration

	// GlobalMaxConcurrency 全局正在 RUNNING 的字幕任务上限。
	// 0 表示不限（默认）。设置正值时 ClaimNextJob 会先查全局 RUNNING 数，
	// 已达上限则返回 nil，worker 自然 sleep 后重试。
	// 用于在共享 ASR / 翻译 API 配额时防止 worker 集群把后端 LLM 打垮。
	GlobalMaxConcurrency int
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
	Subtitle     SubtitleConfig
	TrustCDN     bool
	CookieSecure bool
	// CookieSecureAuto 为 true 时，handler 按 TLS 连接或可信 X-Forwarded-Proto=https 动态决定
	// cookie 的 Secure 标志；user 显式设置 COOKIE_SECURE=true/false 会退回静态值。
	CookieSecureAuto bool
	UploadsDir       string
	DataDir          string
	// ECDHPrivateKeyPath 登录加密协议用的长寿 ECDH P-256 私钥存放路径。
	// 默认 <DataDir>/ecdh.pem；首次启动自动生成（0600）。
	ECDHPrivateKeyPath string
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
			Origins: parseOrigins(getenv("CORS_ORIGIN", "http://localhost:5173")),
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
		CookieSecure:         parseCookieSecure(os.Getenv("COOKIE_SECURE"), getenv("CORS_ORIGIN", "http://localhost:5173")),
		CookieSecureAuto:     os.Getenv("COOKIE_SECURE") == "",
		UploadsDir:           getenv("UPLOADS_DIR", filepath.Join(projectRoot, "uploads")),
		DataDir:              getenv("DATA_DIR", filepath.Join(projectRoot, "data")),
		ThumbnailConcurrency: clamp(atoiDefault(os.Getenv("THUMBNAIL_CONCURRENCY"), 5), 1, 20),
		PosterConcurrency:    clamp(atoiDefault(os.Getenv("POSTER_MIGRATION_CONCURRENCY"), 2), 1, 10),
		Subtitle: SubtitleConfig{
			Enabled:              parseBoolDefault(os.Getenv("SUBTITLE_ENABLED"), false),
			AutoGenerate:         parseBoolDefault(os.Getenv("SUBTITLE_AUTO_GENERATE"), true),
			WhisperBin:           getenv("SUBTITLE_WHISPER_BIN", "whisper-cli"),
			WhisperModel:         os.Getenv("SUBTITLE_WHISPER_MODEL"),
			WhisperLanguage:      getenv("SUBTITLE_WHISPER_LANG", "ja"),
			WhisperThreads:       clamp(atoiDefault(os.Getenv("SUBTITLE_WHISPER_THREADS"), 0), 0, 64),
			TranslateBaseURL:     strings.TrimRight(os.Getenv("SUBTITLE_TRANSLATE_BASE_URL"), "/"),
			TranslateAPIKey:      os.Getenv("SUBTITLE_TRANSLATE_API_KEY"),
			TranslateModel:       getenv("SUBTITLE_TRANSLATE_MODEL", "deepseek-chat"),
			TargetLang:           getenv("SUBTITLE_TARGET_LANG", "zh"),
			BatchSize:            clamp(atoiDefault(os.Getenv("SUBTITLE_BATCH_SIZE"), 8), 1, 50),
			MaxRetries:           clamp(atoiDefault(os.Getenv("SUBTITLE_MAX_RETRIES"), 2), 0, 5),
			SignatureTTL:         4 * time.Hour,
			LocalWorkerEnabled:   parseBoolDefault(os.Getenv("SUBTITLE_LOCAL_WORKER_ENABLED"), false),
			WorkerStaleThreshold: time.Duration(clamp(atoiDefault(os.Getenv("SUBTITLE_WORKER_STALE_MINUTES"), 10), 1, 120)) * time.Minute,
			GlobalMaxConcurrency: clamp(atoiDefault(os.Getenv("SUBTITLE_GLOBAL_MAX_CONCURRENCY"), 0), 0, 1000),
		},
	}

	// SubtitlesDir：env 优先，否则 <UploadsDir>/subtitles
	if p := os.Getenv("SUBTITLE_DIR"); p != "" {
		cfg.Subtitle.SubtitlesDir = p
	} else {
		cfg.Subtitle.SubtitlesDir = filepath.Join(cfg.UploadsDir, "subtitles")
	}

	// ECDH 私钥路径：优先 env，否则落到 DataDir/ecdh.pem
	if p := os.Getenv("ECDH_PRIVATE_KEY_PATH"); p != "" {
		cfg.ECDHPrivateKeyPath = p
	} else {
		cfg.ECDHPrivateKeyPath = filepath.Join(cfg.DataDir, "ecdh.pem")
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
	if len(c.CORS.Origins) == 0 {
		return fmt.Errorf("FATAL: CORS_ORIGIN must be explicitly configured in production and cannot be *")
	}
	for _, origin := range c.CORS.Origins {
		if origin == "" || origin == "*" {
			return fmt.Errorf("FATAL: CORS_ORIGIN must be explicitly configured in production and cannot be *")
		}
		if _, err := url.Parse(origin); err != nil {
			return fmt.Errorf("FATAL: CORS_ORIGIN %q must be a valid URL: %w", origin, err)
		}
	}
	return nil
}

// SQLitePath 将 DatabaseURL（file:./... 或 file:/abs/...）转成普通文件系统路径。
func (c *Config) SQLitePath() string {
	s := c.DatabaseURL
	s = strings.TrimPrefix(s, "file:")

	// 如果路径以 / 开头（Unix 绝对路径），直接返回
	// 这在 Docker/Linux 环境中很常见：DATABASE_URL=file:/data/db.db
	if strings.HasPrefix(s, "/") {
		return s
	}

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

// parseCookieSecure 决定 Cookie 的 Secure 标志。
// 优先使用 COOKIE_SECURE 环境变量；未设置时根据 CORS_ORIGIN 是否为 HTTPS 自动推断。
// 多个 origin 时：只要有任何一个是 https://，就按 Secure 处理（保守选择——
// https 前端在 Secure cookie 下能工作，http 前端在 Secure cookie 下拿不到 cookie
// 会体现为"刷新掉登录"，比 http 前端拿到被窃取的 cookie 更安全）。
func parseCookieSecure(explicit, corsOrigin string) bool {
	if explicit != "" {
		return parseBoolDefault(explicit, false)
	}
	for _, origin := range parseOrigins(corsOrigin) {
		if strings.HasPrefix(strings.ToLower(origin), "https://") {
			return true
		}
	}
	return false
}

// parseOrigins 按逗号拆分 CORS_ORIGIN，trim 空格，去掉空元素，规范化后去重。
// 规范化：
//   - 去掉尾部 `/`（gin-contrib/cors 做精确比较，`http://x/` 永远不会匹配浏览器发来的 `http://x`）
//   - scheme 小写（`HTTP://X` → `http://X`；host 保留原大小写：IDN/punycode 语义允许但不规范）
//
// 不在这里做 URL 合法性校验——校验放在 Config.validate()，保持本函数纯粹。
func parseOrigins(raw string) []string {
	if raw == "" {
		return nil
	}
	parts := strings.Split(raw, ",")
	out := make([]string, 0, len(parts))
	seen := make(map[string]struct{}, len(parts))
	for _, p := range parts {
		s := strings.TrimSpace(p)
		if s == "" {
			continue
		}
		s = normalizeOrigin(s)
		if _, dup := seen[s]; dup {
			continue
		}
		seen[s] = struct{}{}
		out = append(out, s)
	}
	return out
}

// normalizeOrigin 把 scheme 小写、去掉尾 `/`，其它部分保持原样。
// 单星号 `*` 保持原样（validate 会拒掉生产环境的 `*`）。
func normalizeOrigin(s string) string {
	if s == "*" {
		return s
	}
	s = strings.TrimRight(s, "/")
	if idx := strings.Index(s, "://"); idx > 0 {
		s = strings.ToLower(s[:idx]) + s[idx:]
	}
	return s
}
