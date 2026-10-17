package server

import (
	"encoding/json"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
)

// createJSON 发送 POST /api/create 并返回响应与错误文案（测试辅助）。
func createJSON(t *testing.T, url, body string) (*http.Response, string) {
	t.Helper()
	resp := postJSON(t, url, body)
	raw, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	var j struct {
		Error string `json:"error"`
	}
	_ = json.Unmarshal(raw, &j)
	return resp, j.Error
}

// TestCreateEmptyFile 建非白名单扩展名的空文件，落盘 0 字节。
func TestCreateEmptyFile(t *testing.T) {
	ts, _, root := newTestHTTPServer(t)

	resp, msg := createJSON(t, ts.URL+"/api/create", `{"dir":"/","kind":"file","name":"movie.mkv"}`)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("建空文件应 200, 得到 %d (%s)", resp.StatusCode, msg)
	}
	fi, err := os.Stat(filepath.Join(root, "movie.mkv"))
	if err != nil || fi.IsDir() {
		t.Fatalf("空文件未创建: %v", err)
	}
	if fi.Size() != 0 {
		t.Fatalf("应为 0 字节空文件, 得到 %d", fi.Size())
	}
}

// TestCreateTextFileWithContent 白名单扩展名 + 初始内容写入。
func TestCreateTextFileWithContent(t *testing.T) {
	ts, _, root := newTestHTTPServer(t)

	resp, msg := createJSON(t, ts.URL+"/api/create", `{"dir":"/","kind":"file","name":"notes.txt","content":"hello 内容"}`)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("建文本文件应 200, 得到 %d (%s)", resp.StatusCode, msg)
	}
	got, _ := os.ReadFile(filepath.Join(root, "notes.txt"))
	if string(got) != "hello 内容" {
		t.Fatalf("内容不符: %q", got)
	}
}

// TestCreateNonWhitelistIgnoresContent 非白名单扩展名忽略 content，只建空文件。
func TestCreateNonWhitelistIgnoresContent(t *testing.T) {
	ts, _, root := newTestHTTPServer(t)

	resp, _ := createJSON(t, ts.URL+"/api/create", `{"dir":"/","kind":"file","name":"a.bin","content":"should be ignored"}`)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("应 200, 得到 %d", resp.StatusCode)
	}
	fi, _ := os.Stat(filepath.Join(root, "a.bin"))
	if fi == nil || fi.Size() != 0 {
		t.Fatalf("非白名单扩展名应只建 0 字节空文件, size=%v", fi)
	}
}

// TestCreateDir 建单层文件夹。
func TestCreateDir(t *testing.T) {
	ts, _, root := newTestHTTPServer(t)

	resp, msg := createJSON(t, ts.URL+"/api/create", `{"dir":"/","kind":"dir","name":"新建 文件夹"}`)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("建文件夹应 200, 得到 %d (%s)", resp.StatusCode, msg)
	}
	fi, err := os.Stat(filepath.Join(root, "新建 文件夹"))
	if err != nil || !fi.IsDir() {
		t.Fatalf("文件夹未创建: %v", err)
	}
}

// TestCreateInvalidNames 命名校验矩阵（非法名一律 400 且不落盘）。
func TestCreateInvalidNames(t *testing.T) {
	ts, _, root := newTestHTTPServer(t)
	before := dirEntrySet(t, root)

	longName := strings.Repeat("a", 256) // >255 字节
	cases := []struct {
		name string
		body string
	}{
		{"穿越斜杠", `{"dir":"/","kind":"file","name":"../x"}`},
		{"路径分隔符", `{"dir":"/","kind":"file","name":"a/b"}`},
		{"反斜杠", `{"dir":"/","kind":"file","name":"a\\b"}`},
		{"隐藏名", `{"dir":"/","kind":"file","name":".hidden"}`},
		{"单点", `{"dir":"/","kind":"file","name":"."}`},
		{"双点", `{"dir":"/","kind":"file","name":".."}`},
		{"结尾点", `{"dir":"/","kind":"file","name":"bad."}`},
		{"结尾空格", `{"dir":"/","kind":"file","name":"bad "}`},
		{"首部空格", `{"dir":"/","kind":"file","name":" bad"}`},
		{"保留名 con", `{"dir":"/","kind":"file","name":"con"}`},
		{"保留名 CON.txt", `{"dir":"/","kind":"file","name":"CON.txt"}`},
		{"保留名 com1.log", `{"dir":"/","kind":"file","name":"com1.log"}`},
		{"超长名", `{"dir":"/","kind":"file","name":"` + longName + `"}`},
		{"冒号", `{"dir":"/","kind":"file","name":"a:b"}`},
		{"星号", `{"dir":"/","kind":"file","name":"a*b"}`},
		{"问号", `{"dir":"/","kind":"file","name":"a?b"}`},
		{"竖线", `{"dir":"/","kind":"file","name":"a|b"}`},
		{"控制字符", `{"dir":"/","kind":"file","name":"a\u0001b"}`},
		{"空名", `{"dir":"/","kind":"file","name":""}`},
		{"纯空白", `{"dir":"/","kind":"file","name":"   "}`},
		{"非法 kind", `{"dir":"/","kind":"symlink","name":"x"}`},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			resp, msg := createJSON(t, ts.URL+"/api/create", c.body)
			if resp.StatusCode != http.StatusBadRequest {
				t.Fatalf("应 400, 得到 %d (%s)", resp.StatusCode, msg)
			}
		})
	}

	// 确认非法名未意外落盘
	after := dirEntrySet(t, root)
	for n := range after {
		if !before[n] {
			t.Fatalf("非法名不应落盘，新增了: %q", n)
		}
	}
}

// dirEntrySet 返回目录下条目名集合（测试辅助）。
func dirEntrySet(t *testing.T, dir string) map[string]bool {
	t.Helper()
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	set := map[string]bool{}
	for _, e := range entries {
		set[e.Name()] = true
	}
	return set
}

// TestValidEntryNameUnit validEntryName 的合法/非法判定与错误文案。
func TestValidEntryNameUnit(t *testing.T) {
	ok := []string{"notes.txt", "a.bin", "中文 文件.txt", "a..b", "a.b.c", "UPPER.TXT"}
	for _, n := range ok {
		if msg := validEntryName(n); msg != "" {
			t.Errorf("%q 应为合法名, 得到: %s", n, msg)
		}
	}
	bad := []string{"", "  ", ".hidden", ".", "..", "a/b", "a\\b", "con", "CON.txt", "lpt9", "a.", "a ", " a", "a:b", "a*b", "a\u0000b"}
	for _, n := range bad {
		if msg := validEntryName(n); msg == "" {
			t.Errorf("%q 应被拒绝", n)
		}
	}
}

// TestCreateConflict 同名文件/目录已存在返回 409，且不覆盖。
func TestCreateConflict(t *testing.T) {
	ts, _, root := newTestHTTPServer(t)

	// 同名目录
	r1, _ := createJSON(t, ts.URL+"/api/create", `{"dir":"/","kind":"dir","name":"dup"}`)
	if r1.StatusCode != http.StatusOK {
		t.Fatalf("首次建目录应 200, 得到 %d", r1.StatusCode)
	}
	r2, msg := createJSON(t, ts.URL+"/api/create", `{"dir":"/","kind":"dir","name":"dup"}`)
	if r2.StatusCode != http.StatusConflict {
		t.Fatalf("同名目录应 409, 得到 %d (%s)", r2.StatusCode, msg)
	}

	// 同名文件
	r3, _ := createJSON(t, ts.URL+"/api/create", `{"dir":"/","kind":"file","name":"f.txt","content":"v1"}`)
	if r3.StatusCode != http.StatusOK {
		t.Fatalf("首次建文件应 200, 得到 %d", r3.StatusCode)
	}
	r4, msg := createJSON(t, ts.URL+"/api/create", `{"dir":"/","kind":"file","name":"f.txt","content":"v2"}`)
	if r4.StatusCode != http.StatusConflict {
		t.Fatalf("同名文件应 409, 得到 %d (%s)", r4.StatusCode, msg)
	}
	if got, _ := os.ReadFile(filepath.Join(root, "f.txt")); string(got) != "v1" {
		t.Fatalf("同名文件被覆盖: %q", got)
	}
}

// TestCreateConcurrentDir 并发建同名目录，恰一个成功、其余 409。
func TestCreateConcurrentDir(t *testing.T) {
	ts, _, _ := newTestHTTPServer(t)

	const n = 8
	var wg sync.WaitGroup
	results := make([]int, n)
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			resp, err := http.Post(ts.URL+"/api/create", "application/json",
				strings.NewReader(`{"dir":"/","kind":"dir","name":"race"}`))
			if err != nil {
				results[i] = -1
				return
			}
			results[i] = resp.StatusCode
			resp.Body.Close()
		}(i)
	}
	wg.Wait()

	success, conflict := 0, 0
	for _, code := range results {
		switch code {
		case http.StatusOK:
			success++
		case http.StatusConflict:
			conflict++
		default:
			t.Fatalf("意外状态码: %d (%v)", code, results)
		}
	}
	if success != 1 || conflict != n-1 {
		t.Fatalf("并发建同名目录应恰一个成功, 得到 成功=%d 冲突=%d", success, conflict)
	}
}

// TestCreateReadOnly 只读目录（0o555）建文件/文件夹均 403。
func TestCreateReadOnly(t *testing.T) {
	ts, _, root := newTestHTTPServer(t)
	roDir := filepath.Join(root, "rodir")
	if err := os.Mkdir(roDir, 0o555); err != nil {
		t.Fatal(err)
	}

	f, _ := createJSON(t, ts.URL+"/api/create", `{"dir":"/rodir","kind":"file","name":"n.txt"}`)
	if f.StatusCode != http.StatusForbidden {
		t.Errorf("只读目录建文件应 403, 得到 %d", f.StatusCode)
	}
	d, _ := createJSON(t, ts.URL+"/api/create", `{"dir":"/rodir","kind":"dir","name":"sub"}`)
	if d.StatusCode != http.StatusForbidden {
		t.Errorf("只读目录建文件夹应 403, 得到 %d", d.StatusCode)
	}
	os.Chmod(roDir, 0o755)
}

// TestCreateBadDir 父目录不存在 / 目标不是目录 / 目录在根外，均为 400 或 403。
func TestCreateBadDir(t *testing.T) {
	ts, _, root := newTestHTTPServer(t)
	os.WriteFile(filepath.Join(root, "file.txt"), []byte("x"), 0o644)

	// 父目录不存在 → 400
	r, _ := createJSON(t, ts.URL+"/api/create", `{"dir":"/nope","kind":"file","name":"x.txt"}`)
	if r.StatusCode != http.StatusBadRequest {
		t.Errorf("父目录不存在应 400, 得到 %d", r.StatusCode)
	}
	// dir 指向文件 → 400
	r2, _ := createJSON(t, ts.URL+"/api/create", `{"dir":"/file.txt","kind":"file","name":"x.txt"}`)
	if r2.StatusCode != http.StatusBadRequest {
		t.Errorf("dir 指向文件应 400, 得到 %d", r2.StatusCode)
	}
	// 目录穿越 → 403
	r3, _ := createJSON(t, ts.URL+"/api/create", `{"dir":"../..","kind":"file","name":"x.txt"}`)
	if r3.StatusCode != http.StatusForbidden {
		t.Errorf("目录穿越应 403, 得到 %d", r3.StatusCode)
	}
	// 建文件夹同样不递归：父目录不存在 → 400（不会顺带造出父目录）
	r4, _ := createJSON(t, ts.URL+"/api/create", `{"dir":"/no/such/parent","kind":"dir","name":"x"}`)
	if r4.StatusCode != http.StatusBadRequest {
		t.Errorf("父目录不存在建文件夹应 400, 得到 %d", r4.StatusCode)
	}
	if _, err := os.Stat(filepath.Join(root, "no")); !os.IsNotExist(err) {
		t.Errorf("不应递归创建父目录: %v", err)
	}
}

// TestCreateSymlinkEscape 根内符号链接指向根外时，新端点不得写到根外。
func TestCreateSymlinkEscape(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Windows 创建符号链接需要特权，跳过")
	}
	ts, _, root := newTestHTTPServer(t)
	outside := t.TempDir()
	link := filepath.Join(root, "escape")
	if err := os.Symlink(outside, link); err != nil {
		t.Skipf("无法创建符号链接: %v", err)
	}

	r, _ := createJSON(t, ts.URL+"/api/create", `{"dir":"/escape","kind":"file","name":"pwn.txt"}`)
	if r.StatusCode != http.StatusForbidden {
		t.Fatalf("符号链接逃逸应 403, 得到 %d", r.StatusCode)
	}
	if _, err := os.Stat(filepath.Join(outside, "pwn.txt")); !os.IsNotExist(err) {
		t.Fatalf("文件被写到根外: %v", err)
	}
}

// TestTextNewCompat /api/text/new 兼容行为不变，并同样收紧非法名。
func TestTextNewCompat(t *testing.T) {
	ts, _, root := newTestHTTPServer(t)

	// 白名单外仍 403
	r := postJSON(t, ts.URL+"/api/text/new", `{"dir":"/","name":"x.exe","content":"a"}`)
	r.Body.Close()
	if r.StatusCode != http.StatusForbidden {
		t.Errorf("白名单外新建应 403, 得到 %d", r.StatusCode)
	}

	// 带内容仍写入
	r2 := postJSON(t, ts.URL+"/api/text/new", `{"dir":"/","name":"ok.md","content":"# hi"}`)
	r2.Body.Close()
	if r2.StatusCode != http.StatusOK {
		t.Errorf("白名单内新建应 200, 得到 %d", r2.StatusCode)
	}
	if got, _ := os.ReadFile(filepath.Join(root, "ok.md")); string(got) != "# hi" {
		t.Errorf("内容不符: %q", got)
	}

	// 新命名规则对兼容入口同样生效
	r3 := postJSON(t, ts.URL+"/api/text/new", `{"dir":"/","name":"CON.txt","content":"x"}`)
	r3.Body.Close()
	if r3.StatusCode != http.StatusBadRequest {
		t.Errorf("保留名经 text/new 应 400, 得到 %d", r3.StatusCode)
	}
}
