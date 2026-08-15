package server

import (
	"io/fs"
	"net/http"
	"os"
	"path/filepath"
	"strings"
)

const (
	searchMaxDepth = 10
	searchMaxLimit = 2000
)

// SearchResult 搜索结果条目
type SearchResult struct {
	Path    string `json:"path"`
	Name    string `json:"name"`
	IsDir   bool   `json:"is_dir"`
	Size    int64  `json:"size"`
	ModTime int64  `json:"mtime"`
	Kind    string `json:"kind"`
}

// handleSearch GET /api/search?q=&path=&limit= 递归搜索
func (s *Server) handleSearch(w http.ResponseWriter, r *http.Request) {
	q := strings.TrimSpace(r.URL.Query().Get("q"))
	if q == "" {
		writeErr(w, http.StatusBadRequest, "缺少搜索关键字")
		return
	}
	root, err := s.safePath(r.URL.Query().Get("path"))
	if err != nil {
		writeErr(w, errToStatus(err), err.Error())
		return
	}
	fi, err := os.Stat(root)
	if err != nil || !fi.IsDir() {
		writeErr(w, http.StatusBadRequest, "搜索起点不是目录")
		return
	}

	limit := parseIntSafe(r.URL.Query().Get("limit"), 500, searchMaxLimit)

	needle := strings.ToLower(q)
	results := make([]SearchResult, 0, 32)

	_ = filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return fs.SkipDir // 无权限子目录跳过
		}
		if p == root {
			return nil
		}
		// 深度限制
		rel, _ := filepath.Rel(root, p)
		if depth := strings.Count(filepath.ToSlash(rel), "/"); depth > searchMaxDepth {
			return fs.SkipDir
		}
		if !s.hidden && strings.HasPrefix(d.Name(), ".") {
			if d.IsDir() {
				return fs.SkipDir
			}
			return nil
		}
		if len(results) >= limit {
			return fs.SkipAll
		}
		// stat 语义 IsDir：指向目录的符号链接按目录展示（与列表一致）
		isDir := d.IsDir()
		if info, ierr := d.Info(); ierr == nil {
			isDir = info.IsDir()
		}
		if containsFold(d.Name(), needle) {
			e := SearchResult{
				Path:  relOf(s.root, p),
				Name:  d.Name(),
				IsDir: isDir,
				Kind:  fileKind(d.Name(), isDir),
			}
			if info, ierr := d.Info(); ierr == nil {
				e.Size = info.Size()
				e.ModTime = info.ModTime().Unix()
			}
			results = append(results, e)
		}
		return nil
	})

	writeJSON(w, http.StatusOK, map[string]any{"query": q, "results": results, "truncated": len(results) >= limit})
}

// parseIntSafe 安全解析非负整数：非纯数字/空返回 def；超过 max 返回 max。
// 统一了原 clampDim（thumb）与 atoiSafe（search）的重复实现。
func parseIntSafe(s string, def, max int) int {
	n := 0
	for _, ch := range s {
		if ch < '0' || ch > '9' {
			return def
		}
		n = n*10 + int(ch-'0')
		if n > 1<<30 {
			return def
		}
	}
	if n <= 0 {
		return def
	}
	if n > max {
		return max
	}
	return n
}

// containsFold 不区分大小写的包含匹配。
// ASCII 快路径逐字节折叠（零分配，避免大目录搜索时对每个文件名整串 ToLower）；
// 含非 ASCII 时回退到 Unicode ToLower 以保持与原实现一致的语义。
func containsFold(s, sub string) bool {
	if sub == "" {
		return true
	}
	if isASCIIString(s) && isASCIIString(sub) {
		for i := 0; i+len(sub) <= len(s); i++ {
			matched := true
			for j := 0; j < len(sub); j++ {
				if foldASCII(s[i+j]) != foldASCII(sub[j]) {
					matched = false
					break
				}
			}
			if matched {
				return true
			}
		}
		return false
	}
	return strings.Contains(strings.ToLower(s), sub)
}

func isASCIIString(s string) bool {
	for i := 0; i < len(s); i++ {
		if s[i] >= 0x80 {
			return false
		}
	}
	return true
}

func foldASCII(b byte) byte {
	if 'A' <= b && b <= 'Z' {
		return b + 32
	}
	return b
}
