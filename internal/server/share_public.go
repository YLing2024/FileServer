package server

import (
	"archive/zip"
	"errors"
	"io"
	"io/fs"
	"mime"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"time"
)

// ============================================================
// 分享链接公开访问：/s/<token> 页面与 /api/public/share/<token>/* 接口。
//
// 授权模型只有两件东西：不可枚举的 token（16 字节随机）和可选的提取码
// cookie。所有请求都重新查记录与状态，不缓存授权；解析路径时以分享根为
// 边界，拒绝 ..、绝对路径与越界符号链接，响应里不出现真实磁盘路径。
// ============================================================

// sharePublicCSP 公开页的独立 CSP：只加载同源脚本/样式；PDF 用同源 object；
// 禁止被嵌入（frame-ancestors）与 base 改写。
const sharePublicCSP = "default-src 'self'; " +
	"script-src 'self'; " +
	"style-src 'self'; " +
	"img-src 'self' data:; " +
	"media-src 'self'; " +
	"object-src 'self'; " +
	"frame-src 'self'; " +
	"connect-src 'self'; " +
	"frame-ancestors 'none'; " +
	"base-uri 'none'; " +
	"form-action 'self'"

// writeShareStatusError 公开接口错误统一带稳定 code，页面据此展示原因；
// 文案不含路径、服务器信息或「不存在 vs 错误」的细分。
func writeShareStatusError(w http.ResponseWriter, status int, code, msg string) {
	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, status, map[string]string{"error": msg, "code": code})
}

// writeShareRecordFailure 记录状态不可用时写出对应错误；返回 true 表示已处理。
func writeShareRecordFailure(w http.ResponseWriter, rec shareRecord, now time.Time) bool {
	switch rec.status(now) {
	case shareStatusRevoked:
		writeShareStatusError(w, http.StatusForbidden, shareStatusRevoked, "分享已撤销")
	case shareStatusExpired:
		writeShareStatusError(w, http.StatusForbidden, shareStatusExpired, "分享已过期")
	case shareStatusExhausted:
		writeShareStatusError(w, http.StatusForbidden, shareStatusExhausted, "分享的下载次数已用完")
	default:
		return false
	}
	return true
}

// setSharePublicHeaders 公开接口/页面的通用响应头：禁止搜索引擎索引，禁止缓存。
func setSharePublicHeaders(w http.ResponseWriter) {
	w.Header().Set("X-Robots-Tag", "noindex, nofollow")
	w.Header().Set("Cache-Control", "no-store")
}

// safeSharePath 将分享内的相对路径解析为绝对路径，边界是 base 而不是服务根：
// 拒绝 ..、NUL、Windows 盘符与越界符号链接。base 必须是已解析的真实路径。
func safeSharePath(base, rel string) (string, error) {
	if strings.ContainsRune(rel, 0) {
		return "", errForbidden
	}
	if runtime.GOOS == "windows" && strings.Contains(rel, ":") {
		return "", errForbidden
	}
	rel = strings.TrimLeft(rel, "/\\")
	clean := filepath.Clean(filepath.FromSlash(rel))
	if clean == "." {
		clean = ""
	}
	if clean == ".." || strings.HasPrefix(clean, ".."+string(filepath.Separator)) {
		return "", errForbidden
	}
	abs := filepath.Join(base, clean)
	if !pathWithin(base, abs) {
		return "", errForbidden
	}
	real, err := filepath.EvalSymlinks(abs)
	if err != nil {
		if os.IsNotExist(err) {
			return "", errNotFound
		}
		return "", errForbidden
	}
	if !pathWithin(base, real) {
		return "", errForbidden
	}
	return real, nil
}

// openPublicShare 查找记录、校验状态与提取码，并解析分享根真实路径。
// 返回的 isDir 以磁盘当前状态为准（分享后目录被替换也能安全处理）。
func (s *Server) openPublicShare(w http.ResponseWriter, r *http.Request, token string, requireCode bool) (shareRecord, string, bool, bool) {
	rec, ok := s.shares.get(token)
	if !ok {
		writeShareStatusError(w, http.StatusNotFound, "not_found", "分享链接无效或已被删除")
		return shareRecord{}, "", false, false
	}
	now := time.Now()
	if writeShareRecordFailure(w, rec, now) {
		return shareRecord{}, "", false, false
	}
	if requireCode && rec.CodeHash != "" && !s.shareCookieValid(r, token, now) {
		writeShareStatusError(w, http.StatusForbidden, "need_code", "需要提取码")
		return shareRecord{}, "", false, false
	}
	root, err := safeSharePath(s.cfg.root, rec.Path)
	if err != nil {
		writeShareStatusError(w, http.StatusNotFound, "not_found", "分享内容不存在")
		return shareRecord{}, "", false, false
	}
	if s.hiddenBlocked(root) {
		writeShareStatusError(w, http.StatusNotFound, "not_found", "分享内容不存在")
		return shareRecord{}, "", false, false
	}
	fi, err := os.Stat(root)
	if err != nil {
		writeShareStatusError(w, http.StatusNotFound, "not_found", "分享内容不存在")
		return shareRecord{}, "", false, false
	}
	return rec, root, fi.IsDir(), true
}

// resolveShareTarget 在分享根内解析请求路径；文件分享只接受空路径或自身文件名，
// 避免把分享变成「文件所在目录」的入口。
func (s *Server) resolveShareTarget(w http.ResponseWriter, root string, isDir bool, rel string) (string, bool) {
	if !isDir {
		p := strings.TrimLeft(rel, "/\\")
		if p == "" {
			return root, true
		}
		clean := filepath.Clean(filepath.FromSlash(p))
		if clean == filepath.Base(root) {
			return root, true
		}
		writeShareStatusError(w, http.StatusBadRequest, "bad_path", "路径不合法")
		return "", false
	}
	abs, err := safeSharePath(root, rel)
	if err != nil {
		writeShareStatusError(w, http.StatusBadRequest, "bad_path", "路径不合法")
		return "", false
	}
	if s.hiddenBlocked(abs) {
		writeShareStatusError(w, http.StatusNotFound, "not_found", "路径不存在")
		return "", false
	}
	return abs, true
}

// shareRel 返回相对分享根的展示路径（始终以 / 开头，不暴露真实磁盘路径）。
func shareRel(root, abs string) string {
	rel, err := filepath.Rel(root, abs)
	if err != nil || rel == "." {
		return "/"
	}
	return "/" + filepath.ToSlash(rel)
}

// handleSharePage GET /s/{token} 独立公开页。页面本身不区分 token 是否存在，
// 数据由公开接口返回；这样无效链接也不会在 HTML 里泄露任何信息。
func (s *Server) handleSharePage(w http.ResponseWriter, r *http.Request) {
	data, err := fs.ReadFile(webFS, "web/share.html")
	if err != nil {
		http.NotFound(w, r)
		return
	}
	setSharePublicHeaders(w)
	w.Header().Set("Referrer-Policy", "no-referrer")
	w.Header().Set("Content-Security-Policy", sharePublicCSP)
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_, _ = w.Write(data)
}

// publicShareInfo 公开页 info 响应。未通过提取码时只返回展示所需的最小信息
// （备注名/有效期），不返回文件名、大小与类型，避免绕过提取码探测内容。
type publicShareInfo struct {
	Note         string `json:"note"`
	HasCode      bool   `json:"has_code"`
	Verified     bool   `json:"verified"`
	AllowPreview bool   `json:"allow_preview"`
	Created      int64  `json:"created"`
	Expires      int64  `json:"expires"`
	MaxDownloads int    `json:"max_downloads"`
	Downloads    int    `json:"downloads"`
	Status       string `json:"status"`
	Kind         string `json:"kind,omitempty"`
	Name         string `json:"name,omitempty"`
	Size         int64  `json:"size,omitempty"`
	MTime        int64  `json:"mtime,omitempty"`
}

// handlePublicShareInfo GET /api/public/share/{token}/info
func (s *Server) handlePublicShareInfo(w http.ResponseWriter, r *http.Request) {
	setSharePublicHeaders(w)
	token := r.PathValue("token")
	rec, ok := s.shares.get(token)
	if !ok {
		writeShareStatusError(w, http.StatusNotFound, "not_found", "分享链接无效或已被删除")
		return
	}
	now := time.Now()
	if writeShareRecordFailure(w, rec, now) {
		return
	}
	verified := rec.CodeHash == "" || s.shareCookieValid(r, token, now)
	info := publicShareInfo{
		Note:         rec.Note,
		HasCode:      rec.CodeHash != "",
		Verified:     verified,
		AllowPreview: rec.AllowPreview,
		Created:      rec.Created,
		Expires:      rec.Expires,
		MaxDownloads: rec.MaxDownloads,
		Downloads:    rec.Downloads,
		Status:       shareStatusActive,
	}
	if !verified {
		writeJSON(w, http.StatusOK, info)
		return
	}
	root, err := safeSharePath(s.cfg.root, rec.Path)
	if err != nil || s.hiddenBlocked(root) {
		writeShareStatusError(w, http.StatusNotFound, "not_found", "分享内容不存在")
		return
	}
	fi, err := os.Stat(root)
	if err != nil {
		writeShareStatusError(w, http.StatusNotFound, "not_found", "分享内容不存在")
		return
	}
	info.Name = fi.Name()
	info.Size = fi.Size()
	info.MTime = fi.ModTime().Unix()
	if fi.IsDir() {
		info.Kind = "dir"
	} else {
		info.Kind = fileKind(fi.Name(), false)
	}
	writeJSON(w, http.StatusOK, info)
}

// handlePublicShareVerify POST /api/public/share/{token}/verify
func (s *Server) handlePublicShareVerify(w http.ResponseWriter, r *http.Request) {
	setSharePublicHeaders(w)
	token := r.PathValue("token")
	rec, ok := s.shares.get(token)
	if !ok {
		writeShareStatusError(w, http.StatusNotFound, "not_found", "分享链接无效或已被删除")
		return
	}
	now := time.Now()
	if writeShareRecordFailure(w, rec, now) {
		return
	}
	if rec.CodeHash == "" {
		writeJSON(w, http.StatusOK, map[string]any{"ok": true})
		return
	}
	ip := s.clientIP(r)
	if s.shareLimit.blocked(ip, now) {
		writeShareStatusError(w, http.StatusTooManyRequests, "rate_limited", "尝试过于频繁，请稍后再试")
		return
	}
	var body struct {
		Code string `json:"code"`
	}
	if !decodeShareBody(w, r, &body) {
		return
	}
	if !verifyShareCode(rec.CodeHash, token, strings.TrimSpace(body.Code)) {
		s.shareLimit.fail(ip, now)
		// 第 10 次错误即进入限速状态，避免「连续错 10 次仍然只看到 403」。
		if s.shareLimit.blocked(ip, now) {
			writeShareStatusError(w, http.StatusTooManyRequests, "rate_limited", "尝试过于频繁，请稍后再试")
			return
		}
		writeShareStatusError(w, http.StatusForbidden, "bad_code", "提取码不正确")
		return
	}
	s.shareLimit.reset(ip)
	s.setShareCookie(w, r, token, now)
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

// publicEntry 公开列表条目：与内部 Entry 相比不含任何绝对路径。
type publicEntry struct {
	Name  string `json:"name"`
	IsDir bool   `json:"is_dir"`
	Size  int64  `json:"size"`
	MTime int64  `json:"mtime"`
	Ext   string `json:"ext"`
	Kind  string `json:"kind"`
}

// slicePublicPage 与内部列表相同的分页语义，但作用于不含绝对路径的公开条目。
func slicePublicPage(entries []publicEntry, offset, limit int) []publicEntry {
	if offset > len(entries) {
		offset = len(entries)
	}
	if limit > 0 {
		end := offset + limit
		if end > len(entries) {
			end = len(entries)
		}
		return entries[offset:end]
	}
	if offset > 0 {
		return entries[offset:]
	}
	return entries
}

// handlePublicShareList GET /api/public/share/{token}/list?path=&limit=&offset=
func (s *Server) handlePublicShareList(w http.ResponseWriter, r *http.Request) {
	setSharePublicHeaders(w)
	token := r.PathValue("token")
	_, root, isDir, ok := s.openPublicShare(w, r, token, true)
	if !ok {
		return
	}
	if !isDir {
		writeShareStatusError(w, http.StatusBadRequest, "not_a_dir", "该分享不是目录")
		return
	}
	dir, ok := s.resolveShareTarget(w, root, true, r.URL.Query().Get("path"))
	if !ok {
		return
	}
	fi, err := os.Stat(dir)
	if err != nil || !fi.IsDir() {
		writeShareStatusError(w, http.StatusNotFound, "not_found", "路径不存在")
		return
	}
	des, err := os.ReadDir(dir)
	if err != nil {
		writeShareStatusError(w, http.StatusInternalServerError, "read_failed", "读取目录失败")
		return
	}
	entries := make([]publicEntry, 0, len(des))
	for _, de := range des {
		name := de.Name()
		if isCacheEntry(name) || (!s.cfg.hidden && strings.HasPrefix(name, ".")) {
			continue
		}
		entryDir := de.IsDir()
		if de.Type()&fs.ModeSymlink != 0 {
			if inf, ierr := de.Info(); ierr == nil {
				entryDir = inf.IsDir()
			}
		}
		e := publicEntry{
			Name:  name,
			IsDir: entryDir,
			Ext:   strings.ToLower(strings.TrimPrefix(filepath.Ext(name), ".")),
		}
		if entryDir {
			e.Kind = "dir"
		} else {
			e.Kind = fileKind(name, false)
		}
		if inf, ierr := de.Info(); ierr == nil {
			e.Size = inf.Size()
			e.MTime = inf.ModTime().Unix()
		}
		entries = append(entries, e)
	}
	sort.Slice(entries, func(i, j int) bool {
		if entries[i].IsDir != entries[j].IsDir {
			return entries[i].IsDir
		}
		return strings.Compare(strings.ToLower(entries[i].Name), strings.ToLower(entries[j].Name)) < 0
	})
	limit := parseIntSafe(r.URL.Query().Get("limit"), listMaxLimit, listMaxLimit)
	offset := parseIntSafe(r.URL.Query().Get("offset"), 0, 1<<30)
	total := len(entries)
	page := slicePublicPage(entries, offset, limit)
	writeJSON(w, http.StatusOK, map[string]any{
		"path":      shareRel(root, dir),
		"entries":   page,
		"total":     total,
		"truncated": offset+len(page) < total,
	})
}

// handlePublicShareDownload GET /api/public/share/{token}/download?path=
// 每次下载先占额度再出字节；额度用尽/过期/撤销在写响应头前返回明确状态。
func (s *Server) handlePublicShareDownload(w http.ResponseWriter, r *http.Request) {
	setSharePublicHeaders(w)
	token := r.PathValue("token")
	_, root, isDir, ok := s.openPublicShare(w, r, token, true)
	if !ok {
		return
	}
	target, ok := s.resolveShareTarget(w, root, isDir, r.URL.Query().Get("path"))
	if !ok {
		return
	}
	fi, err := os.Stat(target)
	if err != nil || fi.IsDir() {
		writeShareStatusError(w, http.StatusNotFound, "not_found", "文件不存在")
		return
	}
	f, err := os.Open(target)
	if err != nil {
		writeShareStatusError(w, http.StatusInternalServerError, "open_failed", "打开文件失败")
		return
	}
	defer func() { _ = f.Close() }()

	if _, err := s.shares.consumeDownload(token, time.Now()); err != nil {
		switch {
		case errors.Is(err, errShareExpired):
			writeShareStatusError(w, http.StatusForbidden, shareStatusExpired, "分享已过期")
		case errors.Is(err, errShareRevoked):
			writeShareStatusError(w, http.StatusForbidden, shareStatusRevoked, "分享已撤销")
		case errors.Is(err, errShareExhausted):
			writeShareStatusError(w, http.StatusForbidden, shareStatusExhausted, "分享的下载次数已用完")
		case errors.Is(err, errShareNotFound):
			writeShareStatusError(w, http.StatusNotFound, "not_found", "分享链接无效或已被删除")
		default:
			writeShareStatusError(w, http.StatusInternalServerError, "download_failed", "下载失败")
		}
		return
	}

	name := fi.Name()
	kind := fileKind(name, false)
	if (kind == "video" || kind == "audio") && r.Header.Get("Range") != "" {
		s.markVideoRead()
	}
	setShareDownloadHeaders(w, name)
	w.Header().Set("Content-Type", fileMimeType(name, f))
	w.Header().Set("Accept-Ranges", "bytes")
	http.ServeContent(w, r, name, fi.ModTime(), f)
}

// setShareDownloadHeaders 下载必须带 attachment；文件名走 mime 编码，剔除控制字符。
func setShareDownloadHeaders(w http.ResponseWriter, name string) {
	if cd := mime.FormatMediaType("attachment", map[string]string{"filename": sanitizeFilename(name)}); cd != "" {
		w.Header().Set("Content-Disposition", cd)
	}
}

// handlePublicSharePreview GET /api/public/share/{token}/preview?path=
// 仅允许图片/视频/音频/PDF/文本/代码；文本与可脚本扩展名统一按 text/plain 输出，
// 防止在服务源内以 HTML/SVG 渲染执行。
func (s *Server) handlePublicSharePreview(w http.ResponseWriter, r *http.Request) {
	setSharePublicHeaders(w)
	token := r.PathValue("token")
	rec, root, isDir, ok := s.openPublicShare(w, r, token, true)
	if !ok {
		return
	}
	if !rec.AllowPreview {
		writeShareStatusError(w, http.StatusForbidden, "preview_disabled", "该分享未开启预览")
		return
	}
	target, ok := s.resolveShareTarget(w, root, isDir, r.URL.Query().Get("path"))
	if !ok {
		return
	}
	fi, err := os.Stat(target)
	if err != nil || fi.IsDir() {
		writeShareStatusError(w, http.StatusNotFound, "not_found", "文件不存在")
		return
	}
	name := fi.Name()
	kind := fileKind(name, false)
	if kind != "image" && kind != "video" && kind != "audio" && kind != "pdf" && kind != "text" && kind != "code" {
		writeShareStatusError(w, http.StatusForbidden, "preview_unsupported", "该类型不支持预览")
		return
	}
	f, err := os.Open(target)
	if err != nil {
		writeShareStatusError(w, http.StatusInternalServerError, "open_failed", "打开文件失败")
		return
	}
	defer func() { _ = f.Close() }()

	mimeType := fileMimeType(name, f)
	disposition := "inline"
	if kind == "text" || kind == "code" || scriptableExt[strings.ToLower(filepath.Ext(name))] {
		mimeType = "text/plain; charset=utf-8"
	}
	if cd := mime.FormatMediaType(disposition, map[string]string{"filename": sanitizeFilename(name)}); cd != "" {
		w.Header().Set("Content-Disposition", cd)
	}
	w.Header().Set("Content-Type", mimeType)
	w.Header().Set("Accept-Ranges", "bytes")
	http.ServeContent(w, r, name, fi.ModTime(), f)
}

// handlePublicShareZip GET /api/public/share/{token}/zip?path=
// 目录打包：一次下载只占一个额度；zip 内结构为 <目录名>/<相对路径>。
func (s *Server) handlePublicShareZip(w http.ResponseWriter, r *http.Request) {
	setSharePublicHeaders(w)
	token := r.PathValue("token")
	_, root, isDir, ok := s.openPublicShare(w, r, token, true)
	if !ok {
		return
	}
	if !isDir {
		writeShareStatusError(w, http.StatusBadRequest, "not_a_dir", "该分享不是目录")
		return
	}
	dir, ok := s.resolveShareTarget(w, root, true, r.URL.Query().Get("path"))
	if !ok {
		return
	}
	fi, err := os.Stat(dir)
	if err != nil || !fi.IsDir() {
		writeShareStatusError(w, http.StatusNotFound, "not_found", "路径不存在")
		return
	}
	if _, err := s.shares.consumeDownload(token, time.Now()); err != nil {
		switch {
		case errors.Is(err, errShareExpired):
			writeShareStatusError(w, http.StatusForbidden, shareStatusExpired, "分享已过期")
		case errors.Is(err, errShareRevoked):
			writeShareStatusError(w, http.StatusForbidden, shareStatusRevoked, "分享已撤销")
		case errors.Is(err, errShareExhausted):
			writeShareStatusError(w, http.StatusForbidden, shareStatusExhausted, "分享的下载次数已用完")
		case errors.Is(err, errShareNotFound):
			writeShareStatusError(w, http.StatusNotFound, "not_found", "分享链接无效或已被删除")
		default:
			writeShareStatusError(w, http.StatusInternalServerError, "download_failed", "下载失败")
		}
		return
	}
	top := filepath.Base(dir)
	if cd := mime.FormatMediaType("attachment", map[string]string{"filename": sanitizeFilename(top) + ".zip"}); cd != "" {
		w.Header().Set("Content-Disposition", cd)
	}
	w.Header().Set("Content-Type", "application/zip")
	w.Header().Set("X-Accel-Buffering", "no")

	zw := zip.NewWriter(w)
	defer func() { _ = zw.Close() }()
	_ = filepath.WalkDir(dir, func(p string, d fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if p == dir {
			return nil
		}
		if isCacheEntry(d.Name()) || (!s.cfg.hidden && strings.HasPrefix(d.Name(), ".")) {
			if d.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}
		rel, rerr := filepath.Rel(dir, p)
		if rerr != nil {
			return rerr
		}
		zipName := top + "/" + filepath.ToSlash(rel)
		if d.IsDir() {
			_, cerr := zw.Create(zipName + "/")
			return cerr
		}
		if d.Type()&fs.ModeSymlink != 0 {
			return nil
		}
		info, ierr := d.Info()
		if ierr != nil {
			return ierr
		}
		hdr := &zip.FileHeader{Name: zipName, Method: zip.Store}
		hdr.SetMode(info.Mode())
		hdr.Modified = info.ModTime()
		fw, cerr := zw.CreateHeader(hdr)
		if cerr != nil {
			return cerr
		}
		f, oerr := os.Open(p)
		if oerr != nil {
			return oerr
		}
		defer func() { _ = f.Close() }()
		_, cerr = io.Copy(fw, f)
		return cerr
	})
}
