package server

import (
	"encoding/json"
	"net/http"

	"github.com/YLing2024/FileServer/internal/version"
)

// handleSetFFmpeg POST /api/settings/ffmpeg {"enabled":true|false}
// 前端设置面板动态开关冷门格式支持（转码播放 + 服务端缩略图）。
// 无 ffmpeg 时拒绝开启；切换不影响已在进行中的转码会话（正在看的继续播完）。
func (s *Server) handleSetFFmpeg(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Enabled bool `json:"enabled"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		httpError(w, http.StatusBadRequest, "参数格式错误")
		return
	}
	if body.Enabled && (s.deps.ff == nil || !s.deps.ff.Available()) {
		httpError(w, http.StatusBadGateway, "服务端无 ffmpeg，无法开启冷门格式支持")
		return
	}
	s.cfg.transcodeEnabled.Store(body.Enabled)
	writeJSON(w, http.StatusOK, map[string]any{"hls": body.Enabled})
}

// handleInfo 返回服务端能力信息（前端据此决定视频缩略图策略与扩展名映射）
func (s *Server) handleInfo(w http.ResponseWriter, r *http.Request) {
	ffavail := s.deps.ff != nil && s.deps.ff.Available()
	writeJSON(w, http.StatusOK, map[string]any{
		"name":         "FileServer",
		"ffmpeg":       ffavail,
		"hls":          s.cfg.transcodeEnabled.Load(), // 冷门格式在线转码播放（可前端动态开关）
		"version":      version.Version,
		"kinds":        kindExtMap(), // 统一扩展名→类型映射（前端不再自维护）
		"search_limit": searchMaxLimit,
		"list_limit":   listMaxLimit,
	})
}

// handleHealth GET /api/health 健康检查：返回进程存活与构建版本信息，
// 供监控/部署脚本确认服务与版本（version/commit 与 --version 输出来自同一注入源）。
func (s *Server) handleHealth(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{
		"ok":       true,
		"version":  version.Version,
		"commit":   version.Commit,
		"go":       version.GoVersion(),
		"platform": version.Platform(),
	})
}
