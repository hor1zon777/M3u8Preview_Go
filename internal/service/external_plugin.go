// external_plugin.go 声明式外部插件的解析、持久化与健康探测。
//
// 安全边界：manifest 不含任何可执行内容，导入只落一行 external_plugins 记录；
// 唯一的出站行为是可选的 healthUrl 探测——默认拒绝内网/保留地址
// （字面量校验仿 ValidateCaptchaEndpoint，探测走 util.SafeFetch 防 DNS rebinding），
// 自托管场景可设 PLUGIN_HEALTH_ALLOW_PRIVATE=1 放开（此时探测退化为普通 HTTP 客户端，
// 属管理员显式选择）。
package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"sync"
	"time"

	"gorm.io/gorm"

	"github.com/hor1zon777/m3u8-preview-go/internal/middleware"
	"github.com/hor1zon777/m3u8-preview-go/internal/model"
	"github.com/hor1zon777/m3u8-preview-go/internal/util"
)

// MaxManifestBytes manifest.json 的大小上限。声明式元数据 64KB 绰绰有余，
// 上限收紧是为了让"上传恶意大文件"这类攻击在入口就失败。
const MaxManifestBytes = 64 * 1024

// 健康探测参数。
const (
	healthProbeTimeout = 3 * time.Second
	healthCacheTTL     = 30 * time.Second
)

// ErrExternalPluginExists 同 ID 外部插件已存在（未带 overwrite 时导入报此错）。
var ErrExternalPluginExists = errors.New("同 ID 的外部插件已存在")

// externalPluginIDRe 与内置插件 Meta.ID 相同的 kebab-case 约定。
var externalPluginIDRe = regexp.MustCompile(`^[a-z][a-z0-9]*(-[a-z0-9]+)*$`)

// ExternalPluginManifest 是导入文件的 schema（v1）。
// 解码不开 DisallowUnknownFields：未来新增可选字段时旧版本可平滑忽略。
type ExternalPluginManifest struct {
	SchemaVersion int    `json:"schemaVersion"`
	ID            string `json:"id"`
	Name          string `json:"name"`
	Description   string `json:"description"`
	Version       string `json:"version"`
	Icon          string `json:"icon"`
	Category      string `json:"category"`
	HealthURL     string `json:"healthUrl"`
	Homepage      string `json:"homepage"`
}

// HealthResult 单次健康探测结果。
type HealthResult struct {
	OK        bool
	Detail    string
	CheckedAt time.Time
}

type healthCacheEntry struct {
	result   HealthResult
	has      bool
	fetching bool
}

// ExternalPluginService 外部插件的持久化与健康探测。
type ExternalPluginService struct {
	db           *gorm.DB
	allowPrivate bool

	mu    sync.Mutex
	cache map[string]*healthCacheEntry
}

// NewExternalPluginService 构造。allowPrivate 对应 PLUGIN_HEALTH_ALLOW_PRIVATE。
func NewExternalPluginService(db *gorm.DB, allowPrivate bool) *ExternalPluginService {
	return &ExternalPluginService{
		db:           db,
		allowPrivate: allowPrivate,
		cache:        make(map[string]*healthCacheEntry),
	}
}

// ParseManifest 解析并校验 manifest；返回解析结果与原文（存档用）。
// 所有校验失败都返回 user-facing 中文 AppError。
func (s *ExternalPluginService) ParseManifest(r io.Reader) (*ExternalPluginManifest, string, error) {
	data, err := io.ReadAll(io.LimitReader(r, MaxManifestBytes+1))
	if err != nil {
		return nil, "", middleware.WrapAppError(http.StatusBadRequest, "读取文件失败", err)
	}
	if len(data) > MaxManifestBytes {
		return nil, "", middleware.NewAppError(http.StatusBadRequest, "manifest 超过 64KB 上限")
	}

	var m ExternalPluginManifest
	if err := json.Unmarshal(data, &m); err != nil {
		return nil, "", middleware.NewAppError(http.StatusBadRequest, "manifest 不是合法的 JSON: "+err.Error())
	}
	if m.SchemaVersion != 1 {
		return nil, "", middleware.NewAppError(http.StatusBadRequest,
			fmt.Sprintf("不支持的 schemaVersion %d（当前仅支持 1）", m.SchemaVersion))
	}

	m.ID = strings.TrimSpace(m.ID)
	m.Name = strings.TrimSpace(m.Name)
	m.Description = strings.TrimSpace(m.Description)
	m.Version = strings.TrimSpace(m.Version)
	m.Icon = strings.TrimSpace(strings.ToLower(m.Icon))
	m.Category = strings.TrimSpace(m.Category)
	m.HealthURL = strings.TrimSpace(m.HealthURL)
	m.Homepage = strings.TrimSpace(m.Homepage)

	switch {
	case len(m.ID) < 3 || len(m.ID) > 64 || !externalPluginIDRe.MatchString(m.ID):
		return nil, "", middleware.NewAppError(http.StatusBadRequest,
			"id 必须是 3-64 字符的 kebab-case（小写字母/数字/中划线，如 jellyfin-bridge）")
	case m.Name == "" || len([]rune(m.Name)) > 50:
		return nil, "", middleware.NewAppError(http.StatusBadRequest, "name 必填且不超过 50 字符")
	case len([]rune(m.Description)) > 500:
		return nil, "", middleware.NewAppError(http.StatusBadRequest, "description 不超过 500 字符")
	case m.Version == "" || len(m.Version) > 32:
		return nil, "", middleware.NewAppError(http.StatusBadRequest, "version 必填且不超过 32 字符")
	case len([]rune(m.Category)) > 20:
		return nil, "", middleware.NewAppError(http.StatusBadRequest, "category 不超过 20 字符")
	case m.Icon != "" && !regexp.MustCompile(`^[a-z0-9-]{1,40}$`).MatchString(m.Icon):
		return nil, "", middleware.NewAppError(http.StatusBadRequest, "icon 只能是小写 lucide 图标名")
	}
	if m.Category == "" {
		m.Category = "外部服务"
	}

	if m.HealthURL != "" {
		if err := s.validateOutboundURL(m.HealthURL, "healthUrl", !s.allowPrivate); err != nil {
			return nil, "", err
		}
	}
	if m.Homepage != "" {
		// homepage 仅展示、服务端不请求，但仍须校验 scheme 防 javascript: 之类注入。
		if err := s.validateOutboundURL(m.Homepage, "homepage", false); err != nil {
			return nil, "", err
		}
	}

	return &m, string(data), nil
}

// validateOutboundURL 仿 ValidateCaptchaEndpoint 的可信 URL 校验。
// blockPrivate 为 true 时额外拦截内网/保留地址（字面量级；DNS 绑定由 SafeFetch 处理）。
func (s *ExternalPluginService) validateOutboundURL(raw, field string, blockPrivate bool) error {
	u, err := url.Parse(raw)
	if err != nil {
		return middleware.NewAppError(http.StatusBadRequest, field+" 解析失败: "+err.Error())
	}
	scheme := strings.ToLower(u.Scheme)
	if scheme != "http" && scheme != "https" {
		return middleware.NewAppError(http.StatusBadRequest, field+" 必须是 http 或 https")
	}
	if u.Host == "" {
		return middleware.NewAppError(http.StatusBadRequest, field+" 缺少 host")
	}
	if u.User != nil {
		return middleware.NewAppError(http.StatusBadRequest, field+" 不允许携带 userinfo")
	}
	if blockPrivate && util.IsPrivateHostname(u.Hostname()) {
		return middleware.NewAppError(http.StatusBadRequest,
			field+" 不允许指向内网或保留地址（自托管内网服务可设 PLUGIN_HEALTH_ALLOW_PRIVATE=1 放开）")
	}
	return nil
}

// Import 落库一条外部插件。已存在时：overwrite=false 返回 ErrExternalPluginExists；
// overwrite=true 按升级处理（更新元数据、保留 enabled 状态）。返回 (记录, 是否为升级, error)。
func (s *ExternalPluginService) Import(m *ExternalPluginManifest, raw string, overwrite bool) (*model.ExternalPlugin, bool, error) {
	toPtr := func(v string) *string {
		if v == "" {
			return nil
		}
		return &v
	}

	var existing model.ExternalPlugin
	err := s.db.Where("id = ?", m.ID).Take(&existing).Error
	switch {
	case err == nil:
		if !overwrite {
			return nil, false, ErrExternalPluginExists
		}
		existing.Name = m.Name
		existing.Description = m.Description
		existing.Version = m.Version
		existing.Icon = m.Icon
		existing.Category = m.Category
		existing.HealthURL = toPtr(m.HealthURL)
		existing.Homepage = toPtr(m.Homepage)
		existing.RawManifest = raw
		if err := s.db.Save(&existing).Error; err != nil {
			return nil, false, middleware.WrapAppError(http.StatusInternalServerError, "更新失败", err)
		}
		s.invalidateHealthCache(m.ID)
		return &existing, true, nil
	case errors.Is(err, gorm.ErrRecordNotFound):
		rec := model.ExternalPlugin{
			ID:          m.ID,
			Name:        m.Name,
			Description: m.Description,
			Version:     m.Version,
			Icon:        m.Icon,
			Category:    m.Category,
			HealthURL:   toPtr(m.HealthURL),
			Homepage:    toPtr(m.Homepage),
			Enabled:     true,
			RawManifest: raw,
		}
		if err := s.db.Create(&rec).Error; err != nil {
			return nil, false, middleware.WrapAppError(http.StatusInternalServerError, "写入失败", err)
		}
		return &rec, false, nil
	default:
		return nil, false, middleware.WrapAppError(http.StatusInternalServerError, "查询失败", err)
	}
}

// Delete 删除外部插件记录。
func (s *ExternalPluginService) Delete(id string) error {
	if err := s.db.Where("id = ?", id).Delete(&model.ExternalPlugin{}).Error; err != nil {
		return middleware.WrapAppError(http.StatusInternalServerError, "删除失败", err)
	}
	s.invalidateHealthCache(id)
	return nil
}

// SetEnabled 持久化启用状态。
func (s *ExternalPluginService) SetEnabled(id string, enabled bool) error {
	res := s.db.Model(&model.ExternalPlugin{}).Where("id = ?", id).Update("enabled", enabled)
	if res.Error != nil {
		return middleware.WrapAppError(http.StatusInternalServerError, "更新失败", res.Error)
	}
	if res.RowsAffected == 0 {
		return middleware.NewAppError(http.StatusNotFound, "外部插件不存在")
	}
	return nil
}

// ListAll 按导入顺序返回全部外部插件（启动时装载注册表用）。
func (s *ExternalPluginService) ListAll() ([]model.ExternalPlugin, error) {
	var rows []model.ExternalPlugin
	if err := s.db.Order("created_at ASC").Find(&rows).Error; err != nil {
		return nil, fmt.Errorf("查询外部插件: %w", err)
	}
	return rows, nil
}

// Health 返回某插件 healthUrl 的健康结果（30s 缓存）。
//
// 刻意设计为永不阻塞调用方：缓存新鲜直接返回；过期则返回旧值并异步触发一次
// 刷新（singleflight）。插件列表是 10s 轮询的同步接口，任何一次探测卡 3s
// 都会拖垮整页——首次无数据时返回"检查中"占位，下一轮轮询自然拿到真实结果。
func (s *ExternalPluginService) Health(id, healthURL string) HealthResult {
	s.mu.Lock()
	entry, ok := s.cache[id]
	if !ok {
		entry = &healthCacheEntry{}
		s.cache[id] = entry
	}
	fresh := entry.has && time.Since(entry.result.CheckedAt) < healthCacheTTL
	needFetch := !fresh && !entry.fetching
	if needFetch {
		entry.fetching = true
	}
	result := entry.result
	has := entry.has
	s.mu.Unlock()

	if needFetch {
		go func() {
			r := s.probe(healthURL)
			s.mu.Lock()
			if e, ok := s.cache[id]; ok {
				e.result = r
				e.has = true
				e.fetching = false
			}
			s.mu.Unlock()
		}()
	}

	if !has {
		return HealthResult{OK: true, Detail: "检查中…", CheckedAt: time.Now()}
	}
	return result
}

// probe 执行一次健康探测：只看状态码，响应体丢弃（不回显外部内容）。
func (s *ExternalPluginService) probe(rawURL string) HealthResult {
	ctx, cancel := context.WithTimeout(context.Background(), healthProbeTimeout)
	defer cancel()

	start := time.Now()
	var resp *http.Response
	var err error
	if s.allowPrivate {
		// 管理员显式放开内网探测：SafeFetch 会拒绝私有地址，这里退化为普通客户端。
		req, rerr := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
		if rerr != nil {
			return HealthResult{OK: false, Detail: "失败: " + rerr.Error(), CheckedAt: time.Now()}
		}
		client := &http.Client{Timeout: healthProbeTimeout}
		resp, err = client.Do(req)
	} else {
		resp, err = util.SafeFetch(ctx, rawURL, util.SafeFetchOptions{
			Timeout:      healthProbeTimeout,
			MaxRedirects: 2,
		})
	}
	if err != nil {
		msg := err.Error()
		if len(msg) > 120 {
			msg = msg[:120] + "…"
		}
		return HealthResult{OK: false, Detail: "失败: " + msg, CheckedAt: time.Now()}
	}
	defer func() { _ = resp.Body.Close() }()
	_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 1024))

	elapsed := time.Since(start).Milliseconds()
	ok := resp.StatusCode >= 200 && resp.StatusCode < 400
	return HealthResult{
		OK:        ok,
		Detail:    fmt.Sprintf("HTTP %d (%dms)", resp.StatusCode, elapsed),
		CheckedAt: time.Now(),
	}
}

func (s *ExternalPluginService) invalidateHealthCache(id string) {
	s.mu.Lock()
	delete(s.cache, id)
	s.mu.Unlock()
}
