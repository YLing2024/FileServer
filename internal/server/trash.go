package server

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"
)

// ============================================================
// 回收站：删除不真删，移到 <root>/.FileServer/trash/<id>/ 下并保留原相对路径。
//
// 目录结构：
//
//	.FileServer/trash/<YYYYMMDD-HHMMSS>-<8位随机>/<原相对路径>
//	.FileServer/trash/.meta/<id>.json                 删除时的元信息
//
// 回收站位于保留目录 .FileServer 内，普通浏览/搜索/缩略图天然不可见；
// 恢复/彻底删除只接受严格格式的 id，且 id 目录的真实路径必须仍在回收站内。
// ============================================================

// trashIDPattern 回收站条目 id：时间戳-随机串，同时用于路径安全校验。
var trashIDPattern = regexp.MustCompile(`^[0-9]{8}-[0-9]{6}-[0-9a-f]{8}$`)

// trashMeta 回收站条目的元信息（不含文件内容）。
type trashMeta struct {
	ID        string `json:"id"`
	Path      string `json:"path"` // 原相对路径，形如 /docs/a.txt
	Kind      string `json:"kind"` // dir/image/video/text/...
	DeletedAt int64  `json:"deleted_at"`
}

// trashItem GET /api/trash 的列表条目。
type trashItem struct {
	ID        string `json:"id"`
	Path      string `json:"path"`
	Name      string `json:"name"`
	Kind      string `json:"kind"`
	IsDir     bool   `json:"is_dir"`
	Size      int64  `json:"size"`
	DeletedAt int64  `json:"deleted_at"`
}

// trashRoot 回收站根目录（永远位于服务根目录的 .FileServer 内）。
func (s *Server) trashRoot() string {
	return filepath.Join(s.cfg.root, cacheDirName, "trash")
}

func (s *Server) trashMetaDir() string { return filepath.Join(s.trashRoot(), ".meta") }

// ensureTrash 创建回收站目录（首次删除/查看时惰性创建）。
func (s *Server) ensureTrash() error {
	if err := os.MkdirAll(s.trashRoot(), 0o700); err != nil {
		return err
	}
	return os.MkdirAll(s.trashMetaDir(), 0o700)
}

// validTrashID 判断 id 是否符合服务端生成格式，杜绝 ../ 与任意路径注入。
func validTrashID(id string) bool { return trashIDPattern.MatchString(id) }

// newTrashID 生成并占用一个新的回收站条目目录。
func (s *Server) newTrashID() (string, string, error) {
	if err := s.ensureTrash(); err != nil {
		return "", "", err
	}
	stamp := time.Now().Format("20060102-150405")
	for i := 0; i < 20; i++ {
		var b [4]byte
		if _, err := rand.Read(b[:]); err != nil {
			return "", "", err
		}
		id := stamp + "-" + hex.EncodeToString(b[:])
		dir := filepath.Join(s.trashRoot(), id)
		err := os.Mkdir(dir, 0o700)
		if err == nil {
			return id, dir, nil
		}
		if !os.IsExist(err) {
			return "", "", err
		}
	}
	return "", "", fmt.Errorf("无法生成回收站条目")
}

// trashItemDir 校验并返回条目目录的真实路径；id 非法或目录越界一律拒绝。
func (s *Server) trashItemDir(id string) (string, error) {
	if !validTrashID(id) {
		return "", manageErr(http.StatusBadRequest, "回收站条目 id 非法")
	}
	dir := filepath.Join(s.trashRoot(), id)
	real, err := filepath.EvalSymlinks(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return "", os.ErrNotExist
		}
		return "", err
	}
	if !pathWithin(s.trashRoot(), real) || real == s.trashRoot() {
		return "", manageErr(http.StatusForbidden, "回收站条目越界")
	}
	return real, nil
}

func (s *Server) writeTrashMeta(meta trashMeta) error {
	data, err := json.Marshal(meta)
	if err != nil {
		return err
	}
	return atomicWriteFile(filepath.Join(s.trashMetaDir(), meta.ID+".json"), data, 0o600)
}

func (s *Server) readTrashMeta(id string) (trashMeta, bool) {
	data, err := os.ReadFile(filepath.Join(s.trashMetaDir(), id+".json"))
	if err != nil {
		return trashMeta{}, false
	}
	var meta trashMeta
	if json.Unmarshal(data, &meta) != nil || meta.ID != id {
		return trashMeta{}, false
	}
	return meta, true
}

// removeTrashMeta 尽力删除元信息文件。
func (s *Server) removeTrashMeta(id string) {
	_ = os.Remove(filepath.Join(s.trashMetaDir(), id+".json"))
}

// handleDelete POST /api/delete：把若干条目移入回收站。
func (s *Server) handleDelete(w http.ResponseWriter, r *http.Request) {
	info, ok := s.manageSession(w, r)
	if !ok {
		return
	}
	var req struct {
		Paths []string `json:"paths"`
	}
	if !decodeManageBody(w, r, &req) {
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
	// 先统一做安全校验：任一路径越界/指向 .FileServer 都整批拒绝。
	sources := make([]string, len(req.Paths))
	for i, p := range req.Paths {
		abs, err := s.manageSource(p)
		if err != nil {
			writeManageError(w, err)
			return
		}
		sources[i] = abs
	}

	moved := make([]map[string]string, 0, len(sources))
	failed := make([]manageFailure, 0)
	for _, abs := range sources {
		rel := relOf(s.cfg.root, abs)
		fi, err := os.Lstat(abs)
		if err != nil {
			failed = append(failed, manageFailure{Path: rel, Error: "路径不存在"})
			continue
		}
		kind := fileKind(filepath.Base(abs), fi.IsDir())
		meta := trashMeta{Path: rel, Kind: kind, DeletedAt: time.Now().Unix()}
		id, idDir, err := s.newTrashID()
		if err != nil {
			failed = append(failed, manageFailure{Path: rel, Error: "无法创建回收站条目"})
			continue
		}
		meta.ID = id
		if err := s.writeTrashMeta(meta); err != nil {
			_ = os.RemoveAll(idDir)
			failed = append(failed, manageFailure{Path: rel, Error: "无法写入回收站元信息"})
			continue
		}
		target := filepath.Join(idDir, filepath.FromSlash(strings.TrimPrefix(rel, "/")))
		if !pathWithin(idDir, target) {
			_ = os.RemoveAll(idDir)
			s.removeTrashMeta(id)
			failed = append(failed, manageFailure{Path: rel, Error: "回收站目标路径不合法"})
			continue
		}
		if err := os.MkdirAll(filepath.Dir(target), 0o700); err == nil {
			err = os.Rename(abs, target)
		}
		if err != nil {
			_ = os.RemoveAll(idDir)
			s.removeTrashMeta(id)
			failed = append(failed, manageFailure{Path: rel, Error: manageOpError(err)})
			continue
		}
		moved = append(moved, map[string]string{"path": rel, "id": id})
	}
	if len(moved) > 0 {
		s.invalidateTextCaches()
	}
	s.logManage("delete", info, r, len(req.Paths), len(moved), len(failed))
	writeJSON(w, http.StatusOK, map[string]any{
		"ok":     len(failed) == 0,
		"moved":  moved,
		"failed": failed,
	})
}

// handleTrash GET /api/trash：列出回收站条目与总占用。
func (s *Server) handleTrash(w http.ResponseWriter, r *http.Request) {
	if _, ok := s.manageSession(w, r); !ok {
		return
	}
	items := make([]trashItem, 0)
	var total int64
	des, err := os.ReadDir(s.trashRoot())
	if err != nil {
		if !os.IsNotExist(err) {
			httpError(w, http.StatusInternalServerError, "读取回收站失败")
			return
		}
	} else {
		for _, de := range des {
			id := de.Name()
			if !de.IsDir() || !validTrashID(id) {
				continue
			}
			dir, err := s.trashItemDir(id)
			if err != nil {
				continue
			}
			item, ok := s.trashItemFromDir(id, dir)
			if !ok {
				continue
			}
			items = append(items, item)
			total += item.Size
		}
	}
	sort.Slice(items, func(i, j int) bool {
		if items[i].DeletedAt != items[j].DeletedAt {
			return items[i].DeletedAt > items[j].DeletedAt
		}
		return items[i].ID > items[j].ID
	})
	writeJSON(w, http.StatusOK, map[string]any{
		"items":      items,
		"count":      len(items),
		"total_size": total,
	})
}

// trashItemFromDir 依据元信息与磁盘现状生成列表条目；条目已丢失时返回 false。
func (s *Server) trashItemFromDir(id, dir string) (trashItem, bool) {
	meta, hasMeta := s.readTrashMeta(id)
	if !hasMeta {
		// 元信息缺失（异常中断）：退化为条目目录下第一层名称，仍可展示但无法可靠恢复。
		des, err := os.ReadDir(dir)
		if err != nil || len(des) == 0 {
			return trashItem{}, false
		}
		meta = trashMeta{ID: id, Path: "/" + des[0].Name(), DeletedAt: time.Now().Unix()}
		if fi, err := des[0].Info(); err == nil {
			meta.Kind = fileKind(des[0].Name(), fi.IsDir())
		}
	}
	entry := filepath.Join(dir, filepath.FromSlash(strings.TrimPrefix(meta.Path, "/")))
	if !pathWithin(dir, entry) {
		return trashItem{}, false
	}
	fi, err := os.Lstat(entry)
	if err != nil {
		return trashItem{}, false
	}
	kind := meta.Kind
	if kind == "" {
		kind = fileKind(filepath.Base(entry), fi.IsDir())
	}
	return trashItem{
		ID:        id,
		Path:      meta.Path,
		Name:      filepath.Base(entry),
		Kind:      kind,
		IsDir:     fi.IsDir(),
		Size:      pathSize(entry),
		DeletedAt: meta.DeletedAt,
	}, true
}

// trashRestoreTarget 把元信息里的原路径解析为根目录内的恢复目标。
func (s *Server) trashRestoreTarget(rel string) (string, error) {
	clean := filepath.Clean(filepath.FromSlash(strings.TrimPrefix(filepath.ToSlash(rel), "/")))
	if clean == "." || clean == ".." || strings.HasPrefix(clean, ".."+string(filepath.Separator)) ||
		filepath.IsAbs(clean) {
		return "", manageErr(http.StatusBadRequest, "回收站条目的原路径不合法")
	}
	target := filepath.Join(s.cfg.root, clean)
	if target == s.cfg.root || !pathWithin(s.cfg.root, target) {
		return "", manageErr(http.StatusBadRequest, "回收站条目的原路径不合法")
	}
	if s.hiddenBlocked(target) {
		return "", manageErr(http.StatusBadRequest, "回收站条目的原路径不可操作")
	}
	// 最近已存在祖先必须是根内真实路径，防止恢复时经由越界符号链接写出根目录。
	if real, ok := nearestRealPath(filepath.Dir(target)); ok && !pathWithin(s.cfg.root, real) {
		return "", manageErr(http.StatusForbidden, "恢复目标路径越界")
	}
	return target, nil
}

// restorePlan 一个待恢复条目。
type restorePlan struct {
	id     string
	entry  string
	target string
	rel    string
	err    string
}

// handleTrashRestore POST /api/trash/restore：把条目恢复到原位置。
// mode=ask 遇到同名冲突整批 409（前端询问后可用 mode=rename 重试）；
// mode=rename 自动生成 "name (1).ext" 形式的名称。
func (s *Server) handleTrashRestore(w http.ResponseWriter, r *http.Request) {
	info, ok := s.manageSession(w, r)
	if !ok {
		return
	}
	var req struct {
		IDs  []string `json:"ids"`
		Mode string   `json:"mode"`
	}
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
	if mode != transferModeAsk && mode != transferModeRename {
		httpError(w, http.StatusBadRequest, "mode 非法")
		return
	}
	if len(req.IDs) == 0 {
		httpError(w, http.StatusBadRequest, "未选择回收站条目")
		return
	}
	if len(req.IDs) > manageMaxItems {
		httpError(w, http.StatusBadRequest, fmt.Sprintf("单次最多 %d 项，请分批操作", manageMaxItems))
		return
	}

	plans := make([]restorePlan, 0, len(req.IDs))
	seen := make(map[string]bool, len(req.IDs))
	taken := make(map[string]bool, len(req.IDs))
	conflicts := make([]string, 0)
	targets := make(map[string]bool, len(req.IDs))
	for _, id := range req.IDs {
		if seen[id] {
			continue
		}
		seen[id] = true
		plan := restorePlan{id: id}
		dir, err := s.trashItemDir(id)
		if err != nil {
			if os.IsNotExist(err) {
				plan.err = "回收站条目不存在"
				plans = append(plans, plan)
				continue
			}
			writeManageError(w, err)
			return
		}
		meta, hasMeta := s.readTrashMeta(id)
		if !hasMeta {
			plan.err = "回收站条目元信息缺失"
			plans = append(plans, plan)
			continue
		}
		plan.rel = meta.Path
		target, err := s.trashRestoreTarget(meta.Path)
		if err != nil {
			writeManageError(w, err)
			return
		}
		plan.target = target
		plan.entry = filepath.Join(dir, filepath.FromSlash(strings.TrimPrefix(meta.Path, "/")))
		if !pathWithin(dir, plan.entry) {
			plan.err = "回收站条目路径不合法"
			plans = append(plans, plan)
			continue
		}
		if _, err := os.Lstat(plan.entry); err != nil {
			plan.err = "回收站条目已丢失"
			plans = append(plans, plan)
			continue
		}
		key := target
		if real, ok := nearestRealPath(filepath.Dir(target)); ok {
			key = filepath.Join(real, filepath.Base(target))
		}
		if targets[key] {
			httpError(w, http.StatusBadRequest, "同一批次存在重名恢复目标，请分批操作")
			return
		}
		targets[key] = true
		if _, err := os.Lstat(target); err == nil {
			switch mode {
			case transferModeAsk:
				conflicts = append(conflicts, meta.Path)
				continue
			case transferModeRename:
				renamed, ok := nextUploadName(filepath.Dir(target), filepath.Base(target), taken)
				if !ok {
					plan.err = "无法生成不冲突的名称"
					plans = append(plans, plan)
					continue
				}
				plan.target = renamed
			}
		}
		taken[filepath.Base(plan.target)] = true
		plans = append(plans, plan)
	}
	if len(conflicts) > 0 {
		writeJSON(w, http.StatusConflict, map[string]any{
			"error":    "原位置已有同名项",
			"code":     "conflict",
			"existing": uniqueSorted(conflicts),
		})
		return
	}

	restored := make([]map[string]string, 0, len(plans))
	failed := make([]manageFailure, 0)
	for _, plan := range plans {
		if plan.err != "" {
			failed = append(failed, manageFailure{Path: plan.rel, Error: plan.err})
			continue
		}
		parent := filepath.Dir(plan.target)
		if fi, err := os.Stat(parent); err == nil && isReadOnlyMode(fi.Mode()) {
			failed = append(failed, manageFailure{Path: plan.rel, Error: "原目录不可写（只读）"})
			continue
		}
		if err := os.MkdirAll(parent, 0o755); err != nil {
			failed = append(failed, manageFailure{Path: plan.rel, Error: manageOpError(err)})
			continue
		}
		if err := os.Rename(plan.entry, plan.target); err != nil {
			failed = append(failed, manageFailure{Path: plan.rel, Error: manageOpError(err)})
			continue
		}
		_ = os.RemoveAll(filepath.Join(s.trashRoot(), plan.id))
		s.removeTrashMeta(plan.id)
		restored = append(restored, map[string]string{
			"id":   plan.id,
			"path": relOf(s.cfg.root, plan.target),
			"from": plan.rel,
		})
	}
	if len(restored) > 0 {
		s.invalidateTextCaches()
	}
	s.logManage("restore", info, r, len(req.IDs), len(restored), len(failed))
	writeJSON(w, http.StatusOK, map[string]any{
		"ok":       len(failed) == 0,
		"restored": restored,
		"failed":   failed,
	})
}

// handleTrashEmpty POST /api/trash/empty：彻底删除指定条目；ids 为空或 all=true 时清空。
func (s *Server) handleTrashEmpty(w http.ResponseWriter, r *http.Request) {
	info, ok := s.manageSession(w, r)
	if !ok {
		return
	}
	var req struct {
		IDs []string `json:"ids"`
		All bool     `json:"all"`
	}
	if !decodeManageBody(w, r, &req) {
		return
	}
	ids := req.IDs
	if req.All || len(ids) == 0 {
		des, err := os.ReadDir(s.trashRoot())
		if err != nil && !os.IsNotExist(err) {
			httpError(w, http.StatusInternalServerError, "读取回收站失败")
			return
		}
		ids = ids[:0]
		for _, de := range des {
			if de.IsDir() && validTrashID(de.Name()) {
				ids = append(ids, de.Name())
			}
		}
	} else {
		if len(ids) > manageMaxItems {
			httpError(w, http.StatusBadRequest, fmt.Sprintf("单次最多 %d 项，请分批操作", manageMaxItems))
			return
		}
		for _, id := range ids {
			if !validTrashID(id) {
				httpError(w, http.StatusBadRequest, "回收站条目 id 非法")
				return
			}
		}
	}

	deleted := make([]string, 0, len(ids))
	failed := make([]manageFailure, 0)
	seen := make(map[string]bool, len(ids))
	for _, id := range ids {
		if seen[id] {
			continue
		}
		seen[id] = true
		dir, err := s.trashItemDir(id)
		if err != nil {
			if os.IsNotExist(err) {
				failed = append(failed, manageFailure{Path: id, Error: "回收站条目不存在"})
				continue
			}
			writeManageError(w, err)
			return
		}
		if err := os.RemoveAll(dir); err != nil {
			failed = append(failed, manageFailure{Path: id, Error: manageOpError(err)})
			continue
		}
		s.removeTrashMeta(id)
		deleted = append(deleted, id)
	}
	// 清空时顺手清理遗留的元信息文件（无对应目录的孤儿记录）。
	if req.All || len(req.IDs) == 0 {
		if des, err := os.ReadDir(s.trashMetaDir()); err == nil {
			for _, de := range des {
				if de.IsDir() || !strings.HasSuffix(de.Name(), ".json") {
					continue
				}
				_ = os.Remove(filepath.Join(s.trashMetaDir(), de.Name()))
			}
		}
	}
	s.logManage("trash-empty", info, r, len(ids), len(deleted), len(failed))
	writeJSON(w, http.StatusOK, map[string]any{
		"ok":      len(failed) == 0,
		"deleted": deleted,
		"failed":  failed,
	})
}
