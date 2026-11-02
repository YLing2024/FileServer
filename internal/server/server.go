// Package server 实现 FileServer 的 HTTP 服务：目录浏览、缩略图、下载、打包、搜索。
package server

import (
	"errors"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/YLing2024/FileServer/internal/auth"
)

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

	shares      *shareStore   // 分享记录（.FileServer/shares.json）
	shareLimit  *shareLimiter // 提取码失败限速（按 IP 滑动窗口）
	shareSecret []byte        // 提取码通过 cookie 的 HMAC 密钥（进程级随机）

	cleanupStop chan struct{} // 每日清理协程退出信号
	cleanupOnce sync.Once     // Close 幂等关闭清理协程
}

// Options 服务器选项
type Options struct {
	Hidden         bool
	Auth           string // 兼容：--auth user:pass（等价 password 模式预置口令）
	Verbose        bool
	FFmpeg         bool   // 开启冷门格式（MKV/RMVB/HEVC 等）的在线转码播放 + 服务端缩略图
	TextMaxBytes   int64  // 可编辑文本大小上限（0 用默认 2 MiB）
	AllowUpload    bool   // 是否允许上传（默认关闭）
	MaxUploadMB    int64  // 单文件上传上限（MiB，0 用默认 1024）
	MaxUploadBytes int64  // 单文件上传上限（字节，优先于 MaxUploadMB；测试/嵌入用）
	AuthMode       string // none|totp|password|password+totp（空视为 none）
	TokenDays      int    // 会话 token 有效期天数（0 用默认 7）
	TrustProxy     bool   // 信任 X-Forwarded-For（取最左）
	CookieSecure   bool   // 强制 cookie Secure
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
				TokenTTL:       time.Duration(opts.TokenDays) * 24 * time.Hour,
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
			root:          root,
			hidden:        opts.Hidden,
			verbose:       opts.Verbose,
			textMaxBytes:  opts.TextMaxBytes,
			uploadAllowed: opts.AllowUpload,
			trustProxy:    opts.TrustProxy,
			cookieSecure:  opts.CookieSecure,
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
		started:     time.Now(),
		pw:          &prewarmState{warmed: make(map[string]time.Time)},
		pb:          &playbackState{},
		shares:      newShareStore(filepath.Join(base, shareFileName)),
		shareLimit:  newShareLimiter(),
		shareSecret: newShareCookieSecret(),
		cleanupStop: make(chan struct{}),
	}
	srv.cfg.transcodeEnabled.Store(opts.FFmpeg && ff != nil && ff.Available())
	srv.cfg.authRequired = authRequired
	srv.cfg.auth = authMan
	if srv.cfg.textMaxBytes <= 0 {
		srv.cfg.textMaxBytes = textMaxDefaultBytes
	}
	if opts.MaxUploadBytes > 0 {
		srv.cfg.uploadMaxBytes = opts.MaxUploadBytes
	} else if opts.MaxUploadMB > 0 {
		srv.cfg.uploadMaxBytes = opts.MaxUploadMB << 20
	}
	if srv.cfg.uploadMaxBytes <= 0 {
		srv.cfg.uploadMaxBytes = uploadMaxDefaultBytes
	}
	// 启动时先清一次：上次异常退出可能留下超过 24 小时的分片会话与孤儿 blob。
	srv.cleanupUploadStore(time.Now())
	go srv.cleanupUploadLoop()
	return srv
}

// Close 释放资源（终止 HLS 转码进程）
func (s *Server) Close() {
	s.cleanupOnce.Do(func() { close(s.cleanupStop) })
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
		{"POST", "/api/create", s.handleCreate},
		{"POST", "/api/upload", s.handleUpload},
		{"POST", "/api/upload/chunk", s.handleUploadChunk},
		{"GET", "/api/upload/status", s.handleUploadStatus},
		{"POST", "/api/upload/complete", s.handleUploadComplete},
		{"POST", "/api/upload/instant", s.handleUploadInstant},
		{"POST", "/api/upload/mkdir", s.handleUploadMkdir},
		{"POST", "/api/rename", s.handleRename},
		{"POST", "/api/move", s.handleMove},
		{"POST", "/api/copy", s.handleCopy},
		{"POST", "/api/delete", s.handleDelete},
		{"GET", "/api/trash", s.handleTrash},
		{"POST", "/api/trash/restore", s.handleTrashRestore},
		{"POST", "/api/trash/empty", s.handleTrashEmpty},
		{"POST", "/api/settings/ffmpeg", s.handleSetFFmpeg},
		// 分享管理（认证启用时需登录；公开访问见 /api/public/share/*）
		{"POST", "/api/share", s.handleShareCreate},
		{"GET", "/api/share", s.handleShareList},
		{"POST", "/api/share/revoke", s.handleShareRevoke},
		{"DELETE", "/api/share/record", s.handleShareDelete},
		// 公开分享页与访问接口：不经过登录门禁，只凭 token + 可选提取码授权
		{"GET", "/s/{token}", s.handleSharePage},
		{"GET", "/api/public/share/{token}/info", s.handlePublicShareInfo},
		{"POST", "/api/public/share/{token}/verify", s.handlePublicShareVerify},
		{"GET", "/api/public/share/{token}/list", s.handlePublicShareList},
		{"GET", "/api/public/share/{token}/download", s.handlePublicShareDownload},
		{"GET", "/api/public/share/{token}/zip", s.handlePublicShareZip},
		{"GET", "/api/public/share/{token}/preview", s.handlePublicSharePreview},
		// 认证：/api/auth/* 不受门禁
		{"GET", "/api/auth/status", s.handleAuthStatus},
		{"GET", "/api/auth/setup", s.handleAuthSetupGet},
		{"POST", "/api/auth/setup", s.handleAuthSetupPost},
		{"GET", "/api/auth/totp-qr", s.handleAuthTOTPQR},
		{"POST", "/api/auth/login", s.handleAuthLogin},
		{"POST", "/api/auth/ticket", s.handleAuthTicket},
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
	// React 前端是唯一入口：/ 与 /v2/ 共用同一份构建产物（同一个 handler 与
	// 同一个 index.html），不保留两套页面。旧 /v2 前缀统一 301 回根路径。
	mux.HandleFunc("/v2", func(w http.ResponseWriter, r *http.Request) {
		target := "/"
		if r.URL.RawQuery != "" {
			target += "?" + r.URL.RawQuery
		}
		http.Redirect(w, r, target, http.StatusMovedPermanently)
	})
	spa := s.spaHandler()
	mux.Handle("/v2/", spa)
	// 根路径兜底注册为任意方法：SPA 仅需 GET，未匹配的 POST 会落到前端
	// 文件服务并返回 404，而不是被 net/http 以「方法不允许」回成 405。
	mux.Handle("/", spa)

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
