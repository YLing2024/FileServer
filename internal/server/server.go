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

	"github.com/YLing2024/FileServer/internal/auth"
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

	list    listCache   // 目录列表短缓存（返回/翻页秒开）
	layouts layoutCache // MP4 顶层布局缓存（thumb-src 抽帧源用）

	pw      *prewarmState  // moov 预读预热（机械硬盘冷读提速）
	pb      *playbackState // 直链播放活动跟踪（预热据此让路）
	started time.Time
}

// Options 服务器选项
type Options struct {
	Hidden       bool
	Auth         string // 兼容：--auth user:pass（等价 password 模式预置口令）
	Verbose      bool
	FFmpeg       bool   // 开启冷门格式（MKV/RMVB/HEVC 等）的在线转码播放 + 服务端缩略图
	TextMaxBytes int64  // 可编辑文本大小上限（0 用默认 2 MiB）
	AuthMode     string // none|totp|password|password+totp（空视为 none）
	RememberDays int    // 「记住我」持久 cookie 天数（0 用默认 30）
	SessionHours int    // 不勾「记住我」的会话有效期小时数（0 用默认 12）
	TrustProxy   bool   // 信任 X-Forwarded-For（取最左）
	CookieSecure bool   // 强制 cookie Secure
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

	// 进程组跟踪：清理上次异常退出遗留的 ffmpeg/ffprobe 孤儿（POSIX）。
	procs := newProcTracker(base)
	procs.reap()

	ff := FindFfmpeg()
	if ff != nil {
		ff.procs = procs
	}

	hlsMgr := NewHlsManager(root)
	hlsMgr.procs = procs

	// 认证模式解析：--auth user:pass 等价于 password 模式 + 预置口令。
	mode := auth.Mode(opts.AuthMode)
	if mode == "" {
		mode = auth.ModeNone
	}
	hasPreset := opts.Auth != ""
	preset := ""
	if hasPreset {
		_, preset, _ = strings.Cut(opts.Auth, ":")
		if opts.AuthMode == "" {
			mode = auth.ModePassword
		}
	}
	authRequired := false
	var authMan *auth.Manager
	if mode != auth.ModeNone {
		if !auth.ValidMode(string(mode)) {
			slog.Warn("未知的 --auth-mode，已按不启用认证处理", "mode", opts.AuthMode)
		} else {
			// authRequired 标记「配置要求认证」：即便 Manager 初始化失败也要失败关闭
			// （拒绝访问），绝不因为读不到 auth.json 而静默敞开。
			authRequired = true
			mgr, merr := auth.NewManager(auth.Config{
				Mode:           mode,
				Path:           filepath.Join(base, "auth.json"),
				PresetPassword: preset,
				HasPreset:      hasPreset,
				SessionTTL:     time.Duration(opts.SessionHours) * time.Hour,
				RememberDays:   opts.RememberDays,
				TrustProxy:     opts.TrustProxy,
			})
			if merr != nil {
				slog.Error("认证初始化失败，将拒绝所有访问（失败关闭）", "err", merr)
			} else {
				authMan = mgr
			}
		}
	}

	srv := &Server{
		cfg: config{
			root:         root,
			hidden:       opts.Hidden,
			verbose:      opts.Verbose,
			textMaxBytes: opts.TextMaxBytes,
			trustProxy:   opts.TrustProxy,
			cookieSecure: opts.CookieSecure,
		},
		deps: deps{
			thumbs: NewThumbCache(root),
			ff:     ff,
			hls:    hlsMgr,
		},
		sem: semaphores{
			img:     make(chan struct{}, thumbImgMaxConc),
			ffThumb: make(chan struct{}, ffThumbMaxConc),
		},
		started: time.Now(),
		pw:      &prewarmState{warmed: make(map[string]time.Time)},
		pb:      &playbackState{},
	}
	srv.cfg.transcodeEnabled.Store(opts.FFmpeg && ff != nil && ff.Available())
	srv.cfg.authRequired = authRequired
	srv.cfg.auth = authMan
	if srv.cfg.textMaxBytes <= 0 {
		srv.cfg.textMaxBytes = textMaxDefaultBytes
	}
	return srv
}

// Close 释放资源（终止 HLS 转码进程）
func (s *Server) Close() {
	if s.deps.hls != nil {
		s.deps.hls.Close()
	}
}

// route 一条 API 路由（方法与 pattern）。集中登记便于 Handler 装配与
// 「代码路由 ↔ doc/api.md」一致性机检（routes_docs_test.go）。
type route struct {
	method  string
	pattern string
	handler http.HandlerFunc
}

// apiRoutes 返回全部 API 路由（不含根路径前端兜底）。
func (s *Server) apiRoutes() []route {
	return []route{
		{"GET", "/api/info", s.handleInfo},
		{"GET", "/api/health", s.handleHealth},
		{"GET", "/api/list", s.handleList},
		{"GET", "/api/thumb", s.handleThumb},
		{"GET", "/api/thumb-src", s.serveThumbSrc},
		{"GET", "/api/file", s.handleFile},
		{"GET", "/api/video-info", s.handleVideoInfo},
		{"GET", "/api/hls", s.handleHls},
		{"GET", "/api/prewarm", s.handlePrewarm},
		{"GET", "/api/zip", s.handleZip},
		{"GET", "/api/search", s.handleSearch},
		{"GET", "/api/text", s.handleTextGet},
		{"POST", "/api/text", s.handleTextSave},
		{"POST", "/api/text/new", s.handleTextNew},
		{"POST", "/api/settings/ffmpeg", s.handleSetFFmpeg},
		// 认证：/api/auth/* 不受门禁
		{"GET", "/api/auth/status", s.handleAuthStatus},
		{"GET", "/api/auth/setup", s.handleAuthSetupGet},
		{"POST", "/api/auth/setup", s.handleAuthSetupPost},
		{"GET", "/api/auth/totp-qr", s.handleAuthTOTPQR},
		{"POST", "/api/auth/login", s.handleAuthLogin},
		{"POST", "/api/auth/logout", s.handleAuthLogout},
		{"GET", "/api/auth/sessions", s.handleAuthSessions},
		{"POST", "/api/auth/sessions/revoke", s.handleAuthSessionRevoke},
	}
}

// Handler 返回完整 HTTP 处理器
func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	for _, rt := range s.apiRoutes() {
		mux.HandleFunc(rt.method+" "+rt.pattern, rt.handler)
	}
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
	// 认证门禁：放在 mux 外层，未认证时拒绝 /api/*（登录/引导页经 /api/auth/* 放行）。
	if s.cfg.authRequired {
		h = s.requireAuth(h)
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
	// 同时加严格安全响应头（登录/引导页与主应用同一入口）：
	//   - CSP：脚本仅同源（含 hls.js 的 blob worker），禁外链/内联脚本/对象插件，
	//     禁被嵌入（frame-ancestors），禁 base 改写；
	//   - Referrer-Policy: no-referrer；Cache-Control: no-store。
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasPrefix(r.URL.Path, "/api/") {
			w.Header().Set("Cache-Control", "no-store")
		} else {
			w.Header().Set("Cache-Control", "no-cache, no-store, must-revalidate")
		}
		w.Header().Set("Pragma", "no-cache")
		w.Header().Set("Expires", "0")
		w.Header().Set("Referrer-Policy", "no-referrer")
		w.Header().Set("Content-Security-Policy", contentSecurityPolicy)
		fileSrv.ServeHTTP(w, r)
	})
}

// contentSecurityPolicy 前端页面的严格 CSP：默认仅同源；脚本仅同源 + blob
// （hls.js 的 Worker 由 blob 创建）；图片/媒体允许 data:/blob:（缩略图 dataURL、
// MediaSource）；样式允许内联（布局用 CSS 变量在运行时写入）；PDF embed 走同源。
const contentSecurityPolicy = "default-src 'self'; " +
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
