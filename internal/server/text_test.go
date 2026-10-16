package server

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// postJSON 发送 JSON POST（测试辅助）。
func postJSON(t *testing.T, url, body string) *http.Response {
	t.Helper()
	resp, err := http.Post(url, "application/json", strings.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	return resp
}

// TestTextNewSaveFlow 覆盖新建 → 读取 → 编辑保存全流程与版本推进。
func TestTextNewSaveFlow(t *testing.T) {
	ts, _, root := newTestHTTPServer(t)

	// 新建 .md
	resp := postJSON(t, ts.URL+"/api/text/new", `{"dir":"/","name":"notes.md","content":"# 标题"}`)
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("新建失败: %d %s", resp.StatusCode, body)
	}
	var created struct {
		Path    string `json:"path"`
		Version string `json:"version"`
		Size    int64  `json:"size"`
	}
	if err := json.Unmarshal(body, &created); err != nil {
		t.Fatal(err)
	}
	if created.Path != "/notes.md" || created.Version == "" {
		t.Fatalf("新建响应异常: %s", body)
	}
	if got, _ := os.ReadFile(filepath.Join(root, "notes.md")); string(got) != "# 标题" {
		t.Fatalf("文件内容不符: %q", got)
	}

	// 读取：可编辑 + 内容一致
	gr := get(t, ts.URL+"/api/text?path=/notes.md")
	var got textResp
	if err := json.NewDecoder(gr.Body).Decode(&got); err != nil {
		t.Fatal(err)
	}
	gr.Body.Close()
	if !got.Editable || got.Content != "# 标题" {
		t.Fatalf("读取异常: %+v", got)
	}

	// 保存（携带正确版本）
	sr := postJSON(t, ts.URL+"/api/text", `{"path":"/notes.md","content":"# 标题 v2","version":"`+got.Version+`"}`)
	sbody, _ := io.ReadAll(sr.Body)
	sr.Body.Close()
	if sr.StatusCode != http.StatusOK {
		t.Fatalf("保存失败: %d %s", sr.StatusCode, sbody)
	}
	if got, _ := os.ReadFile(filepath.Join(root, "notes.md")); string(got) != "# 标题 v2" {
		t.Fatalf("保存后内容不符: %q", got)
	}
	var saved struct {
		Version string `json:"version"`
	}
	_ = json.Unmarshal(sbody, &saved)
	if saved.Version == got.Version {
		t.Error("保存后版本应变化")
	}
}

// TestTextExtWhitelist 白名单外扩展名的新建/保存一律拒绝。
func TestTextExtWhitelist(t *testing.T) {
	ts, _, root := newTestHTTPServer(t)
	os.WriteFile(filepath.Join(root, "evil.exe"), []byte("MZ"), 0o644)

	r := postJSON(t, ts.URL+"/api/text/new", `{"dir":"/","name":"x.exe","content":"a"}`)
	r.Body.Close()
	if r.StatusCode != http.StatusForbidden {
		t.Errorf("白名单外新建应 403, 得到 %d", r.StatusCode)
	}

	r2 := postJSON(t, ts.URL+"/api/text", `{"path":"/evil.exe","content":"a","version":"x"}`)
	r2.Body.Close()
	if r2.StatusCode != http.StatusForbidden {
		t.Errorf("白名单外保存应 403, 得到 %d", r2.StatusCode)
	}

	// 直链行为不因白名单放宽：.js 仍强制附件下载（由 security_test 覆盖，这里再确认一次）
	os.WriteFile(filepath.Join(root, "a.js"), []byte("alert(1)"), 0o644)
	dr := get(t, ts.URL+"/api/file?path=/a.js")
	dr.Body.Close()
	if cd := dr.Header.Get("Content-Disposition"); !strings.HasPrefix(cd, "attachment") {
		t.Errorf(".js 直链应强制 attachment, 得到 %q", cd)
	}
}

// TestTextOversize 超过上限时只读预览（不返回内容），保存返回 413。
func TestTextOversize(t *testing.T) {
	srv := New(t.TempDir(), Options{TextMaxBytes: 16})
	root := srv.cfg.root
	os.WriteFile(filepath.Join(root, "big.txt"), bytes.Repeat([]byte("a"), 100), 0o644)
	ts := httptest.NewServer(srv.Handler())
	defer ts.Close()

	gr := get(t, ts.URL+"/api/text?path=/big.txt")
	var got textResp
	if err := json.NewDecoder(gr.Body).Decode(&got); err != nil {
		t.Fatal(err)
	}
	gr.Body.Close()
	if got.Editable || got.Reason != "too_large" {
		t.Fatalf("超限文件应不可编辑: %+v", got)
	}
	if got.Content != "" {
		t.Error("超限文件不应返回内容")
	}

	sr := postJSON(t, ts.URL+"/api/text", `{"path":"/big.txt","content":"`+strings.Repeat("b", 100)+`","version":"x"}`)
	sr.Body.Close()
	if sr.StatusCode != http.StatusRequestEntityTooLarge {
		t.Errorf("超限保存应 413, 得到 %d", sr.StatusCode)
	}
}

// TestTextConflict 版本不匹配返回 409（并发防覆盖）。
func TestTextConflict(t *testing.T) {
	ts, _, root := newTestHTTPServer(t)
	os.WriteFile(filepath.Join(root, "c.txt"), []byte("v1"), 0o644)

	// 用过期版本保存
	r := postJSON(t, ts.URL+"/api/text", `{"path":"/c.txt","content":"v2","version":"0-0"}`)
	r.Body.Close()
	if r.StatusCode != http.StatusConflict {
		t.Fatalf("版本过期应 409, 得到 %d", r.StatusCode)
	}
	if got, _ := os.ReadFile(filepath.Join(root, "c.txt")); string(got) != "v1" {
		t.Fatalf("冲突时不应改写文件: %q", got)
	}

	// 取正确版本后保存成功
	gr := get(t, ts.URL+"/api/text?path=/c.txt")
	var got textResp
	json.NewDecoder(gr.Body).Decode(&got)
	gr.Body.Close()
	r2 := postJSON(t, ts.URL+"/api/text", `{"path":"/c.txt","content":"v2","version":"`+got.Version+`"}`)
	r2.Body.Close()
	if r2.StatusCode != http.StatusOK {
		t.Fatalf("正确版本保存应 200, 得到 %d", r2.StatusCode)
	}
}

// TestTextReadOnly 只读文件/目录保存失败，且不留临时文件残留。
func TestTextReadOnly(t *testing.T) {
	ts, _, root := newTestHTTPServer(t)
	roFile := filepath.Join(root, "ro.txt")
	os.WriteFile(roFile, []byte("x"), 0o444)

	gr := get(t, ts.URL+"/api/text?path=/ro.txt")
	var got textResp
	json.NewDecoder(gr.Body).Decode(&got)
	gr.Body.Close()

	r := postJSON(t, ts.URL+"/api/text", `{"path":"/ro.txt","content":"y","version":"`+got.Version+`"}`)
	r.Body.Close()
	if r.StatusCode != http.StatusForbidden {
		t.Errorf("只读文件保存应 403, 得到 %d", r.StatusCode)
	}

	roDir := filepath.Join(root, "rodir")
	os.Mkdir(roDir, 0o555)
	n := postJSON(t, ts.URL+"/api/text/new", `{"dir":"/rodir","name":"n.txt","content":"y"}`)
	n.Body.Close()
	if n.StatusCode != http.StatusForbidden {
		t.Errorf("只读目录新建应 403, 得到 %d", n.StatusCode)
	}
	// 关闭只读位以便清理
	os.Chmod(roDir, 0o755)

	// 无临时文件残留
	residue, _ := filepath.Glob(filepath.Join(root, ".fs-text-*.tmp"))
	if len(residue) != 0 {
		t.Fatalf("只读失败后残留临时文件: %v", residue)
	}
}

// TestTextPathSafety 新建/保存入口的路径穿越仍被拦截。
func TestTextPathSafety(t *testing.T) {
	ts, _, root := newTestHTTPServer(t)
	os.WriteFile(filepath.Join(root, "s.txt"), []byte("s"), 0o644)

	// 保存越界路径
	for _, p := range []string{"../../etc/passwd", "..%2f..%2fetc", "/../x.txt"} {
		r := postJSON(t, ts.URL+"/api/text", `{"path":"`+p+`","content":"x","version":"0-0"}`)
		r.Body.Close()
		if r.StatusCode != http.StatusForbidden && r.StatusCode != http.StatusNotFound {
			t.Errorf("越界保存 %q 应被拒绝, 得到 %d", p, r.StatusCode)
		}
	}

	// 新建：目录越界 / 文件名带分隔符
	for _, body := range []string{
		`{"dir":"../..","name":"x.txt","content":"x"}`,
		`{"dir":"/","name":"a/b.txt","content":"x"}`,
		`{"dir":"/","name":"..","content":"x"}`,
		`{"dir":"/","name":"","content":"x"}`,
	} {
		r := postJSON(t, ts.URL+"/api/text/new", body)
		r.Body.Close()
		if r.StatusCode != http.StatusForbidden && r.StatusCode != http.StatusBadRequest && r.StatusCode != http.StatusNotFound {
			t.Errorf("非法新建 %s 应被拒绝, 得到 %d", body, r.StatusCode)
		}
	}

	// 同名文件不被覆盖
	r := postJSON(t, ts.URL+"/api/text/new", `{"dir":"/","name":"s.txt","content":"overwrite"}`)
	r.Body.Close()
	if r.StatusCode != http.StatusConflict {
		t.Errorf("同名新建应 409, 得到 %d", r.StatusCode)
	}
	if got, _ := os.ReadFile(filepath.Join(root, "s.txt")); string(got) != "s" {
		t.Errorf("同名文件被覆盖: %q", got)
	}
}

// TestTextInfoFields /api/info 下发可编辑白名单与上限。
func TestTextInfoFields(t *testing.T) {
	ts, srv, _ := newTestHTTPServer(t)
	resp := get(t, ts.URL+"/api/info")
	defer resp.Body.Close()
	var raw map[string]json.RawMessage
	if err := json.NewDecoder(resp.Body).Decode(&raw); err != nil {
		t.Fatal(err)
	}
	var exts []string
	if err := json.Unmarshal(raw["text_exts"], &exts); err != nil {
		t.Fatalf("text_exts 应为数组: %v", err)
	}
	found := false
	for _, e := range exts {
		if e == ".md" {
			found = true
		}
	}
	if !found {
		t.Errorf("text_exts 应包含 .md: %v", exts)
	}
	if srv.cfg.textMaxBytes != textMaxDefaultBytes {
		t.Errorf("默认上限应为 %d, 得到 %d", textMaxDefaultBytes, srv.cfg.textMaxBytes)
	}
}
