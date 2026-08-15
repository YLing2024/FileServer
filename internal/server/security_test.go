package server

import (
	"encoding/json"
	"io"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestNoSniffAndScriptableAttachment 全局 nosniff + HTML/SVG/JS 强制 attachment（1.3）
func TestNoSniffAndScriptableAttachment(t *testing.T) {
	srv := New(t.TempDir(), Options{})
	root := srv.root
	os.WriteFile(filepath.Join(root, "evil.html"), []byte(`<script>alert(1)</script>`), 0o644)
	os.WriteFile(filepath.Join(root, "evil.svg"), []byte(`<svg xmlns="http://www.w3.org/2000/svg"/>`), 0o644)
	os.WriteFile(filepath.Join(root, "evil.js"), []byte(`alert(1)`), 0o644)
	os.WriteFile(filepath.Join(root, "pic.png"), []byte("fake"), 0o644)
	ts := httptest.NewServer(srv.Handler())
	defer ts.Close()

	// 所有响应（含前端资源）都应带 nosniff
	r := get(t, ts.URL+"/api/list?path=/")
	r.Body.Close()
	if r.Header.Get("X-Content-Type-Options") != "nosniff" {
		t.Error("API 响应应带 X-Content-Type-Options: nosniff")
	}
	r2 := get(t, ts.URL+"/")
	r2.Body.Close()
	if r2.Header.Get("X-Content-Type-Options") != "nosniff" {
		t.Error("前端页面响应应带 X-Content-Type-Options: nosniff")
	}

	// 可执行内容必须 attachment
	for _, p := range []string{"/evil.html", "/evil.svg", "/evil.js"} {
		rr := get(t, ts.URL+"/api/file?path="+url.QueryEscape(p))
		rr.Body.Close()
		cd := rr.Header.Get("Content-Disposition")
		if !strings.HasPrefix(cd, "attachment") {
			t.Errorf("%s 应强制 attachment, 得到 %q", p, cd)
		}
	}

	// 普通图片仍可 inline（预览不回归）
	rr2 := get(t, ts.URL+"/api/file?path=/pic.png")
	rr2.Body.Close()
	if cd := rr2.Header.Get("Content-Disposition"); !strings.HasPrefix(cd, "inline") {
		t.Errorf("普通图片应 inline, 得到 %q", cd)
	}
}

// TestSanitizeFilename Content-Disposition 控制字符过滤（2.9）
func TestSanitizeFilename(t *testing.T) {
	cases := map[string]string{
		"normal.txt":     "normal.txt",
		"line\nbreak.txt": "linebreak.txt",
		"tab\tfile":      "tabfile",
		"cr\rfile":       "crfile",
		"中文 文件.txt":     "中文 文件.txt",
	}
	for in, want := range cases {
		if got := sanitizeFilename(in); got != want {
			t.Errorf("sanitizeFilename(%q) = %q, want %q", in, got, want)
		}
	}
}

// TestInfoKinds /api/info 下发统一扩展名映射与上限（4.1、4.5）
func TestInfoKinds(t *testing.T) {
	ts, _, _ := newTestHTTPServer(t)
	resp := get(t, ts.URL+"/api/info")
	defer resp.Body.Close()
	var info struct {
		Kinds       map[string]string `json:"kinds"`
		SearchLimit int               `json:"search_limit"`
		ListLimit   int               `json:"list_limit"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&info); err != nil {
		t.Fatal(err)
	}
	if info.Kinds == nil || len(info.Kinds) == 0 {
		t.Fatal("info.kinds 应为非空映射")
	}
	// fileKind 与 kinds 一致
	for name, kind := range map[string]string{".jpg": "image", ".mp4": "video", ".mp3": "audio", ".pdf": "pdf", ".zip": "archive", ".txt": "text", ".go": "code"} {
		if info.Kinds[name] != kind {
			t.Errorf("kinds[%s] = %q, want %q", name, info.Kinds[name], kind)
		}
	}
	// 新增扩展名必须出现在 kinds（防漂移）
	if info.Kinds[".ogv"] != "video" || info.Kinds[".rmvb"] != "video" {
		t.Error("fileKind 中的视频扩展名应同步到 kinds")
	}
	if info.SearchLimit != searchMaxLimit {
		t.Errorf("search_limit 应=%d, 得到 %d", searchMaxLimit, info.SearchLimit)
	}
	if info.ListLimit != listMaxLimit {
		t.Errorf("list_limit 应=%d, 得到 %d", listMaxLimit, info.ListLimit)
	}
}

// TestMediaMime 视频/音频扩展名映射完整（fileKind 与 mediaMime 不漂移）
func TestMediaMime(t *testing.T) {
	for _, ext := range []string{".ogv", ".rm", ".rmvb"} {
		if mediaMime(ext) == "" {
			t.Errorf("mediaMime(%s) 不应为空（与 fileKind 保持一致）", ext)
		}
	}
	if got := mediaMime(".ogv"); got != "video/ogg" {
		t.Errorf(".ogv 应为 video/ogg, 得到 %q", got)
	}
}

// TestListRespFields 分页响应字段 Total/Truncated（3.1）
func TestListRespFields(t *testing.T) {
	srv := New(t.TempDir(), Options{})
	for i := 0; i < 5; i++ {
		os.WriteFile(filepath.Join(srv.root, "f"+itoa(i)), []byte("x"), 0o644)
	}
	ts := httptest.NewServer(srv.Handler())
	defer ts.Close()

	var lr ListResp
	resp := get(t, ts.URL+"/api/list?path=/&limit=2")
	if err := json.NewDecoder(resp.Body).Decode(&lr); err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if lr.Total != 5 {
		t.Errorf("Total 应为 5, 得到 %d", lr.Total)
	}
	if !lr.Truncated {
		t.Error("limit=2 且共 5 条时应 truncated")
	}
	if len(lr.Entries) != 2 {
		t.Errorf("应有 2 条, 得到 %d", len(lr.Entries))
	}
}

// TestHiddenBlockedRoot 根目录本身不应被隐藏过滤误拦（1.2 回归）
func TestHiddenBlockedRoot(t *testing.T) {
	srv := New(t.TempDir(), Options{})
	if srv.hiddenBlocked(srv.root) {
		t.Error("根目录不应视为隐藏路径")
	}
	sub := filepath.Join(srv.root, "sub")
	os.MkdirAll(sub, 0o755)
	if srv.hiddenBlocked(sub) {
		t.Error("普通子目录不应视为隐藏路径")
	}
	os.WriteFile(filepath.Join(srv.root, ".hidden"), []byte("x"), 0o644)
	if !srv.hiddenBlocked(filepath.Join(srv.root, ".hidden")) {
		t.Error("隐藏文件应被识别")
	}
	if !srv.hiddenBlocked(filepath.Join(srv.root, ".git", "config")) {
		t.Error("隐藏祖先目录内的文件应被识别")
	}
}

// TestDownloadSanitizedFilename 下载文件名经 sanitize 后进 Content-Disposition（2.9）
func TestDownloadSanitizedFilename(t *testing.T) {
	srv := New(t.TempDir(), Options{})
	name := "bad\nname.txt"
	os.WriteFile(filepath.Join(srv.root, name), []byte("x"), 0o644)
	ts := httptest.NewServer(srv.Handler())
	defer ts.Close()

	resp := get(t, ts.URL+"/api/file?path="+url.QueryEscape("/"+name))
	io.Copy(io.Discard, resp.Body)
	resp.Body.Close()
	cd := resp.Header.Get("Content-Disposition")
	if strings.ContainsAny(cd, "\r\n") {
		t.Fatalf("Content-Disposition 含控制字符（响应头注入风险）: %q", cd)
	}
}
