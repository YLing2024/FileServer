package server

import (
	"bytes"
	"context"
	"log/slog"
	"mime"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// handleFile GET /api/file?path= 下载/预览文件（支持 Range 断点续传）。
// 直链播放（faststart MP4 / WebM 等浏览器原生格式）也走这里；
// 需要转码的格式由前端经 /api/video-info 判定后走 /api/hls。
func (s *Server) handleFile(w http.ResponseWriter, r *http.Request) {
	abs, err := s.safePath(r.URL.Query().Get("path"))
	if err != nil {
		httpError(w, errToStatus(err), err.Error())
		return
	}
	// --hidden 未开启时，隐藏路径（含其隐藏祖先目录）与列表/搜索一致地拒绝
	if s.hiddenBlocked(abs) {
		httpError(w, http.StatusNotFound, "路径不存在")
		return
	}
	fi, err := os.Stat(abs)
	if err != nil {
		httpError(w, errToStatus(err), "无法访问该文件")
		return
	}
	if fi.IsDir() {
		httpError(w, http.StatusBadRequest, "该路径是目录，无法下载")
		return
	}
	f, err := os.Open(abs)
	if err != nil {
		httpError(w, http.StatusInternalServerError, "打开文件失败")
		return
	}
	defer func() { _ = f.Close() }()

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
	if r.URL.Query().Get("fs") == "1" && s.deps.ff != nil {
		if fp := s.faststartCachePath(abs, fi); fp != "" {
			if ffs, oerr := os.Open(fp); oerr == nil {
				_ = f.Close()
				defer func() { _ = ffs.Close() }()
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
	if s.faststart.dir == "" {
		return ""
	}
	p := filepath.Join(s.faststart.dir, mediaKey(abs, fi)+".mp4")
	if info, err := os.Stat(p); err == nil && info.Size() > 0 {
		return p
	}
	return ""
}

// warmFaststart 后台把 MP4 重封装为 faststart 缓存（单飞防重复）。
// 大文件是磁盘速长任务：低于正常优先级运行，不抢正在播放的直链链路。
func (s *Server) warmFaststart(abs string, fi os.FileInfo) {
	if s.deps.ff == nil || s.faststartCachePath(abs, fi) != "" {
		return
	}
	key := mediaKey(abs, fi)
	if !s.faststart.busy.tryAcquire(key) {
		return
	}
	defer s.faststart.busy.release(key)

	// 播放进行中让路：重封装是整文件顺序读（大文件要跑几分钟），
	// 机械硬盘上与播放并行会互相拖慢。等播放结束再推进，
	// 缓存只影响「下次打开」的速度，本次播放优先。
	for s.deps.hls.Active() || s.directPlaying() {
		time.Sleep(500 * time.Millisecond)
	}

	dst := filepath.Join(s.faststart.dir, key+".mp4")
	if err := s.deps.ff.Faststart(context.Background(), abs, dst, fi.Size()); err != nil {
		_ = os.Remove(dst + ".tmp")
		// best-effort：预热失败只记日志，绝不影响正在进行的播放。
		slog.Warn("faststart 预热失败", "path", abs, "err", err)
	}
}

// maybeWarmFaststart 在 MP4 直链且尚未 faststart 时调度一次后台重封装预热。
// 触发条件（全部满足才生成缓存，避免无谓重封装）：
//   - ffmpeg 可用；
//   - 浏览器可原生直链（非 HEVC，HEVC MP4 预热了也播不了）；
//   - 原文件 moov 不在头部（未 faststart）且尚无缓存。
//
// 只负责调度：单飞防重复（faststart.busy）与「播放进行中让路」在 warmFaststart
// 内实现，因此多次请求不会退化成多条无条件后台任务。
func (s *Server) maybeWarmFaststart(abs string, fi os.FileInfo) {
	if s.deps.ff == nil || mp4HasHEVC(abs) {
		return
	}
	// 复用布局缓存（moov 头部 256KB 内视为已 faststart），避免重复扫描。
	if l := s.mp4LayoutCached(abs, fi.Size()); l.moovOffset >= 0 && l.moovOffset <= 256*1024 {
		return
	}
	if s.faststartCachePath(abs, fi) != "" {
		return
	}
	if s.faststart.hook != nil {
		s.faststart.hook(abs, fi)
		return
	}
	go s.warmFaststart(abs, fi)
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
	defer func() { _ = f.Close() }()
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
	_, _ = f.Seek(0, 0)
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
