package server

import (
	"os"
	"path/filepath"
	"runtime"
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
	// 拒绝盘符（C:、C:\...、UNC、ADS）——仅 Windows 有此概念；
	// POSIX 上 : 是合法文件名字符（如 report:2024.txt），须放行。
	if runtime.GOOS == "windows" && strings.Contains(rel, ":") {
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
	// 残余 TOCTOU：返回 real 后、handler 打开前，若本地攻击者（需根目录内写权限）
	// 恰好把 real 路径本身替换为符号链接，open 时仍可能跟随逃逸。实际风险极低，
	// 如需彻底加固需 os.Open + O_NOFOLLOW 并在打开后 fstat 校验。
	return real, nil
}

// pathWithin 判断 p 是否位于 root 内。
// 平台差异：Windows/NTFS 大小写不敏感，用 EqualFold 兼容用户输入大小写差异；
// POSIX 大小写敏感，必须严格按字符比较——否则根目录 /srv 内若存在指向 /SRV
// （仅大小写不同的真实外部目录）的符号链接，EvalSymlinks 解析出的 /SRV/secret
// 会被大小写不敏感比较误判为根内，造成符号链接逃逸任意读取根外文件。
func pathWithin(root, p string) bool {
	root = filepath.Clean(root)
	p = filepath.Clean(p)
	if runtime.GOOS == "windows" {
		if strings.EqualFold(root, p) {
			return true
		}
		prefix := strings.TrimRight(root, `/\`) + string(filepath.Separator)
		pl := len(prefix)
		return len(p) > pl && strings.EqualFold(p[:pl], prefix)
	}
	// POSIX：严格大小写敏感的前缀比较（含分隔符边界）
	if root == p {
		return true
	}
	prefix := strings.TrimRight(root, "/") + "/"
	return strings.HasPrefix(p, prefix)
}
