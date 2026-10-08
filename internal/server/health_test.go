package server

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// TestRequestIDHeader 断言每个响应都带 X-Request-Id，且传入的 id 被透传。
func TestRequestIDHeader(t *testing.T) {
	srv, _ := newTestServer(t)
	defer srv.Close()

	rr := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/api/health", nil))
	if rr.Header().Get("X-Request-Id") == "" {
		t.Error("响应缺少 X-Request-Id")
	}

	const want = "abc123def456"
	rr2 := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/health", nil)
	req.Header.Set("X-Request-Id", want)
	srv.Handler().ServeHTTP(rr2, req)
	if got := rr2.Header().Get("X-Request-Id"); got != want {
		t.Errorf("X-Request-Id 透传失败: got %q, want %q", got, want)
	}
}

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
