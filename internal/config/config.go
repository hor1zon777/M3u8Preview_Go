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

	// === v4 调度 / 重试 / 长轮询参数（全部带默认值，向后兼容） ===

	// DefaultMaxAttempts 新建任务时填入 subtitle_jobs.max_attempts 的默认值。
	// 含义为"一条任务允许的总尝试次数（含首次）"。
	// 默认 3：原始 1 + 最多 2 次 retriable 重试。
	DefaultMaxAttempts int

	// AudioUploadedTTL stale recovery 中"FLAC 已上传但长时间无 subtitle worker 接手"
	// 的回收阈值。v3 之前硬编码 24h；现在可调。
	// 默认 1 小时：让 audio worker 本地磁盘不被长期占用。
	AudioUploadedTTL time.Duration

	// StaleRecoveryInterval stale recovery ticker 周期。默认 30s（v3 是 60s）。
	// 越短崩溃恢复越快，但 SQL UPDATE 频率提升；通常 30s 足够。
	StaleRecoveryInterval time.Duration

	// ClaimLongPollMaxSec long-poll claim 端点服务端可 hold 的最长秒数。
	// 默认 25s；客户端可在请求里传更小值，服务端 clamp 到 [0, this]。
	// 0 = 关闭 long-poll（行为退回到 v3 短轮询）。
	ClaimLongPollMaxSec int

	// AudioFetchHoldSec broker GET /audio 端点上游 hold 时长（覆盖 audio worker 推流期间）。
	// 默认 300（5 分钟）：覆盖大 FLAC 文件慢上传；v3 是 30s。
	AudioFetchHoldSec int

	// AudioStreamFirstByteSec broker 等待 audio worker 收到 fetch 通知后开始推流的超时。
	// 默认 30s；v3 是 15s。
	AudioStreamFirstByteSec int

	// RetryBackoffSec 重试退避数组：retriable fail 后按 attempt 索引取退避秒数；
	// 越界用最后一个值。每次退避会在 ±20% 区间内加 jitter（在 service 层完成）。
	// 默认 [30, 120, 600] = 30s / 2min / 10min。
	RetryBackoffSec []int

	// MaxConcurrentTasksHint 服务端在 register 响应里下发给单个 worker 的并发建议。
	// 0 = 不下发，worker 退回本地 settings；> 0 时 worker 会用作 inflight 上限。
	// 默认 0。
	MaxConcurrentTasksHint int
}

// HAConfig 主备双节点高可用配置（完整设计见 docs/ha-failover.md）。
//
// 三档渐进式开关，方便分阶段上线：
//
//	1. LiteFSDir 为空
//	   完全单机模式：角色恒为 primary，写入不受限，不启动任何 HA 协程。
//	   本地 Windows 开发与既有单机部署都落在这一档，无需配置任何 HA 变量。
//
//	2. LiteFSDir 非空、CF 参数不全
//	   只做角色感知：读 <LiteFSDir>/.primary 判定自己是 primary 还是 replica，
//	   据此决定是否放行写请求、是否跑后台写循环。不参与租约仲裁、不切 DNS。
//	   适合"先手工验证 LiteFS 复制、暂不接管自动切换"的过渡期。
//
//	3. 全部配齐
//	   完整 HA：Cloudflare DNS TXT 租约仲裁 + 自动故障切换 + 自动回切。
//
// 注意：故障切换的时间参数（续租周期、租约 TTL、自降级死线、夺取保护期）
// 刻意不做成环境变量——它们之间存在必须满足的安全不等式
//
//	自降级死线 < 租约 TTL < 夺取保护期
//
// 一旦被误配置就会出现双主。这些常量固定在 internal/ha 包内，见该包文档。
type HAConfig struct {
	// LiteFSDir LiteFS FUSE 挂载目录（生产为 "/litefs"）。
	// 空 = 未启用 LiteFS，角色恒为 primary。
	LiteFSDir string

	// NodeID 本节点标识（如 "node-a"）。两台机器必须不同。
	NodeID string
	// PeerID 对端节点标识（如 "node-b"）。
	PeerID string
	// Preferred 标记本节点是不是"主"（两台里只有一台为 true）。
	//
	// 它决定三件事，都是二选一的决断，用布尔比数值优先级更贴切：
	//   - 自动回切：只有 Preferred 节点会在恢复后请求交还领导权
	//   - 首次引导：Preferred 立即创建租约，另一台先等一会儿避免同时创建
	//   - Cloudflare 不可达时的开机兜底：只有 Preferred 允许自升主，
	//     从而保证任何时刻至多一个节点能走这条无仲裁的路径
	Preferred bool

	// ForceRole 人工接管开关（"primary" / "replica"）。
	// 非空时开机决议直接采用它，跳过全部仲裁逻辑——这是排障与灰度期的逃生舱。
	ForceRole string

	// PeerBaseURL 对端节点 App 的可达地址（如 "https://node-b.example.com"）。
	// 用于 §6 决策规则中的"直连探测"信号——正是这个独立于 Cloudflare 的
	// 第二信号，让"CF API 故障"与"主备互相断网"两种情况不会被误判为节点宕机。
	PeerBaseURL string
	// PeerCAFile 可选：探测对端时额外信任的 CA 证书（PEM）。
	// 节点间用自签证书直连时填它，避免退化成 InsecureSkipVerify。
	PeerCAFile string

	// SelfAdvertiseURL 本节点 LiteFS API 的对外地址（如 "https://node-a.internal:20203"）。
	// 本节点是 primary 时写进 litefs.yml 的 advertise-url。
	SelfAdvertiseURL string
	// PeerAdvertiseURL 对端 LiteFS API 地址。本节点是 replica 时用它找 primary。
	PeerAdvertiseURL string

	// PeerPublicIP 对端公网 IP。计划内交接时由旧 owner 把 A 记录改指向对端，
	// 缩短"DNS 还指着旧节点但它已经不可写"的窗口。
	PeerPublicIP string

	// RoleFilePath 开机角色决议结果的落盘路径（默认 <DataDir>/litefs-role）。
	// entrypoint 在 litefs 挂载前调用 `server ha-resolve-role` 写入本文件，
	// 再据此展开 litefs.yml 里的 ${LITEFS_CANDIDATE}。
	RoleFilePath string

	// SelfPublicIP 本节点公网 IP，用于把主域名 A 记录改指向自己。
	SelfPublicIP string

	// --- Cloudflare ---

	// CFAPIToken 权限应收窄到 Zone → DNS → Edit，且只授权目标 zone。
	CFAPIToken string
	// CFZoneID 目标 zone ID。
	CFZoneID string
	// CFRecordName 用户流量的 A 记录全名（如 "media.example.com"）。
	CFRecordName string
	// CFLeaseRecord 租约 TXT 记录全名（如 "_ha-lease.example.com"）。
	CFLeaseRecord string
	// CFHandoffRecord 回切请求 TXT 记录全名（如 "_ha-handoff.example.com"）。
	// 与租约记录分开是为了让两个节点永不写同一条记录，从结构上消除写竞态。
	CFHandoffRecord string
}

// LiteFSEnabled 是否启用 LiteFS 角色感知（档位 2 及以上）。
func (h HAConfig) LiteFSEnabled() bool { return h.LiteFSDir != "" }

// LeaseEnabled 是否启用 Cloudflare 租约仲裁（档位 3）。
// 任一必填项缺失都会退回档位 2，而不是带着半套配置去做危险的自动切换。
func (h HAConfig) LeaseEnabled() bool {
	return h.LiteFSEnabled() &&
		h.NodeID != "" && h.PeerID != "" && h.NodeID != h.PeerID &&
		h.PeerBaseURL != "" &&
		h.SelfAdvertiseURL != "" && h.PeerAdvertiseURL != "" &&
		h.CFAPIToken != "" && h.CFZoneID != "" &&
		h.CFRecordName != "" && h.CFLeaseRecord != "" && h.CFHandoffRecord != "" &&
		h.SelfPublicIP != "" && h.PeerPublicIP != ""
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
	// PublicBaseURL 是服务端对外可见的绝对 URL（如 "https://media.example.com"，无尾斜杠）。
	// 用于在分布式 worker 协议中拼出 audioArtifactUrl 等绝对地址。
	// 留空时 ClaimedJob 仅返回相对路径，要求 worker 与服务端能共享同一 host。
	PublicBaseURL string
	// ECDHPrivateKeyPath 登录加密协议用的长寿 ECDH P-256 私钥存放路径。
	// 默认 <DataDir>/ecdh.pem；首次启动自动生成（0600）。
	ECDHPrivateKeyPath   string
	ThumbnailConcurrency int
	PosterConcurrency    int
	// PluginHealthAllowPrivate 允许外部插件的 healthUrl 指向内网/保留地址
	// （自托管场景健康检查内网服务常见）。默认 false：拒绝内网并用 SafeFetch 防 SSRF。
	PluginHealthAllowPrivate bool
	// UpdateDisabled 关闭应用内自更新（检查与安装均拒绝）的逃生开关。
	UpdateDisabled bool
	// HA 主备高可用；未配置时全部为零值，行为与改造前完全一致。
	HA HAConfig
}

// 已知的弱默认值：这些值出现在生产必须 fatal
var weakDefaults = map[string]bool{
	"change-me-in-production":                          true,
	"change-me-in-production-refresh":                  true,
	"change-me-proxy-secret-in-production":             true,
	"dev-jwt-secret":                                   true,
	"dev-jwt-refresh-secret":                           true,
	"dev-proxy-secret":                                 true,
	"m3u8preview-docker-default-secret-key-change-me":  true,
	"m3u8preview-docker-default-refresh-key-change-me": true,
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
		TrustCDN:                 parseBoolDefault(os.Getenv("TRUST_CDN"), true),
		PluginHealthAllowPrivate: parseBoolDefault(os.Getenv("PLUGIN_HEALTH_ALLOW_PRIVATE"), false),
		UpdateDisabled:           parseBoolDefault(os.Getenv("UPDATE_DISABLED"), false),
		CookieSecure:             parseCookieSecure(os.Getenv("COOKIE_SECURE"), getenv("CORS_ORIGIN", "http://localhost:5173")),
		CookieSecureAuto:         os.Getenv("COOKIE_SECURE") == "",
		UploadsDir:               getenv("UPLOADS_DIR", filepath.Join(projectRoot, "uploads")),
		DataDir:                  getenv("DATA_DIR", filepath.Join(projectRoot, "data")),
		PublicBaseURL:            strings.TrimRight(os.Getenv("PUBLIC_BASE_URL"), "/"),
		ThumbnailConcurrency:     clamp(atoiDefault(os.Getenv("THUMBNAIL_CONCURRENCY"), 5), 1, 20),
		PosterConcurrency:        clamp(atoiDefault(os.Getenv("POSTER_MIGRATION_CONCURRENCY"), 2), 1, 10),
		Subtitle: SubtitleConfig{
			Enabled:              parseBoolDefault(os.Getenv("SUBTITLE_ENABLED"), false),
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

			// v4 调度 / 重试参数
			DefaultMaxAttempts:      clamp(atoiDefault(os.Getenv("SUBTITLE_DEFAULT_MAX_ATTEMPTS"), 3), 1, 20),
			AudioUploadedTTL:        time.Duration(clamp(atoiDefault(os.Getenv("SUBTITLE_AUDIO_UPLOADED_TTL_MINUTES"), 60), 5, 24*60)) * time.Minute,
			StaleRecoveryInterval:   time.Duration(clamp(atoiDefault(os.Getenv("SUBTITLE_STALE_RECOVERY_SECONDS"), 30), 5, 300)) * time.Second,
			ClaimLongPollMaxSec:     clamp(atoiDefault(os.Getenv("SUBTITLE_CLAIM_LONGPOLL_MAX_SEC"), 25), 0, 60),
			AudioFetchHoldSec:       clamp(atoiDefault(os.Getenv("SUBTITLE_AUDIO_FETCH_HOLD_SEC"), 300), 30, 1800),
			AudioStreamFirstByteSec: clamp(atoiDefault(os.Getenv("SUBTITLE_AUDIO_STREAM_FIRSTBYTE_SEC"), 30), 5, 300),
			RetryBackoffSec:         parseIntListEnv(os.Getenv("SUBTITLE_RETRY_BACKOFF_SECONDS"), []int{30, 120, 600}),
			MaxConcurrentTasksHint:  clamp(atoiDefault(os.Getenv("SUBTITLE_MAX_CONCURRENT_HINT"), 0), 0, 64),
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

	// HA：全部可选。LITEFS_DIR 未设置时整块保持零值，服务行为与单机部署一致。
	cfg.HA = HAConfig{
		LiteFSDir:        strings.TrimRight(os.Getenv("LITEFS_DIR"), "/"),
		NodeID:           strings.TrimSpace(os.Getenv("HA_NODE_ID")),
		PeerID:           strings.TrimSpace(os.Getenv("HA_PEER_ID")),
		Preferred:        parseBoolDefault(os.Getenv("HA_PREFERRED"), false),
		ForceRole:        strings.ToLower(strings.TrimSpace(os.Getenv("HA_FORCE_ROLE"))),
		PeerBaseURL:      strings.TrimRight(os.Getenv("HA_PEER_BASE_URL"), "/"),
		PeerCAFile:       strings.TrimSpace(os.Getenv("HA_PEER_CA_FILE")),
		SelfAdvertiseURL: strings.TrimRight(os.Getenv("HA_SELF_ADVERTISE_URL"), "/"),
		PeerAdvertiseURL: strings.TrimRight(os.Getenv("HA_PEER_ADVERTISE_URL"), "/"),
		PeerPublicIP:     strings.TrimSpace(os.Getenv("HA_PEER_PUBLIC_IP")),
		RoleFilePath:     os.Getenv("HA_ROLE_FILE"),
		SelfPublicIP:     strings.TrimSpace(os.Getenv("HA_SELF_PUBLIC_IP")),
		CFAPIToken:       strings.TrimSpace(os.Getenv("CF_API_TOKEN")),
		CFZoneID:         strings.TrimSpace(os.Getenv("CF_ZONE_ID")),
		CFRecordName:     strings.TrimSpace(os.Getenv("HA_DNS_RECORD")),
		CFLeaseRecord:    strings.TrimSpace(os.Getenv("HA_LEASE_RECORD")),
		CFHandoffRecord:  strings.TrimSpace(os.Getenv("HA_HANDOFF_RECORD")),
	}
	if cfg.HA.RoleFilePath == "" {
		cfg.HA.RoleFilePath = filepath.Join(cfg.DataDir, "litefs-role")
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

// parseIntListEnv 解析 "30,120,600" 这种逗号分隔整数列表。空 / 任一非法 → fallback。
// 用于 SUBTITLE_RETRY_BACKOFF_SECONDS 等"列表型"参数。
func parseIntListEnv(s string, fallback []int) []int {
	s = strings.TrimSpace(s)
	if s == "" {
		return fallback
	}
	parts := strings.Split(s, ",")
	out := make([]int, 0, len(parts))
	for _, p := range parts {
		p = strings.TrimSpace(p)
		if p == "" {
			continue
		}
		n, err := strconvAtoiPositive(p)
		if err != nil {
			return fallback
		}
		out = append(out, n)
	}
	if len(out) == 0 {
		return fallback
	}
	return out
}

// strconvAtoiPositive 等同于 strconv.Atoi 但要求 ≥1。
// 单独抽出来避免在 parseIntListEnv 顶部引 strconv 改动太大。
func strconvAtoiPositive(s string) (int, error) {
	n := 0
	for _, c := range s {
		if c < '0' || c > '9' {
			return 0, fmt.Errorf("non-digit %q", c)
		}
		n = n*10 + int(c-'0')
		if n > 1_000_000 {
			return 0, fmt.Errorf("too large")
		}
	}
	if n < 1 {
		return 0, fmt.Errorf("must be >= 1")
	}
	return n, nil
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
