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

// frontendHandler 服务嵌入的前端（/、/style.css、/app.js）
func (s *Server) frontendHandler() http.Handler {
	sub, err := fs.Sub(webFS, "web")
	if err != nil {
		return http.NotFoundHandler()
	}
	return http.FileServer(http.FS(sub))
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
	mimeType := mime.TypeByExtension(filepath.Ext(name))
	if mimeType == "" {
		buf := make([]byte, 512)
		n, _ := f.Read(buf)
		mimeType = http.DetectContentType(buf[:n])
		f.Seek(0, 0)
	}
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
	http.ServeContent(w, r, name, fi.ModTime(), f)
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
