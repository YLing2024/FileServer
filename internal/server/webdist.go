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
// 运行时再检查 index.html，缺失时根路径与 /v2/ 返回 503 而不是 panic。
//
//go:embed all:web-dist
var webDistFS embed.FS

// init 补两个系统 mime.types 里通常缺失的类型：
//   - .webmanifest：PWA 清单，缺了会按 octet-stream 下发，浏览器仍能解析但不够规范
//   - .ico：部分精简发行版没有登记，图标会被当成二进制下载
func init() {
	_ = mime.AddExtensionType(".webmanifest", "application/manifest+json")
	_ = mime.AddExtensionType(".ico", "image/x-icon")
}

// spaContentSecurityPolicy 前端页面的严格 CSP：默认仅同源；脚本仅同源 + blob
// （hls.js 的 Worker 由 blob 创建）；图片/媒体允许 data:/blob:（缩略图 dataURL、
// MediaSource）；样式允许内联（布局用 CSS 变量在运行时写入）；PDF embed 走同源。
// 切换根路径时保持与旧前端一致的策略，不得放宽。
const spaContentSecurityPolicy = "default-src 'self'; " +
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

// spaHandler 服务 React 前端，同时挂到 / 与 /v2/，两者返回同一份
// index.html（不存在第二套页面）。构建产物里的哈希资源固定在
// web-dist/v2/assets，浏览器请求路径仍是 /v2/assets/*，保持切换前的资源
// 路径与缓存策略不变。
//
// 缓存策略：index.html 与 SPA fallback 一律 no-store；assets/ 下带内容哈希的
// 文件长缓存（immutable）；其他静态文件按 no-cache 处理。未知的非资源路径
// 回退到 index.html，支持前端路由刷新；带扩展名的未知文件返回 404，避免把
// HTML 当成旧版 JS/CSS 返回给浏览器。
func (s *Server) spaHandler() http.Handler {
	sub, err := fs.Sub(webDistFS, "web-dist")
	if err != nil {
		return frontendUnavailableHandler()
	}
	index, err := fs.ReadFile(sub, "index.html")
	if err != nil {
		return frontendUnavailableHandler()
	}

	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Referrer-Policy", "no-referrer")
		w.Header().Set("Content-Security-Policy", spaContentSecurityPolicy)

		if r.Method != http.MethodGet && r.Method != http.MethodHead {
			w.Header().Set("Allow", "GET, HEAD")
			http.Error(w, "方法不允许", http.StatusMethodNotAllowed)
			return
		}
		// 未匹配的 API 路由绝不能落到 SPA 上，否则客户端会收到 HTML 而非 JSON。
		if strings.HasPrefix(r.URL.Path, "/api/") {
			http.NotFound(w, r)
			return
		}

		rel := r.URL.Path
		switch {
		case rel == "/" || rel == "/v2/":
			serveSPAFile(w, r, "index.html", index, false)
			return
		case strings.HasPrefix(rel, "/v2/"):
			rel = strings.TrimPrefix(rel, "/v2/")
		default:
			rel = strings.TrimPrefix(rel, "/")
		}

		name := strings.TrimPrefix(path.Clean("/"+rel), "/")
		// 点文件是构建占位或内部文件，不对浏览器暴露。
		if name == "" || strings.HasPrefix(path.Base(name), ".") {
			http.NotFound(w, r)
			return
		}

		if strings.HasPrefix(name, "assets/") {
			// Vite 的 assetsDir 是 v2/assets：/v2/assets/* 与 /assets/* 都映射到
			// web-dist/v2/assets/*，便于根路径与旧前缀共用同一份哈希资源。
			data, readErr := fs.ReadFile(sub, "v2/"+name)
			if readErr != nil {
				http.NotFound(w, r)
				return
			}
			serveSPAFile(w, r, name, data, true)
			return
		}

		// index.html/share.html 只经各自的入口返回，不直接暴露为普通静态文件。
		if name != "index.html" && name != "share.html" {
			if info, statErr := fs.Stat(sub, name); statErr == nil && !info.IsDir() {
				data, readErr := fs.ReadFile(sub, name)
				if readErr != nil {
					http.NotFound(w, r)
					return
				}
				serveSPAFile(w, r, name, data, false)
				return
			}
		}

		// 带扩展名的未知文件（例如旧版 /app.js）不要回退成 HTML。
		if path.Ext(name) != "" {
			http.NotFound(w, r)
			return
		}

		// SPA fallback：非资源路径交给前端路由。
		serveSPAFile(w, r, "index.html", index, false)
	})
}

// frontendUnavailableHandler 前端产物缺失时的明确降级：根路径返回 503，
// 不影响 API。
func frontendUnavailableHandler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-store")
		http.Error(w, "前端尚未构建（缺少 internal/server/web-dist/index.html）", http.StatusServiceUnavailable)
	})
}

func serveSPAFile(w http.ResponseWriter, r *http.Request, name string, data []byte, immutable bool) {
	contentType := mime.TypeByExtension(path.Ext(name))
	if name == "index.html" {
		contentType = "text/html; charset=utf-8"
	}
	if contentType == "" {
		contentType = "application/octet-stream"
	}
	w.Header().Set("Content-Type", contentType)
	if immutable {
		w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
	} else {
		w.Header().Set("Cache-Control", "no-store, must-revalidate")
		w.Header().Set("Pragma", "no-cache")
		w.Header().Set("Expires", "0")
	}
	http.ServeContent(w, r, name, time.Time{}, bytes.NewReader(data))
}
