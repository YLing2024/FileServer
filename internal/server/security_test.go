package server

import (
	"encoding/json"
	"io"
	"net/http"
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

// TestCacheDirNeverExposed 保留缓存目录 .FileServer 在任何配置下都不可见/不可访问。
// 回归背景：开启 --hidden 后用户点开头文件全部可见，但服务缓存目录（含 HLS
// 转码副本、缩略图缓存）曾被当作普通点开头目录暴露在列表/搜索/zip/直链中。
func TestCacheDirNeverExposed(t *testing.T) {
	for _, hidden := range []bool{false, true} {
		srv := New(t.TempDir(), Options{Hidden: hidden})
		root := srv.root
		cache := filepath.Join(root, cacheDirName)
		// 模拟真实缓存布局
		os.MkdirAll(filepath.Join(cache, "hls"), 0o755)
		os.WriteFile(filepath.Join(cache, "hls", "index.m3u8"), []byte("#EXTM3U"), 0o644)
		os.WriteFile(filepath.Join(cache, "leak.txt"), []byte("cache-secret"), 0o644)
		// 用户自己的普通文件与隐藏目录（--hidden 开启时应正常可见）
		os.WriteFile(filepath.Join(root, "movie.mp4"), []byte("v"), 0o644)
		os.MkdirAll(filepath.Join(root, ".userdir"), 0o755)
		os.WriteFile(filepath.Join(root, ".userdir", "u.txt"), []byte("u"), 0o644)

		ts := httptest.NewServer(srv.Handler())
		t.Cleanup(ts.Close)
		base := ts.URL
		status := func(u string) int {
			r := get(t, u)
			io.Copy(io.Discard, r.Body)
			r.Body.Close()
			return r.StatusCode
		}

		// 根列表：不含缓存目录；普通文件恒在；用户隐藏目录按 --hidden 语义
		var lr ListResp
		resp := get(t, base+"/api/list?path=/")
		json.NewDecoder(resp.Body).Decode(&lr)
		resp.Body.Close()
		names := map[string]bool{}
		for _, e := range lr.Entries {
			names[e.Name] = true
		}
		if names[cacheDirName] {
			t.Errorf("hidden=%v: 根列表不应包含缓存目录 %s", hidden, cacheDirName)
		}
		if !names["movie.mp4"] {
			t.Errorf("hidden=%v: 普通文件应在列表中", hidden)
		}
		if names[".userdir"] == !hidden {
			t.Errorf("hidden=%v: 用户隐藏目录可见性错误（出现=%v）", hidden, names[".userdir"])
		}

		// 直接列缓存目录（含大小写变体）：404
		for _, p := range []string{"/" + cacheDirName, "/.fileserver", "/" + cacheDirName + "/hls"} {
			if code := status(base+"/api/list?path="+url.QueryEscape(p)); code != http.StatusNotFound {
				t.Errorf("hidden=%v: 列表缓存目录 %s 应 404, 得到 %d", hidden, p, code)
			}
		}

		// 直链/缩略图/抽帧源/播放决策/打包/搜索起点：全部拒绝
		// （prewarm 是 fire-and-forget 接口，被拦时按设计返回 204 且不做任何预热，
		// 拦截逻辑已由上方 hiddenBlocked 单元断言覆盖，不在此重复）
		checks := map[string]string{
			"/api/file?path=/.FileServer/leak.txt":       "直链",
			"/api/thumb?path=/.FileServer/leak.txt":      "缩略图",
			"/api/thumb-src?path=/.FileServer/leak.txt":  "抽帧源",
			"/api/video-info?path=/.FileServer/leak.txt": "播放决策",
			"/api/zip?path=/.FileServer":                 "打包",
			"/api/search?q=index&path=/.fileserver":      "搜索起点",
		}
		for u, what := range checks {
			if code := status(base + u); code != http.StatusNotFound {
				t.Errorf("hidden=%v: %s 访问缓存目录应 404, 得到 %d (%s)", hidden, what, code, u)
			}
		}

		// 全局搜索不泄漏缓存内容
		resp2 := get(t, base+"/api/search?q=index&path=/")
		var out struct {
			Results []SearchResult `json:"results"`
		}
		json.NewDecoder(resp2.Body).Decode(&out)
		resp2.Body.Close()
		for _, r := range out.Results {
			if isCacheEntry(filepath.Base(r.Path)) {
				t.Errorf("hidden=%v: 搜索结果不应包含缓存路径: %+v", hidden, r)
			}
		}
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
