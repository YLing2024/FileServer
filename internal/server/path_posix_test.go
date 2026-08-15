//go:build !windows

package server

import (
	"os"
	"path/filepath"
	"testing"
)

// TestPathWithinPOSIX 验证 POSIX 大小写敏感语义（1.1）：
// 根 /srv 下若有指向真实外部目录 /SRV 的符号链接，必须被拒绝。
// 该用例只在 Linux/macOS CI 上执行。
func TestPathWithinPOSIX(t *testing.T) {
	cases := []struct {
		root, p string
		want    bool
	}{
		{"/srv", "/srv", true},
		{"/srv", "/srv/a.txt", true},
		{"/srv", "/srv/sub/x", true},
		{"/srv", "/SRV/secret", false},      // 大小写变体 = 外部目录
		{"/srv", "/srv2/x", false},          // 前缀兄弟目录
		{"/srv", "/srvatic/x", false},       // 无分隔符边界
		{"/srv", "/srv/sub/../../etc", false},
	}
	for _, c := range cases {
		if got := pathWithin(c.root, c.p); got != c.want {
			t.Errorf("pathWithin(%q, %q) = %v, want %v", c.root, c.p, got, c.want)
		}
	}
}

// TestSafePathPosixCaseSymlink 端到端：根内符号链接指向根目录的大小写变体，
// safePath 必须拒绝（不能读取 /SRV/secret）
func TestSafePathPosixCaseSymlink(t *testing.T) {
	base := t.TempDir()
	root := filepath.Join(base, "srv")
	os.MkdirAll(root, 0o755)
	// 外部真实目录：仅是根目录的大写变体
	outside := filepath.Join(base, "SRV")
	os.MkdirAll(outside, 0o755)
	os.WriteFile(filepath.Join(outside, "secret.txt"), []byte("secret"), 0o644)
	// 根内符号链接指向外部目录
	link := filepath.Join(root, "link")
	if err := os.Symlink(outside, link); err != nil {
		t.Skipf("无法创建符号链接: %v", err)
	}

	srv := New(root, Options{})
	if abs, err := srv.safePath("link/secret.txt"); err == nil {
		t.Fatalf("大写变体符号链接逃逸未被拦截: %q", abs)
	}
	// 根内正常文件仍可访问
	os.WriteFile(filepath.Join(root, "ok.txt"), []byte("ok"), 0o644)
	if _, err := srv.safePath("ok.txt"); err != nil {
		t.Fatalf("正常路径应可访问: %v", err)
	}
}
