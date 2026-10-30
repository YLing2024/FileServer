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
	"strconv"
	"strings"
	"testing"
	"time"
)

// newChunkTS 构造允许上传、上限 16 MiB 的测试服务器。
func newChunkTS(t *testing.T) (*httptest.Server, *Server, string, string) {
	t.Helper()
	root := t.TempDir()
	srv := New(root, Options{AuthMode: "password", Auth: "user:secretpass", AllowUpload: true, MaxUploadBytes: 16 << 20})
	if srv.cfg.auth == nil {
		t.Fatal("认证未初始化")
	}
	token, _, err := srv.cfg.auth.Login("secretpass", "", true, "chunk-test", "127.0.0.1")
	if err != nil {
		t.Fatalf("登录失败: %v", err)
	}
	ts := httptest.NewServer(srv.Handler())
	t.Cleanup(func() {
		ts.Close()
		srv.Close()
	})
	return ts, srv, root, token
}

// chunkRequest 发送一个原始分片。
func chunkRequest(t *testing.T, base, token, id, name, dir string, index, total int, size int64, data []byte) (*http.Response, []byte) {
	t.Helper()
	q := url.Values{}
	q.Set("upload_id", id)
	q.Set("index", strconv.Itoa(index))
	q.Set("total", strconv.Itoa(total))
	q.Set("size", strconv.FormatInt(size, 10))
	q.Set("name", name)
	q.Set("dir", dir)
	req, err := http.NewRequest(http.MethodPost, base+"/api/upload/chunk?"+q.Encode(), bytes.NewReader(data))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Content-Type", "application/octet-stream")
	req.Header.Set("Authorization", "Bearer "+token)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	raw, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	return resp, raw
}

// completeRequest 发送合并请求。
func completeRequest(t *testing.T, base, token, body string) (*http.Response, []byte) {
	t.Helper()
	req, err := http.NewRequest(http.MethodPost, base+"/api/upload/complete", strings.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+token)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	raw, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	return resp, raw
}

// TestChunkUploadMergeOrderAndResume 分片乱序到达仍按索引合并，status 返回已收索引。
func TestChunkUploadMergeOrderAndResume(t *testing.T) {
	ts, srv, root, token := newChunkTS(t)
	content := []byte("0123456789abcdefghijklmnopqrstuvwxyz-分片合并测试")
	const name = "merge.bin"
	id := deriveUploadID(content, int64(len(content)), name)
	parts := [][]byte{content[0:17], content[17:41], content[41:]}
	total := len(parts)

	// 乱序：先发最后一片，再发第一片。
	for _, idx := range []int{2, 0} {
		resp, raw := chunkRequest(t, ts.URL, token, id, name, "/", idx, total, int64(len(content)), parts[idx])
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("分片 %d 应 200，得到 %d (%s)", idx, resp.StatusCode, raw)
		}
	}

	// status 只报告已收到的索引。
	resp := apiGet(t, ts.URL+"/api/upload/status?upload_id="+id, token)
	var st struct {
		Received []int `json:"received"`
		Total    int   `json:"total"`
		Complete bool  `json:"complete"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&st); err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if st.Total != total || st.Complete || len(st.Received) != 2 || st.Received[0] != 0 || st.Received[1] != 2 {
		t.Fatalf("status 不符: %+v", st)
	}

	// 补最后一片后 complete。
	if r, raw := chunkRequest(t, ts.URL, token, id, name, "/", 1, total, int64(len(content)), parts[1]); r.StatusCode != http.StatusOK {
		t.Fatalf("分片 1 应 200，得到 %d (%s)", r.StatusCode, raw)
	}
	body := `{"upload_id":"` + id + `","dir":"/","name":"` + name + `","mode":"ask"}`
	r, raw := completeRequest(t, ts.URL, token, body)
	if r.StatusCode != http.StatusOK {
		t.Fatalf("合并应 200，得到 %d (%s)", r.StatusCode, raw)
	}
	got, err := os.ReadFile(filepath.Join(root, name))
	if err != nil || !bytes.Equal(got, content) {
		t.Fatalf("合并内容不符: %v (%q)", err, got)
	}
	if _, err := os.Stat(srv.uploadSessionDir(id)); !os.IsNotExist(err) {
		t.Fatalf("完成会话应被清理: %v", err)
	}
	assertNoUploadTemps(t, root)
}

// TestChunkUploadMissingRejectedAndCleaned 缺片拒绝合并，并清掉残缺会话。
func TestChunkUploadMissingRejectedAndCleaned(t *testing.T) {
	ts, srv, root, token := newChunkTS(t)
	content := []byte(strings.Repeat("x", 90))
	const name = "missing.bin"
	id := deriveUploadID(content, int64(len(content)), name)
	if r, raw := chunkRequest(t, ts.URL, token, id, name, "/", 0, 3, int64(len(content)), content[:30]); r.StatusCode != http.StatusOK {
		t.Fatalf("分片 0 应 200: %d (%s)", r.StatusCode, raw)
	}
	body := `{"upload_id":"` + id + `","dir":"/","name":"` + name + `","mode":"ask"}`
	r, raw := completeRequest(t, ts.URL, token, body)
	if r.StatusCode != http.StatusConflict {
		t.Fatalf("缺片应 409，得到 %d (%s)", r.StatusCode, raw)
	}
	var out struct {
		Code    string `json:"code"`
		Missing []int  `json:"missing"`
	}
	if err := json.Unmarshal(raw, &out); err != nil {
		t.Fatal(err)
	}
	if out.Code != "missing_chunks" || len(out.Missing) != 2 || out.Missing[0] != 1 || out.Missing[1] != 2 {
		t.Fatalf("缺片响应不符: %+v", out)
	}
	if _, err := os.Stat(srv.uploadSessionDir(id)); !os.IsNotExist(err) {
		t.Fatalf("缺片拒绝后会话应清理: %v", err)
	}
	if _, err := os.Stat(filepath.Join(root, name)); !os.IsNotExist(err) {
		t.Fatalf("缺片时不应产生目标文件: %v", err)
	}
}

// TestUploadSessionExpiryCleanup 超过 24 小时的残缺会话在清理时删除，未过期保留。
func TestUploadSessionExpiryCleanup(t *testing.T) {
	_, srv, root, _ := newChunkTS(t)
	oldID := strings.Repeat("a", 64)
	newID := strings.Repeat("b", 64)
	for _, id := range []string{oldID, newID} {
		if err := os.MkdirAll(srv.uploadSessionDir(id), 0o700); err != nil {
			t.Fatal(err)
		}
	}
	if err := srv.writeUploadMeta(uploadMeta{ID: oldID, Name: "old.bin", Size: 10, Total: 2, CreatedAt: time.Now().Add(-25 * time.Hour).Unix()}); err != nil {
		t.Fatal(err)
	}
	if err := srv.writeUploadMeta(uploadMeta{ID: newID, Name: "new.bin", Size: 10, Total: 2, CreatedAt: time.Now().Unix()}); err != nil {
		t.Fatal(err)
	}
	removed, _ := srv.cleanupUploadStore(time.Now())
	if removed != 1 {
		t.Fatalf("应清理 1 个过期会话，得到 %d", removed)
	}
	if _, err := os.Stat(srv.uploadSessionDir(oldID)); !os.IsNotExist(err) {
		t.Fatalf("过期会话应删除: %v", err)
	}
	if _, err := os.Stat(srv.uploadSessionDir(newID)); err != nil {
		t.Fatalf("未过期会话不应删除: %v", err)
	}
	_ = root
}

// TestInstantHitAndMiss 秒传命中直接落位并共享 inode，未命中返回 instant:false。
func TestInstantHitAndMiss(t *testing.T) {
	ts, srv, root, token := newChunkTS(t)
	data := []byte("same content for instant upload")
	src := filepath.Join(root, "source.bin")
	if err := os.WriteFile(src, data, 0o644); err != nil {
		t.Fatal(err)
	}
	sha, err := hashFileSHA256(src)
	if err != nil {
		t.Fatal(err)
	}
	if err := srv.registerBlobFile(src, sha); err != nil {
		t.Fatal(err)
	}
	body := `{"sha256":"` + sha + `","name":"copy.bin","dir":"/","size":` + strconv.Itoa(len(data)) + `,"mode":"ask"}`
	resp, raw := postJSONBody(t, ts.URL+"/api/upload/instant", token, body)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("秒传应 200，得到 %d (%s)", resp.StatusCode, raw)
	}
	var out struct {
		Instant  bool           `json:"instant"`
		Uploaded []uploadedFile `json:"uploaded"`
	}
	if err := json.Unmarshal(raw, &out); err != nil {
		t.Fatal(err)
	}
	if !out.Instant || len(out.Uploaded) != 1 || out.Uploaded[0].Name != "copy.bin" {
		t.Fatalf("秒传响应不符: %+v", out)
	}
	got, err := os.ReadFile(filepath.Join(root, "copy.bin"))
	if err != nil || !bytes.Equal(got, data) {
		t.Fatalf("秒传内容不符: %v", err)
	}
	a, _ := os.Stat(src)
	b, _ := os.Stat(filepath.Join(root, "copy.bin"))
	if !os.SameFile(a, b) {
		t.Fatal("秒传应使用硬链接（inode 相同）")
	}

	missSHA := strings.Repeat("0", 64)
	resp2, raw2 := postJSONBody(t, ts.URL+"/api/upload/instant", token, `{"sha256":"`+missSHA+`","name":"miss.bin","dir":"/","size":4}`)
	if resp2.StatusCode != http.StatusOK {
		t.Fatalf("未命中应 200，得到 %d (%s)", resp2.StatusCode, raw2)
	}
	var miss struct {
		Instant bool `json:"instant"`
	}
	if err := json.Unmarshal(raw2, &miss); err != nil {
		t.Fatal(err)
	}
	if miss.Instant {
		t.Fatalf("未命中不应 instant: %s", raw2)
	}
}

// TestBlobLinkCountAndReclaim 多链接时保留 blob，最后一个用户链接消失后回收。
func TestBlobLinkCountAndReclaim(t *testing.T) {
	_, srv, root, _ := newChunkTS(t)
	data := []byte("dedup payload")
	first := filepath.Join(root, "first.bin")
	second := filepath.Join(root, "second.bin")
	for _, p := range []string{first, second} {
		if err := os.WriteFile(p, data, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	sha, err := hashFileSHA256(first)
	if err != nil {
		t.Fatal(err)
	}
	if err := srv.registerBlobFile(first, sha); err != nil {
		t.Fatal(err)
	}
	// 第二个文件通过秒传链到同一 blob。
	if err := srv.materializeBlob(sha, second); err != nil {
		t.Fatal(err)
	}
	blob := srv.blobPath(sha)
	if n, ok := linkCountSafe(blob); !ok || n != 3 {
		t.Fatalf("blob 硬链接数应为 3，得到 %d (ok=%v)", n, ok)
	}
	if err := os.Remove(first); err != nil {
		t.Fatal(err)
	}
	if got := srv.pruneOrphanBlobs(); got != 0 {
		t.Fatalf("仍有一个用户链接时不应回收，回收了 %d", got)
	}
	if _, err := os.Stat(blob); err != nil {
		t.Fatalf("仍有用户链接时 blob 应保留: %v", err)
	}
	if err := os.Remove(second); err != nil {
		t.Fatal(err)
	}
	if got := srv.pruneOrphanBlobs(); got != 1 {
		t.Fatalf("最后一个用户链接消失应回收 1 个 blob，得到 %d", got)
	}
	if _, err := os.Stat(blob); !os.IsNotExist(err) {
		t.Fatalf("blob 应已回收: %v", err)
	}
}

// TestUploadMkdirPathValidation 目录创建复用命名与路径校验：拒绝越界、分隔符、隐藏名。
func TestUploadMkdirPathValidation(t *testing.T) {
	ts, _, root, token := newChunkTS(t)
	if err := os.Mkdir(filepath.Join(root, "base"), 0o755); err != nil {
		t.Fatal(err)
	}
	body := `{"dir":"/","name":"folder","mode":"ask"}`
	resp, raw := postJSONBody(t, ts.URL+"/api/upload/mkdir", token, body)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("创建目录应 200，得到 %d (%s)", resp.StatusCode, raw)
	}
	if fi, err := os.Stat(filepath.Join(root, "folder")); err != nil || !fi.IsDir() {
		t.Fatalf("目录未创建: %v", err)
	}
	// 同名 ask 冲突，overwrite 复用，rename 另建。
	if r, _ := postJSONBody(t, ts.URL+"/api/upload/mkdir", token, body); r.StatusCode != http.StatusConflict {
		t.Fatalf("同名 ask 应 409，得到 %d", r.StatusCode)
	}
	if r, raw := postJSONBody(t, ts.URL+"/api/upload/mkdir", token, `{"dir":"/","name":"folder","mode":"overwrite"}`); r.StatusCode != http.StatusOK {
		t.Fatalf("overwrite 应复用，得到 %d (%s)", r.StatusCode, raw)
	}
	if r, raw := postJSONBody(t, ts.URL+"/api/upload/mkdir", token, `{"dir":"/","name":"folder","mode":"rename"}`); r.StatusCode != http.StatusOK {
		t.Fatalf("rename 应成功，得到 %d (%s)", r.StatusCode, raw)
	}
	if fi, err := os.Stat(filepath.Join(root, "folder (1)")); err != nil || !fi.IsDir() {
		t.Fatalf("rename 应创建 folder (1): %v", err)
	}
	for _, bad := range []string{`{"dir":"/","name":".."}`, `{"dir":"/","name":"a/b"}`, `{"dir":"/","name":".hidden"}`, `{"dir":"/","name":"con"}`} {
		if r, raw := postJSONBody(t, ts.URL+"/api/upload/mkdir", token, bad); r.StatusCode != http.StatusBadRequest {
			t.Errorf("非法目录名 %s 应 400，得到 %d (%s)", bad, r.StatusCode, raw)
		}
	}
	if r, raw := postJSONBody(t, ts.URL+"/api/upload/mkdir", token, `{"dir":"..","name":"x"}`); r.StatusCode != http.StatusBadRequest {
		t.Errorf("越界父目录应 400，得到 %d (%s)", r.StatusCode, raw)
	}
}

// TestUploadChunkEndpointsRequireAuth 认证开启时未登录访问分片/秒传端点返回 401。
func TestUploadChunkEndpointsRequireAuth(t *testing.T) {
	srv := New(t.TempDir(), Options{AuthMode: "password", AllowUpload: true})
	ts := httptest.NewServer(srv.Handler())
	defer ts.Close()
	defer srv.Close()
	for _, ep := range []string{
		"/api/upload/status?upload_id=" + strings.Repeat("a", 64),
		"/api/upload/chunk",
		"/api/upload/complete",
		"/api/upload/instant",
		"/api/upload/mkdir",
	} {
		method := http.MethodPost
		if strings.Contains(ep, "status") {
			method = http.MethodGet
		}
		req, _ := http.NewRequest(method, ts.URL+ep, strings.NewReader("{}"))
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		raw, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		if resp.StatusCode != http.StatusUnauthorized {
			t.Errorf("%s 未登录应 401，得到 %d (%s)", ep, resp.StatusCode, raw)
		}
	}
}

// apiGet 发送带会话的 GET 请求。
func apiGet(t *testing.T, url, token string) *http.Response {
	t.Helper()
	req, err := http.NewRequest(http.MethodGet, url, nil)
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Authorization", "Bearer "+token)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	return resp
}

// postJSONBody 发送 JSON POST 并读回响应体。
func postJSONBody(t *testing.T, url, token, body string) (*http.Response, []byte) {
	t.Helper()
	req, err := http.NewRequest(http.MethodPost, url, strings.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+token)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	raw, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	return resp, raw
}
