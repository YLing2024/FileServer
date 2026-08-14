package server

import (
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

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
	Path    string  `json:"path"`
	Entries []Entry `json:"entries"`
}

// fileKind 按扩展名对文件分类，前端据此选择图标与缩略图策略
func fileKind(name string, isDir bool) string {
	if isDir {
		return "dir"
	}
	switch strings.ToLower(filepath.Ext(name)) {
	case ".jpg", ".jpeg", ".png", ".gif", ".webp", ".bmp", ".tiff", ".tif", ".svg", ".ico", ".avif", ".jfif":
		return "image"
	case ".mp4", ".mkv", ".mov", ".webm", ".avi", ".wmv", ".flv", ".m4v", ".m2ts", ".3gp", ".rmvb", ".rm", ".mpg", ".mpeg", ".ogv":
		return "video"
	case ".mp3", ".wav", ".flac", ".aac", ".ogg", ".m4a", ".wma", ".opus", ".mid", ".midi", ".ape", ".amr":
		return "audio"
	case ".pdf":
		return "pdf"
	case ".zip", ".rar", ".7z", ".tar", ".gz", ".bz2", ".xz", ".iso", ".zst":
		return "archive"
	case ".txt", ".md", ".log", ".json", ".xml", ".yaml", ".yml", ".ini", ".conf", ".cfg", ".csv", ".toml", ".srt", ".ass", ".vtt", ".nfo", ".rtf", ".url":
		return "text"
	case ".doc", ".docx", ".xls", ".xlsx", ".ppt", ".pptx", ".odt", ".ods", ".odp", ".wps":
		return "doc"
	case ".c", ".cpp", ".h", ".hpp", ".go", ".rs", ".py", ".js", ".mjs", ".ts", ".tsx", ".jsx", ".html", ".htm", ".css", ".scss", ".java", ".kt", ".swift", ".sh", ".bat", ".cmd", ".ps1", ".sql", ".php", ".rb", ".lua", ".pl", ".vue", ".svelte", ".dockerfile", ".gradle", ".properties":
		return "code"
	default:
		return "other"
	}
}

// handleList GET /api/list?path=&sort=&order=
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
		isDir := de.IsDir() // Lstat 语义：符号链接不视为目录，避免跟随循环
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

	rel := relOf(s.root, dir)
	writeJSON(w, http.StatusOK, ListResp{Path: rel, Entries: entries})
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

var _ = time.Now // 保留 time 导入（后续扩展用）
