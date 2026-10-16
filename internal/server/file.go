package server

import (
	"bytes"
	"mime"
	"net/http"
	"os"
	"path/filepath"
	"strings"
)

// handleFile GET /api/file?path= 下载/预览文件（支持 Range 断点续传）。
// 直链播放（浏览器原生格式 MP4/WebM 等）也走这里；
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
	// moov 预热/缩略图抽帧据此让路——机械硬盘上并行读者
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
	http.ServeContent(w, r, name, fi.ModTime(), f)
}

// mp4HasHEVC 判断 MP4 视频编码是否为 HEVC：读文件头/尾各 256KB，
// 查找 HEVC sample entry 标志（hvc1/hev1）。moov 可能在头部也可能在尾部，
// 两侧都查即可毫秒级判定，无需 ffprobe
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
