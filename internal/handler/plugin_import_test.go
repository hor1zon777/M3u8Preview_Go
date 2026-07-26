package handler

import (
	"bytes"
	"fmt"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/glebarez/sqlite"
	"gorm.io/gorm"

	"github.com/hor1zon777/m3u8-preview-go/internal/middleware"
	"github.com/hor1zon777/m3u8-preview-go/internal/model"
	"github.com/hor1zon777/m3u8-preview-go/internal/plugin"
	"github.com/hor1zon777/m3u8-preview-go/internal/service"
)

// builtinFake 测试用内置插件（非 External，删除应被保护）。
type builtinFake struct{ id string }

func (f *builtinFake) Meta() plugin.Meta     { return plugin.Meta{ID: f.id, Name: f.id} }
func (f *builtinFake) Enabled() bool         { return true }
func (f *builtinFake) SetEnabled(bool) error { return nil }
func (f *builtinFake) Status() plugin.Status { return plugin.Status{Healthy: true} }

func newPluginTestRouter(t *testing.T) (*gin.Engine, *plugin.Registry) {
	t.Helper()
	gin.SetMode(gin.TestMode)

	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	if err := db.AutoMigrate(&model.ExternalPlugin{}); err != nil {
		t.Fatalf("migrate: %v", err)
	}

	reg := plugin.NewRegistry()
	if err := reg.Register(&builtinFake{id: "subtitle-worker"}); err != nil {
		t.Fatalf("register builtin: %v", err)
	}
	h := NewPluginHandler(reg, service.NewExternalPluginService(db, false))

	r := gin.New()
	r.Use(middleware.ErrorHandler(false))
	h.Register(r.Group("/api/v1/admin/plugins"))
	return r, reg
}

// postManifest 以 multipart 上传一份 manifest JSON。
func postManifest(t *testing.T, r *gin.Engine, body string, overwrite bool) *httptest.ResponseRecorder {
	t.Helper()
	var buf bytes.Buffer
	mw := multipart.NewWriter(&buf)
	fw, err := mw.CreateFormFile("file", "manifest.json")
	if err != nil {
		t.Fatalf("create form file: %v", err)
	}
	if _, err := fw.Write([]byte(body)); err != nil {
		t.Fatalf("write form file: %v", err)
	}
	_ = mw.Close()

	url := "/api/v1/admin/plugins/import"
	if overwrite {
		url += "?overwrite=true"
	}
	req := httptest.NewRequest(http.MethodPost, url, &buf)
	req.Header.Set("Content-Type", mw.FormDataContentType())
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	return w
}

func validManifest(id string) string {
	return fmt.Sprintf(`{"schemaVersion":1,"id":%q,"name":"测试插件","description":"desc","version":"1.0.0","icon":"server","category":"外部服务"}`, id)
}

func TestPluginImportAndList(t *testing.T) {
	r, reg := newPluginTestRouter(t)

	w := postManifest(t, r, validManifest("demo-svc"), false)
	if w.Code != http.StatusCreated {
		t.Fatalf("导入应 201，得到 %d: %s", w.Code, w.Body.String())
	}
	if !strings.Contains(w.Body.String(), `"external":true`) {
		t.Fatalf("响应应标记 external=true: %s", w.Body.String())
	}
	if _, ok := reg.Get("demo-svc"); !ok {
		t.Fatalf("导入后注册表应有 demo-svc")
	}

	// 重复导入：无 overwrite 409，带 overwrite 成功升级
	if w := postManifest(t, r, validManifest("demo-svc"), false); w.Code != http.StatusConflict ||
		!strings.Contains(w.Body.String(), CodePluginExists) {
		t.Fatalf("重复导入应 409 %s: %d %s", CodePluginExists, w.Code, w.Body.String())
	}
	if w := postManifest(t, r, validManifest("demo-svc"), true); w.Code != http.StatusCreated {
		t.Fatalf("overwrite 导入应成功: %d %s", w.Code, w.Body.String())
	}
}

func TestPluginImportRejectsBuiltinConflictAndBadManifest(t *testing.T) {
	r, _ := newPluginTestRouter(t)

	// 与内置插件同 id
	if w := postManifest(t, r, validManifest("subtitle-worker"), false); w.Code != http.StatusConflict ||
		!strings.Contains(w.Body.String(), CodePluginIDConflictBuiltin) {
		t.Fatalf("与内置冲突应 409 %s: %d %s", CodePluginIDConflictBuiltin, w.Code, w.Body.String())
	}

	// 非法 id / 非法 schema / healthUrl 指向内网
	for name, body := range map[string]string{
		"非法id":     `{"schemaVersion":1,"id":"Bad_ID","name":"x","version":"1"}`,
		"schema版本": `{"schemaVersion":2,"id":"ok-id","name":"x","version":"1"}`,
		"内网health": `{"schemaVersion":1,"id":"ok-id","name":"x","version":"1","healthUrl":"http://192.168.1.1/health"}`,
	} {
		if w := postManifest(t, r, body, false); w.Code != http.StatusBadRequest {
			t.Fatalf("%s 应 400，得到 %d: %s", name, w.Code, w.Body.String())
		}
	}
}

func TestPluginDeleteProtectsBuiltin(t *testing.T) {
	r, reg := newPluginTestRouter(t)

	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest(http.MethodDelete, "/api/v1/admin/plugins/subtitle-worker", nil))
	if w.Code != http.StatusBadRequest || !strings.Contains(w.Body.String(), CodePluginBuiltinProtected) {
		t.Fatalf("删除内置插件应 400 %s: %d %s", CodePluginBuiltinProtected, w.Code, w.Body.String())
	}

	// 外部插件可删除，删除后注册表同步摘除
	if w := postManifest(t, r, validManifest("temp-svc"), false); w.Code != http.StatusCreated {
		t.Fatalf("导入失败: %d %s", w.Code, w.Body.String())
	}
	w = httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest(http.MethodDelete, "/api/v1/admin/plugins/temp-svc", nil))
	if w.Code != http.StatusOK {
		t.Fatalf("删除外部插件应 200: %d %s", w.Code, w.Body.String())
	}
	if _, ok := reg.Get("temp-svc"); ok {
		t.Fatalf("删除后注册表不应残留 temp-svc")
	}
}
