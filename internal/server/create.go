package server

import (
	"errors"
	"net/http"
	"os"
	"path/filepath"
	"strings"
)

// ============================================================
// 统一的新建入口：POST /api/create 支持新建空文件（扩展名不限）与新建单层文件夹。
//
// 写入范围在「新建/编辑文本」的基础上再放开两项：新建空文件与新建文件夹；
// 删除 / 重命名 / 移动 / 上传仍不做。命名校验对 /api/create 与兼容入口
// /api/text/new 同时生效——前端只做提示，服务端为准。
// ============================================================

// createReq POST /api/create 请求体。
type createReq struct {
	Dir     string `json:"dir"`     // 当前目录相对路径
	Kind    string `json:"kind"`    // file | dir
	Name    string `json:"name"`    // 新建名称
	Content string `json:"content"` // 仅 kind=file 且扩展名在文本白名单内时写入
}

// windowsReservedNames Windows 保留设备名（大小写不敏感，带扩展名时取首段判断）。
var windowsReservedNames = map[string]bool{
	"CON": true, "PRN": true, "AUX": true, "NUL": true,
	"COM1": true, "COM2": true, "COM3": true, "COM4": true, "COM5": true,
	"COM6": true, "COM7": true, "COM8": true, "COM9": true,
	"LPT1": true, "LPT2": true, "LPT3": true, "LPT4": true, "LPT5": true,
	"LPT6": true, "LPT7": true, "LPT8": true, "LPT9": true,
}

// validEntryName 校验新建文件/文件夹名称，返回中文错误原因（合法时为空串）。
//
// 规则跨平台统一（取 Windows 与 POSIX 的交集，宁严勿松）：去首尾空白后非空、
// 不得为 . / ..、不含路径分隔符与 NUL、不含控制字符与 Windows 非法字符、
// 不以 . 开头（隐藏）、不以 . 或空格结尾（Windows 会静默截断）、
// 非 Windows 保留设备名、UTF-8 字节长度 ≤ 255。
func validEntryName(name string) string {
	if strings.TrimSpace(name) == "" {
		return "名称不能为空"
	}
	if strings.TrimSpace(name) != name {
		return "名称首尾不能有空白"
	}
	if name == "." || name == ".." {
		return "名称不合法"
	}
	if strings.ContainsAny(name, "/\\\x00") {
		return "名称不能包含路径分隔符"
	}
	for _, r := range name {
		if r < 0x20 || r == 0x7f {
			return "名称不能包含控制字符"
		}
	}
	if strings.ContainsAny(name, `<>:"|?*`) {
		return `名称不能包含 < > : " | ? * 等字符`
	}
	if strings.HasPrefix(name, ".") {
		return "不能创建隐藏文件或文件夹"
	}
	if strings.HasSuffix(name, ".") || strings.HasSuffix(name, " ") {
		return "名称不能以点或空格结尾"
	}
	if isWindowsReservedName(name) {
		return "该名称为系统保留名"
	}
	// 名称按 UTF-8 字节计长，跨文件系统上限一致（Windows/POSIX 常见 255）。
	if len(name) > 255 {
		return "名称过长（UTF-8 上限 255 字节）"
	}
	return ""
}

// isWindowsReservedName 名称首段（首个 . 之前）是否为 Windows 保留设备名。
// 例：CON、con、CON.txt、COM1.log 一律视为保留。
func isWindowsReservedName(name string) bool {
	base := name
	if i := strings.IndexByte(name, '.'); i >= 0 {
		base = name[:i]
	}
	return windowsReservedNames[strings.ToUpper(base)]
}

// handleCreate POST /api/create 新建空文件或文件夹。
func (s *Server) handleCreate(w http.ResponseWriter, r *http.Request) {
	var req createReq
	if !decodeJSONBody(w, r, s.cfg.textMaxBytes, &req) {
		return
	}
	if contentTooLarge(req.Content, s.cfg.textMaxBytes) {
		httpError(w, http.StatusRequestEntityTooLarge, "文本超过大小上限")
		return
	}
	if req.Kind != "file" && req.Kind != "dir" {
		httpError(w, http.StatusBadRequest, "kind 非法")
		return
	}
	s.createEntry(w, req.Dir, req.Kind, req.Name, req.Content)
}

// createEntry 是 /api/create 与 /api/text/new 共用的写入内核。
// 只创建、绝不覆盖：文件用 O_CREATE|O_EXCL，目录用 os.Mkdir（父目录必须已存在，
// 不递归）。路径沿用 safePath + pathWithin 双重校验。
func (s *Server) createEntry(w http.ResponseWriter, dir, kind, name, content string) {
	if msg := validEntryName(name); msg != "" {
		httpError(w, http.StatusBadRequest, msg)
		return
	}
	dirAbs, err := s.safePath(dir)
	if err != nil {
		// 父目录不存在属于参数问题（400），与穿越/符号链接逃逸（403）区分开。
		if errors.Is(err, errNotFound) {
			httpError(w, http.StatusBadRequest, "目标目录不存在")
			return
		}
		httpError(w, errToStatus(err), err.Error())
		return
	}
	di, err := os.Stat(dirAbs)
	if err != nil || !di.IsDir() {
		httpError(w, http.StatusBadRequest, "目标不是目录")
		return
	}
	if isReadOnlyMode(di.Mode()) {
		httpError(w, http.StatusForbidden, "目标目录不可写（只读）")
		return
	}
	target := filepath.Join(dirAbs, name)
	if !pathWithin(s.cfg.root, target) {
		httpError(w, http.StatusForbidden, "路径访问被拒绝")
		return
	}
	if s.hiddenBlocked(target) {
		httpError(w, http.StatusForbidden, "不能在此创建隐藏文件或文件夹")
		return
	}

	switch kind {
	case "dir":
		if err := os.Mkdir(target, 0o755); err != nil {
			writeCreateWriteError(w, err, true)
			return
		}
	case "file":
		if err := createFileExclusive(target, name, content); err != nil {
			writeCreateWriteError(w, err, false)
			return
		}
	}

	s.invalidateTextCaches()
	fi, err := os.Stat(target)
	if err != nil {
		httpError(w, http.StatusInternalServerError, "创建后无法读取状态")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"ok":      true,
		"kind":    kind,
		"name":    name,
		"path":    relOf(s.cfg.root, target),
		"size":    fi.Size(),
		"mtime":   fi.ModTime().Unix(),
		"version": textVersion(fi),
	})
}

// createFileExclusive 创建空文件；白名单文本且带内容时改用原子写写入内容。
// 先用 O_EXCL 占位保证不覆盖任何已存在条目（含同名目录）；写内容失败时删除
// 占位，不留半截产物。
func createFileExclusive(target, name, content string) error {
	f, err := os.OpenFile(target, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o644)
	if err != nil {
		return err
	}
	if err := f.Close(); err != nil {
		_ = os.Remove(target)
		return err
	}
	if content == "" || !textEditableExt(name) {
		return nil // 空文件（0 字节）
	}
	if err := atomicWriteFile(target, []byte(content), 0o644); err != nil {
		_ = os.Remove(target)
		return err
	}
	return nil
}

// writeCreateWriteError 将底层写入错误映射为明确的 HTTP 语义。
// isDir 用于区分同名冲突的文案（文件夹 / 文件）。
func writeCreateWriteError(w http.ResponseWriter, err error, isDir bool) {
	if os.IsExist(err) {
		if isDir {
			httpError(w, http.StatusConflict, "同名文件夹已存在")
		} else {
			httpError(w, http.StatusConflict, "同名文件已存在")
		}
		return
	}
	writeTextWriteError(w, err)
}
