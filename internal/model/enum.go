// Package model 汇总全部 GORM 实体定义。
// enum.go 存放跨模型使用的枚举常量，与 TS 版 shared/types.ts 保持字符串一致。
package model

// 用户角色
const (
	RoleUser  = "USER"
	RoleAdmin = "ADMIN"
)

// 媒体状态
const (
	MediaStatusActive   = "ACTIVE"
	MediaStatusInactive = "INACTIVE"
	MediaStatusError    = "ERROR"
)

// 导入格式
const (
	ImportFormatText  = "TEXT"
	ImportFormatCSV   = "CSV"
	ImportFormatExcel = "EXCEL"
	ImportFormatJSON  = "JSON"
)

// 导入状态
const (
	ImportStatusPending = "PENDING"
	ImportStatusSuccess = "SUCCESS"
	ImportStatusPartial = "PARTIAL"
	ImportStatusFailed  = "FAILED"
)

// 字幕任务状态
const (
	SubtitleStatusPending  = "PENDING"  // 已入队，未开始
	SubtitleStatusRunning  = "RUNNING"  // 正在处理（音频抽取/ASR/翻译/写文件）
	SubtitleStatusDone     = "DONE"     // 完成，VTT 已落盘
	SubtitleStatusFailed   = "FAILED"   // 失败，error_msg 含原因
	SubtitleStatusDisabled = "DISABLED" // 管理员禁用此 media 的字幕生成
	// SubtitleStatusMissing 是合成状态，仅用于 admin 列表回填——
	// 当 media 行尚未对应任何 subtitle_jobs 行时（手动入队前的新建媒体），
	// 列表 LEFT JOIN 后用此值代替 NULL，让前端能选中并触发"重新生成所选"。
	SubtitleStatusMissing = "MISSING"
)

// 字幕处理阶段（progress 推送用）
//
// 阶段含义：
//   - queued：等待 audio_extract worker 抢占
//   - downloading：audio worker 正在下载 m3u8 切片
//   - extracting：audio worker 在用 ffmpeg 抽 PCM WAV
//   - encoding_intermediate：audio worker 把 WAV 编成 FLAC 中间产物
//   - audio_uploaded：FLAC 已上传到服务端中转池，等 asr_subtitle worker 抢占
//   - asr：subtitle worker 跑 whisper.cpp 识别
//   - translate：subtitle worker 调 LLM 翻译
//   - writing：subtitle worker 写 VTT 文件
//   - done：完成
//
// 兼容性：
//   - 旧 worker（不区分 audio/subtitle）只会上报 extracting/asr/translate/writing/done，
//     与新 stage 集合并集，互不冲突；服务端 stage 校验同时接受新旧值。
const (
	SubtitleStageQueued                = "queued"
	SubtitleStageDownloading           = "downloading"            // audio worker：下载 m3u8 切片
	SubtitleStageExtracting            = "extracting"             // ffmpeg 抽音频（audio worker / 旧版 worker 都用这个）
	SubtitleStageEncodingIntermediate  = "encoding_intermediate"  // audio worker：FLAC 编码
	SubtitleStageAudioUploaded         = "audio_uploaded"         // FLAC 落到服务端，等待 subtitle worker
	SubtitleStageASR                   = "asr"                    // whisper.cpp 识别
	SubtitleStageTranslate             = "translate"              // LLM 翻译
	SubtitleStageWriting               = "writing"                // 写 VTT
	SubtitleStageDone                  = "done"
)

// SubtitleAudioStages 与 SubtitleSubtitleStages 用于 fail/recover 时按 stage 分组判断。
//
// SubtitleAudioStages 是 audio_extract worker 持有任务期间可能上报的 stage 集合（不含 queued）；
// SubtitleSubtitleStages 是 asr_subtitle worker 持有任务期间可能上报的 stage 集合。
var (
	SubtitleAudioStages = map[string]bool{
		SubtitleStageDownloading:          true,
		SubtitleStageExtracting:           true,
		SubtitleStageEncodingIntermediate: true,
	}
	SubtitleSubtitleStages = map[string]bool{
		SubtitleStageASR:       true,
		SubtitleStageTranslate: true,
		SubtitleStageWriting:   true,
	}
)

// Worker capability 字符串（与 subtitle_workers.capabilities JSON 数组元素一致）。
const (
	WorkerCapAudioExtract = "audio_extract" // 下载 m3u8 + 抽音 + FLAC 编码
	WorkerCapASRSubtitle  = "asr_subtitle"  // ASR + 翻译 + 写 VTT
)

// 系统设置 key（集中管理，避免拼写错误）
const (
	SettingSiteName               = "siteName"
	SettingAllowRegistration      = "allowRegistration"
	SettingEnableRateLimit        = "enableRateLimit"
	SettingProxyAllowedExtensions = "proxyAllowedExtensions"
	// 新建/更新媒体时，若 posterUrl 为外部 http(s)，是否同步下载到本地。
	// 默认 false：保留原 URL，避免首次入库同步阻塞。
	SettingDownloadExternalPosters = "downloadExternalPosters"

	SettingEnableCaptcha    = "enableCaptcha"
	SettingCaptchaEndpoint  = "captchaEndpoint"
	SettingCaptchaSiteKey   = "captchaSiteKey"
	SettingCaptchaSecretKey = "captchaSecretKey"
	// SettingCaptchaManifestPubKey 是 Portcullis Tier 2 manifest 签名的 Ed25519 公钥，base64(32B)。
	// 配置后前端强制校验 /sdk/manifest.json 的 X-Portcullis-Signature；缺失或失配直接拒绝加载 SDK。
	// 留空则跳过签名校验，行为与 Tier 1 一致（仅 SRI integrity 防篡改）。
	SettingCaptchaManifestPubKey = "captchaManifestPubKey"
)
