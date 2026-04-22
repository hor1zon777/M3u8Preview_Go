package service

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"

	"gorm.io/gorm"

	"github.com/hor1zon777/m3u8-preview-go/internal/middleware"
	"github.com/hor1zon777/m3u8-preview-go/internal/model"
)

type CaptchaService struct {
	db     *gorm.DB
	client *http.Client
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

func (s *CaptchaService) loadSettings() captchaSettings {
	keys := []string{
		model.SettingEnableCaptcha,
		model.SettingCaptchaEndpoint,
		model.SettingCaptchaSiteKey,
		model.SettingCaptchaSecretKey,
	}
	var rows []model.SystemSetting
	s.db.Where("key IN ?", keys).Find(&rows)

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
	return cs
}

func (s *CaptchaService) IsEnabled() bool {
	return s.loadSettings().enabled
}

func (s *CaptchaService) GetPublicConfig() CaptchaPublicConfig {
	cs := s.loadSettings()
	if !cs.enabled {
		return CaptchaPublicConfig{Enabled: false}
	}
	return CaptchaPublicConfig{
		Enabled:  true,
		Endpoint: cs.endpoint,
		SiteKey:  cs.siteKey,
	}
}

func (s *CaptchaService) VerifyToken(token string) error {
	cs := s.loadSettings()
	if !cs.enabled {
		return nil
	}

	body, _ := json.Marshal(map[string]string{
		"token":      token,
		"secret_key": cs.secretKey,
	})

	resp, err := s.client.Post(
		cs.endpoint+"/api/v1/siteverify",
		"application/json",
		bytes.NewReader(body),
	)
	if err != nil {
		return middleware.WrapAppError(
			http.StatusBadGateway,
			"验证服务不可用",
			fmt.Errorf("captcha siteverify: %w", err),
		)
	}
	defer resp.Body.Close()

	var result struct {
		Success bool   `json:"success"`
		Error   string `json:"error"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return middleware.WrapAppError(
			http.StatusBadGateway,
			"验证服务响应异常",
			fmt.Errorf("captcha decode: %w", err),
		)
	}
	if !result.Success {
		msg := "验证码校验失败"
		if result.Error != "" {
			msg = result.Error
		}
		return middleware.NewAppError(http.StatusForbidden, msg)
	}
	return nil
}
