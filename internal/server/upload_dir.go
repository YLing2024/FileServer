package server

import (
	"net/http"
	"os"
	"path/filepath"
)

// ============================================================
// 文件夹上传的目录预创建。
//
// 浏览器端递归读取文件夹后，按父目录优先的顺序调用本接口建立目录结构
// （包括空目录）。顶层目录冲突一次性按目录名整体处理：ask 返回 409，
// overwrite 复用已存在的目录继续合并，rename 生成 "name (n)" 新目录。
// 路径校验与 /api/upload 完全一致。
// ============================================================

// uploadMkdirReq POST /api/upload/mkdir 请求体。
type uploadMkdirReq struct {
	Dir  string `json:"dir"`  // 父目录相对路径
	Name string `json:"name"` // 单层目录名
	Mode string `json:"mode"` // ask | overwrite | rename
}

// handleUploadMkdir POST /api/upload/mkdir：创建一个目录（不递归）。
func (s *Server) handleUploadMkdir(w http.ResponseWriter, r *http.Request) {
	if _, ok := s.manageSession(w, r); !ok {
		return
	}
	if !s.cfg.uploadAllowed {
		httpError(w, http.StatusForbidden, "上传未启用")
		return
	}
	var req uploadMkdirReq
	if !decodeManageBody(w, r, &req) {
		return
	}
	if msg := validEntryName(req.Name); msg != "" {
		httpError(w, http.StatusBadRequest, msg)
		return
	}
	mode := req.Mode
	if mode == "" {
		mode = uploadModeAsk
	}
	if mode != uploadModeAsk && mode != uploadModeOverwrite && mode != uploadModeRename {
		httpError(w, http.StatusBadRequest, "mode 非法")
		return
	}
	dirAbs, status, msg := s.uploadTargetDir(req.Dir)
	if status != 0 {
		httpError(w, status, msg)
		return
	}
	target := filepath.Join(dirAbs, req.Name)
	if !pathWithin(s.cfg.root, target) || s.hiddenBlocked(target) {
		httpError(w, http.StatusForbidden, "目标路径不可操作")
		return
	}

	exists, isDir := uploadTargetState(target)
	if exists {
		if isDir {
			switch mode {
			case uploadModeOverwrite:
				writeJSON(w, http.StatusOK, map[string]any{
					"ok":      true,
					"name":    req.Name,
					"path":    relOf(s.cfg.root, target),
					"created": false,
				})
				return
			case uploadModeAsk:
				writeJSON(w, http.StatusConflict, map[string]any{
					"error":    "同名文件夹已存在",
					"code":     "conflict",
					"existing": []string{req.Name},
				})
				return
			case uploadModeRename:
				renamed, ok := nextUploadName(dirAbs, req.Name, nil)
				if !ok {
					httpError(w, http.StatusBadRequest, "无法生成不冲突的文件夹名")
					return
				}
				target = renamed
			}
		} else {
			httpError(w, http.StatusConflict, "同名文件已存在")
			return
		}
	}
	if err := os.Mkdir(target, 0o755); err != nil {
		writeCreateWriteError(w, err, true)
		return
	}
	s.invalidateTextCaches()
	writeJSON(w, http.StatusOK, map[string]any{
		"ok":      true,
		"name":    filepath.Base(target),
		"path":    relOf(s.cfg.root, target),
		"created": true,
	})
}
