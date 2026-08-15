package server

import (
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"
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
}

// ListResp 目录列表响应
type ListResp struct {
	Path      string  `json:"path"`
	Entries   []Entry `json:"entries"`
	Total     int     `json:"total,omitempty"`
	Truncated bool    `json:"truncated,omitempty"`
}

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
func (s *Server) handleList(w http.ResponseWriter, r *http.Request) {
	dir, err := s.safePath(r.URL.Query().Get("path"))
	if err != nil {
		writeErr(w, errToStatus(err), err.Error())
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

	des, err := os.ReadDir(dir)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "读取目录失败")
		return
	}

	entries := make([]Entry, 0, len(des))
	for _, de := range des {
		name := de.Name()
		if !s.hidden && strings.HasPrefix(name, ".") {
			continue
		}
		// stat 语义的 IsDir：指向目录的符号链接按目录显示/操作（前端可正常进入），
		// 不会循环跟随——列表只展示名字，点击后再由 safePath 解析校验
		isDir := de.IsDir()
		if info, ierr := de.Info(); ierr == nil {
			isDir = info.IsDir()
		}
		e := Entry{Name: name, IsDir: isDir, Ext: strings.ToLower(strings.TrimPrefix(filepath.Ext(name), "."))}
		if !isDir {
			e.Kind = fileKind(name, false)
		} else {
			e.Kind = "dir"
		}
		if info, ierr := de.Info(); ierr == nil {
			e.Size = info.Size()
			e.ModTime = info.ModTime().Unix()
		}
		entries = append(entries, e)
	}

	sortEntries(entries, r.URL.Query().Get("sort"), r.URL.Query().Get("order"))

	// 服务端分页：避免大目录一次性全量传输（前端按 offset/limit 逐页加载）
	total := len(entries)
	limit := parseIntSafe(r.URL.Query().Get("limit"), 0, listMaxLimit)
	offset := parseIntSafe(r.URL.Query().Get("offset"), 0, 1<<30)
	if offset > total {
		offset = total
	}
	if limit > 0 {
		end := offset + limit
		if end > total {
			end = total
		}
		entries = entries[offset:end]
	} else if offset > 0 {
		entries = entries[offset:]
	}

	rel := relOf(s.root, dir)
	writeJSON(w, http.StatusOK, ListResp{
		Path:      rel,
		Entries:   entries,
		Total:     total,
		Truncated: offset+len(entries) < total,
	})
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
