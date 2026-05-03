// Package service
// subtitle_settings.go 把字幕模块的运行时配置（whisper / 翻译 API / 启用开关等）
// 持久化到 system_settings 表，让管理员能在网页端修改而不必重启服务 / 改 .env。
//
// 持久化策略：
//   - 把 *config.SubtitleConfig 中的"用户可调"字段拍平为一组 system_settings 行
//   - key 前缀统一 "subtitle."（避免与其它模块设置冲突）
//   - 启动阶段调用 LoadSubtitleOverrides 把 DB 中的覆盖值合并到 cfg 上，
//     缺失的 key 维持 .env / 默认值——保证旧部署在没有 DB 行时行为不变
//   - admin 通过 SubtitleService.UpdateSettings 更新；写 DB 成功后再更新 in-memory cfg，
//     保证后续 ASR / 翻译客户端构造、worker 心跳判断都使用新值
//
// 不持久化的字段：
//   SubtitlesDir / SignatureTTL / WorkerStaleThreshold / GlobalMaxConcurrency /
//   LocalWorkerEnabled / MaxRetries —— 这些与部署环境强相关，仍由 .env 控制。
//
// 安全性：
//   - 只接收白名单 key，未列入的请求字段直接忽略
//   - 翻译 API Key 等敏感值不会出现在普通日志中（CurrentSettings 已脱敏）
package service

import (
	"errors"
	"fmt"
	"net/url"
	"strconv"
	"strings"

	"gorm.io/gorm"

	"github.com/hor1zon777/m3u8-preview-go/internal/config"
	"github.com/hor1zon777/m3u8-preview-go/internal/dto"
	"github.com/hor1zon777/m3u8-preview-go/internal/model"
)

// 字幕配置在 system_settings 中的 key 前缀。
const subtitleSettingPrefix = "subtitle."

// 用户可在网页端修改的字段对应的 system_settings key。
// 任何未列入这里的 *config.SubtitleConfig 字段都不会被持久化 / 修改。
const (
	settingSubtitleEnabled          = subtitleSettingPrefix + "enabled"
	settingSubtitleWhisperBin       = subtitleSettingPrefix + "whisperBin"
	settingSubtitleWhisperModel     = subtitleSettingPrefix + "whisperModel"
	settingSubtitleWhisperLanguage  = subtitleSettingPrefix + "whisperLanguage"
	settingSubtitleWhisperThreads   = subtitleSettingPrefix + "whisperThreads"
	settingSubtitleTranslateBaseURL = subtitleSettingPrefix + "translateBaseUrl"
	settingSubtitleTranslateAPIKey  = subtitleSettingPrefix + "translateApiKey"
	settingSubtitleTranslateModel   = subtitleSettingPrefix + "translateModel"
	settingSubtitleTargetLang       = subtitleSettingPrefix + "targetLang"
	settingSubtitleBatchSize        = subtitleSettingPrefix + "batchSize"
)

// allSubtitleSettingKeys 返回全部字幕相关 key（DB 查询时用 IN (...)）。
func allSubtitleSettingKeys() []string {
	return []string{
		settingSubtitleEnabled,
		settingSubtitleWhisperBin,
		settingSubtitleWhisperModel,
		settingSubtitleWhisperLanguage,
		settingSubtitleWhisperThreads,
		settingSubtitleTranslateBaseURL,
		settingSubtitleTranslateAPIKey,
		settingSubtitleTranslateModel,
		settingSubtitleTargetLang,
		settingSubtitleBatchSize,
	}
}

// LoadSubtitleOverrides 从 system_settings 读取字幕配置覆盖项并就地应用到 cfg。
// 任何未存储的 key 保持 cfg 原值（.env / 默认值）。
//
// 数据库表不存在 / 查询失败时返回错误；该错误在启动期是 fatal——避免在配置不一致时
// 把 worker 跑起来。如果 DB 行解析失败（脏数据），跳过该 key 但记录日志，保证启动不阻塞。
func LoadSubtitleOverrides(db *gorm.DB, cfg *config.SubtitleConfig) error {
	if db == nil || cfg == nil {
		return errors.New("LoadSubtitleOverrides: db or cfg nil")
	}

	var rows []model.SystemSetting
	if err := db.Where("key IN ?", allSubtitleSettingKeys()).Find(&rows).Error; err != nil {
		return fmt.Errorf("query subtitle settings: %w", err)
	}

	for _, row := range rows {
		applySubtitleSettingRow(cfg, row.Key, row.Value)
	}
	return nil
}

// applySubtitleSettingRow 解析单行 system_settings 并就地写到 cfg 上。
// 解析失败时跳过该字段（不返回 error，保证 LoadSubtitleOverrides 整体不被脏数据卡住）。
func applySubtitleSettingRow(cfg *config.SubtitleConfig, key, value string) {
	switch key {
	case settingSubtitleEnabled:
		cfg.Enabled = parseBoolWithDefault(value, cfg.Enabled)
	case settingSubtitleWhisperBin:
		if v := strings.TrimSpace(value); v != "" {
			cfg.WhisperBin = v
		}
	case settingSubtitleWhisperModel:
		cfg.WhisperModel = strings.TrimSpace(value)
	case settingSubtitleWhisperLanguage:
		if v := strings.TrimSpace(value); v != "" {
			cfg.WhisperLanguage = v
		}
	case settingSubtitleWhisperThreads:
		if n, err := strconv.Atoi(strings.TrimSpace(value)); err == nil {
			cfg.WhisperThreads = clampInt(n, 0, 64)
		}
	case settingSubtitleTranslateBaseURL:
		cfg.TranslateBaseURL = strings.TrimRight(strings.TrimSpace(value), "/")
	case settingSubtitleTranslateAPIKey:
		cfg.TranslateAPIKey = strings.TrimSpace(value)
	case settingSubtitleTranslateModel:
		if v := strings.TrimSpace(value); v != "" {
			cfg.TranslateModel = v
		}
	case settingSubtitleTargetLang:
		if v := strings.TrimSpace(value); v != "" {
			cfg.TargetLang = v
		}
	case settingSubtitleBatchSize:
		if n, err := strconv.Atoi(strings.TrimSpace(value)); err == nil {
			cfg.BatchSize = clampInt(n, 1, 50)
		}
	}
}

// validateSubtitleUpdate 对 admin 提交的 patch 做格式校验。
// 失败返回 user-facing 错误描述（中文），由 handler 直接 400 回显。
func validateSubtitleUpdate(req *dto.SubtitleSettingsUpdateRequest) error {
	if req.WhisperThreads != nil {
		if *req.WhisperThreads < 0 || *req.WhisperThreads > 64 {
			return fmt.Errorf("CPU 线程数应在 0-64 之间")
		}
	}
	if req.BatchSize != nil {
		if *req.BatchSize < 1 || *req.BatchSize > 50 {
			return fmt.Errorf("批大小应在 1-50 之间")
		}
	}
	if req.WhisperLanguage != nil {
		if v := strings.TrimSpace(*req.WhisperLanguage); v != "" {
			if len(v) > 16 {
				return fmt.Errorf("源语言代码过长")
			}
		}
	}
	if req.TargetLang != nil {
		if v := strings.TrimSpace(*req.TargetLang); v != "" {
			if len(v) > 16 {
				return fmt.Errorf("目标语言代码过长")
			}
		}
	}
	if req.TranslateBaseURL != nil {
		v := strings.TrimSpace(*req.TranslateBaseURL)
		if v != "" {
			u, err := url.Parse(v)
			if err != nil || u.Scheme == "" || u.Host == "" {
				return fmt.Errorf("翻译 baseURL 必须是完整 URL（如 https://api.deepseek.com）")
			}
			if u.Scheme != "http" && u.Scheme != "https" {
				return fmt.Errorf("翻译 baseURL 仅支持 http/https 协议")
			}
		}
	}
	if req.WhisperBin != nil {
		if len(*req.WhisperBin) > 512 {
			return fmt.Errorf("Whisper 二进制路径过长")
		}
	}
	if req.WhisperModel != nil {
		if len(*req.WhisperModel) > 1024 {
			return fmt.Errorf("Whisper 模型路径过长")
		}
	}
	if req.TranslateModel != nil {
		if len(*req.TranslateModel) > 128 {
			return fmt.Errorf("翻译模型名过长")
		}
	}
	if req.TranslateAPIKey != nil {
		if len(*req.TranslateAPIKey) > 512 {
			return fmt.Errorf("翻译 API Key 过长")
		}
	}
	return nil
}

// applySubtitlePatch 把 admin patch 应用到 cfg（in-memory）+ DB（system_settings）。
// 调用方负责持有 SubtitleService 写锁，保证此处的 DB 写与 cfg 更新原子可见。
//
// 字段策略：
//   - 翻译 API Key：明文 "********..." 形态视为"未修改"——前端展示脱敏值后用户没动过它
//     时不应被误覆盖为脱敏字符串
//   - 字符串字段允许设为空（清除），整数字段使用 nil 区分"未修改 vs 设为 0"
//
// 返回新的脱敏 settings 响应，便于 handler 直接回显给前端。
func applySubtitlePatch(db *gorm.DB, cfg *config.SubtitleConfig, req *dto.SubtitleSettingsUpdateRequest) error {
	if err := validateSubtitleUpdate(req); err != nil {
		return err
	}

	updates := make(map[string]string)

	if req.Enabled != nil {
		cfg.Enabled = *req.Enabled
		updates[settingSubtitleEnabled] = boolToStr(*req.Enabled)
	}
	if req.WhisperBin != nil {
		v := strings.TrimSpace(*req.WhisperBin)
		// 空字符串 = 还原为默认 "whisper-cli"
		if v == "" {
			v = "whisper-cli"
		}
		cfg.WhisperBin = v
		updates[settingSubtitleWhisperBin] = v
	}
	if req.WhisperModel != nil {
		v := strings.TrimSpace(*req.WhisperModel)
		cfg.WhisperModel = v
		updates[settingSubtitleWhisperModel] = v
	}
	if req.WhisperLanguage != nil {
		v := strings.TrimSpace(*req.WhisperLanguage)
		if v == "" {
			v = "ja"
		}
		cfg.WhisperLanguage = v
		updates[settingSubtitleWhisperLanguage] = v
	}
	if req.WhisperThreads != nil {
		n := clampInt(*req.WhisperThreads, 0, 64)
		cfg.WhisperThreads = n
		updates[settingSubtitleWhisperThreads] = strconv.Itoa(n)
	}
	if req.TranslateBaseURL != nil {
		v := strings.TrimRight(strings.TrimSpace(*req.TranslateBaseURL), "/")
		cfg.TranslateBaseURL = v
		updates[settingSubtitleTranslateBaseURL] = v
	}
	if req.TranslateAPIKey != nil {
		v := strings.TrimSpace(*req.TranslateAPIKey)
		// 兼容前端展示脱敏值后直接保存：含 * 的字符串视为"用户没修改"，跳过更新
		if !looksLikeMaskedAPIKey(v) {
			cfg.TranslateAPIKey = v
			updates[settingSubtitleTranslateAPIKey] = v
		}
	}
	if req.TranslateModel != nil {
		v := strings.TrimSpace(*req.TranslateModel)
		if v == "" {
			v = "deepseek-chat"
		}
		cfg.TranslateModel = v
		updates[settingSubtitleTranslateModel] = v
	}
	if req.TargetLang != nil {
		v := strings.TrimSpace(*req.TargetLang)
		if v == "" {
			v = "zh"
		}
		cfg.TargetLang = v
		updates[settingSubtitleTargetLang] = v
	}
	if req.BatchSize != nil {
		n := clampInt(*req.BatchSize, 1, 50)
		cfg.BatchSize = n
		updates[settingSubtitleBatchSize] = strconv.Itoa(n)
	}

	if len(updates) == 0 {
		return nil
	}

	// 一次事务写入所有 upsert，避免半成功状态。
	return db.Transaction(func(tx *gorm.DB) error {
		for key, value := range updates {
			if err := upsertSetting(tx, key, value); err != nil {
				return fmt.Errorf("upsert %s: %w", key, err)
			}
		}
		return nil
	})
}

// upsertSetting 先 update 命中行；不存在则 insert。SQLite 上比 ON CONFLICT 兼容性更好。
func upsertSetting(tx *gorm.DB, key, value string) error {
	res := tx.Model(&model.SystemSetting{}).
		Where("key = ?", key).
		Update("value", value)
	if res.Error != nil {
		return res.Error
	}
	if res.RowsAffected > 0 {
		return nil
	}
	return tx.Create(&model.SystemSetting{Key: key, Value: value}).Error
}

// looksLikeMaskedAPIKey 判断字符串是否是 CurrentSettings 输出的脱敏形态
// （形如 "abcd****wxyz" 或全 "*"），用于忽略未修改的脱敏回显值。
func looksLikeMaskedAPIKey(s string) bool {
	if s == "" {
		return false
	}
	// 含 "***" 视为脱敏；真实 API Key 不会包含连续三个星号
	return strings.Contains(s, "***")
}

// parseBoolWithDefault 解析 "true"/"false" 字符串；非法值返回默认值。
func parseBoolWithDefault(s string, def bool) bool {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "true", "1", "yes":
		return true
	case "false", "0", "no":
		return false
	}
	return def
}

func boolToStr(b bool) string {
	if b {
		return "true"
	}
	return "false"
}
