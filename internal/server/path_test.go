package server

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

func newTestServer(t *testing.T) (*Server, string) {
	t.Helper()
	root := t.TempDir()
	// 构造测试目录结构
	mk := func(p string) {
		t.Helper()
		full := filepath.Join(root, p)
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(full, []byte("hello "+p), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	mk("a.txt")
	mk("dir1/b.txt")
	mk("dir1/sub/c.txt")
	mk("中文 文件.txt")
	return New(root, Options{}), root
}

func TestSafePath(t *testing.T) {
	srv, _ := newTestServer(t)

	cases := []struct {
		name    string
		rel     string
		ok      bool
		winOnly bool // 仅 Windows 语义成立（反斜杠为路径分隔符）
	}{
		{"空路径=根", "", true, false},
		{"根斜杠", "/", true, false},
		{"直接子文件", "a.txt", true, false},
		{"子目录文件", "dir1/b.txt", true, false},
		{"深层", "dir1/sub/c.txt", true, false},
		{"中文与空格", "中文 文件.txt", true, false},
		{"前导斜杠", "/a.txt", true, false},
		{"反斜杠", `dir1\b.txt`, true, true},
		{"父目录逃逸", "../a.txt", false, false},
		{"双层逃逸", "../../etc/passwd", false, false},
		{"编码层逃逸", "..%2f..%2fa.txt", false, false},
		{"混合逃逸", "dir1/../../a.txt", false, false},
		{"反斜杠逃逸", `..\..\a.txt`, false, false},
		{"子串诱骗", "dir1..a.txt", false, false}, // 不存在
		{"绝对路径", "C:\\windows\\system32", false, false},
		{"盘符相对", "C:foo", false, false},
		{"UNC", `\\server\share\x`, false, false},
		{"空字节", "a\x00b.txt", false, false},
		{"点路径", ".", true, false},
		{"点点路径", "./a.txt", true, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if c.winOnly && runtime.GOOS != "windows" {
				t.Skip("反斜杠仅在 Windows 上是路径分隔符")
			}
			abs, err := srv.safePath(c.rel)
			if c.ok {
				if err != nil {
					t.Fatalf("期望成功, 得到错误: %v", err)
				}
				if !pathWithin(srv.cfg.root, abs) {
					t.Fatalf("结果 %q 不在根目录 %q 内", abs, srv.cfg.root)
				}
			} else if err == nil {
				t.Fatalf("期望被拒绝, 却得到: %q", abs)
			}
		})
	}
}

func TestSafePathSymlink(t *testing.T) {
	root := t.TempDir()
	os.MkdirAll(filepath.Join(root, "real"), 0o755)
	os.WriteFile(filepath.Join(root, "real", "f.txt"), []byte("x"), 0o644)
	outside := t.TempDir()
	os.WriteFile(filepath.Join(outside, "secret.txt"), []byte("secret"), 0o644)

	// Windows 上创建符号链接可能需要权限；失败则跳过
	link := filepath.Join(root, "link")
	if err := os.Symlink(outside, link); err != nil {
		t.Skipf("无法创建符号链接: %v", err)
	}

	srv := New(root, Options{})
	// 通过链接逃逸到外部目录必须被拒绝
	if abs, err := srv.safePath("link/secret.txt"); err == nil {
		t.Fatalf("符号链接逃逸未被拦截: %q", abs)
	}
	// 链接自身指向外部目录，同样拒绝（防止经链接访问外部）
	if abs, err := srv.safePath("link"); err == nil {
		t.Fatalf("指向外部的链接自身应被拒绝: %q", abs)
	}
	// 根目录内正常路径不受影响
	if _, err := srv.safePath("real/f.txt"); err != nil {
		t.Fatalf("正常路径应可访问: %v", err)
	}
}

// TestRootSymlinkResolved 服务根目录本身是符号链接/junction 时不应全站 403（M1）。
// 修复前 New() 保存的是链接词法路径，safePath 里 EvalSymlinks 出的真实路径
// 落在该前缀之外，导致每个请求都被判为越界。
func TestRootSymlinkResolved(t *testing.T) {
	target := t.TempDir()
	if err := os.WriteFile(filepath.Join(target, "hello.txt"), []byte("hi"), 0o644); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(t.TempDir(), "rootlink")
	if err := os.Symlink(target, link); err != nil {
		// Windows 创建目录符号链接需开发者模式/管理员权限，环境不允许时跳过，
		// 不保留一个恒真的假用例。
		t.Skipf("当前环境无法创建目录符号链接（Windows 需开发者模式/管理员）: %v", err)
	}
	resolved, err := filepath.EvalSymlinks(target)
	if err != nil {
		t.Fatal(err)
	}

	srv := New(link, Options{})
	defer srv.Close()
	if srv.cfg.root != resolved {
		t.Errorf("New() 应保存解析后的根: root=%q, want %q", srv.cfg.root, resolved)
	}
	// 根内文件必须可访问（修复前这里返回 errForbidden → 全站 403）
	abs, err := srv.safePath("hello.txt")
	if err != nil {
		t.Fatalf("符号链接根目录下的文件应可访问, 却失败: %v", err)
	}
	if !pathWithin(srv.cfg.root, abs) {
		t.Fatalf("解析结果 %q 不在根 %q 内", abs, srv.cfg.root)
	}
}

func TestPathWithin(t *testing.T) {
	// 该用例使用 Windows 盘符路径，仅 Windows 语义有效
	if runtime.GOOS != "windows" {
		t.Skip("Windows 盘符路径用例仅在 Windows 上有效")
	}
	cases := []struct {
		root, p string
		want    bool
	}{
		{`C:\srv`, `C:\srv`, true},
		{`C:\srv`, `C:\srv\a.txt`, true},
		{`C:\srv`, `C:\srv\a\b`, true},
		{`C:\srv`, `C:\srv2\a`, false},
		{`C:\srv`, `C:\srv\..\srv2`, false},
		{`C:\SRV`, `c:\srv\a.txt`, true}, // 大小写不敏感
		{`C:\srv\`, `C:\srv\x`, true},
		{`C:\srv`, `D:\srv\x`, false},
	}
	for _, c := range cases {
		if got := pathWithin(c.root, c.p); got != c.want {
			t.Errorf("pathWithin(%q, %q) = %v, want %v", c.root, c.p, got, c.want)
		}
	}
}

// TestPathWithinPlatform 按当前平台验证大小写语义：Windows 不敏感、POSIX 敏感
func TestPathWithinPlatform(t *testing.T) {
	if runtime.GOOS == "windows" {
		if !pathWithin(`C:\SRV`, `c:\srv\a.txt`) {
			t.Error("Windows 应大小写不敏感（EqualFold）")
		}
		if pathWithin(`C:\srv`, `C:\srv2\a`) {
			t.Error("Windows 下不同前缀应拒绝")
		}
		return
	}
	// POSIX：大小写变体目录必须视为根外（防符号链接逃逸）
	if pathWithin("/srv", "/SRV/secret") {
		t.Error("POSIX 应大小写敏感：/SRV 不是 /srv 内")
	}
	if !pathWithin("/srv", "/srv/secret.txt") {
		t.Error("POSIX 正常子路径应通过")
	}
	if !pathWithin("/srv", "/srv/sub/x") {
		t.Error("POSIX 深层路径应通过")
	}
	if pathWithin("/srv", "/srv2/x") {
		t.Error("POSIX 前缀/兄弟目录应拒绝")
	}
	if pathWithin("/srv", "/srvatic/x") {
		t.Error("POSIX 前缀子串（无分隔符边界）应拒绝")
	}
}

// TestSafePathPosixColon POSIX 上 : 为合法文件名字符（2.5）
func TestSafePathPosixColon(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Windows 文件系统禁止文件名含 :，无需此用例")
	}
	srv, root := newTestServer(t)
	p := filepath.Join(root, "report:2024.txt")
	if err := os.WriteFile(p, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := srv.safePath("report:2024.txt"); err != nil {
		t.Errorf("POSIX 含 : 文件名应可访问, 得到 %v", err)
	}
}

func TestFileKind(t *testing.T) {
	cases := map[string]string{
		"a.jpg": "image", "b.PNG": "image", "c.webp": "image", "d.svg": "image",
		"v.mp4": "video", "v.MKV": "video", "v.webm": "video",
		"s.mp3": "audio", "s.flac": "audio",
		"d.pdf": "pdf",
		"z.zip": "archive", "z.rar": "archive",
		"t.txt": "text", "t.md": "text", "t.json": "text",
		"code.go": "code", "code.py": "code",
		"x.xyz": "other", "noext": "other",
	}
	for name, want := range cases {
		if got := fileKind(name, false); got != want {
			t.Errorf("fileKind(%q) = %q, want %q", name, got, want)
		}
	}
	if fileKind("dir", true) != "dir" {
		t.Error("目录应归类为 dir")
	}
}

func TestSortEntries(t *testing.T) {
	entries := []Entry{
		{Name: "b.txt", IsDir: false, Size: 10, ModTime: 100},
		{Name: "a.txt", IsDir: false, Size: 5, ModTime: 200},
		{Name: "dir", IsDir: true},
		{Name: "C.txt", IsDir: false, Size: 5, ModTime: 300},
	}
	sortEntries(entries, "name", "asc")
	if entries[0].Name != "dir" {
		t.Fatalf("目录应排最前: %+v", entries)
	}
	if entries[1].Name != "a.txt" || entries[2].Name != "b.txt" || entries[3].Name != "C.txt" {
		t.Fatalf("名称排序错误（应大小写不敏感）: %+v", entries)
	}
	sortEntries(entries, "size", "desc")
	if entries[0].Name != "dir" || entries[1].Name != "b.txt" {
		t.Fatalf("大小降序错误: %+v", entries)
	}
}
