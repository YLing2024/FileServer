package server

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// ============================================================
// 文本文件的新建 / 预览 / 编辑保存。
//
// 本轮在「浏览即读，不写用户文件」上做**有限放开**：仅允许新建与编辑白名单
// 扩展名的文本文件，其余（删除 / 重命名 / 移动 / 上传 / 建目录）一律不做。
// 写入使用「同目录临时文件 + rename」原子替换，失败不留半截文件。
// ============================================================

// textMaxDefaultBytes 可编辑文本的默认大小上限（2 MiB）；--text-max-bytes 可配。
const textMaxDefaultBytes = 2 << 20

// editableExts 可新建 / 编辑的文本扩展名白名单（小写，含点）。
// 明确**不含** .html/.htm/.svg 等可脚本化内容——它们尽管是文本，但直链会被强制
// 附件下载、且允许写回会扩大 XSS 面，故不在白名单内（见 security.md）。
var editableExts = map[string]bool{
	".txt": true, ".text": true, ".md": true, ".markdown": true, ".rst": true,
	".json": true, ".jsonc": true, ".yaml": true, ".yml": true, ".toml": true,
	".ini": true, ".cfg": true, ".conf": true, ".config": true, ".properties": true,
	".env": true, ".log": true, ".csv": true, ".tsv": true, ".xml": true,
	".sh": true, ".bash": true, ".zsh": true, ".fish": true, ".bat": true, ".cmd": true, ".ps1": true,
	".py": true, ".rb": true, ".php": true, ".pl": true, ".lua": true,
	".js": true, ".mjs": true, ".cjs": true, ".ts": true, ".tsx": true, ".jsx": true,
	".css": true, ".scss": true, ".less": true, ".vue": true, ".svelte": true,
	".go": true, ".java": true, ".kt": true, ".c": true, ".h": true, ".cpp": true, ".hpp": true,
	".rs": true, ".swift": true, ".sql": true, ".gradle": true, ".dockerfile": true,
	".srt": true, ".vtt": true, ".ass": true, ".nfo": true, ".url": true,
	".tex": true, ".org": true, ".adoc": true, ".mk": true, ".cmake": true,
}

// editableExtList 白名单排序后的扩展名列表（供 /api/info 下发）。
func editableExtList() []string {
	out := make([]string, 0, len(editableExts))
	for e := range editableExts {
		out = append(out, e)
	}
	sort.Strings(out)
	return out
}

// textEditableExt 判断扩展名是否在白名单内。
func textEditableExt(name string) bool {
	return editableExts[strings.ToLower(filepath.Ext(name))]
}

// textVersion 版本标识：mtime(ns) + size。写请求必须携带读取时的版本，
// 不一致说明文件已被他人改动，返回 409，避免静默覆盖。
func textVersion(fi os.FileInfo) string {
	return fmt.Sprintf("%d-%d", fi.ModTime().UnixNano(), fi.Size())
}

// textResp GET /api/text 的响应体。
type textResp struct {
	Path     string `json:"path"`
	Content  string `json:"content,omitempty"`
	Size     int64  `json:"size"`
	MTime    int64  `json:"mtime"` // Unix 秒
	Version  string `json:"version"`
	Editable bool   `json:"editable"`
	MaxBytes int64  `json:"max_bytes"`
	Reason   string `json:"reason,omitempty"` // 不可编辑原因：ext / too_large
}

// textSaveReq 保存/新建请求体。
type textSaveReq struct {
	Path    string `json:"path"`    // 保存：目标文件相对路径
	Dir     string `json:"dir"`     // 新建：所在目录相对路径
	Name    string `json:"name"`    // 新建：文件名
	Content string `json:"content"` // 文本内容
	Version string `json:"version"` // 保存：读取时版本
}

// handleTextGet GET /api/text?path= 返回文本内容与版本标识，供编辑态使用。
func (s *Server) handleTextGet(w http.ResponseWriter, r *http.Request) {
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
	if !textEditableExt(fi.Name()) {
		httpError(w, http.StatusForbidden, "该扩展名不在可编辑白名单内")
		return
	}
	maxBytes := s.cfg.textMaxBytes
	resp := textResp{Path: relOf(s.cfg.root, abs), Size: fi.Size(), MTime: fi.ModTime().Unix(), Version: textVersion(fi), MaxBytes: maxBytes}
	if fi.Size() > maxBytes {
		resp.Editable = false
		resp.Reason = "too_large"
		writeJSON(w, http.StatusOK, resp)
		return
	}
	data, err := os.ReadFile(abs)
	if err != nil {
		httpError(w, http.StatusInternalServerError, "读取文件失败")
		return
	}
	resp.Content = string(data)
	resp.Editable = true
	writeJSON(w, http.StatusOK, resp)
}

// handleTextSave POST /api/text 保存已有文本文件（原子写 + 版本校验）。
func (s *Server) handleTextSave(w http.ResponseWriter, r *http.Request) {
	var req textSaveReq
	if !decodeTextBody(w, r, s.cfg.textMaxBytes, &req) {
		return
	}
	abs, err := s.safePath(req.Path)
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
	if !textEditableExt(fi.Name()) {
		httpError(w, http.StatusForbidden, "该扩展名不在可编辑白名单内")
		return
	}
	// 只读回退：文件本身或所在目录无写权限位时明确报错（比事后 EACCES 更早、
	// 且不受以 root 运行时内核绕过权限位的影响）。
	if isReadOnlyMode(fi.Mode()) || !dirWritable(filepath.Dir(abs)) {
		httpError(w, http.StatusForbidden, "目标目录或文件不可写（只读）")
		return
	}
	// 并发防覆盖：版本（mtime+size）不一致说明已被他人改动
	if req.Version != textVersion(fi) {
		httpError(w, http.StatusConflict, "文件已被其他人修改，请刷新后重试")
		return
	}
	if err := atomicWriteFile(abs, []byte(req.Content), fi.Mode().Perm()); err != nil {
		writeTextWriteError(w, err)
		return
	}
	s.invalidateTextCaches()
	writeTextSaved(w, s.cfg.root, abs)
}

// handleTextNew POST /api/text/new 在指定目录内新建文本文件。
func (s *Server) handleTextNew(w http.ResponseWriter, r *http.Request) {
	var req textSaveReq
	if !decodeTextBody(w, r, s.cfg.textMaxBytes, &req) {
		return
	}
	name := req.Name
	if !validNewTextName(name) {
		httpError(w, http.StatusBadRequest, "文件名不合法")
		return
	}
	if !textEditableExt(name) {
		httpError(w, http.StatusForbidden, "该扩展名不在新建白名单内")
		return
	}
	dirAbs, err := s.safePath(req.Dir)
	if err != nil {
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
		httpError(w, http.StatusForbidden, "不能在此创建隐藏文件")
		return
	}
	// 并发/存在性：目标已存在则拒绝，避免覆盖他人文件
	f, err := os.OpenFile(target, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o644)
	if err != nil {
		if os.IsExist(err) {
			httpError(w, http.StatusConflict, "同名文件已存在")
			return
		}
		writeTextWriteError(w, err)
		return
	}
	// 先用 O_EXCL 占位确保不覆盖，再走原子写替换为完整内容
	_ = f.Close()
	if err := atomicWriteFile(target, []byte(req.Content), 0o644); err != nil {
		_ = os.Remove(target)
		writeTextWriteError(w, err)
		return
	}
	s.invalidateTextCaches()
	writeTextSaved(w, s.cfg.root, target)
}

// writeTextSaved 返回写入后文件的新版本信息。
func writeTextSaved(w http.ResponseWriter, root, target string) {
	fi, err := os.Stat(target)
	if err != nil {
		httpError(w, http.StatusInternalServerError, "写入后无法读取文件状态")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"path":    relOf(root, target),
		"size":    fi.Size(),
		"mtime":   fi.ModTime().Unix(),
		"version": textVersion(fi),
	})
}

// invalidateTextCaches 写文件后失效目录列表缓存（让列表立即看到新内容）。
func (s *Server) invalidateTextCaches() {
	s.list.mu.Lock()
	s.list.items = nil
	s.list.mu.Unlock()
}

// decodeTextBody 解析并校验请求体：体积超上限返回 413，JSON 非法返回 400。
func decodeTextBody(w http.ResponseWriter, r *http.Request, maxBytes int64, out *textSaveReq) bool {
	// 正文上限：内容上限 + JSON 结构开销余量
	r.Body = http.MaxBytesReader(w, r.Body, maxBytes+64*1024)
	dec := json.NewDecoder(r.Body)
	if err := dec.Decode(out); err != nil {
		var tooLarge *http.MaxBytesError
		if errors.As(err, &tooLarge) {
			httpError(w, http.StatusRequestEntityTooLarge, "文本超过大小上限")
			return false
		}
		httpError(w, http.StatusBadRequest, "参数格式错误")
		return false
	}
	if int64(len(out.Content)) > maxBytes {
		httpError(w, http.StatusRequestEntityTooLarge, "文本超过大小上限")
		return false
	}
	return true
}

// writeTextWriteError 将写入底层错误映射为明确的 HTTP 语义。
func writeTextWriteError(w http.ResponseWriter, err error) {
	if errors.Is(err, errReadOnly) {
		httpError(w, http.StatusForbidden, "目标目录或文件不可写（只读）")
		return
	}
	if os.IsPermission(err) {
		httpError(w, http.StatusForbidden, "目标目录或文件不可写（权限不足）")
		return
	}
	httpError(w, http.StatusInternalServerError, "写入失败")
}

// errReadOnly 目标不可写（只读文件系统 / 只读介质）。
var errReadOnly = errors.New("read-only")

// isReadOnlyMode 权限位是否完全不可写（owner/group/other 均无写位）。
// 用权限位而非实际写入探测：以 root 运行时内核会绕过权限位，直接写探测无法
// 复现只读场景，权限位判定则与 mount ro 之外的常见只读语义一致。
func isReadOnlyMode(m os.FileMode) bool { return m.Perm()&0o222 == 0 }

// dirWritable 目录权限位是否可写。
func dirWritable(dir string) bool {
	fi, err := os.Stat(dir)
	return err == nil && !isReadOnlyMode(fi.Mode())
}

// atomicWriteFile 原子写入：同目录临时文件 + rename，保留原权限位，
// 失败清理临时文件（不留半截文件）。
func atomicWriteFile(path string, data []byte, perm os.FileMode) error {
	dir := filepath.Dir(path)
	tmp, err := os.CreateTemp(dir, ".fs-text-*.tmp")
	if err != nil {
		if os.IsPermission(err) {
			return errReadOnly
		}
		return err
	}
	tmpName := tmp.Name()
	cleanup := func() { _ = os.Remove(tmpName) }
	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close()
		cleanup()
		return err
	}
	if err := tmp.Sync(); err != nil {
		_ = tmp.Close()
		cleanup()
		return err
	}
	if err := tmp.Chmod(perm); err != nil {
		_ = tmp.Close()
		cleanup()
		return err
	}
	if err := tmp.Close(); err != nil {
		cleanup()
		return err
	}
	if err := os.Rename(tmpName, path); err != nil {
		cleanup()
		return err
	}
	return nil
}

// validNewTextName 校验新建文件名：非空、无路径分隔符、非 . / ..、无 NUL。
func validNewTextName(name string) bool {
	if name == "" || name == "." || name == ".." {
		return false
	}
	if strings.ContainsAny(name, "/\\\x00") {
		return false
	}
	if filepath.Base(name) != name {
		return false
	}
	return true
}
