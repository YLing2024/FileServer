package server

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"
)

// ============================================================
// 分片上传 / 断点续传。
//
// 大文件由前端切成 8 MiB 分片写入 .FileServer/uploads/<upload_id>/<index>，
// 全部分片到齐后由 /api/upload/complete 顺序合并到目标目录的 .upload-* 临时文件，
// 校验大小与可选 SHA-256 后再原子 rename。upload_id 由「前 1 MiB + 大小 + 名称」
// 派生，同一文件重试会命中同一目录，已完成分片因此可以复用。
//
// 上传会话只保存元数据与分片，不触碰用户目录；24 小时内未完成的分片目录
// 由启动清理与每日清理回收。
// ============================================================

const (
	// uploadChunkThreshold 超过该大小的文件前端改走分片上传。
	uploadChunkThreshold = 8 << 20
	// uploadChunkMax 单个分片上限（防止单请求无限灌流）。
	uploadChunkMax = 64 << 20
	// uploadChunkTotalMax 单个文件的分片数上限（1 GiB / 8 MiB ≈ 128，留足余量）。
	uploadChunkTotalMax = 65536
	// uploadRetention 残缺上传会话保留时长。
	uploadRetention = 24 * time.Hour
	// uploadDerivePrefix 派生 upload_id 时读取的文件前缀长度。
	uploadDerivePrefix = 1 << 20
)

// uploadMeta 分片上传会话的元数据（upload_id 已能由内容派生，这里只做校验）。
type uploadMeta struct {
	ID        string `json:"id"`
	Name      string `json:"name"`
	Size      int64  `json:"size"`
	Total     int    `json:"total"`
	CreatedAt int64  `json:"created_at"`
}

// uploadsRoot 分片上传会话根目录（位于保留目录 .FileServer 内）。
func (s *Server) uploadsRoot() string {
	return filepath.Join(s.cfg.root, cacheDirName, "uploads")
}

// uploadSessionDir 返回 upload_id 对应的分片目录；upload_id 必须先通过格式校验。
func (s *Server) uploadSessionDir(id string) string {
	return filepath.Join(s.uploadsRoot(), id)
}

func (s *Server) uploadMetaPath(id string) string {
	return filepath.Join(s.uploadSessionDir(id), "meta.json")
}

// normalizeHex64 校验并规范化 64 位十六进制串（upload_id / SHA-256）。
func normalizeHex64(v string) (string, bool) {
	raw, err := hex.DecodeString(strings.TrimSpace(v))
	if err != nil || len(raw) != sha256.Size {
		return "", false
	}
	return hex.EncodeToString(raw), true
}

// deriveUploadID 由文件前 1 MiB、字节大小与名称派生 upload_id。
// 客户端必须使用同一公式，保证中断后重新选择同一文件得到相同 id。
func deriveUploadID(head []byte, size int64, name string) string {
	h := sha256.New()
	_, _ = h.Write(head)
	_, _ = h.Write([]byte{0})
	_, _ = h.Write([]byte(strconv.FormatInt(size, 10)))
	_, _ = h.Write([]byte{0})
	_, _ = h.Write([]byte(name))
	return hex.EncodeToString(h.Sum(nil))
}

// readUploadMeta 读取会话元数据；目录/文件不存在时返回 false。
func (s *Server) readUploadMeta(id string) (uploadMeta, bool) {
	data, err := os.ReadFile(s.uploadMetaPath(id))
	if err != nil {
		return uploadMeta{}, false
	}
	var meta uploadMeta
	if json.Unmarshal(data, &meta) != nil || meta.ID != id || meta.Size <= 0 || meta.Total <= 0 {
		return uploadMeta{}, false
	}
	return meta, true
}

// writeUploadMeta 原子写会话元数据。
func (s *Server) writeUploadMeta(meta uploadMeta) error {
	data, err := json.Marshal(meta)
	if err != nil {
		return err
	}
	return atomicWriteFile(s.uploadMetaPath(meta.ID), data, 0o600)
}

// uploadTargetDir 校验分片/完成接口的目标目录，语义与 /api/upload 一致。
func (s *Server) uploadTargetDir(rel string) (string, int, string) {
	abs, err := s.safePath(rel)
	if err != nil {
		return "", http.StatusBadRequest, "目标目录不合法"
	}
	if s.hiddenBlocked(abs) {
		return "", http.StatusForbidden, "不能上传到隐藏目录"
	}
	fi, err := os.Stat(abs)
	if err != nil || !fi.IsDir() {
		return "", http.StatusBadRequest, "目标不是目录"
	}
	if isReadOnlyMode(fi.Mode()) {
		return "", http.StatusForbidden, "目标目录不可写（只读）"
	}
	return abs, 0, ""
}

// uploadRequestMax 单文件上限（配置为 0 时回退默认值）。
func (s *Server) uploadRequestMax() int64 {
	if s.cfg.uploadMaxBytes > 0 {
		return s.cfg.uploadMaxBytes
	}
	return uploadMaxDefaultBytes
}

// handleUploadChunk POST /api/upload/chunk?upload_id=&index=&total=&size=&dir=&name=
// 请求体为原始分片（application/octet-stream）。
func (s *Server) handleUploadChunk(w http.ResponseWriter, r *http.Request) {
	if _, ok := s.manageSession(w, r); !ok {
		return
	}
	if !s.cfg.uploadAllowed {
		httpError(w, http.StatusForbidden, "上传未启用")
		return
	}
	id, ok := normalizeHex64(r.URL.Query().Get("upload_id"))
	if !ok {
		httpError(w, http.StatusBadRequest, "upload_id 不合法")
		return
	}
	index, err1 := strconv.Atoi(r.URL.Query().Get("index"))
	total, err2 := strconv.Atoi(r.URL.Query().Get("total"))
	size, err3 := strconv.ParseInt(r.URL.Query().Get("size"), 10, 64)
	if err1 != nil || err2 != nil || err3 != nil ||
		index < 0 || total <= 0 || index >= total || total > uploadChunkTotalMax ||
		size <= 0 || size > s.uploadRequestMax() {
		httpError(w, http.StatusBadRequest, "分片参数不合法")
		return
	}
	name := r.URL.Query().Get("name")
	if msg := validUploadName(name); msg != "" {
		httpError(w, http.StatusBadRequest, msg)
		return
	}
	if _, status, msg := s.uploadTargetDir(r.URL.Query().Get("dir")); status != 0 {
		httpError(w, status, msg)
		return
	}

	dir := s.uploadSessionDir(id)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		httpError(w, http.StatusInternalServerError, "无法创建分片目录")
		return
	}

	// 先落临时分片，校验通过后再原子改名到 <index>。
	// 同一分片重传直接覆盖，天然幂等（断点续传会重发边界分片）。
	r.Body = http.MaxBytesReader(w, r.Body, uploadChunkMax+1)
	tmp, err := os.CreateTemp(dir, ".chunk-*")
	if err != nil {
		httpError(w, http.StatusInternalServerError, "无法创建分片临时文件")
		return
	}
	tmpName := tmp.Name()
	okTmp := false
	defer func() {
		if !okTmp {
			_ = os.Remove(tmpName)
		}
	}()
	n, copyErr := io.Copy(tmp, io.LimitReader(r.Body, uploadChunkMax+1))
	if copyErr != nil {
		_ = tmp.Close()
		if isMaxBytesError(copyErr) || n > uploadChunkMax {
			httpError(w, http.StatusRequestEntityTooLarge, "分片超过大小上限")
			return
		}
		httpError(w, statusForWriteError(copyErr), "读取分片内容失败")
		return
	}
	if n <= 0 {
		_ = tmp.Close()
		httpError(w, http.StatusBadRequest, "分片内容为空")
		return
	}
	if n > uploadChunkMax {
		_ = tmp.Close()
		httpError(w, http.StatusRequestEntityTooLarge, "分片超过大小上限")
		return
	}
	if err := tmp.Sync(); err != nil {
		_ = tmp.Close()
		httpError(w, http.StatusInternalServerError, "写入分片失败")
		return
	}
	if err := tmp.Chmod(0o600); err != nil {
		_ = tmp.Close()
		httpError(w, http.StatusInternalServerError, "设置分片权限失败")
		return
	}
	if err := tmp.Close(); err != nil {
		httpError(w, http.StatusInternalServerError, "写入分片失败")
		return
	}

	// 首个分片包含完整前缀时，验证 upload_id 是否确实由文件前缀派生，
	// 避免任意 id 占盘。分片小于 1 MiB 时无法判断，留到合并阶段校验。
	headLen := size
	if headLen > uploadDerivePrefix {
		headLen = uploadDerivePrefix
	}
	if index == 0 && n >= headLen {
		head, herr := readUploadPrefix(tmpName, headLen)
		if herr != nil {
			_ = os.RemoveAll(dir)
			httpError(w, http.StatusInternalServerError, "读取分片失败")
			return
		}
		if deriveUploadID(head, size, name) != id {
			_ = os.RemoveAll(dir)
			httpError(w, http.StatusBadRequest, "upload_id 与文件内容不匹配")
			return
		}
	}

	meta, exists := s.readUploadMeta(id)
	if !exists {
		meta = uploadMeta{ID: id, Name: name, Size: size, Total: total, CreatedAt: time.Now().Unix()}
		if err := s.writeUploadMeta(meta); err != nil {
			httpError(w, http.StatusInternalServerError, "无法保存上传会话")
			return
		}
	} else if meta.Name != name || meta.Size != size || meta.Total != total {
		httpError(w, http.StatusConflict, "上传会话参数不一致")
		return
	}

	target := filepath.Join(dir, strconv.Itoa(index))
	if err := os.Rename(tmpName, target); err != nil {
		httpError(w, http.StatusInternalServerError, "保存分片失败")
		return
	}
	okTmp = true
	received := countUploadChunks(dir, meta.Total)
	writeJSON(w, http.StatusOK, map[string]any{
		"ok":       true,
		"received": received,
		"total":    meta.Total,
	})
}

// readUploadPrefix 从分片文件读取指定长度的前缀（调用方保证分片足够长）。
func readUploadPrefix(path string, n int64) ([]byte, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	buf := make([]byte, n)
	if _, err := io.ReadFull(f, buf); err != nil {
		return nil, err
	}
	return buf, nil
}

// countUploadChunks 统计 0..total-1 中已存在的分片数量。
func countUploadChunks(dir string, total int) int {
	n := 0
	for i := 0; i < total; i++ {
		fi, err := os.Stat(filepath.Join(dir, strconv.Itoa(i)))
		if err == nil && fi.Mode().IsRegular() {
			n++
		}
	}
	return n
}

// receivedUploadChunks 返回已收到的分片索引（升序）。
func receivedUploadChunks(dir string, total int) []int {
	out := make([]int, 0, total)
	for i := 0; i < total; i++ {
		fi, err := os.Stat(filepath.Join(dir, strconv.Itoa(i)))
		if err == nil && fi.Mode().IsRegular() {
			out = append(out, i)
		}
	}
	sort.Ints(out)
	return out
}

// handleUploadStatus GET /api/upload/status?upload_id=&size=&name=&dir=
// 返回已收到的分片索引，供前端断点续传跳过已完成部分。
func (s *Server) handleUploadStatus(w http.ResponseWriter, r *http.Request) {
	if _, ok := s.manageSession(w, r); !ok {
		return
	}
	if !s.cfg.uploadAllowed {
		httpError(w, http.StatusForbidden, "上传未启用")
		return
	}
	id, ok := normalizeHex64(r.URL.Query().Get("upload_id"))
	if !ok {
		httpError(w, http.StatusBadRequest, "upload_id 不合法")
		return
	}
	if name := r.URL.Query().Get("name"); name != "" {
		if msg := validUploadName(name); msg != "" {
			httpError(w, http.StatusBadRequest, msg)
			return
		}
	}
	if dir := r.URL.Query().Get("dir"); dir != "" {
		if _, status, msg := s.uploadTargetDir(dir); status != 0 {
			httpError(w, status, msg)
			return
		}
	}
	resp := map[string]any{
		"upload_id": id,
		"received":  []int{},
		"total":     0,
		"complete":  false,
	}
	meta, exists := s.readUploadMeta(id)
	if !exists {
		writeJSON(w, http.StatusOK, resp)
		return
	}
	received := receivedUploadChunks(s.uploadSessionDir(id), meta.Total)
	resp["received"] = received
	resp["total"] = meta.Total
	resp["size"] = meta.Size
	resp["complete"] = len(received) == meta.Total
	resp["created_at"] = meta.CreatedAt
	resp["expires_at"] = meta.CreatedAt + int64(uploadRetention/time.Second)
	writeJSON(w, http.StatusOK, resp)
}

// uploadCompleteReq POST /api/upload/complete 请求体。
type uploadCompleteReq struct {
	UploadID string `json:"upload_id"`
	Dir      string `json:"dir"`
	Name     string `json:"name"`
	Mode     string `json:"mode"`
	SHA256   string `json:"sha256"`
}

// handleUploadComplete POST /api/upload/complete：合并分片并原子落位。
func (s *Server) handleUploadComplete(w http.ResponseWriter, r *http.Request) {
	if _, ok := s.manageSession(w, r); !ok {
		return
	}
	if !s.cfg.uploadAllowed {
		httpError(w, http.StatusForbidden, "上传未启用")
		return
	}
	var req uploadCompleteReq
	if !decodeManageBody(w, r, &req) {
		return
	}
	id, ok := normalizeHex64(req.UploadID)
	if !ok {
		httpError(w, http.StatusBadRequest, "upload_id 不合法")
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
	if msg := validUploadName(req.Name); msg != "" {
		httpError(w, http.StatusBadRequest, msg)
		return
	}
	dirAbs, status, msg := s.uploadTargetDir(req.Dir)
	if status != 0 {
		httpError(w, status, msg)
		return
	}
	meta, exists := s.readUploadMeta(id)
	if !exists {
		httpError(w, http.StatusBadRequest, "上传会话不存在或已过期")
		return
	}
	if meta.Name != req.Name {
		httpError(w, http.StatusConflict, "上传会话参数不一致")
		return
	}
	sha := ""
	if strings.TrimSpace(req.SHA256) != "" {
		var ok bool
		sha, ok = normalizeHex64(req.SHA256)
		if !ok {
			httpError(w, http.StatusBadRequest, "sha256 不合法")
			return
		}
	}

	sessionDir := s.uploadSessionDir(id)
	missing := make([]int, 0)
	for i := 0; i < meta.Total; i++ {
		fi, err := os.Stat(filepath.Join(sessionDir, strconv.Itoa(i)))
		if err != nil || !fi.Mode().IsRegular() {
			missing = append(missing, i)
		}
	}
	if len(missing) > 0 {
		// 缺片无法合并：拒绝并清掉残缺会话，避免半成品长期占盘。
		_ = os.RemoveAll(sessionDir)
		writeJSON(w, http.StatusConflict, map[string]any{
			"error":   "分片不完整",
			"code":    "missing_chunks",
			"missing": missing,
		})
		return
	}

	tmp, err := os.CreateTemp(dirAbs, uploadTempPattern)
	if err != nil {
		httpError(w, statusForWriteError(err), "无法创建临时文件")
		return
	}
	tmpName := tmp.Name()
	keepTmp := false
	defer func() {
		if !keepTmp {
			_ = os.Remove(tmpName)
		}
	}()
	var written int64
	hasher := sha256.New()
	for i := 0; i < meta.Total; i++ {
		src, err := os.Open(filepath.Join(sessionDir, strconv.Itoa(i)))
		if err != nil {
			_ = tmp.Close()
			_ = os.RemoveAll(sessionDir)
			httpError(w, http.StatusInternalServerError, "读取分片失败")
			return
		}
		var dst io.Writer = tmp
		if sha != "" {
			dst = io.MultiWriter(tmp, hasher)
		}
		n, copyErr := io.Copy(dst, src)
		_ = src.Close()
		if copyErr != nil {
			_ = tmp.Close()
			_ = os.RemoveAll(sessionDir)
			httpError(w, statusForWriteError(copyErr), "合并分片失败")
			return
		}
		written += n
	}
	if written != meta.Size {
		_ = tmp.Close()
		_ = os.RemoveAll(sessionDir)
		httpError(w, http.StatusBadRequest, "合并后大小与声明不一致")
		return
	}
	if sha != "" && hex.EncodeToString(hasher.Sum(nil)) != sha {
		_ = tmp.Close()
		_ = os.RemoveAll(sessionDir)
		httpError(w, http.StatusBadRequest, "内容校验失败")
		return
	}
	if err := tmp.Sync(); err != nil {
		_ = tmp.Close()
		_ = os.RemoveAll(sessionDir)
		httpError(w, http.StatusInternalServerError, "写入临时文件失败")
		return
	}
	if err := tmp.Chmod(0o644); err != nil {
		_ = tmp.Close()
		_ = os.RemoveAll(sessionDir)
		httpError(w, http.StatusInternalServerError, "设置文件权限失败")
		return
	}
	if err := tmp.Close(); err != nil {
		_ = os.RemoveAll(sessionDir)
		httpError(w, http.StatusInternalServerError, "写入临时文件失败")
		return
	}

	items := []*uploadItem{{tmp: tmpName, name: req.Name, size: meta.Size}}
	targets, conflict := planUploadTargets(dirAbs, items, mode)
	if conflict != nil {
		// 冲突时保留分片，用户选覆盖/改名后可重新 complete，无需重传。
		writeJSON(w, conflict.status, map[string]any{
			"error":    conflict.message,
			"code":     conflict.code,
			"existing": conflict.existing,
		})
		return
	}
	target := targets[0]
	if !pathWithin(s.cfg.root, target) {
		httpError(w, http.StatusForbidden, "路径访问被拒绝")
		return
	}
	oldInfo, oldErr := os.Stat(target)
	if err := commitUploadTemp(tmpName, target); err != nil {
		writeTextWriteError(w, err)
		return
	}
	keepTmp = true
	_ = os.RemoveAll(sessionDir)
	if sha != "" {
		if err := s.registerBlobFile(target, sha); err != nil {
			slog.Warn("登记内容索引失败", "err", err)
		}
	}
	if oldErr == nil && oldInfo.Mode().IsRegular() {
		s.pruneOrphanBlobs()
	}
	s.invalidateTextCaches()
	fi, err := os.Stat(target)
	if err != nil {
		httpError(w, http.StatusInternalServerError, "上传后无法读取文件状态")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"uploaded": []uploadedFile{{
			Name:  filepath.Base(target),
			Size:  fi.Size(),
			MTime: fi.ModTime().Unix(),
		}},
		"instant": false,
	})
}
