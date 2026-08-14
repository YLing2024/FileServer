package server

import (
	"os"
	"path/filepath"
	"strings"
)

// safePath 将客户端提交的相对路径安全解析为根目录内绝对路径。
// 防御：URL 解码后的 ../、绝对路径、盘符、空字节、大小写绕过、符号链接逃逸。
func (s *Server) safePath(rel string) (string, error) {
	if rel == "" {
		rel = "/"
	}
	if strings.ContainsRune(rel, 0) {
		return "", errForbidden
	}
	// 拒绝盘符（C:、C:\...、UNC）
	if strings.Contains(rel, ":") {
		return "", errForbidden
	}
	// 允许以 / 或 \ 开头（表示根下），统一去掉
	rel = strings.TrimLeft(rel, "/\\")
	clean := filepath.Clean(filepath.FromSlash(rel))
	if clean == "." {
		clean = ""
	}
	// 词法层：拒绝任何形式的上级逃逸
	if clean == ".." || strings.HasPrefix(clean, ".."+string(filepath.Separator)) {
		return "", errForbidden
	}
	abs := filepath.Join(s.root, clean)
	if !pathWithin(s.root, abs) {
		return "", errForbidden
	}
	// 真实路径层：跟随符号链接后必须仍在根目录内
	real, err := filepath.EvalSymlinks(abs)
	if err != nil {
		if os.IsNotExist(err) {
			return "", errNotFound
		}
		return "", errForbidden
	}
	if !pathWithin(s.root, real) {
		return "", errForbidden
	}
	return real, nil
}

// pathWithin 判断 p 是否位于 root 内（词法 + 大小写不敏感，兼容 Windows）
func pathWithin(root, p string) bool {
	root = filepath.Clean(root)
	p = filepath.Clean(p)
	if strings.EqualFold(root, p) {
		return true
	}
	prefix := strings.TrimRight(root, `/\`) + string(filepath.Separator)
	pl := len(prefix)
	return len(p) > pl && strings.EqualFold(p[:pl], prefix)
}
