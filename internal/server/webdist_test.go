package server

import (
	"io/fs"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// TestV2FrontendRoutes 验证 /v2/ 的静态资源、缓存头、SPA fallback，
// 并确认旧根路径 / 不受影响。
func TestV2FrontendRoutes(t *testing.T) {
	srv := New(t.TempDir(), Options{})
	t.Cleanup(srv.Close)
	handler := srv.Handler()

	get := func(target string) *httptest.ResponseRecorder {
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, target, nil))
		return rec
	}

	t.Run("index", func(t *testing.T) {
		rec := get("/v2/")
		if rec.Code != http.StatusOK {
			t.Fatalf("status = %d, want 200", rec.Code)
		}
		if cc := rec.Header().Get("Cache-Control"); !strings.Contains(cc, "no-store") {
			t.Errorf("index Cache-Control = %q, want no-store", cc)
		}
		if csp := rec.Header().Get("Content-Security-Policy"); csp == "" {
			t.Error("缺少 Content-Security-Policy")
		}
		if !strings.Contains(rec.Body.String(), `id="root"`) {
			t.Error("index.html 未包含 #root 挂载点")
		}
	})

	t.Run("redirect without slash", func(t *testing.T) {
		rec := get("/v2?path=videos")
		if rec.Code != http.StatusMovedPermanently {
			t.Fatalf("status = %d, want 301", rec.Code)
		}
		if loc := rec.Header().Get("Location"); loc != "/v2/?path=videos" {
			t.Errorf("Location = %q", loc)
		}
	})

	t.Run("hashed asset is immutable", func(t *testing.T) {
		matches, err := fs.Glob(webDistFS, "web-dist/v2/assets/*.js")
		if err != nil || len(matches) == 0 {
			t.Fatalf("构建产物缺少 v2/assets/*.js: matches=%v err=%v", matches, err)
		}
		name := strings.TrimPrefix(matches[0], "web-dist/v2/")
		rec := get("/v2/" + name)
		if rec.Code != http.StatusOK {
			t.Fatalf("status = %d, want 200", rec.Code)
		}
		if cc := rec.Header().Get("Cache-Control"); !strings.Contains(cc, "immutable") {
			t.Errorf("asset Cache-Control = %q, want immutable", cc)
		}
		if ct := rec.Header().Get("Content-Type"); !strings.Contains(ct, "javascript") {
			t.Errorf("asset Content-Type = %q, want javascript", ct)
		}
		if rec.Body.Len() == 0 {
			t.Error("asset 响应体为空")
		}
	})

	t.Run("unknown asset is 404", func(t *testing.T) {
		if rec := get("/v2/assets/not-exists.js"); rec.Code != http.StatusNotFound {
			t.Errorf("status = %d, want 404", rec.Code)
		}
	})

	t.Run("dotfile is hidden", func(t *testing.T) {
		if rec := get("/v2/.keep"); rec.Code != http.StatusNotFound {
			t.Errorf("status = %d, want 404", rec.Code)
		}
	})

	t.Run("spa fallback", func(t *testing.T) {
		rec := get("/v2/browse/deep/link")
		if rec.Code != http.StatusOK {
			t.Fatalf("status = %d, want 200", rec.Code)
		}
		if !strings.Contains(rec.Body.String(), `id="root"`) {
			t.Error("SPA fallback 未返回 index.html")
		}
	})

	t.Run("legacy root untouched", func(t *testing.T) {
		rec := get("/")
		if rec.Code != http.StatusOK {
			t.Fatalf("status = %d, want 200", rec.Code)
		}
		if !strings.Contains(rec.Body.String(), "/app.js") {
			t.Error("旧前端 index 未包含 app.js")
		}
		asset := get("/style.css")
		if asset.Code != http.StatusOK {
			t.Fatalf("旧 style.css status = %d, want 200", asset.Code)
		}
		if cc := asset.Header().Get("Cache-Control"); !strings.Contains(cc, "no-store") {
			t.Errorf("旧静态资源 Cache-Control = %q, want no-store", cc)
		}
	})
}
