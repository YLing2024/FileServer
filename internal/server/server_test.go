package server

import (
	"archive/zip"
	"bytes"
	"encoding/json"
	"image"
	"image/color"
	"image/jpeg"
	"image/png"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func zipNewReader(data []byte) ([]*zip.File, error) {
	zr, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		return nil, err
	}
	return zr.File, nil
}

func newTestHTTPServer(t *testing.T) (*httptest.Server, *Server, string) {
	t.Helper()
	srv, root := newTestServer(t)
	ts := httptest.NewServer(srv.Handler())
	t.Cleanup(ts.Close)
	return ts, srv, root
}

func get(t *testing.T, url string) *http.Response {
	t.Helper()
	resp, err := http.Get(url)
	if err != nil {
		t.Fatal(err)
	}
	return resp
}

func TestAPIList(t *testing.T) {
	ts, _, _ := newTestHTTPServer(t)
	resp := get(t, ts.URL+"/api/list?path=/")
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		t.Fatalf("状态码 %d", resp.StatusCode)
	}
	var lr ListResp
	if err := json.NewDecoder(resp.Body).Decode(&lr); err != nil {
		t.Fatal(err)
	}
	names := map[string]bool{}
	for _, e := range lr.Entries {
		names[e.Name] = true
	}
	if !names["a.txt"] || !names["dir1"] || !names["中文 文件.txt"] {
		t.Fatalf("列表缺少条目: %v", names)
	}
	// 子目录列表
	resp2 := get(t, ts.URL+"/api/list?path=/dir1")
	defer resp2.Body.Close()
	var lr2 ListResp
	json.NewDecoder(resp2.Body).Decode(&lr2)
	if len(lr2.Entries) != 2 { // b.txt + sub
		t.Fatalf("子目录条目数错误: %d", len(lr2.Entries))
	}
}

func TestAPIPathTraversal(t *testing.T) {
	ts, _, _ := newTestHTTPServer(t)
	for _, p := range []string{"../a.txt", "..%2fa.txt", "dir1/../../a.txt", `C:\Windows\win.ini`, "/etc/passwd"} {
		resp := get(t, ts.URL+"/api/list?path="+p)
		resp.Body.Close()
		if resp.StatusCode != http.StatusForbidden && resp.StatusCode != http.StatusNotFound {
			t.Errorf("路径 %q 应被拒绝, 得到 %d", p, resp.StatusCode)
		}
	}
}

func TestAPIFileAndRange(t *testing.T) {
	ts, _, root := newTestHTTPServer(t)
	path := filepath.Join(root, "a.txt")
	os.WriteFile(path, []byte("0123456789"), 0o644)

	// 完整下载
	resp := get(t, ts.URL+"/api/file?path=/a.txt")
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != 200 || string(body) != "0123456789" {
		t.Fatalf("下载失败: %d %q", resp.StatusCode, body)
	}
	if ct := resp.Header.Get("Content-Disposition"); !strings.Contains(ct, "inline") {
		t.Errorf("文本应 inline 预览: %q", ct)
	}

	// Range 请求
	req, _ := http.NewRequest("GET", ts.URL+"/api/file?path=/a.txt", nil)
	req.Header.Set("Range", "bytes=2-5")
	resp2, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	body2, _ := io.ReadAll(resp2.Body)
	resp2.Body.Close()
	if resp2.StatusCode != 206 || string(body2) != "2345" {
		t.Fatalf("Range 失败: %d %q", resp2.StatusCode, body2)
	}

	// 目录拒绝下载
	resp3 := get(t, ts.URL+"/api/file?path=/dir1")
	resp3.Body.Close()
	if resp3.StatusCode != 400 {
		t.Errorf("目录下载应拒绝, 得到 %d", resp3.StatusCode)
	}

	// 中文文件名下载
	resp4 := get(t, ts.URL+"/api/file?path=/中文%20文件.txt")
	body4, _ := io.ReadAll(resp4.Body)
	resp4.Body.Close()
	if string(body4) != "hello 中文 文件.txt" {
		t.Errorf("中文文件名下载失败: %q", body4)
	}
}

func TestAPIThumb(t *testing.T) {
	ts, _, root := newTestHTTPServer(t)
	// 生成一张 800x600 的 PNG
	img := image.NewRGBA(image.Rect(0, 0, 800, 600))
	for y := 0; y < 600; y++ {
		for x := 0; x < 800; x++ {
			img.Set(x, y, color.RGBA{R: uint8(x % 256), G: uint8(y % 256), B: 128, A: 255})
		}
	}
	var buf bytes.Buffer
	png.Encode(&buf, img)
	os.WriteFile(filepath.Join(root, "pic.png"), buf.Bytes(), 0o644)

	resp := get(t, ts.URL+"/api/thumb?path=/pic.png&w=256&h=256")
	data, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != 200 {
		t.Fatalf("缩略图失败: %d", resp.StatusCode)
	}
	thumb, err := jpeg.Decode(bytes.NewReader(data))
	if err != nil {
		t.Fatalf("缩略图不是有效 JPEG: %v", err)
	}
	b := thumb.Bounds()
	if b.Dx() != 256 || b.Dy() != 256 {
		t.Fatalf("缩略图尺寸 %dx%d, 期望 256x256", b.Dx(), b.Dy())
	}
	etag := resp.Header.Get("ETag")
	if etag == "" {
		t.Fatal("缺少 ETag")
	}

	// 304 缓存命中
	req, _ := http.NewRequest("GET", ts.URL+"/api/thumb?path=/pic.png&w=256&h=256", nil)
	req.Header.Set("If-None-Match", etag)
	resp2, _ := http.DefaultClient.Do(req)
	resp2.Body.Close()
	if resp2.StatusCode != 304 {
		t.Errorf("304 未命中: %d", resp2.StatusCode)
	}

	// 小图直接返回原图
	small := image.NewRGBA(image.Rect(0, 0, 100, 80))
	var buf2 bytes.Buffer
	png.Encode(&buf2, small)
	os.WriteFile(filepath.Join(root, "small.png"), buf2.Bytes(), 0o644)
	resp3 := get(t, ts.URL+"/api/thumb?path=/small.png&w=256&h=256")
	body3, _ := io.ReadAll(resp3.Body)
	resp3.Body.Close()
	if resp3.StatusCode != 200 || len(body3) == 0 {
		t.Errorf("小图缩略失败")
	}

	// 非图片 404
	resp4 := get(t, ts.URL+"/api/thumb?path=/a.txt")
	resp4.Body.Close()
	if resp4.StatusCode != 404 {
		t.Errorf("文本应无缩略图, 得到 %d", resp4.StatusCode)
	}
}

func TestAPIZip(t *testing.T) {
	ts, _, _ := newTestHTTPServer(t)
	resp := get(t, ts.URL+"/api/zip?path=/dir1")
	data, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != 200 {
		t.Fatalf("zip 失败: %d", resp.StatusCode)
	}
	// 解包验证内容
	zr, err := zipNewReader(data)
	if err != nil {
		t.Fatal(err)
	}
	found := map[string]bool{}
	for _, f := range zr {
		found[f.Name] = true
	}
	if !found["dir1/b.txt"] || !found["dir1/sub/c.txt"] {
		t.Fatalf("zip 内容缺失: %v", found)
	}
}

func TestAPISearch(t *testing.T) {
	ts, _, _ := newTestHTTPServer(t)
	resp := get(t, ts.URL+"/api/search?q=b.txt&path=/")
	defer resp.Body.Close()
	var out struct {
		Results []SearchResult `json:"results"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		t.Fatal(err)
	}
	if len(out.Results) != 1 || out.Results[0].Name != "b.txt" {
		t.Fatalf("搜索结果错误: %+v", out.Results)
	}
	// 空关键字 400
	resp2 := get(t, ts.URL+"/api/search?q=&path=/")
	resp2.Body.Close()
	if resp2.StatusCode != 400 {
		t.Errorf("空关键字应 400, 得到 %d", resp2.StatusCode)
	}
}

func TestAPIAuth(t *testing.T) {
	srv := New(t.TempDir(), Options{Auth: "admin:secret"})
	ts := httptest.NewServer(srv.Handler())
	defer ts.Close()

	resp := get(t, ts.URL+"/api/list")
	resp.Body.Close()
	if resp.StatusCode != 401 {
		t.Fatalf("无凭证应 401, 得到 %d", resp.StatusCode)
	}
	req, _ := http.NewRequest("GET", ts.URL+"/api/list", nil)
	req.SetBasicAuth("admin", "secret")
	resp2, _ := http.DefaultClient.Do(req)
	resp2.Body.Close()
	if resp2.StatusCode != 200 {
		t.Fatalf("正确凭证应 200, 得到 %d", resp2.StatusCode)
	}
}

func TestAPIFrontend(t *testing.T) {
	srv := New(t.TempDir(), Options{})
	ts := httptest.NewServer(srv.Handler())
	defer ts.Close()
	resp := get(t, ts.URL+"/")
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != 200 || !strings.Contains(string(body), "html") {
		t.Fatalf("前端页面异常: %d", resp.StatusCode)
	}
	// 静态资源可访问
	for _, p := range []string{"/style.css", "/app.js"} {
		r := get(t, ts.URL+p)
		r.Body.Close()
		if r.StatusCode != 200 {
			t.Fatalf("静态资源 %s 异常: %d", p, r.StatusCode)
		}
	}
}
