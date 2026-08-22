// Package server 实现 FileServer 的 HTTP 服务：目录浏览、缩略图、下载、打包、搜索。
package server

import (
	"bytes"
	"context"
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
	"sync"
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

// Server 文件服务器
type Server struct {
	root    string // 服务根目录（绝对路径）
	hidden  bool   // 是否显示隐藏文件
	auth    string // 可选口令 "user:pass"
	verbose bool
	thumbs  *ThumbCache
	ff      *Ffmpeg
	hls     *HlsManager
	imgSem  chan struct{} // 图片缩略图解码/整读并发上限
	started time.Time

	listMu    sync.Mutex
	listCache map[string]*listCacheEntry // 目录列表短缓存（返回/翻页秒开）

	fsDir string // 小文件 faststart 重封装缓存目录
	fsMu  sync.Mutex
	fsBusy map[string]bool // faststart 重封装进行中（防重复）

	pw *prewarmState // moov 预读预热（机械硬盘冷读提速）
	pb *playbackState // 直链播放活动跟踪（预热/重封装据此让路）

	layoutMu sync.Mutex
	layouts  map[string]layoutEntry // MP4 顶层布局缓存（thumb-src 抽帧源用）
}

// Options 服务器选项
type Options struct {
	Hidden  bool
	Auth    string
	Verbose bool
}

// New 创建文件服务器
func New(root string, opts Options) *Server {
	root = filepath.Clean(root)

	// 缓存基目录：共享根目录下的隐藏文件夹 .FileServer（不往系统目录写文件）。
	// 根目录不可写（只读介质）时回退系统临时目录。
	base := filepath.Join(root, cacheDirName)
	if err := os.MkdirAll(base, 0o755); err != nil {
		base = filepath.Join(os.TempDir(), "FileServer")
		os.MkdirAll(base, 0o755)
	}
	fsDir := filepath.Join(base, "faststart")
	os.MkdirAll(fsDir, 0o755)
	go cleanupOldFiles(fsDir, 7*24*time.Hour) // 清理 7 天前的重封装缓存

	return &Server{
		root:    root,
		hidden:  opts.Hidden,
		auth:    opts.Auth,
		verbose: opts.Verbose,
		thumbs:  NewThumbCache(root),
		ff:      FindFfmpeg(),
		hls:     NewHlsManager(root),
		imgSem:  make(chan struct{}, thumbImgMaxConc),
		started: time.Now(),
		fsDir:   fsDir,
		fsBusy:  make(map[string]bool),
		pw:      &prewarmState{warmed: make(map[string]time.Time)},
		pb:      &playbackState{},
		layouts: make(map[string]layoutEntry),
	}
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
			os.Remove(p)
		}
	}
}

// Close 释放资源（终止 HLS 转码进程）
func (s *Server) Close() {
	if s.hls != nil {
		s.hls.Close()
	}
}

// Handler 返回完整 HTTP 处理器
func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/info", s.handleInfo)
	mux.HandleFunc("GET /api/list", s.handleList)
	mux.HandleFunc("GET /api/thumb", s.handleThumb)
	mux.HandleFunc("GET /api/thumb-src", s.serveThumbSrc)
	mux.HandleFunc("GET /api/file", s.handleFile)
	mux.HandleFunc("GET /api/video-info", s.handleVideoInfo)
	mux.HandleFunc("GET /api/hls", s.handleHls)
	mux.HandleFunc("GET /api/prewarm", s.handlePrewarm)
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
		"hls":          s.ff.Available(), // HLS 转码与 ffmpeg 同开关
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

// handleFile GET /api/file?path= 下载/预览文件（支持 Range 断点续传）。
// 直链播放（faststart MP4 / WebM 等浏览器原生格式）也走这里；
// 需要转码的格式由前端经 /api/video-info 判定后走 /api/hls。
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
	// 直链播放跟踪：视频/音频的 Range 请求（浏览器流式播放）标记播放活动，
	// moov 预热/缩略图抽帧/faststart 重封装据此让路——机械硬盘上并行读者
	// 会把播放拖死（用户核心痛点：目录浏览后点开视频加载不出来）。
	if (kind == "video" || kind == "audio") && r.Header.Get("Range") != "" {
		s.markVideoRead()
	}
	mimeType := fileMimeType(name, f)
	disposition := "attachment"
	switch kind {
	case "image", "video", "audio", "pdf", "text":
		disposition = "inline"
	}
	// ?dl=1 强制附件下载（下载按钮使用）：即使视频/图片也要原始文件字节，
	// 不能给浏览器 inline 预览语义（部分浏览器会忽略 download 属性）。
	if r.URL.Query().Get("dl") == "1" {
		disposition = "attachment"
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

	// ?fs=1：小文件 faststart 化后的直链播放（video-info 已预热重封装缓存；
	// 缓存未就绪时回退原文件——小文件 moov 在尾部也只是几百毫秒内起播）
	if r.URL.Query().Get("fs") == "1" && s.ff != nil {
		if fp := s.faststartCachePath(abs, fi); fp != "" {
			if ffs, oerr := os.Open(fp); oerr == nil {
				f.Close()
				defer ffs.Close()
				http.ServeContent(w, r, name, fi.ModTime(), ffs)
				return
			}
		}
	}
	http.ServeContent(w, r, name, fi.ModTime(), f)
}

// faststartCachePath 返回 faststart 重封装缓存的路径；不存在/不适用返回 ""
// （不限文件大小：大文件首次播放后后台重封装，之后直链缓存秒开）
func (s *Server) faststartCachePath(abs string, fi os.FileInfo) string {
	if s.fsDir == "" {
		return ""
	}
	p := filepath.Join(s.fsDir, mediaKey(abs, fi)+".mp4")
	if info, err := os.Stat(p); err == nil && info.Size() > 0 {
		return p
	}
	return ""
}

// warmFaststart 后台把 MP4 重封装为 faststart 缓存（单飞防重复）。
// 大文件是磁盘速长任务：低于正常优先级运行，不抢正在播放的直链链路。
func (s *Server) warmFaststart(abs string, fi os.FileInfo) {
	if s.ff == nil || s.faststartCachePath(abs, fi) != "" {
		return
	}
	key := mediaKey(abs, fi)
	s.fsMu.Lock()
	if s.fsBusy[key] {
		s.fsMu.Unlock()
		return
	}
	s.fsBusy[key] = true
	s.fsMu.Unlock()
	defer func() {
		s.fsMu.Lock()
		delete(s.fsBusy, key)
		s.fsMu.Unlock()
	}()

	// 播放进行中让路：重封装是整文件顺序读（大文件要跑几分钟），
	// 机械硬盘上与播放并行会互相拖慢。等播放结束再推进，
	// 缓存只影响「下次打开」的速度，本次播放优先。
	for s.hls.Active() || s.directPlaying() {
		time.Sleep(500 * time.Millisecond)
	}

	dst := filepath.Join(s.fsDir, key+".mp4")
	if err := s.ff.Faststart(context.Background(), abs, dst, fi.Size()); err != nil {
		os.Remove(dst + ".tmp")
	}
}

// handleVideoInfo GET /api/video-info?path=
// 返回播放决策：direct（浏览器原生直链）/ hls（需服务端转码流化），
// 以及时长、分辨率等元数据（前端播放器进度条/信息展示）。
//
// 性能要点：决策本身毫秒级返回，绝不等待 ffprobe（部分源探测一次要十几秒，
// 会饿死播放链路）——决策仅依赖文件扩展名与 MP4 box 头部（本地读取）；
// 元数据探测转入后台缓存，前端 2 秒后二次查询可拿到时长。
func (s *Server) handleVideoInfo(w http.ResponseWriter, r *http.Request) {
	abs, err := s.safePath(r.URL.Query().Get("path"))
	if err != nil {
		writeErr(w, errToStatus(err), err.Error())
		return
	}
	if s.hiddenBlocked(abs) {
		writeErr(w, http.StatusNotFound, "路径不存在")
		return
	}
	fi, err := os.Stat(abs)
	if err != nil || fi.IsDir() {
		writeErr(w, http.StatusNotFound, "无法访问该文件")
		return
	}

	kind := fileKind(fi.Name(), false)
	if kind != "video" {
		writeErr(w, http.StatusBadRequest, "该文件不是视频")
		return
	}

	ext := strings.ToLower(filepath.Ext(fi.Name()))
	resp := map[string]any{
		"mode": "direct",
		"mime": mediaMime(ext),
	}

	// 快速决策（完全不依赖 ffprobe，毫秒级）：
	// - WebM：浏览器原生可播 → direct
	// - MP4 家族：读文件头/尾 256KB 字节判断 HEVC（hvc1/hev1 标志）——
	//   HEVC → hls；小文件（≤32MB，完整下载毫秒级）或 moov 在头部
	//   （≤256KB，含怪封装巨 moov：Chrome 顺序下载 moov 即可解析起播，
	//   ffmpeg 对碎片化 moov 构建索引反而要十几秒）→ direct；
	//   moov 在中/尾部的大文件 → hls（Chrome 直链要下整个文件，
	//   ffmpeg 读尾部 moov 快，秒级出片）
	// - 其余容器（MKV/AVI/...）：hls
	switch ext {
	case ".webm":
		// direct
	case ".mp4", ".m4v", ".mov":
		if mp4HasHEVC(abs) {
			resp["mode"] = "hls"
		} else if fi.Size() <= 32*1024*1024 || mp4IsFastStart(abs) {
			// 直链即点即播。首次播放时后台 faststart 化
			// （copy 重封装，大文件磁盘速任务低于正常优先级），
			// 完成后走缓存直链，二次打开秒开。
			if s.ff != nil && s.faststartCachePath(abs, fi) == "" {
				go s.warmFaststart(abs, fi)
			}
			if s.faststartCachePath(abs, fi) != "" {
				resp["faststart"] = true // 直链缓存已就绪（起播最快）
			}
		} else {
			// moov 中/尾的大文件：直链要下整个文件才能解析，走 HLS copy 重封装
			resp["mode"] = "hls"
		}
	default:
		if s.ff != nil {
			resp["mode"] = "hls"
		}
	}

	// 元数据：优先内存缓存（命中即返回完整信息）；
	// 未命中则后台探测（不阻塞本次响应），前端稍后二次查询获得 duration 等。
	if s.ff != nil {
		if info, ierr := s.ff.ProbeMediaCached(abs, fi); ierr == nil {
			resp["duration"] = info.Duration
			resp["width"] = info.Width
			resp["height"] = info.Height
		} else {
			go s.ff.ProbeMedia(context.Background(), abs, fi)
		}
	}
	writeJSON(w, http.StatusOK, resp)
}

// mp4HasHEVC 判断 MP4 视频编码是否为 HEVC：读文件头/尾各 256KB，
// 查找 HEVC sample entry 标志（hvc1/hev1）。faststart 文件 moov 在头部、
// 非 faststart 在尾部，两侧都查即可毫秒级判定，无需 ffprobe
// （部分源 ffprobe 一次要十几秒，绝不能放决策主链路上）。
func mp4HasHEVC(abs string) bool {
	f, err := os.Open(abs)
	if err != nil {
		return false
	}
	defer f.Close()
	st, err := f.Stat()
	if err != nil || st.Size() < 1024 {
		return false
	}
	chunk := int64(256 * 1024)
	buf := make([]byte, chunk)
	// 头部
	n, _ := f.ReadAt(buf, 0)
	if n > 0 && bytes.Contains(buf[:n], []byte("hvc1")) {
		return true
	}
	if n > 0 && bytes.Contains(buf[:n], []byte("hev1")) {
		return true
	}
	// 尾部（moov 在末尾的文件）
	off := st.Size() - chunk
	if off < 0 {
		off = 0
	}
	n2, _ := f.ReadAt(buf, off)
	if n2 > 0 && bytes.Contains(buf[:n2], []byte("hvc1")) {
		return true
	}
	if n2 > 0 && bytes.Contains(buf[:n2], []byte("hev1")) {
		return true
	}
	return false
}

// handleHls GET /api/hls?path=&f=index.m3u8|seg_000001.m4s
// 提供 HLS 播放列表与分片。首次请求触发（或复用）转码会话并等待首片产出。
// abandon=1：前端关闭播放器时通知服务端终止该文件的转码会话——
// 用户已经离开这个视频，机械硬盘上持续读写会拖慢下一个视频的首片。
func (s *Server) handleHls(w http.ResponseWriter, r *http.Request) {
	if s.ff == nil {
		writeErr(w, http.StatusNotFound, "服务端未启用视频转码")
		return
	}
	abs, err := s.safePath(r.URL.Query().Get("path"))
	if err != nil {
		writeErr(w, errToStatus(err), err.Error())
		return
	}
	if s.hiddenBlocked(abs) {
		writeErr(w, http.StatusNotFound, "路径不存在")
		return
	}
	fi, err := os.Stat(abs)
	if err != nil || fi.IsDir() {
		writeErr(w, http.StatusNotFound, "无法访问该文件")
		return
	}
	if r.URL.Query().Get("abandon") == "1" {
		s.hls.Abandon(mediaKey(abs, fi))
		w.WriteHeader(http.StatusNoContent)
		return
	}
	fname := r.URL.Query().Get("f")
	if fname == "" {
		fname = "index.m3u8"
	}
	session, err := s.hls.Get(r.Context(), abs, fi, s.ff)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	s.serveHlsFile(w, r, session, fname)
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

// hiddenBlocked 判断 abs 是否不可通过 HTTP 访问：
//  1. 任一路径分量为保留缓存目录名 .FileServer —— 无论 --hidden 与否一律拒绝；
//  2. 未开启 --hidden 时，任一分量以 . 开头（隐藏）拒绝——与列表/搜索/zip
//     的过滤语义一致，防止直链或打包绕过 UI 隐藏设置。
func (s *Server) hiddenBlocked(abs string) bool {
	rel, err := filepath.Rel(s.root, abs)
	if err != nil {
		return false
	}
	if rel == "." {
		return false // 根目录本身永不视为隐藏
	}
	for _, part := range strings.Split(filepath.ToSlash(rel), "/") {
		if isCacheEntry(part) {
			return true
		}
		if !s.hidden && strings.HasPrefix(part, ".") {
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
