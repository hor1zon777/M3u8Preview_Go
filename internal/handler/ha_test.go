package handler

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/glebarez/sqlite"
	"gorm.io/gorm"

	"github.com/hor1zon777/m3u8-preview-go/internal/config"
	"github.com/hor1zon777/m3u8-preview-go/internal/ha"
	"github.com/hor1zon777/m3u8-preview-go/internal/litefs"
	"github.com/hor1zon777/m3u8-preview-go/internal/middleware"
	"github.com/hor1zon777/m3u8-preview-go/internal/model"
	"github.com/hor1zon777/m3u8-preview-go/internal/service"
)

// newHATestRouter 组装一个档位 1（单机，无 Agent）的 HA 管理路由。
func newHATestRouter(t *testing.T, haCfg config.HAConfig) *gin.Engine {
	t.Helper()
	gin.SetMode(gin.TestMode)

	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	if err := db.AutoMigrate(&model.SystemSetting{}); err != nil {
		t.Fatalf("migrate: %v", err)
	}

	h := NewHAHandler(
		func() *ha.Agent { return nil }, // 未启用租约仲裁
		litefs.New("", ""),              // LITEFS_DIR 为空 → 恒 primary
		haCfg,
		service.NewAdminService(db),
		func() int { return 0 },
	)
	r := gin.New()
	r.Use(middleware.ErrorHandler(false))
	h.Register(r.Group("/api/v1/admin/ha"))
	return r
}

func TestHAStatusStandalone(t *testing.T) {
	r := newHATestRouter(t, config.HAConfig{})

	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/api/v1/admin/ha/status", nil))
	if w.Code != http.StatusOK {
		t.Fatalf("期望 200，得到 %d: %s", w.Code, w.Body.String())
	}

	var body struct {
		Success bool `json:"success"`
		Data    struct {
			Mode           string          `json:"mode"`
			SetupDismissed bool            `json:"setupDismissed"`
			AutoFailback   bool            `json:"autoFailback"`
			Local          struct{ Role string } `json:"local"`
			Peer           json.RawMessage `json:"peer"`
			Switch         struct{ Phase string } `json:"switch"`
		} `json:"data"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatalf("解析响应: %v (原始: %s)", err, w.Body.String())
	}
	if !body.Success || body.Data.Mode != "standalone" {
		t.Fatalf("单机部署应返回 mode=standalone: %s", w.Body.String())
	}
	if body.Data.Local.Role != "primary" {
		t.Fatalf("单机角色应恒为 primary: %s", w.Body.String())
	}
	// 缺省值：未忽略引导、自动回切开启、无进行中的切换、peer 不输出。
	if body.Data.SetupDismissed || !body.Data.AutoFailback {
		t.Fatalf("settings 缺省值错误: %s", w.Body.String())
	}
	if body.Data.Switch.Phase != ha.PhaseIdle {
		t.Fatalf("无 Agent 时 phase 应为 idle: %s", w.Body.String())
	}
	if len(body.Data.Peer) != 0 && string(body.Data.Peer) != "null" {
		t.Fatalf("档位 1 不应输出 peer: %s", w.Body.String())
	}
}

func TestHAStatusModeRoleAware(t *testing.T) {
	// 配了 LITEFS_DIR 但 Cloudflare 不全 → role-aware，前端据此提示补齐仲裁配置。
	r := newHATestRouter(t, config.HAConfig{LiteFSDir: "/litefs", NodeID: "node-a"})

	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/api/v1/admin/ha/status", nil))
	if !strings.Contains(w.Body.String(), `"mode":"role-aware"`) {
		t.Fatalf("期望 mode=role-aware: %s", w.Body.String())
	}
}

func TestHASwitchRejectedWhenNotEnabled(t *testing.T) {
	r := newHATestRouter(t, config.HAConfig{})

	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/v1/admin/ha/switch", strings.NewReader(`{"mode":"graceful"}`))
	req.Header.Set("Content-Type", "application/json")
	r.ServeHTTP(w, req)

	if w.Code != http.StatusBadRequest {
		t.Fatalf("未启用租约仲裁时应 400，得到 %d: %s", w.Code, w.Body.String())
	}
	if !strings.Contains(w.Body.String(), CodeHANotEnabled) {
		t.Fatalf("应返回机器可读 code %s: %s", CodeHANotEnabled, w.Body.String())
	}
}

func TestHASwitchRejectsInvalidMode(t *testing.T) {
	r := newHATestRouter(t, config.HAConfig{})

	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/v1/admin/ha/switch", strings.NewReader(`{"mode":"yolo"}`))
	req.Header.Set("Content-Type", "application/json")
	r.ServeHTTP(w, req)

	if w.Code != http.StatusBadRequest {
		t.Fatalf("非法 mode 应 400，得到 %d: %s", w.Code, w.Body.String())
	}
}
