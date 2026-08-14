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

	limit := 500
	if v := r.URL.Query().Get("limit"); v != "" {
		if n := atoiSafe(v); n > 0 {
			limit = n
		}
	}
	if limit > searchMaxLimit {
		limit = searchMaxLimit
	}

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
		if strings.Contains(strings.ToLower(d.Name()), needle) {
			e := SearchResult{
				Path:  relOf(s.root, p),
				Name:  d.Name(),
				IsDir: d.IsDir(),
				Kind:  fileKind(d.Name(), d.IsDir()),
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

func atoiSafe(v string) int {
	n := 0
	for _, ch := range v {
		if ch < '0' || ch > '9' {
			return 0
		}
		n = n*10 + int(ch-'0')
		if n > 1<<30 {
			return 0
		}
	}
	return n
}
