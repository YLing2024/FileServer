package server

import (
	"errors"
	"fmt"
	"io"
	"log/slog"
	"mime"
	"mime/multipart"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
)

// ============================================================
// 文件上传：POST /api/upload（multipart，可多文件）。
//
// 写入范围在「新建 / 编辑文本」之上再放开一条：上传到当前目录。
// 安全边界与既有写入路径一致：safePath + pathWithin + hiddenBlocked，
// 只创建/替换文件，不递归建目录、不接受路径分隔符、不写 .FileServer。
// 每个文件先落到同目录临时文件（.upload-*），全部校验通过后才 rename，
// 任何失败都清理临时文件，不留半截产物。
// ============================================================

// uploadMaxDefaultBytes 单文件默认上限（1024 MiB）；--max-upload-mb 可配。
const uploadMaxDefaultBytes = 1024 << 20

// uploadMaxFiles 单次请求最多文件数（同时用于请求体总量兜底）。
const uploadMaxFiles = 64

// uploadTempPattern 临时文件模式：与目标同目录，隐藏且可被启动清理识别。
const uploadTempPattern = ".upload-*"

// 上传支持的冲突处理模式。
const (
	uploadModeAsk       = "ask"
	uploadModeOverwrite = "overwrite"
	uploadModeRename    = "rename"
)

// uploadedFile 单个上传成功条目（响应体）。
type uploadedFile struct {
	Name  string `json:"name"`
	Size  int64  `json:"size"`
	MTime int64  `json:"mtime"` // Unix 秒
}

// uploadItem 一个已写入临时文件、等待落位的上传条目。
type uploadItem struct {
	tmp  string // 临时文件绝对路径；rename 成功后置空
	name string // 客户端文件名（原始，已通过校验）
	size int64
}

// handleUpload POST /api/upload?dir=&mode= 上传一个或多个文件。
func (s *Server) handleUpload(w http.ResponseWriter, r *http.Request) {
	if !s.cfg.uploadAllowed {
		httpError(w, http.StatusForbidden, "上传未启用")
		return
	}
	mode := r.URL.Query().Get("mode")
	if mode == "" {
		mode = uploadModeAsk
	}
	if mode != uploadModeAsk && mode != uploadModeOverwrite && mode != uploadModeRename {
		httpError(w, http.StatusBadRequest, "mode 非法")
		return
	}
	// 可选 SHA-256：前端已计算整文件哈希时随上传带来，落位后登记内容索引。
	// 仅单文件请求生效；哈希由服务端重新计算确认，避免错误索引。
	sha := ""
	if raw := strings.TrimSpace(r.URL.Query().Get("sha256")); raw != "" {
		var ok bool
		sha, ok = normalizeHex64(raw)
		if !ok {
			httpError(w, http.StatusBadRequest, "sha256 不合法")
			return
		}
	}

	dirAbs, err := s.safePath(r.URL.Query().Get("dir"))
	if err != nil {
		// 上传采用 400（参数不合法）：与验收约定一致，且不向调用方区分
		// 「不存在」与「越界」，避免探测目录结构。
		httpError(w, http.StatusBadRequest, "目标目录不合法")
		return
	}
	if s.hiddenBlocked(dirAbs) {
		httpError(w, http.StatusForbidden, "不能上传到隐藏目录")
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

	maxBytes := s.cfg.uploadMaxBytes
	if maxBytes <= 0 {
		maxBytes = uploadMaxDefaultBytes
	}
	// 请求体总量兜底：单文件上限 × 最多文件数 + multipart 头开销。
	// 单文件是否超限仍按 maxBytes 精确判定（413），此处只防无限灌流。
	r.Body = http.MaxBytesReader(w, r.Body, maxBytes*uploadMaxFiles+(1<<20))

	items, status, msg := s.receiveUploadParts(r, dirAbs, maxBytes)
	if status != 0 {
		cleanupUploadItems(items)
		httpError(w, status, msg)
		return
	}
	// 无论后续成功与否，未 rename 的临时文件都必须清理。
	defer cleanupUploadItems(items)

	if len(items) == 0 {
		httpError(w, http.StatusBadRequest, "没有可上传的文件")
		return
	}

	targets, conflict := planUploadTargets(dirAbs, items, mode)
	if conflict != nil {
		writeJSON(w, conflict.status, map[string]any{
			"error":    conflict.message,
			"code":     conflict.code,
			"existing": conflict.existing,
		})
		return
	}

	uploaded := make([]uploadedFile, 0, len(items))
	for i := range items {
		target := targets[i]
		if !pathWithin(s.cfg.root, target) {
			httpError(w, http.StatusForbidden, "路径访问被拒绝")
			return
		}
		oldInfo, oldErr := os.Stat(target)
		if err := commitUploadTemp(items[i].tmp, target); err != nil {
			writeTextWriteError(w, err)
			return
		}
		items[i].tmp = ""
		if oldErr == nil && oldInfo.Mode().IsRegular() {
			// 覆盖会把旧 inode 的最后一条用户链接摘掉，顺手回收无引用 blob。
			s.pruneOrphanBlobs()
		}
		if sha != "" && len(items) == 1 {
			if got, herr := hashFileSHA256(target); herr == nil && got == sha {
				if rerr := s.registerBlobFile(target, sha); rerr != nil {
					slog.Warn("登记内容索引失败", "err", rerr)
				}
			}
		}
		fi, err := os.Stat(target)
		if err != nil {
			httpError(w, http.StatusInternalServerError, "上传后无法读取文件状态")
			return
		}
		uploaded = append(uploaded, uploadedFile{
			Name:  filepath.Base(target),
			Size:  fi.Size(),
			MTime: fi.ModTime().Unix(),
		})
	}

	s.invalidateTextCaches()
	writeJSON(w, http.StatusOK, map[string]any{"uploaded": uploaded})
}

// receiveUploadParts 依次读取 multipart 的 file 字段并写入目标目录临时文件。
// 返回非 0 status 表示失败（此时已写入的临时文件由调用方清理）。
func (s *Server) receiveUploadParts(r *http.Request, dirAbs string, maxBytes int64) ([]*uploadItem, int, string) {
	mr, err := r.MultipartReader()
	if err != nil {
		return nil, http.StatusBadRequest, "请求不是 multipart/form-data"
	}
	items := make([]*uploadItem, 0, 4)
	for {
		part, err := mr.NextPart()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			if isMaxBytesError(err) {
				return items, http.StatusRequestEntityTooLarge, "上传内容超过大小上限"
			}
			return items, http.StatusBadRequest, "请求格式错误"
		}
		if part.FormName() != "file" {
			_ = part.Close()
			continue
		}
		rawName := rawPartFilename(part)
		if msg := validUploadName(rawName); msg != "" {
			_ = part.Close()
			return items, http.StatusBadRequest, msg
		}
		if len(items) >= uploadMaxFiles {
			_ = part.Close()
			return items, http.StatusBadRequest, fmt.Sprintf("单次最多上传 %d 个文件", uploadMaxFiles)
		}
		tmp, err := os.CreateTemp(dirAbs, uploadTempPattern)
		if err != nil {
			_ = part.Close()
			return items, statusForWriteError(err), "无法创建临时文件"
		}
		item := &uploadItem{tmp: tmp.Name(), name: rawName, size: 0}
		n, copyErr := io.Copy(tmp, io.LimitReader(part, maxBytes+1))
		_ = part.Close()
		if copyErr != nil {
			_ = tmp.Close()
			items = append(items, item)
			if isMaxBytesError(copyErr) || n > maxBytes {
				return items, http.StatusRequestEntityTooLarge, "单个文件超过大小上限"
			}
			return items, statusForWriteError(copyErr), "读取上传内容失败"
		}
		if n > maxBytes {
			_ = tmp.Close()
			items = append(items, item)
			return items, http.StatusRequestEntityTooLarge, "单个文件超过大小上限"
		}
		if err := tmp.Sync(); err != nil {
			_ = tmp.Close()
			items = append(items, item)
			return items, statusForWriteError(err), "写入临时文件失败"
		}
		if err := tmp.Chmod(0o644); err != nil {
			_ = tmp.Close()
			items = append(items, item)
			return items, statusForWriteError(err), "设置文件权限失败"
		}
		if err := tmp.Close(); err != nil {
			items = append(items, item)
			return items, http.StatusInternalServerError, "写入临时文件失败"
		}
		item.size = n
		items = append(items, item)
	}
	return items, 0, ""
}

// uploadConflict 冲突描述，直接映射为 HTTP 响应。
type uploadConflict struct {
	status   int
	code     string
	message  string
	existing []string
}

// planUploadTargets 为每个条目确定最终目标路径：
//   - ask：任一目标已存在（或同批次重名）→ 409，existing 列出冲突名；
//   - overwrite：允许替换普通文件，目标是目录 → 409；
//   - rename：按 "name (1).ext" 递增挑选未被占用的名字（含本批次已占用）。
func planUploadTargets(dirAbs string, items []*uploadItem, mode string) ([]string, *uploadConflict) {
	targets := make([]string, len(items))
	taken := make(map[string]bool, len(items))
	conflicts := make([]string, 0)
	dirs := make([]string, 0)

	for i, it := range items {
		target := filepath.Join(dirAbs, it.name)
		targets[i] = target
		targetExists, targetIsDir := uploadTargetState(target)

		switch mode {
		case uploadModeAsk:
			if targetIsDir {
				dirs = append(dirs, it.name)
				continue
			}
			if targetExists || taken[it.name] {
				conflicts = append(conflicts, it.name)
				continue
			}
		case uploadModeOverwrite:
			if targetIsDir {
				dirs = append(dirs, it.name)
				continue
			}
		case uploadModeRename:
			if targetExists || taken[it.name] {
				renamed, ok := nextUploadName(dirAbs, it.name, taken)
				if !ok {
					return nil, &uploadConflict{
						status:  http.StatusBadRequest,
						message: "无法生成不冲突的文件名（名称过长或同名文件过多）",
					}
				}
				targets[i] = renamed
			}
		}
		taken[it.name] = true
		taken[filepath.Base(targets[i])] = true
	}

	if len(conflicts) > 0 {
		return nil, conflictResponse(conflicts, "conflict", "同名文件已存在")
	}
	if len(dirs) > 0 {
		return nil, conflictResponse(dirs, "target_is_dir", "目标是文件夹，不能覆盖")
	}
	return targets, nil
}

// conflictResponse 生成 409 冲突描述：{code, existing, error}。
func conflictResponse(names []string, code, message string) *uploadConflict {
	return &uploadConflict{
		status:   http.StatusConflict,
		code:     code,
		message:  message,
		existing: uniqueSorted(names),
	}
}

// uploadTargetState 判断目标是否存在、是否为目录。
// 用 Lstat：符号链接本身视为已存在条目；overwrite 替换链接而不是跟随它。
func uploadTargetState(target string) (exists, isDir bool) {
	fi, err := os.Lstat(target)
	if err != nil {
		return false, false
	}
	return true, fi.IsDir()
}

// nextUploadName 生成 "base (n).ext" 形式的不冲突名称；n 从 1 起递增。
func nextUploadName(dirAbs, name string, taken map[string]bool) (string, bool) {
	ext := filepath.Ext(name)
	base := strings.TrimSuffix(name, ext)
	for n := 1; n <= 10000; n++ {
		candidate := fmt.Sprintf("%s (%d)%s", base, n, ext)
		if len(candidate) > 255 {
			return "", false
		}
		if taken[candidate] {
			continue
		}
		exists, isDir := uploadTargetState(filepath.Join(dirAbs, candidate))
		if exists || isDir {
			continue
		}
		return filepath.Join(dirAbs, candidate), true
	}
	return "", false
}

// commitUploadTemp 把临时文件落位到 target。
// POSIX 上 os.Rename 可直接覆盖普通文件；Windows 的 rename 不覆盖已存在文件，
// 故失败且目标存在时先删除再重试（overwrite 语义下唯一的非原子窗口）。
func commitUploadTemp(tmp, target string) error {
	err := os.Rename(tmp, target)
	if err == nil {
		return nil
	}
	if runtime.GOOS != "windows" {
		// POSIX 的 rename 可原子覆盖已存在文件；此时失败就是真错误（权限/磁盘等），
		// 不能通过删除目标来“修复”。Windows 的部分文件系统/版本不允许 rename 覆盖，
		// 才走下面的删除重试分支。
		return err
	}
	if _, statErr := os.Lstat(target); statErr != nil {
		return err
	}
	if removeErr := os.Remove(target); removeErr != nil {
		return removeErr
	}
	return os.Rename(tmp, target)
}

// cleanupUploadItems 删除尚未落位的临时文件（幂等）。
func cleanupUploadItems(items []*uploadItem) {
	for _, it := range items {
		if it != nil && it.tmp != "" {
			_ = os.Remove(it.tmp)
		}
	}
}

// validUploadName 上传文件名校验：在通用命名规则上额外拒绝路径分隔符。
// 保留中文与空格；隐藏文件（点开头）、Windows 保留名、.FileServer 等一律拒绝。
func validUploadName(name string) string {
	if name == "" {
		return "文件名不能为空"
	}
	if strings.ContainsAny(name, "/\\\x00") {
		return "文件名不能包含路径分隔符"
	}
	if isCacheEntry(name) {
		return "不能使用保留名称"
	}
	return validEntryName(name)
}

// rawPartFilename 读取 multipart 头里的原始 filename。
// multipart.Part.FileName() 会先取 basename，无法识别客户端传入的 "../x"；
// 这里直接解析 Content-Disposition，拒绝含路径分隔符的名字而不是静默改名。
func rawPartFilename(part *multipart.Part) string {
	if _, params, err := mime.ParseMediaType(part.Header.Get("Content-Disposition")); err == nil {
		return params["filename"]
	}
	return part.FileName()
}

// isMaxBytesError 判断是否为 http.MaxBytesReader 的超限错误。
func isMaxBytesError(err error) bool {
	var tooLarge *http.MaxBytesError
	return errors.As(err, &tooLarge)
}

// statusForWriteError 把写入底层错误映射为状态码（413 已在调用处单独处理）。
func statusForWriteError(err error) int {
	if errors.Is(err, errReadOnly) || os.IsPermission(err) {
		return http.StatusForbidden
	}
	if isMaxBytesError(err) {
		return http.StatusRequestEntityTooLarge
	}
	return http.StatusInternalServerError
}

// uniqueSorted 去重并按字典序排序（冲突列表稳定输出，便于前端与测试）。
func uniqueSorted(in []string) []string {
	if len(in) == 0 {
		return in
	}
	seen := make(map[string]bool, len(in))
	out := make([]string, 0, len(in))
	for _, v := range in {
		if !seen[v] {
			seen[v] = true
			out = append(out, v)
		}
	}
	sort.Strings(out)
	return out
}
