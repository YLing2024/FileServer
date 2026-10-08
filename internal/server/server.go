// Package server 实现 FileServer 的 HTTP 服务：目录浏览、缩略图、下载、打包、搜索。
package server

import (
	"embed"
	"errors"
	"io/fs"
	"log/slog"
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

// cacheDirName 服务内部缓存目录名（位于服务根目录下）。
// 该名字为保留名：无论是否开启 --hidden，列表/搜索/zip/直链等所有
// HTTP 访问都不可见、不可访问——否则开启 --hidden 后，缩略图/HLS 转码
// 缓存（含转码副本）会被当作普通点开头文件暴露成可下载资源。
const cacheDirName = ".FileServer"

// isCacheEntry 条目名是否为保留缓存目录名（大小写不敏感：
// Windows 文件系统大小写不敏感，需兜住 .fileserver 等大小写变体；
// POSIX 上同名不同大小写的目录极罕见，误伤面可忽略）。
func isCacheEntry(name string) bool { return strings.EqualFold(name, cacheDirName) }

// deps 服务器运行时依赖的组件句柄。
type deps struct {
	thumbs *ThumbCache
	ff     *Ffmpeg
	hls    *HlsManager
}

// Server 文件服务器
type Server struct {
	cfg  config     // 运行配置（root/hidden/auth/verbose/转码开关）
	deps deps       // 组件句柄（缩略图缓存 / ffmpeg / HLS 管理器）
	sem  semaphores // 并发信号量

	list      listCache      // 目录列表短缓存（返回/翻页秒开）
	layouts   layoutCache    // MP4 顶层布局缓存（thumb-src 抽帧源用）
	faststart faststartState // 小文件 faststart 重封装：缓存目录 + 单飞防重 + 测试钩子

	pw      *prewarmState  // moov 预读预热（机械硬盘冷读提速）
	pb      *playbackState // 直链播放活动跟踪（预热/重封装据此让路）
	started time.Time
}

// Options 服务器选项
type Options struct {
	Hidden  bool
	Auth    string
	Verbose bool
	FFmpeg  bool // 开启冷门格式（MKV/RMVB/HEVC 等）的在线转码播放 + 服务端缩略图
}

// New 创建文件服务器
func New(root string, opts Options) *Server {
	root = filepath.Clean(root)
	// 解析根目录中的符号链接 / junction / subst：safePath 拿 EvalSymlinks 后的
	// real 与 s.cfg.root 做前缀比较，若根目录本身是链接而 s.cfg.root 保存词法路径，两者
	// 前缀永不匹配 → 全站每个请求都 403。解析失败（路径暂不存在等）回退原值并
	// 记日志，交由后续 MkdirAll / 调用方报错。
	if resolved, err := filepath.EvalSymlinks(root); err == nil {
		root = resolved
	} else {
		slog.Warn("解析服务根目录真实路径失败，沿用原路径", "path", root, "err", err)
	}

	// 缓存基目录：共享根目录下的隐藏文件夹 .FileServer（不往系统目录写文件）。
	// 根目录不可写（只读介质）时回退系统临时目录。
	base := filepath.Join(root, cacheDirName)
	if err := os.MkdirAll(base, 0o755); err != nil {
		base = filepath.Join(os.TempDir(), "FileServer")
		_ = os.MkdirAll(base, 0o755)
	}
	fsDir := filepath.Join(base, "faststart")
	_ = os.MkdirAll(fsDir, 0o755)
	go cleanupOldFiles(fsDir, 7*24*time.Hour) // 清理 7 天前的重封装缓存

	ff := FindFfmpeg()

	srv := &Server{
		cfg: config{
			root:    root,
			hidden:  opts.Hidden,
			auth:    opts.Auth,
			verbose: opts.Verbose,
		},
		deps: deps{
			thumbs: NewThumbCache(root),
			ff:     ff,
			hls:    NewHlsManager(root),
		},
		sem: semaphores{
			img:     make(chan struct{}, thumbImgMaxConc),
			ffThumb: make(chan struct{}, ffThumbMaxConc),
		},
		faststart: faststartState{dir: fsDir},
		started:   time.Now(),
		pw:        &prewarmState{warmed: make(map[string]time.Time)},
		pb:        &playbackState{},
	}
	srv.cfg.transcodeEnabled.Store(opts.FFmpeg && ff != nil && ff.Available())
	return srv
}

// cleanupOldFiles 删除 dir 下超过 maxAge 的文件（启动时调用一次）
func cleanupOldFiles(dir string, maxAge time.Duration) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return
	}
	cutoff := time.Now().Add(-maxAge)
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		p := filepath.Join(dir, e.Name())
		if info, err := e.Info(); err == nil && info.ModTime().Before(cutoff) {
			_ = os.Remove(p)
		}
	}
}

// Close 释放资源（终止 HLS 转码进程）
func (s *Server) Close() {
	if s.deps.hls != nil {
		s.deps.hls.Close()
	}
}

// Handler 返回完整 HTTP 处理器
func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/info", s.handleInfo)
	mux.HandleFunc("GET /api/health", s.handleHealth)
	mux.HandleFunc("GET /api/list", s.handleList)
	mux.HandleFunc("GET /api/thumb", s.handleThumb)
	mux.HandleFunc("GET /api/thumb-src", s.serveThumbSrc)
	mux.HandleFunc("GET /api/file", s.handleFile)
	mux.HandleFunc("GET /api/video-info", s.handleVideoInfo)
	mux.HandleFunc("GET /api/hls", s.handleHls)
	mux.HandleFunc("GET /api/prewarm", s.handlePrewarm)
	mux.HandleFunc("GET /api/zip", s.handleZip)
	mux.HandleFunc("GET /api/search", s.handleSearch)
	mux.HandleFunc("POST /api/settings/ffmpeg", s.handleSetFFmpeg)
	// 根路径兜底注册为任意方法：SPA 仅需 GET，但这样未匹配的旧 POST 接口
	// 会落到前端文件服务并返回 404，而不是被 net/http 以「方法不允许」回成 405。
	mux.Handle("/", s.frontendHandler())

	var h http.Handler
	// 全局安全头：nosniff 防止浏览器嗅探内容类型，
	// 配合 handleFile 对可执行 MIME 强制 attachment，阻断源内存储型 XSS。
	h = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Content-Type-Options", "nosniff")
		mux.ServeHTTP(w, r)
	})
	if s.cfg.auth != "" {
		h = s.basicAuth(h)
	}
	if s.cfg.verbose {
		h = s.accessLog(h)
	}
	// 最外层：为每个请求生成/透传 request id，写入响应头并注入 context。
	// 放在最外层保证 accessLog 也能带上 request_id（-v 时）。
	h = s.withRequestID(h)
	return h
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
