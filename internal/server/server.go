// Package server 实现 FileServer 的 HTTP 服务：目录浏览、缩略图、下载、打包、搜索。
package server

import (
	"crypto/subtle"
	"embed"
	"errors"
	"io/fs"
	"log"
	"mime"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"
)

//go:embed web
var webFS embed.FS

var (
	errForbidden = errors.New("路径访问被拒绝")
	errNotFound  = errors.New("路径不存在")
)

// Server 文件服务器
type Server struct {
	root       string // 服务根目录（绝对路径）
	hidden     bool   // 是否显示隐藏文件
	auth       string // 可选口令 "user:pass"
	verbose    bool
	thumbs     *ThumbCache
	ff         *Ffmpeg
	fastStart  *FastStartCache
	imgSem     chan struct{} // 图片缩略图解码/整读并发上限
	started    time.Time
}

// Options 服务器选项
type Options struct {
	Hidden  bool
	Auth    string
	Verbose bool
}

// New 创建文件服务器
func New(root string, opts Options) *Server {
	return &Server{
		root:      filepath.Clean(root),
		hidden:    opts.Hidden,
		auth:      opts.Auth,
		verbose:   opts.Verbose,
		thumbs:    NewThumbCache(),
		ff:        FindFfmpeg(),
		fastStart: NewFastStartCache(),
		imgSem:    make(chan struct{}, thumbImgMaxConc),
		started:   time.Now(),
	}
}

// Handler 返回完整 HTTP 处理器
func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/info", s.handleInfo)
	mux.HandleFunc("GET /api/list", s.handleList)
	mux.HandleFunc("GET /api/thumb", s.handleThumb)
	mux.HandleFunc("GET /api/file", s.handleFile)
	mux.HandleFunc("GET /api/zip", s.handleZip)
	mux.HandleFunc("GET /api/search", s.handleSearch)
	mux.Handle("GET /", s.frontendHandler())

	var h http.Handler = mux
	// 全局安全头：nosniff 防止浏览器嗅探内容类型，
	// 配合 handleFile 对可执行 MIME 强制 attachment，阻断源内存储型 XSS。
	h = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Content-Type-Options", "nosniff")
		mux.ServeHTTP(w, r)
	})
	if s.auth != "" {
		h = s.basicAuth(h)
	}
	if s.verbose {
		h = s.accessLog(h)
	}
	return h
}

// handleInfo 返回服务端能力信息（前端据此决定视频缩略图策略与扩展名映射）
func (s *Server) handleInfo(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{
		"name":         "FileServer",
		"ffmpeg":       s.ff.Available(),
		"version":      "1.0.0",
		"kinds":        kindExtMap(), // 统一扩展名→类型映射（前端不再自维护）
		"search_limit": searchMaxLimit,
		"list_limit":   listMaxLimit,
	})
}

// frontendHandler 服务嵌入的前端（/、/style.css、/app.js）
func (s *Server) frontendHandler() http.Handler {
	sub, err := fs.Sub(webFS, "web")
	if err != nil {
		return http.NotFoundHandler()
	}
	fileSrv := http.FileServer(http.FS(sub))
	// 禁用浏览器对前端静态资源的缓存：开发迭代/重新构建后必须拿到最新 JS/CSS，
	// 否则旧缓存会让改动看似"无效"（用户点进去还是旧版行为）。
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasPrefix(r.URL.Path, "/api/") {
			w.Header().Set("Cache-Control", "no-store")
		} else {
			w.Header().Set("Cache-Control", "no-cache, no-store, must-revalidate")
		}
		w.Header().Set("Pragma", "no-cache")
		w.Header().Set("Expires", "0")
		fileSrv.ServeHTTP(w, r)
	})
}

// handleFile GET /api/file?path= 下载/预览文件（支持 Range 断点续传）
func (s *Server) handleFile(w http.ResponseWriter, r *http.Request) {
	abs, err := s.safePath(r.URL.Query().Get("path"))
	if err != nil {
		writeErr(w, errToStatus(err), err.Error())
		return
	}
	// --hidden 未开启时，隐藏路径（含其隐藏祖先目录）与列表/搜索一致地拒绝
	if s.hiddenBlocked(abs) {
		writeErr(w, http.StatusNotFound, "路径不存在")
		return
	}
	fi, err := os.Stat(abs)
	if err != nil {
		writeErr(w, errToStatus(err), "无法访问该文件")
		return
	}
	if fi.IsDir() {
		writeErr(w, http.StatusBadRequest, "该路径是目录，无法下载")
		return
	}
	f, err := os.Open(abs)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "打开文件失败")
		return
	}
	defer f.Close()

	name := fi.Name()
	// 设置正确的 MIME 与 inline/attachment 策略
	kind := fileKind(name, false)
	mimeType := fileMimeType(name, f)
	disposition := "attachment"
	switch kind {
	case "image", "video", "audio", "pdf", "text":
		disposition = "inline"
	}
	// HTML/SVG/JS 等可执行内容强制 attachment：即使攻击者能写入共享目录，
	// 直链打开也只是下载，不会在 FileServer 源内以 text/html 渲染执行脚本。
	// （配合全局 X-Content-Type-Options: nosniff 阻断嗅探）
	if scriptableExt[strings.ToLower(filepath.Ext(name))] {
		disposition = "attachment"
	}
	if ct := mime.FormatMediaType(disposition, map[string]string{"filename": sanitizeFilename(name)}); ct != "" {
		w.Header().Set("Content-Disposition", ct)
	}
	w.Header().Set("Content-Type", mimeType)
	w.Header().Set("Accept-Ranges", "bytes")

	// MP4 非 faststart（moov 在文件尾部或缺失）时，浏览器必须抓取整个索引才能起播，
	// 大视频表现为「点开后长时间黑屏」。有 ffmpeg 时先重封装为分片 MP4 缓存文件
	// （-c copy 几乎零开销），然后统一按缓存文件服务——初始请求与后续 seek 的
	// Range 请求共用同一份字节布局，不再出现「流式 remux + 原始文件 Range」
	// 两种表示混用导致的 seek 错位。
	if kind == "video" && strings.HasPrefix(mimeType, "video/mp4") &&
		s.ff != nil && mp4NeedsFastStart(abs) {
		if fp, cerr := s.fastStartVideo(abs, fi); cerr == nil {
			if ff, oerr := os.Open(fp); oerr == nil {
				f.Close() // 走缓存文件，原始句柄不再需要（避免整段流式期间持续占用）
				defer ff.Close()
				http.ServeContent(w, r, name, fi.ModTime(), ff)
				return
			} else {
				log.Printf("faststart 缓存打开失败，回退原始文件: %v", oerr)
			}
		} else {
			// 重封装失败（如 ffmpeg 被移除）：回退原始文件，仍可下载，
			// 仅浏览器需整段缓冲才能起播
			log.Printf("faststart 重封装失败，回退原始文件: %v", cerr)
		}
	}
	http.ServeContent(w, r, name, fi.ModTime(), f)
}

// fileMimeType 返回文件正确的 MIME 类型。
// 关键: 视频/音频扩展名必须返回浏览器能识别的媒体类型，否则浏览器会把视频当
// octet-stream 整段下载，不做流式/Range seek，导致点开后长时间黑屏等待。
func fileMimeType(name string, f *os.File) string {
	ext := strings.ToLower(filepath.Ext(name))
	if mt := mediaMime(ext); mt != "" {
		return mt
	}
	if mt := mime.TypeByExtension(ext); mt != "" {
		return mt
	}
	buf := make([]byte, 512)
	n, _ := f.Read(buf)
	f.Seek(0, 0)
	return http.DetectContentType(buf[:n])
}

// mediaMime 常见音视频扩展名 → 标准 MIME（浏览器必须识别为媒体类型才能流式播放）
func mediaMime(ext string) string {
	switch ext {
	case ".mp4", ".m4v", ".mp4v", ".mov":
		return "video/mp4"
	case ".mkv":
		return "video/x-matroska"
	case ".webm":
		return "video/webm"
	case ".avi":
		return "video/x-msvideo"
	case ".mpg", ".mpeg":
		return "video/mpeg"
	case ".ts", ".m2ts":
		return "video/mp2t"
	case ".wmv", ".asf":
		return "video/x-ms-wmv"
	case ".flv":
		return "video/x-flv"
	case ".3gp":
		return "video/3gpp"
	case ".mp3":
		return "audio/mpeg"
	case ".wav":
		return "audio/wav"
	case ".flac":
		return "audio/flac"
	case ".ogg", ".oga":
		return "audio/ogg"
	case ".ogv":
		return "video/ogg"
	case ".rm", ".rmvb":
		return "application/vnd.rn-realmedia"
	case ".aac":
		return "audio/aac"
	case ".m4a":
		return "audio/mp4"
	case ".opus":
		return "audio/ogg"
	case ".wma":
		return "audio/x-ms-wma"
	default:
		return ""
	}
}

// basicAuth 简单口令保护
func (s *Server) basicAuth(next http.Handler) http.Handler {
	user, pass, _ := strings.Cut(s.auth, ":")
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		u, p, ok := r.BasicAuth()
		// 恒定时间比较，避免字符串比较的时序侧信道（局域网内可被测量）
		if !ok ||
			subtle.ConstantTimeCompare([]byte(u), []byte(user)) != 1 ||
			subtle.ConstantTimeCompare([]byte(p), []byte(pass)) != 1 {
			w.Header().Set("WWW-Authenticate", `Basic realm="FileServer"`)
			writeErr(w, http.StatusUnauthorized, "需要访问口令")
			return
		}
		next.ServeHTTP(w, r)
	})
}

// scriptableExt 可执行/可脚本化内容：直链访问强制附件下载（防源内存储型 XSS）。
// 前端预览走 fetch + textContent 渲染，不受 Content-Disposition 影响。
var scriptableExt = map[string]bool{
	".html": true, ".htm": true, ".xhtml": true,
	".svg": true, ".js": true, ".mjs": true, ".cjs": true,
}

// sanitizeFilename 剔除文件名中的控制字符，作为 Content-Disposition 纵深防御，
// 避免任何残留的响应头注入面（POSIX 文件名可含 \r \n 等）。
func sanitizeFilename(name string) string {
	return strings.Map(func(r rune) rune {
		if r < 0x20 || r == 0x7f {
			return -1
		}
		return r
	}, name)
}

// hiddenBlocked 判断 abs 相对根目录的任一路径分量是否以 . 开头（隐藏）。
// --hidden 未开启时，隐藏文件/目录（含隐藏祖先目录）在 file/thumb/zip 上
// 与列表、搜索保持一致的拒绝语义，防止直链或打包绕过 UI 隐藏设置。
func (s *Server) hiddenBlocked(abs string) bool {
	if s.hidden {
		return false
	}
	rel, err := filepath.Rel(s.root, abs)
	if err != nil {
		return false
	}
	if rel == "." {
		return false // 根目录本身永不视为隐藏
	}
	for _, part := range strings.Split(filepath.ToSlash(rel), "/") {
		if strings.HasPrefix(part, ".") {
			return true
		}
	}
	return false
}

// accessLog 简单访问日志（-v 开启）
func (s *Server) accessLog(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		next.ServeHTTP(w, r)
		log.Printf("%s %s %s (%s)", r.RemoteAddr, r.Method, r.URL.RequestURI(), time.Since(start))
	})
}
