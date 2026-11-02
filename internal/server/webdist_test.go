package server

import (
	"io/fs"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// TestFrontendRoutes 验证 React 前端在根路径与 /v2/ 共用同一份构建产物，
// 并确认静态资源、缓存头、SPA fallback、旧资源清理与分享页入口。
func TestFrontendRoutes(t *testing.T) {
	srv := New(t.TempDir(), Options{})
	t.Cleanup(srv.Close)
	handler := srv.Handler()

	get := func(target string) *httptest.ResponseRecorder {
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, target, nil))
		return rec
	}

	t.Run("root index", func(t *testing.T) {
		rec := get("/")
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

	t.Run("v2 and root share one index", func(t *testing.T) {
		root := get("/")
		v2 := get("/v2/")
		if v2.Code != http.StatusOK {
			t.Fatalf("/v2/ status = %d, want 200", v2.Code)
		}
		if root.Body.String() != v2.Body.String() {
			t.Error("/ 与 /v2/ 返回了不同的 index.html")
		}
	})

	t.Run("v2 redirects to root", func(t *testing.T) {
		rec := get("/v2?path=videos")
		if rec.Code != http.StatusMovedPermanently {
			t.Fatalf("status = %d, want 301", rec.Code)
		}
		if loc := rec.Header().Get("Location"); loc != "/?path=videos" {
			t.Errorf("Location = %q", loc)
		}
	})

	t.Run("hashed asset is immutable", func(t *testing.T) {
		matches, err := fs.Glob(webDistFS, "web-dist/v2/assets/*.js")
		if err != nil || len(matches) == 0 {
			t.Fatalf("构建产物缺少 v2/assets/*.js: matches=%v err=%v", matches, err)
		}
		name := strings.TrimPrefix(matches[0], "web-dist/v2/")
		for _, target := range []string{"/v2/" + name, "/" + name} {
			rec := get(target)
			if rec.Code != http.StatusOK {
				t.Fatalf("%s status = %d, want 200", target, rec.Code)
			}
			if cc := rec.Header().Get("Cache-Control"); !strings.Contains(cc, "immutable") {
				t.Errorf("%s asset Cache-Control = %q, want immutable", target, cc)
			}
			if ct := rec.Header().Get("Content-Type"); !strings.Contains(ct, "javascript") {
				t.Errorf("%s asset Content-Type = %q, want javascript", target, ct)
			}
			if rec.Body.Len() == 0 {
				t.Errorf("%s asset 响应体为空", target)
			}
		}
	})

	t.Run("unknown asset is 404", func(t *testing.T) {
		for _, target := range []string{"/v2/assets/not-exists.js", "/assets/not-exists.js"} {
			if rec := get(target); rec.Code != http.StatusNotFound {
				t.Errorf("%s status = %d, want 404", target, rec.Code)
			}
		}
	})

	t.Run("dotfile is hidden", func(t *testing.T) {
		if rec := get("/v2/.keep"); rec.Code != http.StatusNotFound {
			t.Errorf("status = %d, want 404", rec.Code)
		}
	})

	t.Run("spa fallback", func(t *testing.T) {
		for _, target := range []string{"/v2/browse/deep/link", "/browse/deep/link"} {
			rec := get(target)
			if rec.Code != http.StatusOK {
				t.Fatalf("%s status = %d, want 200", target, rec.Code)
			}
			if !strings.Contains(rec.Body.String(), `id="root"`) {
				t.Errorf("%s SPA fallback 未返回 index.html", target)
			}
		}
	})

	t.Run("legacy assets are gone", func(t *testing.T) {
		for _, target := range []string{"/app.js", "/style.css", "/manage.js", "/share.js", "/hls.min.js"} {
			if rec := get(target); rec.Code != http.StatusNotFound {
				t.Errorf("旧资源 %s status = %d, want 404", target, rec.Code)
			}
		}
	})

	t.Run("unknown api is not html", func(t *testing.T) {
		if rec := get("/api/not-exists"); rec.Code != http.StatusNotFound {
			t.Errorf("status = %d, want 404", rec.Code)
		}
	})

	t.Run("share page uses v2 entry", func(t *testing.T) {
		rec := get("/s/test-token")
		if rec.Code != http.StatusOK {
			t.Fatalf("status = %d, want 200", rec.Code)
		}
		body := rec.Body.String()
		if !strings.Contains(body, `id="root"`) {
			t.Error("公开分享页未包含 #root 挂载点")
		}
		if !strings.Contains(body, "/v2/assets/") {
			t.Error("公开分享页未引用 /v2/assets 资源")
		}
		csp := rec.Header().Get("Content-Security-Policy")
		if !strings.Contains(csp, "style-src 'self'") || strings.Contains(csp, "unsafe-inline") {
			t.Errorf("公开分享页 CSP 不应允许内联样式: %q", csp)
		}
		if !strings.Contains(rec.Header().Get("X-Robots-Tag"), "noindex") {
			t.Error("公开分享页缺少 noindex")
		}
	})
}
