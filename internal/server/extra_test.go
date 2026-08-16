package server

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

// TestHiddenConsistency --hidden=false（默认）时，隐藏文件/目录在
// list/file/zip/thumb 上行为一致（1.2、5.2）
func TestHiddenConsistency(t *testing.T) {
	srv := New(t.TempDir(), Options{}) // hidden=false
	root := srv.root
	os.WriteFile(filepath.Join(root, ".secret"), []byte("s"), 0o644)
	os.WriteFile(filepath.Join(root, ".git", "config"), []byte("git"), 0o644)
	os.MkdirAll(filepath.Join(root, ".git"), 0o755)
	os.WriteFile(filepath.Join(root, ".git", "config"), []byte("git"), 0o644)
	os.WriteFile(filepath.Join(root, "ok.txt"), []byte("ok"), 0o644)
	ts := httptest.NewServer(srv.Handler())
	defer ts.Close()

	// 列表不显示隐藏条目
	var lr ListResp
	resp := get(t, ts.URL+"/api/list?path=/")
	if err := json.NewDecoder(resp.Body).Decode(&lr); err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	for _, e := range lr.Entries {
		if strings.HasPrefix(e.Name, ".") {
			t.Fatalf("列表不应显示隐藏条目: %q", e.Name)
		}
	}

	// 直链下载隐藏文件/隐藏目录内文件：404
	for _, p := range []string{"/.secret", "/.git/config"} {
		r := get(t, ts.URL+"/api/file?path="+url.QueryEscape(p))
		r.Body.Close()
		if r.StatusCode != http.StatusNotFound {
			t.Errorf("隐藏文件 %s 应 404, 得到 %d", p, r.StatusCode)
		}
	}

	// 缩略图：隐藏文件 404
	r := get(t, ts.URL+"/api/thumb?path="+url.QueryEscape("/.secret"))
	r.Body.Close()
	if r.StatusCode != http.StatusNotFound {
		t.Errorf("隐藏文件缩略图应 404, 得到 %d", r.StatusCode)
	}

	// zip 打包根目录：不含隐藏条目
	zr := get(t, ts.URL+"/api/zip?path=/")
	zdata, _ := io.ReadAll(zr.Body)
	zr.Body.Close()
	zfiles, err := zipNewReader(zdata)
	if err != nil {
		t.Fatal(err)
	}
	for _, f := range zfiles {
		if strings.HasPrefix(filepath.Base(f.Name), ".") {
			t.Fatalf("zip 不应包含隐藏条目: %q", f.Name)
		}
	}

	// 打包隐藏目录本身：拒绝
	zr2 := get(t, ts.URL+"/api/zip?path="+url.QueryEscape("/.git"))
	zr2.Body.Close()
	if zr2.StatusCode != http.StatusNotFound {
		t.Errorf("打包隐藏目录应 404, 得到 %d", zr2.StatusCode)
	}
}

// TestHiddenEnabled --hidden=true 时隐藏文件应正常可见/可下载（5.2）
func TestHiddenEnabled(t *testing.T) {
	srv := New(t.TempDir(), Options{Hidden: true})
	os.WriteFile(filepath.Join(srv.root, ".secret"), []byte("s"), 0o644)
	ts := httptest.NewServer(srv.Handler())
	defer ts.Close()

	var lr ListResp
	resp := get(t, ts.URL+"/api/list?path=/")
	if err := json.NewDecoder(resp.Body).Decode(&lr); err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	found := false
	for _, e := range lr.Entries {
		if e.Name == ".secret" {
			found = true
		}
	}
	if !found {
		t.Fatal("--hidden 开启后列表应显示隐藏文件")
	}
	r := get(t, ts.URL+"/api/file?path="+url.QueryEscape("/.secret"))
	body, _ := io.ReadAll(r.Body)
	r.Body.Close()
	if r.StatusCode != 200 || string(body) != "s" {
		t.Fatalf("--hidden 开启后直链应可下载: %d %q", r.StatusCode, body)
	}
}

// TestThumbCacheDoPanic 生成函数 panic 时等待者必须被唤醒而非永久阻塞（2.4、5.3）
func TestThumbCacheDoPanic(t *testing.T) {
	c := NewThumbCache(t.TempDir())
	key := "panic-key"
	const waiters = 4

	release := make(chan struct{}) // 触发生成函数 panic
	done := make(chan struct{}, waiters)
	results := make([]bool, waiters)

	// 生成者：fn 阻塞直到 release，然后 panic
	go func() {
		c.Do(key, func() ([]byte, bool) {
			<-release
			panic("boom")
		})
	}()

	// 等待生成者完成注册（calls 中出现 key，随后其 fn 阻塞在 release 上）
	deadline := time.Now().Add(2 * time.Second)
	for {
		c.genMu.Lock()
		_, registered := c.calls[key]
		c.genMu.Unlock()
		if registered {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("生成者未注册")
		}
		time.Sleep(time.Millisecond)
	}

	// 等待者：生成者 key 尚未删除，它们只可能成为「真正等待者」而非新生成者，
	// 因此绝不可能在 release 之前完成
	for i := 0; i < waiters; i++ {
		go func(idx int) {
			_, results[idx] = c.Do(key, func() ([]byte, bool) { return []byte("x"), true })
			done <- struct{}{}
		}(i)
	}
	select {
	case <-done:
		t.Fatal("等待者在生成者结束前不应完成（应为等待者而非新生成者）")
	case <-time.After(300 * time.Millisecond):
	}

	close(release) // 触发 panic

	// 所有等待者必须被唤醒并返回失败结果
	for i := 0; i < waiters; i++ {
		select {
		case <-done:
		case <-time.After(5 * time.Second):
			t.Fatal("panic 后等待者仍被阻塞（singleflight 泄漏）")
		}
	}
	for i, ok := range results {
		if ok {
			t.Errorf("调用者 #%d 应得到失败结果", i)
		}
	}
	// key 不应残留在 calls 中（后续请求可重新生成）
	c.genMu.Lock()
	_, leaked := c.calls[key]
	c.genMu.Unlock()
	if leaked {
		t.Error("panic 后 key 残留在 calls 中")
	}
}

// TestThumbCacheLRU 内存缓存按最近使用淘汰（2.10、5.5）
func TestThumbCacheLRU(t *testing.T) {
	c := NewThumbCache(t.TempDir())
	c.max = 3
	c.Put("a", []byte("a"))
	c.Put("b", []byte("b"))
	c.Put("c", []byte("c"))
	// 触达 a（移到最近），淘汰时 a 应保留
	if _, ok := c.Get("a"); !ok {
		t.Fatal("a 应在缓存中")
	}
	c.Put("d", []byte("d"))
	if _, ok := c.mem["a"]; !ok {
		t.Error("LRU: a 刚被触达不应被淘汰")
	}
	if _, ok := c.mem["b"]; ok {
		t.Error("LRU: b 应是最久未使用被淘汰")
	}
	// 再次触达 c，然后淘汰应淘汰最久未使用的 a
	// （当前顺序 [a,d,c]，Put e 时 a 应被淘汰）
	if _, ok := c.Get("c"); !ok {
		t.Fatal("c 应在缓存中")
	}
	c.Put("e", []byte("e"))
	if _, ok := c.mem["a"]; ok {
		t.Error("LRU: a 应是最久未使用被淘汰")
	}
	if _, ok := c.mem["d"]; !ok {
		t.Error("LRU: d 不应被淘汰")
	}
	if _, ok := c.mem["c"]; !ok {
		t.Error("LRU: c 刚触达不应被淘汰")
	}
}

// TestThumbCacheCleanupTmp 清理 .tmp 残留与过期缓存（2.8、5.5）
func TestThumbCacheCleanupTmp(t *testing.T) {
	c := NewThumbCache(t.TempDir())
	old := time.Now().Add(-10 * 24 * time.Hour) // 超过 7 天清理阈值
	fresh := time.Now()

	write := func(name string, mt time.Time) {
		p := filepath.Join(c.dir, name)
		if err := os.WriteFile(p, []byte("x"), 0o644); err != nil {
			t.Fatal(err)
		}
		os.Chtimes(p, mt, mt)
	}

	// 缓存文件名为 40 位 SHA1 + ".jpg"（共 44 字符）
	oldCache := strings.Repeat("a", 40) + ".jpg"
	freshCache := strings.Repeat("b", 40) + ".jpg"
	write(oldCache, old) // 过期缓存
	write(freshCache, fresh) // 新鲜缓存
	write("stale.tmp", old) // 过期 .tmp 残留
	write("fresh.tmp", fresh)
	write("short.tmp", old) // 任意 .tmp 残留（不以 .jpg 结尾）
	c.cleanup()

	if _, err := os.Stat(filepath.Join(c.dir, oldCache)); err == nil {
		t.Error("过期缓存应被清理")
	}
	if _, err := os.Stat(filepath.Join(c.dir, freshCache)); err != nil {
		t.Error("新鲜缓存不应被清理")
	}
	if _, err := os.Stat(filepath.Join(c.dir, "stale.tmp")); err == nil {
		t.Error("过期 .tmp 残留应被清理")
	}
	if _, err := os.Stat(filepath.Join(c.dir, "fresh.tmp")); err != nil {
		t.Error("新鲜 .tmp 不应被清理")
	}
}

// TestListPagination 服务端分页与 truncated 标志（3.1、5.4）
func TestListPagination(t *testing.T) {
	srv := New(t.TempDir(), Options{})
	root := srv.root
	const n = 1050
	for i := 0; i < n; i++ {
		os.WriteFile(filepath.Join(root, "f"+pad4(i)), []byte("x"), 0o644)
	}
	ts := httptest.NewServer(srv.Handler())
	defer ts.Close()

	// 分页：每页 300
	var lr ListResp
	resp := get(t, ts.URL+"/api/list?path=/&limit=300&offset=0")
	if err := json.NewDecoder(resp.Body).Decode(&lr); err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if len(lr.Entries) != 300 {
		t.Fatalf("第一页应有 300 条, 得到 %d", len(lr.Entries))
	}
	if !lr.Truncated {
		t.Error("超过一页应 truncated=true")
	}
	if lr.Total != n {
		t.Errorf("Total 应为 %d, 得到 %d", n, lr.Total)
	}

	// 第二页 offset=300
	resp2 := get(t, ts.URL+"/api/list?path=/&limit=300&offset=300")
	var lr2 ListResp
	if err := json.NewDecoder(resp2.Body).Decode(&lr2); err != nil {
		t.Fatal(err)
	}
	resp2.Body.Close()
	if len(lr2.Entries) != 300 {
		t.Fatalf("第二页应有 300 条, 得到 %d", len(lr2.Entries))
	}
	// 两页不重叠
	first := lr.Entries[0].Name
	for _, e := range lr2.Entries {
		if e.Name == first {
			t.Fatal("分页出现重叠")
		}
	}

	// 最后一页
	lastResp := get(t, ts.URL+"/api/list?path=/&limit=300&offset=900")
	var lr3 ListResp
	if err := json.NewDecoder(lastResp.Body).Decode(&lr3); err != nil {
		t.Fatal(err)
	}
	lastResp.Body.Close()
	if len(lr3.Entries) != 150 {
		t.Fatalf("最后一页应有 150 条, 得到 %d", len(lr3.Entries))
	}
	if lr3.Truncated {
		t.Error("最后一页不应 truncated")
	}

	// 超上限的 limit 被截断到文件总数（1050 < listMaxLimit）
	resp4 := get(t, ts.URL+"/api/list?path=/&limit=99999")
	var lr4 ListResp
	json.NewDecoder(resp4.Body).Decode(&lr4)
	resp4.Body.Close()
	if len(lr4.Entries) != n {
		t.Errorf("limit 应返回全部 %d 条, 得到 %d", n, len(lr4.Entries))
	}
	if lr4.Truncated {
		t.Error("返回全部条目时不应 truncated")
	}
}

// pad4 0 填充到 4 位（如 7 → "0007"），保证文件名排序稳定
func pad4(i int) string {
	s := intString(i)
	for len(s) < 4 {
		s = "0" + s
	}
	return s
}

func intString(i int) string {
	return itoa(i)
}

func itoa(i int) string {
	if i == 0 {
		return "0"
	}
	var b [16]byte
	pos := len(b)
	for i > 0 {
		pos--
		b[pos] = byte('0' + i%10)
		i /= 10
	}
	return string(b[pos:])
}

// TestSearchBoundaries 搜索深度上限与 limit 截断（5.4）
func TestSearchBoundaries(t *testing.T) {
	srv := New(t.TempDir(), Options{})
	root := srv.root
	// 深度 9（可搜索）与深度 11（超限）
	deepOK := root
	for i := 0; i < 9; i++ {
		deepOK = filepath.Join(deepOK, "d")
	}
	os.MkdirAll(deepOK, 0o755)
	os.WriteFile(filepath.Join(deepOK, "needle.txt"), []byte("x"), 0o644)

	deepSkip := root
	for i := 0; i < 11; i++ {
		deepSkip = filepath.Join(deepSkip, "e")
	}
	os.MkdirAll(deepSkip, 0o755)
	os.WriteFile(filepath.Join(deepSkip, "needle.txt"), []byte("x"), 0o644)

	// 同层大量命中文件
	for i := 0; i < 10; i++ {
		os.WriteFile(filepath.Join(root, "needle"+intString(i)+".txt"), []byte("x"), 0o644)
	}

	ts := httptest.NewServer(srv.Handler())
	defer ts.Close()

	// 深度 11 的结果不应出现
	var out struct {
		Results []SearchResult `json:"results"`
	}
	resp := get(t, ts.URL+"/api/search?q=needle&path=/")
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	for _, r := range out.Results {
		if strings.Count(r.Path, "/") > 10 {
			t.Errorf("超过深度上限的结果不应出现: %s", r.Path)
		}
	}
	// 至少应包含浅层命中
	if len(out.Results) < 10 {
		t.Errorf("浅层命中文件未全部返回: %d", len(out.Results))
	}

	// limit 截断 + truncated 标志
	resp2 := get(t, ts.URL+"/api/search?q=needle&path=/&limit=2")
	var out2 struct {
		Results   []SearchResult `json:"results"`
		Truncated bool           `json:"truncated"`
	}
	if err := json.NewDecoder(resp2.Body).Decode(&out2); err != nil {
		t.Fatal(err)
	}
	resp2.Body.Close()
	if len(out2.Results) != 2 {
		t.Errorf("limit=2 应只返回 2 条, 得到 %d", len(out2.Results))
	}
	if !out2.Truncated {
		t.Error("结果超过 limit 应 truncated=true")
	}
}

// TestAuthPasswordColon pass 含 : 的认证（1.4、5.5）
func TestAuthPasswordColon(t *testing.T) {
	srv := New(t.TempDir(), Options{Auth: "admin:pa:ss"})
	ts := httptest.NewServer(srv.Handler())
	defer ts.Close()

	// 无凭证：401 且带 WWW-Authenticate
	resp := get(t, ts.URL+"/api/list")
	resp.Body.Close()
	if resp.StatusCode != 401 {
		t.Fatalf("应 401, 得到 %d", resp.StatusCode)
	}
	if resp.Header.Get("WWW-Authenticate") == "" {
		t.Error("401 应带 WWW-Authenticate 头")
	}

	// 错误口令：401
	req, _ := http.NewRequest("GET", ts.URL+"/api/list", nil)
	req.SetBasicAuth("admin", "wrong")
	resp2, _ := http.DefaultClient.Do(req)
	resp2.Body.Close()
	if resp2.StatusCode != 401 {
		t.Fatalf("错误口令应 401, 得到 %d", resp2.StatusCode)
	}

	// 正确口令（pass 含 :）：200
	req3, _ := http.NewRequest("GET", ts.URL+"/api/list", nil)
	req3.SetBasicAuth("admin", "pa:ss")
	resp3, _ := http.DefaultClient.Do(req3)
	resp3.Body.Close()
	if resp3.StatusCode != 200 {
		t.Fatalf("正确口令（含 :）应 200, 得到 %d", resp3.StatusCode)
	}
}

// TestFileBoundaryRanges 文件边界：空文件、超尾 Range、多段 Range、HEAD（5.5）
func TestFileBoundaryRanges(t *testing.T) {
	srv := New(t.TempDir(), Options{})
	root := srv.root
	os.WriteFile(filepath.Join(root, "empty.txt"), []byte{}, 0o644)
	os.WriteFile(filepath.Join(root, "ten.txt"), []byte("0123456789"), 0o644)
	ts := httptest.NewServer(srv.Handler())
	defer ts.Close()

	// 空文件：无 Range 请求 200
	r := get(t, ts.URL+"/api/file?path=/empty.txt")
	r.Body.Close()
	if r.StatusCode != 200 {
		t.Errorf("空文件应 200, 得到 %d", r.StatusCode)
	}
	// 空文件带 Range：Go ServeContent 对空文件忽略 Range，返回 200
	req, _ := http.NewRequest("GET", ts.URL+"/api/file?path=/empty.txt", nil)
	req.Header.Set("Range", "bytes=0-")
	r2, _ := http.DefaultClient.Do(req)
	r2.Body.Close()
	if r2.StatusCode != 200 {
		t.Errorf("空文件 Range 应 200, 得到 %d", r2.StatusCode)
	}

	// 超尾 Range：416
	req3, _ := http.NewRequest("GET", ts.URL+"/api/file?path=/ten.txt", nil)
	req3.Header.Set("Range", "bytes=999999-")
	r3, _ := http.DefaultClient.Do(req3)
	r3.Body.Close()
	if r3.StatusCode != 416 {
		t.Errorf("超尾 Range 应 416, 得到 %d", r3.StatusCode)
	}

	// 多段 Range：Go ServeContent 返回 multipart/byteranges 206，各段内容正确
	req4, _ := http.NewRequest("GET", ts.URL+"/api/file?path=/ten.txt", nil)
	req4.Header.Set("Range", "bytes=0-1,5-9")
	r4, _ := http.DefaultClient.Do(req4)
	body4, _ := io.ReadAll(r4.Body)
	r4.Body.Close()
	if r4.StatusCode != 206 {
		t.Errorf("多段 Range 应 206, 得到 %d", r4.StatusCode)
	}
	if ct := r4.Header.Get("Content-Type"); !strings.HasPrefix(ct, "multipart/byteranges") {
		t.Errorf("多段 Range 应为 multipart/byteranges: %q", ct)
	}
	if !bytes.Contains(body4, []byte("01")) || !bytes.Contains(body4, []byte("56789")) {
		t.Errorf("多段 Range 内容缺失: %q", body4)
	}

	// HEAD：200 且有 Content-Length，无 body
	req5, _ := http.NewRequest("HEAD", ts.URL+"/api/file?path=/ten.txt", nil)
	r5, _ := http.DefaultClient.Do(req5)
	body5, _ := io.ReadAll(r5.Body)
	r5.Body.Close()
	if r5.StatusCode != 200 {
		t.Errorf("HEAD 应 200, 得到 %d", r5.StatusCode)
	}
	if len(body5) != 0 {
		t.Errorf("HEAD 不应有 body, 得到 %d 字节", len(body5))
	}
}

// TestZipConcurrent 大目录 zip 与列表并发（5.5）
func TestZipConcurrent(t *testing.T) {
	srv := New(t.TempDir(), Options{})
	for i := 0; i < 50; i++ {
		os.WriteFile(filepath.Join(srv.root, "file"+intString(i)+".txt"), bytes.Repeat([]byte("data"), 100), 0o644)
	}
	ts := httptest.NewServer(srv.Handler())
	defer ts.Close()

	var wg sync.WaitGroup
	errs := make([]error, 8)
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			url := ts.URL + "/api/zip?path=/"
			if idx%2 == 1 {
				url = ts.URL + "/api/list?path=/"
			}
			r, err := http.Get(url)
			if err != nil {
				errs[idx] = err
				return
			}
			defer r.Body.Close()
			if r.StatusCode != 200 {
				errs[idx] = &httpError{r.StatusCode}
			}
		}(i)
	}
	wg.Wait()
	for i, err := range errs {
		if err != nil {
			t.Errorf("并发 #%d 失败: %v", i, err)
		}
	}
}

type httpError struct{ code int }

func (e *httpError) Error() string { return "http status " + intString(e.code) }
