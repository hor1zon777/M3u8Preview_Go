package handler

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"

	"github.com/hor1zon777/m3u8-preview-go/internal/version"
)

// serveHealth 跑一次 GET /api/health 并解析响应体。
func serveHealth(t *testing.T, status func() NodeStatus) (int, map[string]any) {
	t.Helper()
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.GET("/api/health", Health(status))

	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/api/health", nil))

	var body map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatalf("解析响应体: %v (原始: %s)", err, w.Body.String())
	}
	return w.Code, body
}

func TestHealthExposesVersion(t *testing.T) {
	code, body := serveHealth(t, nil)
	if code != http.StatusOK {
		t.Fatalf("期望 200，得到 %d", code)
	}
	// 登录页页脚与排查脚本都靠这三个字段，且必须无鉴权可读。
	for _, k := range []string{"status", "version", "commit", "buildTime"} {
		if _, ok := body[k]; !ok {
			t.Fatalf("响应缺少字段 %q: %v", k, body)
		}
	}
	if body["version"] != version.Version {
		t.Fatalf("version = %v，期望 %q", body["version"], version.Version)
	}
}

// TestHealthReplicaStillReturns200 守住一条容易被"优化"掉的设计：
// replica 是健康的，只是不接受写入。若这里对 replica 返回非 200，
// Docker HEALTHCHECK 会把备节点判为不健康并反复重启，热备直接失效。
func TestHealthReplicaStillReturns200(t *testing.T) {
	code, body := serveHealth(t, func() NodeStatus {
		return NodeStatus{Role: "replica", NodeID: "node-b", TXID: "00000000000004d2", Draining: true, BusyStreams: 2}
	})
	if code != http.StatusOK {
		t.Fatalf("replica 必须返回 200，得到 %d", code)
	}
	if body["status"] != "ok" {
		t.Fatalf("status = %v，期望 ok", body["status"])
	}
	if body["role"] != "replica" || body["nodeId"] != "node-b" {
		t.Fatalf("节点信息未正确回显: %v", body)
	}
	if body["draining"] != true {
		t.Fatalf("draining 未回显: %v", body)
	}
}

// TestHealthOmitsEmptyNodeFields 单机部署不该在响应里出现主备相关的空字段，
// 否则前端会误判成"启用了主备"而显示节点信息。
func TestHealthOmitsEmptyNodeFields(t *testing.T) {
	_, body := serveHealth(t, func() NodeStatus { return NodeStatus{Role: "primary"} })
	for _, k := range []string{"nodeId", "epoch", "txid"} {
		if _, ok := body[k]; ok {
			t.Fatalf("单机部署不应输出字段 %q: %v", k, body)
		}
	}
}

func TestVersionString(t *testing.T) {
	orig := version.Commit
	t.Cleanup(func() { version.Commit = orig })

	version.Commit = "877c77a"
	if got := version.String(); got != version.Version+" (877c77a)" {
		t.Fatalf("String() = %q", got)
	}

	// 本地未注入构建信息时不该显示 "(unknown)" 这种噪音。
	version.Commit = "unknown"
	if got := version.String(); got != version.Version {
		t.Fatalf("commit 未知时应只返回版本号，得到 %q", got)
	}
}
