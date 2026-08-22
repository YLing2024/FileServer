package server

import (
	"encoding/json"
	"fmt"
	"io/fs"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// listMaxLimit 单次目录列表请求的条数上限（服务端分页保护）
const listMaxLimit = 2000

// Entry 目录列表中的单个条目
type Entry struct {
	Name    string `json:"name"`
	IsDir   bool   `json:"is_dir"`
	Size    int64  `json:"size"`
	ModTime int64  `json:"mtime"` // Unix 秒
	Ext     string `json:"ext"`
	Kind    string `json:"kind"`

	de os.DirEntry // 懒加载 size/mtime 用（不序列化）
}

// ListResp 目录列表响应
type ListResp struct {
	Path      string  `json:"path"`
	Entries   []Entry `json:"entries"`
	Total     int     `json:"total,omitempty"`
	Truncated bool    `json:"truncated,omitempty"`
}

// listCacheEntry 目录列表服务端缓存（按 目录路径|排序 键控）
type listCacheEntry struct {
	dirMod  time.Time // 目录修改时间（变化即失效）
	fetched time.Time
	entries []Entry // 已过滤隐藏、已排序（懒 stat：仅分页到的条目填 Size/ModTime）
}

const (
	listCacheTTL = 3 * time.Second // 短 TTL：返回/加载更多秒开，且 3 秒内目录变化即反映
	listCacheMax = 64              // 缓存目录数上限
)

// kindExts 扩展名→类型映射的唯一来源：fileKind 与 /api/info 下发的 kinds 都出自此表，
// 避免后端 fileKind / mediaMime 与前端 KIND_EXT_MAP 三处漂移。
var kindExts = map[string][]string{
	"image":   {".jpg", ".jpeg", ".png", ".gif", ".webp", ".bmp", ".tiff", ".tif", ".svg", ".ico", ".avif", ".jfif"},
	"video":   {".mp4", ".mkv", ".mov", ".webm", ".avi", ".wmv", ".flv", ".m4v", ".m2ts", ".3gp", ".rmvb", ".rm", ".mpg", ".mpeg", ".ogv"},
	"audio":   {".mp3", ".wav", ".flac", ".aac", ".ogg", ".m4a", ".wma", ".opus", ".mid", ".midi", ".ape", ".amr"},
	"pdf":     {".pdf"},
	"archive": {".zip", ".rar", ".7z", ".tar", ".gz", ".bz2", ".xz", ".iso", ".zst"},
	"text":    {".txt", ".md", ".log", ".json", ".xml", ".yaml", ".yml", ".ini", ".conf", ".cfg", ".csv", ".toml", ".srt", ".ass", ".vtt", ".nfo", ".rtf", ".url"},
	"doc":     {".doc", ".docx", ".xls", ".xlsx", ".ppt", ".pptx", ".odt", ".ods", ".odp", ".wps"},
	"code":    {".c", ".cpp", ".h", ".hpp", ".go", ".rs", ".py", ".js", ".mjs", ".ts", ".tsx", ".jsx", ".html", ".htm", ".css", ".scss", ".java", ".kt", ".swift", ".sh", ".bat", ".cmd", ".ps1", ".sql", ".php", ".rb", ".lua", ".pl", ".vue", ".svelte", ".dockerfile", ".gradle", ".properties"},
}

// fileKind 按扩展名对文件分类，前端据此选择图标与缩略图策略
func fileKind(name string, isDir bool) string {
	if isDir {
		return "dir"
	}
	ext := strings.ToLower(filepath.Ext(name))
	for kind, exts := range kindExts {
		for _, e := range exts {
			if ext == e {
				return kind
			}
		}
	}
	return "other"
}

// kindExtMap 返回 ext→kind 全量映射，供 /api/info 下发，前端不再各自维护
func kindExtMap() map[string]string {
	m := make(map[string]string)
	for kind, exts := range kindExts {
		for _, e := range exts {
			m[e] = kind
		}
	}
	return m
}

// handleList GET /api/list?path=&sort=&order=&limit=&offset= 目录列表
//
// 性能要点（慢磁盘/网络盘上的大目录尤其关键）：
// 1) 默认 name 排序不 stat 全量条目——先按名称排序，只对当前页条目懒取
//    size/mtime（2000 文件从 2000 次 stat 降到 300 次）；
// 2) 结果短缓存 3 秒：返回上级目录、加载更多分页均秒开。
func (s *Server) handleList(w http.ResponseWriter, r *http.Request) {
	dir, err := s.safePath(r.URL.Query().Get("path"))
	if err != nil {
		writeErr(w, errToStatus(err), err.Error())
		return
	}
	// 隐藏路径（--hidden 未开启）与保留缓存目录（.FileServer，任何情况）
	// 与直链/zip 语义一致地拒绝列出
	if s.hiddenBlocked(dir) {
		writeErr(w, http.StatusNotFound, "路径不存在")
		return
	}
	fi, err := os.Stat(dir)
	if err != nil {
		writeErr(w, errToStatus(err), "无法访问该路径")
		return
	}
	if !fi.IsDir() {
		writeErr(w, http.StatusBadRequest, "该路径不是目录")
		return
	}

	sortKey := r.URL.Query().Get("sort")
	order := r.URL.Query().Get("order")
	limit := parseIntSafe(r.URL.Query().Get("limit"), 0, listMaxLimit)
	offset := parseIntSafe(r.URL.Query().Get("offset"), 0, 1<<30)

	rel := relOf(s.root, dir)
	cacheKey := fmt.Sprintf("%s|%s|%s", rel, sortKey, order)

	// 命中缓存（目录未变且 TTL 内）：直接分页返回
	if ce := s.listCacheGet(cacheKey); ce != nil && ce.dirMod.Equal(fi.ModTime()) &&
		time.Since(ce.fetched) < listCacheTTL {
		total := len(ce.entries)
		// 复制本页条目再懒填充（缓存切片可能被并发请求共享，不能原地改）
		page := append([]Entry(nil), slicePage(ce.entries, offset, limit)...)
		s.fillStats(page)
		writeJSON(w, http.StatusOK, ListResp{
			Path:      rel,
			Entries:   page,
			Total:     total,
			Truncated: offset+len(page) < total,
		})
		return
	}

	des, err := os.ReadDir(dir)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "读取目录失败")
		return
	}

	entries := make([]Entry, 0, len(des))
	for _, de := range des {
		name := de.Name()
		// 保留缓存目录（.FileServer）任何情况下不出现；其余点开头条目仅 --hidden 时可见
		if isCacheEntry(name) || (!s.hidden && strings.HasPrefix(name, ".")) {
			continue
		}
		// stat 语义的 IsDir：指向目录的符号链接按目录显示/操作（前端可正常进入），
		// 不会循环跟随——列表只展示名字，点击后再由 safePath 解析校验。
		// 常规条目用 de.IsDir()（ReadDir 已带类型，零系统调用）；仅符号链接需要 Info 解析。
		isDir := de.IsDir()
		if de.Type()&fs.ModeSymlink != 0 {
			if info, ierr := de.Info(); ierr == nil {
				isDir = info.IsDir()
			}
		}
		e := Entry{Name: name, IsDir: isDir, Ext: strings.ToLower(strings.TrimPrefix(filepath.Ext(name), ".")), de: de}
		if isDir {
			e.Kind = "dir"
		} else {
			e.Kind = fileKind(name, false)
		}
		entries = append(entries, e)
	}

	// size/time 排序需要全量 stat（懒 stat 只适用于 name 排序）
	if sortKey == "size" || sortKey == "time" {
		s.fillStats(entries)
	}
	sortEntries(entries, sortKey, order)

	// 缓存排序结果（3 秒内返回/翻页复用）
	s.listCachePut(cacheKey, fi.ModTime(), entries)

	// 服务端分页：避免大目录一次性全量传输（前端按 offset/limit 逐页加载）
	total := len(entries)
	page := slicePage(entries, offset, limit)
	s.fillStats(page)
	writeJSON(w, http.StatusOK, ListResp{
		Path:      rel,
		Entries:   page,
		Total:     total,
		Truncated: offset+len(page) < total,
	})
}

// fillStats 为条目懒填充 Size/ModTime（只对传入的切片，避免全目录 stat）
func (s *Server) fillStats(entries []Entry) {
	for i := range entries {
		if entries[i].de == nil {
			continue // 已填充过（缓存复用）
		}
		if info, err := entries[i].de.Info(); err == nil {
			entries[i].Size = info.Size()
			entries[i].ModTime = info.ModTime().Unix()
		}
		entries[i].de = nil
	}
}

// slicePage 按 offset/limit 切片
func slicePage(entries []Entry, offset, limit int) []Entry {
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

// listCacheGet 读列表缓存
func (s *Server) listCacheGet(key string) *listCacheEntry {
	s.listMu.Lock()
	defer s.listMu.Unlock()
	return s.listCache[key]
}

// listCachePut 写列表缓存（超限整体清空重建，防无界增长）
func (s *Server) listCachePut(key string, dirMod time.Time, entries []Entry) {
	s.listMu.Lock()
	defer s.listMu.Unlock()
	if s.listCache == nil {
		s.listCache = make(map[string]*listCacheEntry)
	}
	if len(s.listCache) >= listCacheMax {
		s.listCache = make(map[string]*listCacheEntry, listCacheMax)
	}
	// 深拷贝一份避免共享底层数组被后续 fillStats 修改
	cp := append([]Entry(nil), entries...)
	s.listCache[key] = &listCacheEntry{dirMod: dirMod, fetched: time.Now(), entries: cp}
}

// relOf 返回 root 下的相对路径（正斜杠形式，根为 "/"）
func relOf(root, p string) string {
	rel, err := filepath.Rel(root, p)
	if err != nil || rel == "." {
		return "/"
	}
	return "/" + filepath.ToSlash(rel)
}

func sortEntries(entries []Entry, sortKey, order string) {
	desc := strings.EqualFold(order, "desc")
	// 目录优先级固定在最前，不随升降序反转
	sort.Slice(entries, func(i, j int) bool {
		a, b := entries[i], entries[j]
		if a.IsDir != b.IsDir {
			return a.IsDir
		}
		cmp := func() int {
			switch sortKey {
			case "size":
				if a.Size != b.Size {
					if a.Size < b.Size {
						return -1
					}
					return 1
				}
			case "time":
				if a.ModTime != b.ModTime {
					if a.ModTime < b.ModTime {
						return -1
					}
					return 1
				}
			}
			return strings.Compare(strings.ToLower(a.Name), strings.ToLower(b.Name))
		}()
		if desc {
			return cmp > 0
		}
		return cmp < 0
	})
}

// --- JSON 辅助 ---

func writeJSON(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(code)
	json.NewEncoder(w).Encode(v)
}

func writeErr(w http.ResponseWriter, code int, msg string) {
	writeJSON(w, code, map[string]string{"error": msg})
}

func errToStatus(err error) int {
	if err == errForbidden {
		return http.StatusForbidden
	}
	if err == errNotFound {
		return http.StatusNotFound
	}
	return http.StatusInternalServerError
}
