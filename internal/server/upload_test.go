package server

import (
	"bytes"
	"encoding/json"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"net/textproto"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// uploadTestFile 一个待上传文件；rawName 非空时直接写入该 filename（用于测试非法名）。
type uploadTestFile struct {
	name    string
	data    []byte
	rawName string
}

// newUploadTS 构造允许上传的测试服务器与根目录。
func newUploadTS(t *testing.T, maxBytes int64) (*httptest.Server, string) {
	t.Helper()
	root := t.TempDir()
	opts := Options{AllowUpload: true, MaxUploadMB: 1}
	if maxBytes > 0 {
		opts.MaxUploadMB = 0
		opts.MaxUploadBytes = maxBytes
	}
	srv := New(root, opts)
	ts := httptest.NewServer(srv.Handler())
	t.Cleanup(ts.Close)
	return ts, root
}

// uploadMultipart 发送 POST /api/upload。
func uploadMultipart(t *testing.T, url, dir, mode string, files []uploadTestFile) (*http.Response, []byte) {
	t.Helper()
	var body bytes.Buffer
	mw := multipart.NewWriter(&body)
	for _, f := range files {
		name := f.name
		if f.rawName != "" {
			name = f.rawName
		}
		h := make(textproto.MIMEHeader)
		h.Set("Content-Disposition", `form-data; name="file"; filename="`+strings.ReplaceAll(name, `"`, `\"`)+`"`)
		h.Set("Content-Type", "application/octet-stream")
		part, err := mw.CreatePart(h)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := part.Write(f.data); err != nil {
			t.Fatal(err)
		}
	}
	if err := mw.Close(); err != nil {
		t.Fatal(err)
	}
	q := "?dir=" + dir
	if mode != "" {
		q += "&mode=" + mode
	}
	req, err := http.NewRequest(http.MethodPost, url+"/api/upload"+q, &body)
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Content-Type", mw.FormDataContentType())
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	raw, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	return resp, raw
}

// uploadRespBody 解析上传响应（uploaded / code / existing / error）。
type uploadRespBody struct {
	Uploaded []uploadedFile `json:"uploaded"`
	Code     string         `json:"code"`
	Existing []string       `json:"existing"`
	Error    string         `json:"error"`
}

func decodeUpload(t *testing.T, raw []byte) uploadRespBody {
	t.Helper()
	var out uploadRespBody
	if err := json.Unmarshal(raw, &out); err != nil {
		t.Fatalf("响应不是合法 JSON: %v (%s)", err, raw)
	}
	return out
}

// assertNoUploadTemps 断言目录下没有 .upload-* 临时文件残留。
func assertNoUploadTemps(t *testing.T, dir string) {
	t.Helper()
	matches, err := filepath.Glob(filepath.Join(dir, ".upload-*"))
	if err != nil {
		t.Fatal(err)
	}
	if len(matches) != 0 {
		t.Fatalf("存在临时文件残留: %v", matches)
	}
}

// TestUploadDisabledByDefault 未开启开关时上传端点拒绝（默认关闭）。
func TestUploadDisabledByDefault(t *testing.T) {
	root := t.TempDir()
	srv := New(root, Options{})
	ts := httptest.NewServer(srv.Handler())
	defer ts.Close()

	resp, _ := uploadMultipart(t, ts.URL, "/", "ask", []uploadTestFile{{name: "a.txt", data: []byte("x")}})
	if resp.StatusCode != http.StatusForbidden {
		t.Fatalf("未开启上传应 403, 得到 %d", resp.StatusCode)
	}
	if _, err := os.Stat(filepath.Join(root, "a.txt")); !os.IsNotExist(err) {
		t.Fatalf("未开启上传不应落盘: %v", err)
	}
}

// TestUploadRequiresAuth 认证开启时未登录上传返回 401。
func TestUploadRequiresAuth(t *testing.T) {
	srv := New(t.TempDir(), Options{AuthMode: "password", AllowUpload: true})
	ts := httptest.NewServer(srv.Handler())
	defer ts.Close()

	resp, _ := uploadMultipart(t, ts.URL, "/", "ask", []uploadTestFile{{name: "a.txt", data: []byte("x")}})
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("未认证上传应 401, 得到 %d", resp.StatusCode)
	}
}

// TestUploadMultiFile 一次上传多个文件（含中文与空格），并验证列表缓存已失效。
func TestUploadMultiFile(t *testing.T) {
	ts, root := newUploadTS(t, 0)
	if err := os.Mkdir(filepath.Join(root, "dir1"), 0o755); err != nil {
		t.Fatal(err)
	}
	resp, raw := uploadMultipart(t, ts.URL, "/dir1", "ask", []uploadTestFile{
		{name: "中文 文件.txt", data: []byte("hello 中文")},
		{name: "b.bin", data: []byte("binary")},
	})
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("上传应 200, 得到 %d (%s)", resp.StatusCode, raw)
	}
	out := decodeUpload(t, raw)
	if len(out.Uploaded) != 2 {
		t.Fatalf("应返回 2 个文件: %+v", out)
	}
	got, err := os.ReadFile(filepath.Join(root, "dir1", "中文 文件.txt"))
	if err != nil || string(got) != "hello 中文" {
		t.Fatalf("中文文件名内容不符: %q %v", got, err)
	}
	assertNoUploadTemps(t, filepath.Join(root, "dir1"))

	// 列表立即可见（上传后失效缓存）
	r := get(t, ts.URL+"/api/list?path=/dir1")
	defer r.Body.Close()
	var lr ListResp
	if err := json.NewDecoder(r.Body).Decode(&lr); err != nil {
		t.Fatal(err)
	}
	names := map[string]bool{}
	for _, e := range lr.Entries {
		names[e.Name] = true
	}
	if !names["中文 文件.txt"] || !names["b.bin"] {
		t.Fatalf("列表未立即看到上传文件: %v", names)
	}
}

// TestUploadTraversalRejected 越界目录、隐藏目录、保留目录与含分隔符的文件名全部拒绝。
func TestUploadTraversalRejected(t *testing.T) {
	ts, root := newUploadTS(t, 0)

	resp, raw := uploadMultipart(t, ts.URL, "..", "ask", []uploadTestFile{{name: "escape.txt", data: []byte("x")}})
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("越界目录应 400, 得到 %d (%s)", resp.StatusCode, raw)
	}
	if _, err := os.Stat(filepath.Join(filepath.Dir(root), "escape.txt")); !os.IsNotExist(err) {
		t.Fatalf("越界上传不应落盘: %v", err)
	}

	// 隐藏目录（默认不开放）与保留缓存目录
	for _, dir := range []string{"/.hidden", "/.FileServer"} {
		os.MkdirAll(filepath.Join(root, strings.TrimPrefix(dir, "/")), 0o755)
		r, body := uploadMultipart(t, ts.URL, dir, "ask", []uploadTestFile{{name: "a.txt", data: []byte("x")}})
		if r.StatusCode != http.StatusForbidden {
			t.Errorf("目录 %q 应 403, 得到 %d (%s)", dir, r.StatusCode, body)
		}
	}

	// 文件名带路径分隔符：multipart.Part.FileName 会取 basename，必须在原始头处拒绝
	r2, body2 := uploadMultipart(t, ts.URL, "/", "ask", []uploadTestFile{{name: "x", rawName: "../evil.txt", data: []byte("x")}})
	if r2.StatusCode != http.StatusBadRequest {
		t.Fatalf("含分隔符文件名应 400, 得到 %d (%s)", r2.StatusCode, body2)
	}
	if _, err := os.Stat(filepath.Join(filepath.Dir(root), "evil.txt")); !os.IsNotExist(err) {
		t.Fatalf("分隔符文件名不应逃逸: %v", err)
	}
	assertNoUploadTemps(t, root)

	// 隐藏文件与 .FileServer 名称拒绝
	for _, name := range []string{".htaccess", ".FileServer"} {
		r3, body3 := uploadMultipart(t, ts.URL, "/", "ask", []uploadTestFile{{name: name, data: []byte("x")}})
		if r3.StatusCode != http.StatusBadRequest {
			t.Errorf("文件名 %q 应 400, 得到 %d (%s)", name, r3.StatusCode, body3)
		}
	}
}

// TestUploadConflictAsk 默认 ask：冲突时 409，列出冲突名，原文件不变且无临时残留。
func TestUploadConflictAsk(t *testing.T) {
	ts, root := newUploadTS(t, 0)
	if err := os.WriteFile(filepath.Join(root, "a.txt"), []byte("old"), 0o644); err != nil {
		t.Fatal(err)
	}
	resp, raw := uploadMultipart(t, ts.URL, "/", "", []uploadTestFile{
		{name: "a.txt", data: []byte("new")},
		{name: "fresh.txt", data: []byte("fresh")},
	})
	if resp.StatusCode != http.StatusConflict {
		t.Fatalf("冲突应 409, 得到 %d (%s)", resp.StatusCode, raw)
	}
	out := decodeUpload(t, raw)
	if out.Code != "conflict" || len(out.Existing) != 1 || out.Existing[0] != "a.txt" {
		t.Fatalf("冲突响应不符: %+v", out)
	}
	got, _ := os.ReadFile(filepath.Join(root, "a.txt"))
	if string(got) != "old" {
		t.Fatalf("ask 模式不应覆盖: %q", got)
	}
	if _, err := os.Stat(filepath.Join(root, "fresh.txt")); !os.IsNotExist(err) {
		t.Fatalf("冲突时整批都不应落盘: %v", err)
	}
	assertNoUploadTemps(t, root)
}

// TestUploadOverwrite 覆盖模式：内容被替换，原文件不残留旧内容。
func TestUploadOverwrite(t *testing.T) {
	ts, root := newUploadTS(t, 0)
	os.WriteFile(filepath.Join(root, "a.txt"), []byte("old"), 0o644)
	resp, raw := uploadMultipart(t, ts.URL, "/", "overwrite", []uploadTestFile{{name: "a.txt", data: []byte("new content")}})
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("覆盖应 200, 得到 %d (%s)", resp.StatusCode, raw)
	}
	got, _ := os.ReadFile(filepath.Join(root, "a.txt"))
	if string(got) != "new content" {
		t.Fatalf("覆盖后内容不符: %q", got)
	}
	assertNoUploadTemps(t, root)

	// 目标是目录：拒绝覆盖
	os.Mkdir(filepath.Join(root, "sub"), 0o755)
	r2, body2 := uploadMultipart(t, ts.URL, "/", "overwrite", []uploadTestFile{{name: "sub", data: []byte("x")}})
	if r2.StatusCode != http.StatusConflict {
		t.Fatalf("覆盖目录应 409, 得到 %d (%s)", r2.StatusCode, body2)
	}
	fi, err := os.Stat(filepath.Join(root, "sub"))
	if err != nil || !fi.IsDir() {
		t.Fatalf("目录不应被替换: %v", err)
	}
	assertNoUploadTemps(t, root)
}

// TestUploadRename 自动改名：name (1).ext 递增，且不覆盖原文件。
func TestUploadRename(t *testing.T) {
	ts, root := newUploadTS(t, 0)
	os.WriteFile(filepath.Join(root, "a.txt"), []byte("old"), 0o644)
	os.WriteFile(filepath.Join(root, "a (1).txt"), []byte("one"), 0o644)

	resp, raw := uploadMultipart(t, ts.URL, "/", "rename", []uploadTestFile{{name: "a.txt", data: []byte("new")}})
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("改名应 200, 得到 %d (%s)", resp.StatusCode, raw)
	}
	out := decodeUpload(t, raw)
	if len(out.Uploaded) != 1 || out.Uploaded[0].Name != "a (2).txt" {
		t.Fatalf("应改名为 a (2).txt: %+v", out)
	}
	if got, _ := os.ReadFile(filepath.Join(root, "a.txt")); string(got) != "old" {
		t.Fatalf("原文件应保持不变: %q", got)
	}
	if got, _ := os.ReadFile(filepath.Join(root, "a (2).txt")); string(got) != "new" {
		t.Fatalf("改名文件内容不符: %q", got)
	}
	assertNoUploadTemps(t, root)
}

// TestUploadTooLarge 单文件超限返回 413，不落盘、无临时残留。
func TestUploadTooLarge(t *testing.T) {
	ts, root := newUploadTS(t, 16)
	resp, raw := uploadMultipart(t, ts.URL, "/", "ask", []uploadTestFile{{name: "big.bin", data: bytes.Repeat([]byte("x"), 17)}})
	if resp.StatusCode != http.StatusRequestEntityTooLarge {
		t.Fatalf("超限应 413, 得到 %d (%s)", resp.StatusCode, raw)
	}
	if _, err := os.Stat(filepath.Join(root, "big.bin")); !os.IsNotExist(err) {
		t.Fatalf("超限文件不应落盘: %v", err)
	}
	assertNoUploadTemps(t, root)
}

// TestUploadReadOnlyAndBadDir 只读目录 403；目录不存在/不是目录 400。
func TestUploadReadOnlyAndBadDir(t *testing.T) {
	ts, root := newUploadTS(t, 0)
	ro := filepath.Join(root, "ro")
	if err := os.Mkdir(ro, 0o555); err != nil {
		t.Fatal(err)
	}
	defer os.Chmod(ro, 0o755)

	r, body := uploadMultipart(t, ts.URL, "/ro", "ask", []uploadTestFile{{name: "a.txt", data: []byte("x")}})
	if r.StatusCode != http.StatusForbidden {
		t.Errorf("只读目录应 403, 得到 %d (%s)", r.StatusCode, body)
	}
	assertNoUploadTemps(t, ro)

	for _, dir := range []string{"/nope", "/a.txt"} {
		os.WriteFile(filepath.Join(root, "a.txt"), []byte("x"), 0o644)
		r2, body2 := uploadMultipart(t, ts.URL, dir, "ask", []uploadTestFile{{name: "b.txt", data: []byte("x")}})
		if r2.StatusCode != http.StatusBadRequest {
			t.Errorf("目录 %q 应 400, 得到 %d (%s)", dir, r2.StatusCode, body2)
		}
	}
}

// TestUploadBadRequests 非法 mode、空请求、超文件数上限。
func TestUploadBadRequests(t *testing.T) {
	ts, _ := newUploadTS(t, 0)
	r, body := uploadMultipart(t, ts.URL, "/", "merge", []uploadTestFile{{name: "a.txt", data: []byte("x")}})
	if r.StatusCode != http.StatusBadRequest {
		t.Fatalf("非法 mode 应 400, 得到 %d (%s)", r.StatusCode, body)
	}
	r2, body2 := uploadMultipart(t, ts.URL, "/", "ask", nil)
	if r2.StatusCode != http.StatusBadRequest {
		t.Fatalf("空请求应 400, 得到 %d (%s)", r2.StatusCode, body2)
	}

	files := make([]uploadTestFile, uploadMaxFiles+1)
	for i := range files {
		files[i] = uploadTestFile{name: "f" + string(rune('a'+i%26)) + ".txt", data: []byte("x")}
	}
	r3, body3 := uploadMultipart(t, ts.URL, "/", "rename", files)
	if r3.StatusCode != http.StatusBadRequest {
		t.Fatalf("超过文件数上限应 400, 得到 %d (%s)", r3.StatusCode, body3)
	}
}

// TestUploadInfoExposesCapabilities /api/info 下发开关与上限。
func TestUploadInfoExposesCapabilities(t *testing.T) {
	srv := New(t.TempDir(), Options{AllowUpload: true, MaxUploadMB: 3})
	ts := httptest.NewServer(srv.Handler())
	defer ts.Close()
	r := get(t, ts.URL+"/api/info")
	defer r.Body.Close()
	var info struct {
		AllowUpload    bool  `json:"allow_upload"`
		UploadMaxBytes int64 `json:"upload_max_bytes"`
	}
	if err := json.NewDecoder(r.Body).Decode(&info); err != nil {
		t.Fatal(err)
	}
	if !info.AllowUpload || info.UploadMaxBytes != 3<<20 {
		t.Fatalf("/api/info 上传能力不符: %+v", info)
	}
}

// TestUploadDefaultCap 未显式配置上限时默认 1024 MiB。
func TestUploadDefaultCap(t *testing.T) {
	srv := New(t.TempDir(), Options{AllowUpload: true})
	if srv.cfg.uploadMaxBytes != uploadMaxDefaultBytes {
		t.Fatalf("默认上限应为 %d, 得到 %d", uploadMaxDefaultBytes, srv.cfg.uploadMaxBytes)
	}
}
