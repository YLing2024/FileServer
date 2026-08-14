package server

import (
	"os"
	"path/filepath"
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
		name string
		rel  string
		ok   bool
	}{
		{"空路径=根", "", true},
		{"根斜杠", "/", true},
		{"直接子文件", "a.txt", true},
		{"子目录文件", "dir1/b.txt", true},
		{"深层", "dir1/sub/c.txt", true},
		{"中文与空格", "中文 文件.txt", true},
		{"前导斜杠", "/a.txt", true},
		{"反斜杠", `dir1\b.txt`, true},
		{"父目录逃逸", "../a.txt", false},
		{"双层逃逸", "../../etc/passwd", false},
		{"编码层逃逸", "..%2f..%2fa.txt", false},
		{"混合逃逸", "dir1/../../a.txt", false},
		{"反斜杠逃逸", `..\..\a.txt`, false},
		{"子串诱骗", "dir1..a.txt", false}, // 不存在
		{"绝对路径", "C:\\windows\\system32", false},
		{"盘符相对", "C:foo", false},
		{"UNC", `\\server\share\x`, false},
		{"空字节", "a\x00b.txt", false},
		{"点路径", ".", true},
		{"点点路径", "./a.txt", true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			abs, err := srv.safePath(c.rel)
			if c.ok {
				if err != nil {
					t.Fatalf("期望成功, 得到错误: %v", err)
				}
				if !pathWithin(srv.root, abs) {
					t.Fatalf("结果 %q 不在根目录 %q 内", abs, srv.root)
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

func TestPathWithin(t *testing.T) {
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
