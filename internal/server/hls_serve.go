package server

import (
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// serveHlsFile 从会话目录按文件名安全地提供文件（index.m3u8 / seg_*.m4s）
func (s *Server) serveHlsFile(w http.ResponseWriter, r *http.Request, session *hlsSession, fname string) {
	if !segNameRe.MatchString(fname) || strings.Contains(fname, "..") {
		httpError(w, http.StatusBadRequest, "非法文件名")
		return
	}
	session.touch() // 记录访问时间（空闲取消判定）
	fp := filepath.Join(session.dir, fname)
	fi, err := os.Stat(fp)
	if err != nil {
		// 分片尚未生成（seek 超前于转码进度 / 首片未出）：
		// 阻塞等待该分片生成——copy 重封装是磁盘速任务（秒级），
		// 转码则等待到会话完成或超时。避免 hls.js 404 后放弃。
		if strings.HasSuffix(fname, ".m4s") || fname == "init.mp4" {
			deadline := time.Now().Add(hlsSegWaitTimeout)
			for {
				if fi2, serr := os.Stat(fp); serr == nil && fi2.Size() > 0 {
					fi = fi2
					break
				}
				select {
				case <-session.done:
					httpError(w, http.StatusNotFound, "分片不可用（转码已结束）")
					return
				case <-time.After(200 * time.Millisecond):
				}
				if time.Now().After(deadline) {
					httpError(w, http.StatusNotFound, "分片尚未生成（转码中，请稍后拖动）")
					return
				}
			}
		} else {
			httpError(w, http.StatusNotFound, "播放列表尚未生成")
			return
		}
	}
	if strings.HasSuffix(fname, ".m3u8") {
		// 播放列表随时增长：禁止缓存，hls.js 按需重载
		w.Header().Set("Content-Type", "application/vnd.apple.mpegurl")
		w.Header().Set("Cache-Control", "no-store")
		data, err := os.ReadFile(fp)
		if err != nil {
			httpError(w, http.StatusInternalServerError, "读取播放列表失败")
			return
		}
		// done=true 仅在转码完成时传：进行中的流不加 ENDLIST（EVENT 语义，
		// hls.js 会持续拉新分片）；转码完成后加 ENDLIST 正常结束。
		done := false
		select {
		case <-session.done:
			done = true
		default:
		}
		_, _ = w.Write(rewritePlaylistURIs(data, r, done))
		return
	}
	// 分片内容不变：可缓存
	f, err := os.Open(fp)
	if err != nil {
		httpError(w, http.StatusInternalServerError, "打开分片失败")
		return
	}
	defer func() { _ = f.Close() }()
	w.Header().Set("Content-Type", "video/mp4")
	w.Header().Set("Cache-Control", "public, max-age=3600")
	http.ServeContent(w, r, fname, fi.ModTime(), f)
}

// rewritePlaylistURIs 改写播放列表：
//  1. 相对 URI（init.mp4 / seg_*.m4s）→ 指向 /api/hls 的绝对 URL（浏览器/hls.js
//     按播放列表 URL 目录解析相对 URI 会 404）；
//  2. ENDLIST 仅在转码完成（done=true）时输出：
//     - 转码进行中：不加 ENDLIST（EVENT 语义）——hls.js 会持续拉取随转码增长的
//     新分片，不会「播完当前已生成部分就结束」（此前无条件加 ENDLIST 导致
//     长视频只播开头几秒就停）；
//     - 转码完成：加 ENDLIST，hls.js 正常结束。
//     同时移除 EXT-X-PLAYLIST-TYPE:EVENT 声明（避免部分播放器按严格 live 处理）。
func rewritePlaylistURIs(data []byte, r *http.Request, done bool) []byte {
	path := r.URL.Query().Get("path")
	base := "/api/hls?path=" + url.QueryEscape(path) + "&f="
	lines := strings.Split(string(data), "\n")
	out := make([]string, 0, len(lines)+2)
	hasEnd := false
	for _, ln := range lines {
		trimmed := strings.TrimSpace(ln)
		if trimmed == "" {
			continue
		}
		if strings.HasPrefix(trimmed, "#EXT-X-PLAYLIST-TYPE:") {
			continue // 移除 live/EVENT 声明
		}
		if trimmed == "#EXT-X-ENDLIST" {
			hasEnd = true
			if done {
				out = append(out, trimmed)
			}
			continue
		}
		if strings.HasPrefix(trimmed, "#EXT-X-MAP:") {
			if idx := strings.Index(trimmed, `URI="`); idx >= 0 {
				rest := trimmed[idx+len(`URI="`):]
				if end := strings.Index(rest, `"`); end > 0 {
					uri := rest[:end]
					if !strings.Contains(uri, "://") && !strings.HasPrefix(uri, "/") {
						out = append(out, trimmed[:idx]+`URI="`+base+uri+`"`)
						continue
					}
				}
			}
			out = append(out, trimmed)
			continue
		}
		if strings.HasPrefix(trimmed, "#") {
			out = append(out, trimmed)
			continue
		}
		// 纯分片文件名行（无路径分隔符）→ 绝对 URL
		if !strings.Contains(trimmed, "/") && !strings.Contains(trimmed, "://") {
			out = append(out, base+trimmed)
		} else {
			out = append(out, trimmed)
		}
	}
	if done && !hasEnd {
		out = append(out, "#EXT-X-ENDLIST")
	}
	return []byte(strings.Join(out, "\n"))
}
