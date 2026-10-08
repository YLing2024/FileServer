package server

import (
	"context"
	"net/http"
	"os"
	"path/filepath"
	"strings"
)

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
		httpError(w, errToStatus(err), err.Error())
		return
	}
	if s.hiddenBlocked(abs) {
		httpError(w, http.StatusNotFound, "路径不存在")
		return
	}
	fi, err := os.Stat(abs)
	if err != nil || fi.IsDir() {
		httpError(w, http.StatusNotFound, "无法访问该文件")
		return
	}

	kind := fileKind(fi.Name(), false)
	if kind != "video" {
		httpError(w, http.StatusBadRequest, "该文件不是视频")
		return
	}

	ext := strings.ToLower(filepath.Ext(fi.Name()))
	resp := map[string]any{
		"mode": "direct",
		"mime": mediaMime(ext),
	}

	// 播放决策：
	// - 默认（未开启 --ffmpeg）：一律直链——浏览器原生能播的（H.264 MP4/WebM 等）直接播，
	//   播不了的（MKV/RMVB/HEVC 等）前端提示不可在线播放（可下载）。
	// - 开启 --ffmpeg：浏览器原生可播 → direct；冷门格式（MKV/AVI/WMV/RMVB/FLV/TS/3GP/
	//   MPG/OGV + HEVC MP4）→ hls（服务端 ffmpeg 实时转码，GPU 优先，见 HLS 管理器）。
	if s.cfg.transcodeEnabled.Load() && !s.browserNativePlayable(ext, abs) {
		resp["mode"] = "hls"
	}

	// MP4 家族：直链且尚未 faststart（moov 不在头部）时，后台预热重封装缓存——
	// 本次播放用原文件（best-effort，绝不影响播放），二次打开走 fs=1 缓存秒开，
	// 抽帧源也优先命中（见 thumb.go）。已有缓存则在响应里标记 faststart。
	// 单飞防重复与「播放中让路」语义全部封装在 warmFaststart 内，这里只做触发判定。
	if ext == ".mp4" || ext == ".m4v" || ext == ".mov" {
		if s.faststartCachePath(abs, fi) != "" {
			resp["faststart"] = true
		} else if resp["mode"] == "direct" {
			s.maybeWarmFaststart(abs, fi)
		}
	}

	// 元数据：优先内存缓存（命中即返回完整信息）；
	// 未命中则后台探测（不阻塞本次响应），前端稍后二次查询获得 duration 等。
	if s.deps.ff != nil {
		if info, ierr := s.deps.ff.ProbeMediaCached(abs, fi); ierr == nil {
			resp["duration"] = info.Duration
			resp["width"] = info.Width
			resp["height"] = info.Height
		} else {
			go func() { _, _ = s.deps.ff.ProbeMedia(context.Background(), abs, fi) }()
		}
	}
	writeJSON(w, http.StatusOK, resp)
}

// browserNativePlayable 判断浏览器能否原生播放该扩展名（无需服务端转码）。
// MP4 家族需进一步排除 HEVC（Chrome 无 HEVC 解码）；其余按扩展名白名单。
// 启用 --ffmpeg 时，怪封装 MP4 也返回 false——走 HLS copy 重封装流
// （ffmpeg -c copy 实时把碎片化 mdat 重封装成规整分片，起播从 30s 降到 1-2s，
// 零画质损失）。
func (s *Server) browserNativePlayable(ext, abs string) bool {
	switch ext {
	case ".webm":
		return true
	case ".mp4", ".m4v", ".mov":
		if mp4HasHEVC(abs) {
			return false
		}
		if s.cfg.transcodeEnabled.Load() && s.isWeird(abs) {
			return false // 怪封装 → HLS copy 流
		}
		return true
	case ".mp3", ".wav", ".flac", ".ogg", ".oga", ".aac", ".m4a", ".opus", ".wma", ".ogv":
		// 音频与 Ogv 由浏览器原生处理（音频不支持时按不支持处理，但保持 direct 让浏览器决定）
		return true
	}
	return false
}

// handleHls GET /api/hls?path=&f=index.m3u8|seg_000001.m4s
// 提供 HLS 播放列表与分片。首次请求触发（或复用）转码会话并等待首片产出。
// abandon=1：前端关闭播放器时通知服务端终止该文件的转码会话——
// 用户已经离开这个视频，机械硬盘上持续读写会拖慢下一个视频的首片。
func (s *Server) handleHls(w http.ResponseWriter, r *http.Request) {
	// 与 /api/info 的 "hls" 能力、/api/video-info 的播放决策一致：只有
	// --ffmpeg 开启（或前端设置面板动态开启）时才允许转码。仅凭 s.deps.ff != nil
	// 判断会让未开启转码的实例也能拉 m3u8 并在 .FileServer/hls 下建缓存。
	if !s.cfg.transcodeEnabled.Load() || s.deps.ff == nil {
		httpError(w, http.StatusNotFound, "服务端未启用视频转码")
		return
	}
	abs, err := s.safePath(r.URL.Query().Get("path"))
	if err != nil {
		httpError(w, errToStatus(err), err.Error())
		return
	}
	if s.hiddenBlocked(abs) {
		httpError(w, http.StatusNotFound, "路径不存在")
		return
	}
	fi, err := os.Stat(abs)
	if err != nil || fi.IsDir() {
		httpError(w, http.StatusNotFound, "无法访问该文件")
		return
	}
	if r.URL.Query().Get("abandon") == "1" {
		s.deps.hls.Abandon(mediaKey(abs, fi))
		w.WriteHeader(http.StatusNoContent)
		return
	}
	fname := r.URL.Query().Get("f")
	if fname == "" {
		fname = "index.m3u8"
	}
	session, err := s.deps.hls.Get(r.Context(), abs, fi, s.deps.ff)
	if err != nil {
		httpError(w, http.StatusInternalServerError, err.Error())
		return
	}
	s.serveHlsFile(w, r, session, fname)
}
