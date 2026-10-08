package server

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// TestHealthEndpoint 断言 GET /api/health 返回 ok 与构建版本字段，
// 字段与 --version 同源（internal/version），保证可追溯。
func TestHealthEndpoint(t *testing.T) {
	srv, _ := newTestServer(t)
	defer srv.Close()

	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/health", nil)
	srv.Handler().ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rr.Code)
	}
	var body struct {
		OK       bool   `json:"ok"`
		Version  string `json:"version"`
		Commit   string `json:"commit"`
		Go       string `json:"go"`
		Platform string `json:"platform"`
	}
	if err := json.Unmarshal(rr.Body.Bytes(), &body); err != nil {
		t.Fatalf("解析响应失败: %v (body=%s)", err, rr.Body.String())
	}
	if !body.OK {
		t.Error("ok 应为 true")
	}
	if body.Version == "" {
		t.Error("version 不应为空")
	}
	if !strings.Contains(body.Go, "go") {
		t.Errorf("go = %q, 应形如 goX.Y.Z", body.Go)
	}
	if !strings.Contains(body.Platform, "/") {
		t.Errorf("platform = %q, 应形如 GOOS/GOARCH", body.Platform)
	}
}
