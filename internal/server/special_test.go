package server

import (
	"bytes"
	"encoding/json"
	"fmt"
	"image"
	"image/color"
	"image/png"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

// specialNames 覆盖 URL 编码高危字符的文件/目录名
// 注: Windows 文件系统本身禁止 ? * " < > | 等字符，故不在此列
var specialNames = []string{
	"a#b%c+d&e f.txt",     // # % + & 空格
	"空格 和 中文.txt",      // 空格 + 中文
	"引号'单引号.txt",       // 单引号
	"括号(1)[2]{3}.txt",   // 括号
	"emoji🔥符号.txt",       // emoji
	"a=b&c=d%.txt",        // = & %
}

// TestSpecialCharsEndToEnd 特殊字符文件名/目录名的全链路：列表→缩略图→下载→zip→搜索
func TestSpecialCharsEndToEnd(t *testing.T) {
	srv := New(t.TempDir(), Options{})
	ts := httptest.NewServer(srv.Handler())
	defer ts.Close()

	// 构造特殊字符目录与文件
	dirName := "目录+%#& 空格"
	dir := filepath.Join(srv.root, dirName)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	for _, n := range specialNames {
		if err := os.WriteFile(filepath.Join(dir, n), []byte("content:"+n), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	// 列表：目录与文件名完整返回
	resp := get(t, ts.URL+"/api/list?path="+url.QueryEscape("/"+dirName))
	var lr ListResp
	if err := json.NewDecoder(resp.Body).Decode(&lr); err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	got := map[string]bool{}
	for _, e := range lr.Entries {
		got[e.Name] = true
	}
	for _, n := range specialNames {
		if !got[n] {
			t.Errorf("特殊字符文件名缺失: %q", n)
		}
	}

	// 每个文件：下载内容一致
	for _, n := range specialNames {
		p := "/" + dirName + "/" + n
		r := get(t, ts.URL+"/api/file?path="+url.QueryEscape(p))
		body, _ := io.ReadAll(r.Body)
		r.Body.Close()
		if r.StatusCode != 200 || string(body) != "content:"+n {
			t.Errorf("特殊字符文件下载失败 %q: %d %q", n, r.StatusCode, body)
		}
	}

	// 搜索：特殊字符关键字
	for _, n := range specialNames {
		q := strings.TrimSuffix(n, ".txt")
		r := get(t, ts.URL+"/api/search?q="+url.QueryEscape(q)+"&path="+url.QueryEscape("/"))
		var out struct {
			Results []SearchResult `json:"results"`
		}
		if err := json.NewDecoder(r.Body).Decode(&out); err != nil {
			t.Fatal(err)
		}
		r.Body.Close()
		if len(out.Results) == 0 {
			t.Errorf("特殊字符搜索无结果: %q", q)
		}
	}

	// zip 打包特殊字符目录
	zr := get(t, ts.URL+"/api/zip?path="+url.QueryEscape("/"+dirName))
	zdata, _ := io.ReadAll(zr.Body)
	zr.Body.Close()
	if zr.StatusCode != 200 {
		t.Fatalf("zip 失败: %d", zr.StatusCode)
	}
	zfiles, err := zipNewReader(zdata)
	if err != nil {
		t.Fatal(err)
	}
	found := map[string]bool{}
	for _, f := range zfiles {
		found[f.Name] = true
	}
	for _, n := range specialNames {
		if !found[dirName+"/"+n] {
			t.Errorf("zip 中缺少特殊字符文件: %q", n)
		}
	}
}

// TestURLRoundTrip 编码往返无损：任意文件名经 QueryEscape/QueryUnescape 必须原样还原
func TestURLRoundTrip(t *testing.T) {
	names := append([]string{}, specialNames...)
	names = append(names, "目录+%#& 空格", "/a/b/深层 路径/文件#1%.txt")
	for _, n := range names {
		enc := url.QueryEscape(n)
		dec, err := url.QueryUnescape(enc)
		if err != nil {
			t.Errorf("解码失败 %q: %v", n, err)
			continue
		}
		if dec != n {
			t.Errorf("编码往返不一致: %q -> %q -> %q", n, enc, dec)
		}
		// 前端等价编码（encodeURIComponent 对空格用 %20，QueryEscape 用 +，需等价）
		jsLike := strings.ReplaceAll(enc, "+", "%20")
		dec2, err := url.QueryUnescape(jsLike)
		if err != nil || dec2 != n {
			t.Errorf("JS 风格编码往返不一致: %q -> %q -> %q", n, jsLike, dec2)
		}
	}
}

// TestThumbConcurrent 并发请求同一缩略图：全部成功且内容一致（singleflight 生效）
func TestThumbConcurrent(t *testing.T) {
	srv := New(t.TempDir(), Options{})
	ts := httptest.NewServer(srv.Handler())
	defer ts.Close()

	// 生成一张 800x600 PNG
	img := image.NewRGBA(image.Rect(0, 0, 800, 600))
	for y := 0; y < 600; y++ {
		for x := 0; x < 800; x++ {
			img.Set(x, y, color.RGBA{R: uint8(x % 256), G: uint8(y % 256), B: 128, A: 255})
		}
	}
	var enc bytes.Buffer
	if err := png.Encode(&enc, img); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(srv.root, "pic.png"), enc.Bytes(), 0o644); err != nil {
		t.Fatal(err)
	}

	// 10 个并发请求同一缩略图
	const n = 10
	results := make([]string, n)
	var wg sync.WaitGroup
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			r, err := http.Get(ts.URL + "/api/thumb?path=" + url.QueryEscape("/pic.png") + "&w=256&h=256")
			if err != nil {
				results[idx] = "ERR:" + err.Error()
				return
			}
			body, _ := io.ReadAll(r.Body)
			r.Body.Close()
			results[idx] = fmt.Sprintf("%d:%d", r.StatusCode, len(body))
		}(i)
	}
	wg.Wait()

	first := results[0]
	for i, r := range results {
		if r != first {
			t.Errorf("并发缩略图结果不一致: #%d=%s vs #0=%s", i, r, first)
		}
	}
	if !strings.HasPrefix(first, "200:") {
		t.Fatalf("并发缩略图失败: %v", results)
	}
}

// TestThumbConcurrentDifferent 并发请求不同缩略图：全部成功（并发执行无阻塞）
func TestThumbConcurrentDifferent(t *testing.T) {
	srv := New(t.TempDir(), Options{})
	ts := httptest.NewServer(srv.Handler())
	defer ts.Close()

	// 生成 5 张不同图片
	for i := 0; i < 5; i++ {
		img := image.NewRGBA(image.Rect(0, 0, 600, 400))
		for y := 0; y < 400; y++ {
			for x := 0; x < 600; x++ {
				img.Set(x, y, color.RGBA{R: uint8(i * 40), G: uint8(x % 256), B: uint8(y % 256), A: 255})
			}
		}
		var enc bytes.Buffer
		png.Encode(&enc, img)
		os.WriteFile(filepath.Join(srv.root, fmt.Sprintf("pic%d.png", i)), enc.Bytes(), 0o644)
	}

	var wg sync.WaitGroup
	errs := make([]error, 5)
	for i := 0; i < 5; i++ {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			r, err := http.Get(ts.URL + "/api/thumb?path=" + url.QueryEscape(fmt.Sprintf("/pic%d.png", idx)) + "&w=256&h=256")
			if err != nil {
				errs[idx] = err
				return
			}
			r.Body.Close()
			if r.StatusCode != 200 {
				errs[idx] = fmt.Errorf("status %d", r.StatusCode)
			}
		}(i)
	}
	wg.Wait()
	for i, err := range errs {
		if err != nil {
			t.Errorf("并发不同缩略图失败 #%d: %v", i, err)
		}
	}
}
