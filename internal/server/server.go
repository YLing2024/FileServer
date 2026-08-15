// Package server 实现 FileServer 的 HTTP 服务：目录浏览、缩略图、下载、打包、搜索。
package server

import (
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
	root    string // 服务根目录（绝对路径）
	hidden  bool   // 是否显示隐藏文件
	auth    string // 可选口令 "user:pass"
	verbose bool
	thumbs  *ThumbCache
	ff      *Ffmpeg
	started time.Time
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
		root:    filepath.Clean(root),
		hidden:  opts.Hidden,
		auth:    opts.Auth,
		verbose: opts.Verbose,
		thumbs:  NewThumbCache(),
		ff:      FindFfmpeg(),
		started: time.Now(),
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
	if s.auth != "" {
		h = s.basicAuth(h)
	}
	if s.verbose {
		h = s.accessLog(h)
	}
	return h
}

// handleInfo 返回服务端能力信息（前端据此决定视频缩略图策略）
func (s *Server) handleInfo(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{
		"name":    "FileServer",
		"ffmpeg":  s.ff.Available(),
		"version": "1.0.0",
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
	if ct := mime.FormatMediaType(disposition, map[string]string{"filename": name}); ct != "" {
		w.Header().Set("Content-Disposition", ct)
	}
	w.Header().Set("Content-Type", mimeType)
	w.Header().Set("Accept-Ranges", "bytes")

	// MP4 非 faststart（moov 在文件尾部或缺失）时，浏览器必须抓取整个索引才能起播，
	// 大视频表现为「点开后长时间黑屏」。有 ffmpeg 时改用 -c copy 分片重封装（几乎零开销），
	// 浏览器即可边下载边播放。仅对「初始请求」（无 Range，或从 0 开始的开区间 Range，
	// 即浏览器 <video> 的首次拉取）做流式转封装；精确 Range 断点续传仍走原始文件。
	rangeHeader := r.Header.Get("Range")
	isInitial := rangeHeader == "" || strings.HasPrefix(rangeHeader, "bytes=0-")
	if kind == "video" && strings.HasPrefix(mimeType, "video/mp4") &&
		s.ff != nil && isInitial && mp4NeedsFastStart(abs) {
		w.Header().Del("Content-Length")
		w.Header().Set("Transfer-Encoding", "chunked")
		w.WriteHeader(http.StatusOK)
		if err := s.ff.RemuxFastStart(r.Context(), abs, w); err != nil {
			// 转封装中途失败：响应头已发出，无法再回退，记录日志即可
			log.Printf("视频流式转封装失败: %v", err)
		}
		return
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
		if !ok || u != user || p != pass {
			w.Header().Set("WWW-Authenticate", `Basic realm="FileServer"`)
			writeErr(w, http.StatusUnauthorized, "需要访问口令")
			return
		}
		next.ServeHTTP(w, r)
	})
}

// accessLog 简单访问日志（-v 开启）
func (s *Server) accessLog(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		next.ServeHTTP(w, r)
		log.Printf("%s %s %s (%s)", r.RemoteAddr, r.Method, r.URL.RequestURI(), time.Since(start))
	})
}
