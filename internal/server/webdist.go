package server

import (
	"bytes"
	"embed"
	"io/fs"
	"mime"
	"net/http"
	"path"
	"strings"
	"time"
)

// webDistFS 嵌入 Vite 构建产物。目录里始终保留一个由 pnpm build 生成的
// .keep 占位文件，保证前端尚未构建时 //go:embed 也能通过编译；
// 运行时再检查 index.html，缺失时 /v2/ 返回 503 而不是 panic。
//
//go:embed all:web-dist
var webDistFS embed.FS

// v2ContentSecurityPolicy 与旧前端保持同一套限制；新前端不使用内联脚本，
// 样式暂时保留 unsafe-inline（Vite 注入的 style 属性与后续组件需要）。
const v2ContentSecurityPolicy = "default-src 'self'; " +
	"script-src 'self' blob:; " +
	"style-src 'self' 'unsafe-inline'; " +
	"img-src 'self' data: blob:; " +
	"media-src 'self' data: blob:; " +
	"connect-src 'self'; " +
	"font-src 'self' data:; " +
	"worker-src 'self' blob:; " +
	"object-src 'self'; " +
	"frame-ancestors 'none'; " +
	"base-uri 'none'; " +
	"form-action 'self'"

// v2Handler 服务 /v2/ 下的 React 前端。
//
// 缓存策略：index.html 与 SPA fallback 一律 no-store；assets/ 下带内容哈希的
// 文件长缓存（immutable）；其他静态文件按 no-cache 处理。未知的非资源路径
// 回退到 index.html，支持前端路由刷新。
func (s *Server) v2Handler() http.Handler {
	sub, err := fs.Sub(webDistFS, "web-dist")
	if err != nil {
		return v2UnavailableHandler()
	}
	index, err := fs.ReadFile(sub, "index.html")
	if err != nil {
		return v2UnavailableHandler()
	}

	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet && r.Method != http.MethodHead {
			w.Header().Set("Allow", "GET, HEAD")
			http.Error(w, "方法不允许", http.StatusMethodNotAllowed)
			return
		}

		rel := strings.TrimPrefix(r.URL.Path, "/v2/")
		if rel == "" {
			serveV2File(w, r, "index.html", index, false)
			return
		}

		name := strings.TrimPrefix(path.Clean("/"+rel), "/")
		// 点文件是构建占位或内部文件，不对浏览器暴露。
		if name == "" || strings.HasPrefix(path.Base(name), ".") {
			http.NotFound(w, r)
			return
		}

		if strings.HasPrefix(name, "assets/") {
			// vite.config.ts 用 base='/' + assetsDir='v2/assets'：构建产物在
			// web-dist/v2/assets，浏览器请求路径正好是 /v2/assets/*。
			data, readErr := fs.ReadFile(sub, "v2/"+name)
			if readErr != nil {
				http.NotFound(w, r)
				return
			}
			serveV2File(w, r, name, data, true)
			return
		}

		if info, statErr := fs.Stat(sub, name); statErr == nil && !info.IsDir() {
			data, readErr := fs.ReadFile(sub, name)
			if readErr != nil {
				http.NotFound(w, r)
				return
			}
			serveV2File(w, r, name, data, false)
			return
		}

		// SPA fallback：非资源路径交给前端路由。
		serveV2File(w, r, "index.html", index, false)
	})
}

// v2UnavailableHandler 前端产物缺失时的明确降级：只影响 /v2/，旧 / 照常工作。
func v2UnavailableHandler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-store")
		http.Error(w, "V2 前端尚未构建（缺少 internal/server/web-dist/index.html）", http.StatusServiceUnavailable)
	})
}

func serveV2File(w http.ResponseWriter, r *http.Request, name string, data []byte, immutable bool) {
	contentType := mime.TypeByExtension(path.Ext(name))
	if name == "index.html" {
		contentType = "text/html; charset=utf-8"
	}
	if contentType == "" {
		contentType = "application/octet-stream"
	}
	w.Header().Set("Content-Type", contentType)
	w.Header().Set("Referrer-Policy", "no-referrer")
	w.Header().Set("Content-Security-Policy", v2ContentSecurityPolicy)
	if immutable {
		w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
	} else {
		w.Header().Set("Cache-Control", "no-store, must-revalidate")
		w.Header().Set("Pragma", "no-cache")
		w.Header().Set("Expires", "0")
	}
	http.ServeContent(w, r, name, time.Time{}, bytes.NewReader(data))
}
