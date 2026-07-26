package handler

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"

	"github.com/hor1zon777/m3u8-preview-go/internal/middleware"
	"github.com/hor1zon777/m3u8-preview-go/internal/update"
)

func newUpdateTestRouter(t *testing.T, currentVersion string, disabled bool) *gin.Engine {
	t.Helper()
	gin.SetMode(gin.TestMode)
	h := NewUpdateHandler(update.New(t.TempDir(), currentVersion, "abc1234", disabled))
	r := gin.New()
	r.Use(middleware.ErrorHandler(false))
	h.Register(r.Group("/api/v1/admin/update"))
	return r
}

func TestUpdateStatusDevBuild(t *testing.T) {
	r := newUpdateTestRouter(t, "dev", false)

	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/api/v1/admin/update/status", nil))
	if w.Code != http.StatusOK {
		t.Fatalf("status 应 200: %d %s", w.Code, w.Body.String())
	}
	body := w.Body.String()
	if !strings.Contains(body, `"enabled":false`) || !strings.Contains(body, `"disabledReason":"dev-build"`) {
		t.Fatalf("dev 构建应 enabled=false + dev-build: %s", body)
	}

	// apply 硬拒
	w = httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/v1/admin/update/apply", strings.NewReader(`{"version":"9.9.9"}`))
	req.Header.Set("Content-Type", "application/json")
	r.ServeHTTP(w, req)
	if w.Code != http.StatusBadRequest || !strings.Contains(w.Body.String(), CodeUpdateDevBuild) {
		t.Fatalf("dev 构建 apply 应 400 %s: %d %s", CodeUpdateDevBuild, w.Code, w.Body.String())
	}
}

func TestUpdateApplyGuards(t *testing.T) {
	r := newUpdateTestRouter(t, "0.3.0", false)

	// 未检查过 → 409 NO_UPDATE
	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/v1/admin/update/apply", strings.NewReader(`{"version":"0.4.0"}`))
	req.Header.Set("Content-Type", "application/json")
	r.ServeHTTP(w, req)
	if w.Code != http.StatusConflict || !strings.Contains(w.Body.String(), CodeUpdateNoUpdate) {
		t.Fatalf("未检查过 apply 应 409 %s: %d %s", CodeUpdateNoUpdate, w.Code, w.Body.String())
	}

	// 缺 version 字段 → 400
	w = httptest.NewRecorder()
	req = httptest.NewRequest(http.MethodPost, "/api/v1/admin/update/apply", strings.NewReader(`{}`))
	req.Header.Set("Content-Type", "application/json")
	r.ServeHTTP(w, req)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("缺 version 应 400: %d %s", w.Code, w.Body.String())
	}
}

func TestUpdateDisabledByEnv(t *testing.T) {
	r := newUpdateTestRouter(t, "0.3.0", true)

	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/v1/admin/update/check", nil)
	r.ServeHTTP(w, req)
	if w.Code != http.StatusBadRequest || !strings.Contains(w.Body.String(), CodeUpdateDisabled) {
		t.Fatalf("UPDATE_DISABLED 时 check 应 400 %s: %d %s", CodeUpdateDisabled, w.Code, w.Body.String())
	}
}
