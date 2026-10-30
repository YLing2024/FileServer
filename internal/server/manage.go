package server

import (
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/YLing2024/FileServer/internal/auth"
)

// ============================================================
// 文件管理 HTTP 层：重命名 / 移动 / 复制。
//
// 安全边界：
//   - 只对已登录会话开放（认证未启用时没有会话，一律 401）；
//   - 路径必须落在服务根目录内，拒绝 ..、绝对路径、越界符号链接；
//   - .FileServer 与所有隐藏项不可见、不可操作；
//   - 单批最多 manageMaxItems 项，批量操作逐项返回成功/失败。
// ============================================================

// manageMaxItems 单次批量操作上限（删除/移动/复制/恢复/彻底删除共用）。
const manageMaxItems = 200

// 同名冲突处理策略（移动/复制/恢复）。ask 由前端询问后再重试。
const (
	transferModeAsk       = "ask"
	transferModeOverwrite = "overwrite"
	transferModeRename    = "rename"
)

// manageError 带 HTTP 状态码的参数/安全错误，避免在 handler 里散落状态判断。
type manageError struct {
	status int
	msg    string
}

func (e *manageError) Error() string { return e.msg }

func manageErr(status int, msg string) error { return &manageError{status: status, msg: msg} }

// writeManageError 写出 manageError；未知错误统一 500。
func writeManageError(w http.ResponseWriter, err error) {
	var me *manageError
	if errors.As(err, &me) {
		httpError(w, me.status, me.msg)
		return
	}
	httpError(w, http.StatusInternalServerError, "操作失败")
}

// manageSession 文件管理专用门禁：必须是有效登录会话。
// 与全站门禁的区别：认证未启用（没有登录会话）时也拒绝，避免匿名写文件。
func (s *Server) manageSession(w http.ResponseWriter, r *http.Request) (auth.SessionInfo, bool) {
	if !s.authEnabled() || s.cfg.auth == nil {
		httpError(w, http.StatusUnauthorized, "需要登录后管理文件")
		return auth.SessionInfo{}, false
	}
	info, ok := s.sessionFromRequest(r)
	if !ok {
		httpError(w, http.StatusUnauthorized, "需要登录后管理文件")
		return auth.SessionInfo{}, false
	}
	return info, true
}

// shortSessionID 取会话 id 前 8 位用于日志：能定位是哪次会话，又不落完整凭据标识。
func shortSessionID(id string) string {
	if len(id) > 8 {
		return id[:8]
	}
	return id
}

// logManage 记录一次文件管理操作：操作类型、数量、结果、会话与来源 IP。
// 不记录路径/文件名/内容，避免把用户数据写进日志。
func (s *Server) logManage(op string, info auth.SessionInfo, r *http.Request, total, ok, failed int) {
	slog.Info("file manage",
		"op", op,
		"session", shortSessionID(info.ID),
		"ip", s.clientIP(r),
		"total", total,
		"ok", ok,
		"failed", failed,
	)
}

// decodeManageBody 解析管理类 JSON 请求体（上限 1 MiB，结构简单，无大字段）。
func decodeManageBody(w http.ResponseWriter, r *http.Request, out any) bool {
	r.Body = http.MaxBytesReader(w, r.Body, 1<<20)
	if err := json.NewDecoder(r.Body).Decode(out); err != nil {
		var tooLarge *http.MaxBytesError
		if errors.As(err, &tooLarge) {
			httpError(w, http.StatusRequestEntityTooLarge, "请求体过大")
			return false
		}
		httpError(w, http.StatusBadRequest, "参数格式错误")
		return false
	}
	return true
}

// manageSource 把客户端提交的源路径解析为绝对路径并做安全校验。
// 与 safePath 的区别：保留最后一段符号链接本身（删除/移动链接时操作链接，
// 不误删它指向的目标），但仍校验父目录与链接目标都在根目录内。
// 路径不存在时不在这里报错，交给逐项操作返回「路径不存在」。
func (s *Server) manageSource(rel string) (string, error) {
	if rel == "" || strings.ContainsRune(rel, 0) {
		return "", manageErr(http.StatusBadRequest, "路径不合法")
	}
	if runtime.GOOS == "windows" && strings.Contains(rel, ":") {
		return "", manageErr(http.StatusBadRequest, "路径不合法")
	}
	rel = strings.TrimLeft(rel, "/\\")
	clean := filepath.Clean(filepath.FromSlash(rel))
	if clean == "." || clean == ".." || strings.HasPrefix(clean, ".."+string(filepath.Separator)) ||
		filepath.IsAbs(clean) {
		return "", manageErr(http.StatusBadRequest, "路径不合法")
	}
	abs := filepath.Join(s.cfg.root, clean)
	if !pathWithin(s.cfg.root, abs) || abs == s.cfg.root {
		return "", manageErr(http.StatusBadRequest, "路径不合法")
	}
	if s.hiddenBlocked(abs) {
		return "", manageErr(http.StatusBadRequest, "该路径不可操作")
	}
	// 父目录必须落在根内（若父目录尚不存在，则检查最近的已存在祖先）。
	parent := filepath.Dir(abs)
	if realParent, ok := nearestRealPath(parent); ok {
		if !pathWithin(s.cfg.root, realParent) {
			return "", manageErr(http.StatusBadRequest, "路径越界")
		}
		abs = filepath.Join(realParent, filepath.Base(abs))
	} else if _, err := os.Lstat(parent); err == nil {
		return "", manageErr(http.StatusBadRequest, "路径不合法")
	}
	// 最后一段是符号链接时，链接目标也必须在根内；越界链接直接拒绝。
	if target, err := filepath.EvalSymlinks(abs); err == nil && !pathWithin(s.cfg.root, target) {
		return "", manageErr(http.StatusBadRequest, "路径越界")
	}
	return abs, nil
}

// nearestRealPath 返回 p 最近的已存在祖先的真实路径（含 p 自身）。
// 用于校验「即将创建/操作的目标」不会经由父级符号链接逃出根目录。
func nearestRealPath(p string) (string, bool) {
	for {
		if real, err := filepath.EvalSymlinks(p); err == nil {
			return real, true
		}
		next := filepath.Dir(p)
		if next == p {
			return "", false
		}
		p = next
	}
}

// manageDestDir 解析并校验目标目录：必须存在、是目录、可写、非隐藏。
func (s *Server) manageDestDir(rel string) (string, error) {
	abs, err := s.safePath(rel)
	if err != nil {
		return "", manageErr(http.StatusBadRequest, "目标目录不合法")
	}
	if s.hiddenBlocked(abs) {
		return "", manageErr(http.StatusBadRequest, "目标目录不可操作")
	}
	fi, err := os.Stat(abs)
	if err != nil || !fi.IsDir() {
		return "", manageErr(http.StatusBadRequest, "目标不是目录")
	}
	if isReadOnlyMode(fi.Mode()) {
		return "", manageErr(http.StatusForbidden, "目标目录不可写（只读）")
	}
	return abs, nil
}

// manageFailure 批量操作中的单项失败。
type manageFailure struct {
	Path  string `json:"path"`
	Error string `json:"error"`
}

// manageMove 移动成功的单项。
type manageMove struct {
	From string `json:"from"`
	To   string `json:"to"`
}

// renameReq POST /api/rename 请求体。
type renameReq struct {
	Path string `json:"path"`
	Name string `json:"name"`
}

// handleRename POST /api/rename 重命名文件或文件夹（不改变所在目录）。
func (s *Server) handleRename(w http.ResponseWriter, r *http.Request) {
	info, ok := s.manageSession(w, r)
	if !ok {
		return
	}
	var req renameReq
	if !decodeManageBody(w, r, &req) {
		return
	}
	if msg := validEntryName(req.Name); msg != "" {
		httpError(w, http.StatusBadRequest, msg)
		return
	}
	src, err := s.manageSource(req.Path)
	if err != nil {
		writeManageError(w, err)
		return
	}
	if _, err := os.Lstat(src); err != nil {
		httpError(w, http.StatusNotFound, "源路径不存在")
		return
	}
	parent := filepath.Dir(src)
	if fi, err := os.Stat(parent); err != nil || isReadOnlyMode(fi.Mode()) {
		httpError(w, http.StatusForbidden, "源目录不可写（只读）")
		return
	}
	dst := filepath.Join(parent, req.Name)
	if !pathWithin(s.cfg.root, dst) || s.hiddenBlocked(dst) {
		httpError(w, http.StatusForbidden, "目标路径不可操作")
		return
	}
	if dst != src {
		if _, err := os.Lstat(dst); err == nil {
			// Windows 大小写不敏感：同文件换大小写不算冲突，允许直接改名。
			same := false
			if a, e1 := os.Stat(src); e1 == nil {
				if b, e2 := os.Stat(dst); e2 == nil {
					same = os.SameFile(a, b)
				}
			}
			if !same {
				httpError(w, http.StatusConflict, "同名文件或文件夹已存在")
				return
			}
		} else if !os.IsNotExist(err) {
			httpError(w, http.StatusInternalServerError, "无法检查目标路径")
			return
		}
	}
	if dst != src {
		if err := os.Rename(src, dst); err != nil {
			writeTextWriteError(w, err)
			return
		}
	}
	s.invalidateTextCaches()
	s.logManage("rename", info, r, 1, 1, 0)
	writeJSON(w, http.StatusOK, map[string]any{
		"ok":   true,
		"path": relOf(s.cfg.root, dst),
		"name": req.Name,
	})
}

// transferReq POST /api/move、POST /api/copy 请求体。
// mode 也可由查询串 ?mode=ask|overwrite|rename 提供（查询串优先）。
type transferReq struct {
	Paths []string `json:"paths"`
	Dir   string   `json:"dir"`
	Mode  string   `json:"mode"`
}

// transferItem 一个已规划好目标的移动/复制条目。
type transferItem struct {
	srcAbs string
	srcRel string
	dstAbs string
	dstRel string
	err    string // 预检失败原因（有值则跳过实际操作）
}

// handleMove POST /api/move 把若干条目移动到目标目录。
func (s *Server) handleMove(w http.ResponseWriter, r *http.Request) {
	s.handleTransfer(w, r, false)
}

// handleCopy POST /api/copy 把若干条目复制到目标目录（目录递归、保留 mtime）。
func (s *Server) handleCopy(w http.ResponseWriter, r *http.Request) {
	s.handleTransfer(w, r, true)
}

func (s *Server) handleTransfer(w http.ResponseWriter, r *http.Request, isCopy bool) {
	op := "move"
	if isCopy {
		op = "copy"
	}
	info, ok := s.manageSession(w, r)
	if !ok {
		return
	}
	var req transferReq
	if !decodeManageBody(w, r, &req) {
		return
	}
	mode := r.URL.Query().Get("mode")
	if mode == "" {
		mode = req.Mode
	}
	if mode == "" {
		mode = transferModeAsk
	}
	if mode != transferModeAsk && mode != transferModeOverwrite && mode != transferModeRename {
		httpError(w, http.StatusBadRequest, "mode 非法")
		return
	}
	if len(req.Paths) == 0 {
		httpError(w, http.StatusBadRequest, "未选择文件")
		return
	}
	if len(req.Paths) > manageMaxItems {
		httpError(w, http.StatusBadRequest, fmt.Sprintf("单次最多 %d 项，请分批操作", manageMaxItems))
		return
	}
	destAbs, err := s.manageDestDir(req.Dir)
	if err != nil {
		writeManageError(w, err)
		return
	}

	items := make([]transferItem, 0, len(req.Paths))
	seenTargets := make(map[string]bool, len(req.Paths))
	taken := make(map[string]bool, len(req.Paths))
	conflicts := make([]string, 0)
	for _, p := range req.Paths {
		srcAbs, err := s.manageSource(p)
		if err != nil {
			writeManageError(w, err)
			return
		}
		item := transferItem{srcAbs: srcAbs, srcRel: relOf(s.cfg.root, srcAbs)}
		fi, lerr := os.Lstat(srcAbs)
		if lerr != nil {
			item.err = "路径不存在"
			items = append(items, item)
			continue
		}
		if fi.IsDir() {
			if destAbs == srcAbs || pathWithin(srcAbs, destAbs) {
				httpError(w, http.StatusBadRequest, "目标不能是自身或自身的子目录")
				return
			}
		}
		if isCopy && fi.Mode()&os.ModeSymlink != 0 {
			item.err = "不支持复制符号链接"
			items = append(items, item)
			continue
		}
		dstAbs := filepath.Join(destAbs, filepath.Base(srcAbs))
		if dstAbs == srcAbs {
			item.err = "源与目标位置相同"
			items = append(items, item)
			continue
		}
		if !pathWithin(s.cfg.root, dstAbs) || s.hiddenBlocked(dstAbs) {
			item.err = "目标路径不可操作"
			items = append(items, item)
			continue
		}
		key := dstAbs
		if runtime.GOOS == "windows" {
			key = strings.ToLower(dstAbs)
		}
		if seenTargets[key] {
			httpError(w, http.StatusBadRequest, "同一批次存在重名目标，请分批操作")
			return
		}
		seenTargets[key] = true

		if _, statErr := os.Lstat(dstAbs); statErr == nil {
			switch mode {
			case transferModeAsk:
				conflicts = append(conflicts, relOf(s.cfg.root, dstAbs))
				continue
			case transferModeRename:
				renamed, ok := nextUploadName(destAbs, filepath.Base(srcAbs), taken)
				if !ok {
					item.err = "无法生成不冲突的名称"
					items = append(items, item)
					continue
				}
				dstAbs = renamed
			}
		}
		taken[filepath.Base(dstAbs)] = true
		item.dstAbs = dstAbs
		item.dstRel = relOf(s.cfg.root, dstAbs)
		items = append(items, item)
	}
	if len(conflicts) > 0 {
		writeJSON(w, http.StatusConflict, map[string]any{
			"error":    "目标目录已有同名项",
			"code":     "conflict",
			"existing": uniqueSorted(conflicts),
		})
		return
	}

	moved := make([]manageMove, 0, len(items))
	failed := make([]manageFailure, 0)
	for _, it := range items {
		if it.err != "" {
			failed = append(failed, manageFailure{Path: it.srcRel, Error: it.err})
			continue
		}
		if err := s.transferOne(it, isCopy, mode); err != nil {
			failed = append(failed, manageFailure{Path: it.srcRel, Error: manageOpError(err)})
			continue
		}
		moved = append(moved, manageMove{From: it.srcRel, To: it.dstRel})
	}
	if len(moved) > 0 {
		s.invalidateTextCaches()
	}
	s.logManage(op, info, r, len(req.Paths), len(moved), len(failed))
	key := "moved"
	if isCopy {
		key = "copied"
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"ok":     len(failed) == 0,
		key:      moved,
		"failed": failed,
	})
}

// transferOne 执行单个移动/复制；overwrite 时先把旧目标移到备份，失败可回滚。
func (s *Server) transferOne(it transferItem, isCopy bool, mode string) error {
	if !isCopy && !dirWritable(filepath.Dir(it.srcAbs)) {
		return errReadOnly
	}
	targetExists := false
	if _, err := os.Lstat(it.dstAbs); err == nil {
		targetExists = true
	} else if !os.IsNotExist(err) {
		return err
	}
	if targetExists && mode != transferModeOverwrite {
		return fmt.Errorf("目标已存在")
	}
	var backup string
	if targetExists {
		srcInfo, err := os.Lstat(it.srcAbs)
		if err != nil {
			return err
		}
		dstInfo, err := os.Lstat(it.dstAbs)
		if err != nil {
			return err
		}
		if srcInfo.IsDir() != dstInfo.IsDir() {
			return fmt.Errorf("同名目标类型不同，无法覆盖")
		}
		backup, err = backupTarget(it.dstAbs)
		if err != nil {
			return err
		}
	}
	var opErr error
	if isCopy {
		opErr = copyTree(it.srcAbs, it.dstAbs)
	} else {
		opErr = os.Rename(it.srcAbs, it.dstAbs)
	}
	if opErr != nil {
		_ = os.RemoveAll(it.dstAbs)
		restoreBackup(backup, it.dstAbs)
		return opErr
	}
	if backup != "" {
		_ = os.RemoveAll(backup)
	}
	return nil
}

// manageOpError 把底层错误转成不泄露绝对路径的中文原因。
func manageOpError(err error) string {
	switch {
	case errors.Is(err, errReadOnly), os.IsPermission(err):
		return "无写入权限"
	case os.IsNotExist(err):
		return "路径不存在"
	case os.IsExist(err):
		return "目标已存在"
	default:
		return "操作失败"
	}
}
