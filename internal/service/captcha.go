package service

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"gorm.io/gorm"

	"github.com/hor1zon777/m3u8-preview-go/internal/middleware"
	"github.com/hor1zon777/m3u8-preview-go/internal/model"
)

type CaptchaService struct {
	db     *gorm.DB
	client *http.Client

	mu          sync.Mutex
	cachedCSP   string
	cacheExpiry time.Time
}

type CaptchaPublicConfig struct {
	Enabled  bool   `json:"enabled"`
	Endpoint string `json:"endpoint,omitempty"`
	SiteKey  string `json:"siteKey,omitempty"`
}

func NewCaptchaService(db *gorm.DB) *CaptchaService {
	return &CaptchaService{
		db:     db,
		client: &http.Client{Timeout: 5 * time.Second},
	}
}

type captchaSettings struct {
	enabled   bool
	endpoint  string
	siteKey   string
	secretKey string
}

func (s *CaptchaService) loadSettings() (captchaSettings, error) {
	keys := []string{
		model.SettingEnableCaptcha,
		model.SettingCaptchaEndpoint,
		model.SettingCaptchaSiteKey,
		model.SettingCaptchaSecretKey,
	}
	var rows []model.SystemSetting
	if err := s.db.Where("key IN ?", keys).Find(&rows).Error; err != nil {
		return captchaSettings{}, fmt.Errorf("load captcha settings: %w", err)
	}

	m := make(map[string]string, len(rows))
	for _, r := range rows {
		m[r.Key] = r.Value
	}

	cs := captchaSettings{
		endpoint:  strings.TrimRight(m[model.SettingCaptchaEndpoint], "/"),
		siteKey:   m[model.SettingCaptchaSiteKey],
		secretKey: m[model.SettingCaptchaSecretKey],
	}
	cs.enabled = m[model.SettingEnableCaptcha] == "true" &&
		cs.endpoint != "" && cs.siteKey != "" && cs.secretKey != ""
	return cs, nil
}

func (s *CaptchaService) GetPublicConfig() CaptchaPublicConfig {
	cs, err := s.loadSettings()
	if err != nil || !cs.enabled {
		return CaptchaPublicConfig{Enabled: false}
	}
	return CaptchaPublicConfig{
		Enabled:  true,
		Endpoint: cs.endpoint,
		SiteKey:  cs.siteKey,
	}
}

const maxCaptchaTokenLen = 4096

const cspCacheTTL = 30 * time.Second

func (s *CaptchaService) CSPOrigin() string {
	s.mu.Lock()
	defer s.mu.Unlock()

	if time.Now().Before(s.cacheExpiry) {
		return s.cachedCSP
	}

	origin := ""
	cs, err := s.loadSettings()
	if err != nil {
		log.Printf("[captcha] CSPOrigin loadSettings error: %v", err)
	} else if cs.endpoint == "" {
		log.Printf("[captcha] CSPOrigin: endpoint is empty")
	} else {
		if u, e := url.Parse(cs.endpoint); e == nil && u.Scheme != "" && u.Host != "" {
			origin = u.Scheme + "://" + u.Host
		} else {
			log.Printf("[captcha] CSPOrigin: url.Parse(%q) → scheme=%q host=%q err=%v", cs.endpoint, u.Scheme, u.Host, e)
		}
	}
	log.Printf("[captcha] CSPOrigin resolved: %q", origin)
	s.cachedCSP = origin
	s.cacheExpiry = time.Now().Add(cspCacheTTL)
	return origin
}

func (s *CaptchaService) VerifyIfEnabled(ctx context.Context, token string) error {
	cs, err := s.loadSettings()
	if err != nil {
		return middleware.WrapAppError(http.StatusInternalServerError, "读取验证码配置失败", err)
	}
	if !cs.enabled {
		return nil
	}
	if token == "" {
		return middleware.NewAppError(http.StatusBadRequest, "请完成验证码")
	}
	if len(token) > maxCaptchaTokenLen {
		return middleware.NewAppError(http.StatusBadRequest, "验证码 token 无效")
	}

	reqBody, _ := json.Marshal(map[string]string{
		"token":      token,
		"secret_key": cs.secretKey,
	})

	req, err := http.NewRequestWithContext(ctx, http.MethodPost,
		cs.endpoint+"/api/v1/siteverify", bytes.NewReader(reqBody))
	if err != nil {
		return middleware.WrapAppError(http.StatusInternalServerError, "构造验证请求失败", err)
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := s.client.Do(req)
	if err != nil {
		return middleware.WrapAppError(http.StatusBadGateway, "验证服务不可用", fmt.Errorf("captcha siteverify: %w", err))
	}
	defer func() { io.Copy(io.Discard, resp.Body); resp.Body.Close() }()

	if resp.StatusCode/100 != 2 {
		return middleware.NewAppError(http.StatusBadGateway,
			fmt.Sprintf("验证服务异常 (HTTP %d)", resp.StatusCode))
	}

	var result struct {
		Success bool   `json:"success"`
		Error   string `json:"error"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return middleware.WrapAppError(http.StatusBadGateway, "验证服务响应异常", fmt.Errorf("captcha decode: %w", err))
	}
	if !result.Success {
		return middleware.NewAppError(http.StatusForbidden, "验证码校验失败")
	}
	return nil
}
