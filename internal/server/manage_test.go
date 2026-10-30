package server

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// newManageTestServer 启动一个启用口令认证的测试服务并登录，返回 token。
// 文件管理只对登录会话开放，因此绝大多数用例都需要这个辅助函数。
func newManageTestServer(t *testing.T) (*Server, string, string) {
	t.Helper()
	root := t.TempDir()
	srv := New(root, Options{AuthMode: "password", Auth: "user:secretpass"})
	if srv.cfg.auth == nil {
		t.Fatal("认证未初始化")
	}
	token, _, err := srv.cfg.auth.Login("secretpass", "", true, "manage-test", "127.0.0.1")
	if err != nil {
		t.Fatalf("登录失败: %v", err)
	}
	return srv, root, token
}

// manageReq 发起一次带会话的 API 请求。
func manageReq(t *testing.T, srv *Server, token, method, target string, body any) *httptest.ResponseRecorder {
	t.Helper()
	var rdr io.Reader
	if body != nil {
		data, err := json.Marshal(body)
		if err != nil {
			t.Fatalf("序列化请求失败: %v", err)
		}
		rdr = bytes.NewReader(data)
	}
	req := httptest.NewRequest(method, target, rdr)
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)
	return rec
}

func decodeJSON[T any](t *testing.T, rec *httptest.ResponseRecorder) T {
	t.Helper()
	var out T
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatalf("解析响应失败 (%d): %v body=%s", rec.Code, err, rec.Body.String())
	}
	return out
}

func writeTestFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestManageRequiresLogin(t *testing.T) {
	// 认证未启用：没有登录会话，管理接口也必须拒绝（不能匿名写文件）。
	noAuth := New(t.TempDir(), Options{})
	rec := manageReq(t, noAuth, "", http.MethodPost, "/api/delete", map[string]any{"paths": []string{"/x"}})
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("认证关闭时 delete 状态码 = %d，期望 401", rec.Code)
	}

	srv, root, _ := newManageTestServer(t)
	writeTestFile(t, filepath.Join(root, "x.txt"), "x")
	for _, tc := range []struct{ method, path string }{
		{http.MethodPost, "/api/rename"},
		{http.MethodPost, "/api/move"},
		{http.MethodPost, "/api/copy"},
		{http.MethodPost, "/api/delete"},
		{http.MethodGet, "/api/trash"},
		{http.MethodPost, "/api/trash/restore"},
		{http.MethodPost, "/api/trash/empty"},
	} {
		rec := manageReq(t, srv, "", tc.method, tc.path, nil)
		if rec.Code != http.StatusUnauthorized {
			t.Fatalf("未登录 %s %s 状态码 = %d，期望 401", tc.method, tc.path, rec.Code)
		}
	}
}

func TestManageRejectsTraversalAndCache(t *testing.T) {
	srv, root, token := newManageTestServer(t)
	outside := t.TempDir()
	writeTestFile(t, filepath.Join(outside, "secret.txt"), "secret")
	if err := os.Symlink(outside, filepath.Join(root, "escape")); err != nil {
		t.Skipf("无法创建符号链接: %v", err)
	}

	for _, p := range []string{"../secret.txt", "/escape/secret.txt", "/" + cacheDirName, "/" + cacheDirName + "/auth.json"} {
		rec := manageReq(t, srv, token, http.MethodPost, "/api/delete", map[string]any{"paths": []string{p}})
		if rec.Code != http.StatusBadRequest {
			t.Fatalf("删除 %q 状态码 = %d，期望 400 body=%s", p, rec.Code, rec.Body.String())
		}
	}
}

func TestTrashMoveRestoreEmpty(t *testing.T) {
	srv, root, token := newManageTestServer(t)
	writeTestFile(t, filepath.Join(root, "docs", "a.txt"), "hello")

	rec := manageReq(t, srv, token, http.MethodPost, "/api/delete", map[string]any{"paths": []string{"/docs/a.txt"}})
	if rec.Code != http.StatusOK {
		t.Fatalf("删除状态码 = %d body=%s", rec.Code, rec.Body.String())
	}
	del := decodeJSON[struct {
		Moved  []map[string]string `json:"moved"`
		Failed []manageFailure     `json:"failed"`
	}](t, rec)
	if len(del.Moved) != 1 || len(del.Failed) != 0 {
		t.Fatalf("删除结果异常: %+v", del)
	}
	if _, err := os.Stat(filepath.Join(root, "docs", "a.txt")); !os.IsNotExist(err) {
		t.Fatalf("原文件仍在磁盘: %v", err)
	}

	list := manageReq(t, srv, token, http.MethodGet, "/api/trash", nil)
	if list.Code != http.StatusOK {
		t.Fatalf("回收站列表状态码 = %d", list.Code)
	}
	tr := decodeJSON[struct {
		Items     []trashItem `json:"items"`
		Count     int         `json:"count"`
		TotalSize int64       `json:"total_size"`
	}](t, list)
	if tr.Count != 1 || tr.Items[0].Path != "/docs/a.txt" || tr.Items[0].Size != 5 || tr.TotalSize != 5 {
		t.Fatalf("回收站列表异常: %+v", tr)
	}

	id := tr.Items[0].ID
	rec = manageReq(t, srv, token, http.MethodPost, "/api/trash/restore", map[string]any{"ids": []string{id}})
	if rec.Code != http.StatusOK {
		t.Fatalf("恢复状态码 = %d body=%s", rec.Code, rec.Body.String())
	}
	got, err := os.ReadFile(filepath.Join(root, "docs", "a.txt"))
	if err != nil || string(got) != "hello" {
		t.Fatalf("恢复内容 = %q, err=%v", got, err)
	}
	if trash := decodeJSON[struct {
		Count int `json:"count"`
	}](t, manageReq(t, srv, token, http.MethodGet, "/api/trash", nil)); trash.Count != 0 {
		t.Fatalf("恢复后回收站仍有 %d 条", trash.Count)
	}

	// 再删一次并彻底删除。
	manageReq(t, srv, token, http.MethodPost, "/api/delete", map[string]any{"paths": []string{"/docs/a.txt"}})
	tr = decodeJSON[struct {
		Items     []trashItem `json:"items"`
		Count     int         `json:"count"`
		TotalSize int64       `json:"total_size"`
	}](t, manageReq(t, srv, token, http.MethodGet, "/api/trash", nil))
	rec = manageReq(t, srv, token, http.MethodPost, "/api/trash/empty", map[string]any{"ids": []string{tr.Items[0].ID}})
	if rec.Code != http.StatusOK {
		t.Fatalf("彻底删除状态码 = %d body=%s", rec.Code, rec.Body.String())
	}
	if trash := decodeJSON[struct {
		Count int `json:"count"`
	}](t, manageReq(t, srv, token, http.MethodGet, "/api/trash", nil)); trash.Count != 0 {
		t.Fatalf("彻底删除后回收站仍有 %d 条", trash.Count)
	}
	if _, err := os.Stat(filepath.Join(root, ".FileServer", "trash", tr.Items[0].ID)); !os.IsNotExist(err) {
		t.Fatalf("彻底删除后条目目录仍存在: %v", err)
	}

	// 清空回收站（ids 为空 = 全清）。
	writeTestFile(t, filepath.Join(root, "b.txt"), "b")
	manageReq(t, srv, token, http.MethodPost, "/api/delete", map[string]any{"paths": []string{"/b.txt"}})
	rec = manageReq(t, srv, token, http.MethodPost, "/api/trash/empty", map[string]any{"ids": []string{}})
	if rec.Code != http.StatusOK {
		t.Fatalf("清空回收站状态码 = %d body=%s", rec.Code, rec.Body.String())
	}
	if trash := decodeJSON[struct {
		Count int `json:"count"`
	}](t, manageReq(t, srv, token, http.MethodGet, "/api/trash", nil)); trash.Count != 0 {
		t.Fatalf("清空后回收站仍有 %d 条", trash.Count)
	}
}

func TestMoveConflictModes(t *testing.T) {
	srv, root, token := newManageTestServer(t)
	writeTestFile(t, filepath.Join(root, "a.txt"), "A")
	writeTestFile(t, filepath.Join(root, "sub", "a.txt"), "B")

	// ask：整批 409，不做任何改动。
	rec := manageReq(t, srv, token, http.MethodPost, "/api/move?mode=ask", map[string]any{"paths": []string{"/a.txt"}, "dir": "/sub"})
	if rec.Code != http.StatusConflict {
		t.Fatalf("ask 冲突状态码 = %d body=%s", rec.Code, rec.Body.String())
	}
	if got, _ := os.ReadFile(filepath.Join(root, "sub", "a.txt")); string(got) != "B" {
		t.Fatalf("ask 不应改动目标，实际内容 %q", got)
	}

	// overwrite：覆盖目标文件。
	rec = manageReq(t, srv, token, http.MethodPost, "/api/move?mode=overwrite", map[string]any{"paths": []string{"/a.txt"}, "dir": "/sub"})
	if rec.Code != http.StatusOK {
		t.Fatalf("overwrite 状态码 = %d body=%s", rec.Code, rec.Body.String())
	}
	if got, _ := os.ReadFile(filepath.Join(root, "sub", "a.txt")); string(got) != "A" {
		t.Fatalf("overwrite 后内容 = %q，期望 A", got)
	}
	if _, err := os.Stat(filepath.Join(root, "a.txt")); !os.IsNotExist(err) {
		t.Fatalf("overwrite 后源文件仍存在")
	}

	// rename：生成 "a (1).txt"。
	writeTestFile(t, filepath.Join(root, "a.txt"), "A2")
	rec = manageReq(t, srv, token, http.MethodPost, "/api/move?mode=rename", map[string]any{"paths": []string{"/a.txt"}, "dir": "/sub"})
	if rec.Code != http.StatusOK {
		t.Fatalf("rename 状态码 = %d body=%s", rec.Code, rec.Body.String())
	}
	if got, _ := os.ReadFile(filepath.Join(root, "sub", "a (1).txt")); string(got) != "A2" {
		t.Fatalf("rename 结果 = %q，期望 A2", got)
	}
}

func TestBatchPartialFailure(t *testing.T) {
	srv, root, token := newManageTestServer(t)
	writeTestFile(t, filepath.Join(root, "ok.txt"), "ok")

	rec := manageReq(t, srv, token, http.MethodPost, "/api/delete", map[string]any{
		"paths": []string{"/ok.txt", "/missing.txt"},
	})
	if rec.Code != http.StatusOK {
		t.Fatalf("部分失败状态码 = %d body=%s", rec.Code, rec.Body.String())
	}
	res := decodeJSON[struct {
		Moved  []map[string]string `json:"moved"`
		Failed []manageFailure     `json:"failed"`
	}](t, rec)
	if len(res.Moved) != 1 || len(res.Failed) != 1 || res.Failed[0].Path != "/missing.txt" {
		t.Fatalf("部分失败结果异常: %+v", res)
	}
}

func TestCopyDirRecursivePreservesMetadata(t *testing.T) {
	srv, root, token := newManageTestServer(t)
	writeTestFile(t, filepath.Join(root, "src", "nested", "a.txt"), "content-a")
	writeTestFile(t, filepath.Join(root, "src", "b.txt"), "content-b")
	old := time.Now().Add(-48 * time.Hour).Truncate(time.Second)
	for _, p := range []string{"src/nested/a.txt", "src/b.txt", "src/nested", "src"} {
		if err := os.Chtimes(filepath.Join(root, p), old, old); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.Mkdir(filepath.Join(root, "dest"), 0o755); err != nil {
		t.Fatal(err)
	}

	rec := manageReq(t, srv, token, http.MethodPost, "/api/copy", map[string]any{"paths": []string{"/src"}, "dir": "/dest"})
	if rec.Code != http.StatusOK {
		t.Fatalf("复制状态码 = %d body=%s", rec.Code, rec.Body.String())
	}
	for rel, want := range map[string]string{
		"dest/src/nested/a.txt": "content-a",
		"dest/src/b.txt":        "content-b",
	} {
		got, err := os.ReadFile(filepath.Join(root, rel))
		if err != nil || string(got) != want {
			t.Fatalf("复制结果 %s = %q, err=%v", rel, got, err)
		}
		fi, err := os.Stat(filepath.Join(root, rel))
		if err != nil {
			t.Fatal(err)
		}
		if fi.ModTime().Unix() != old.Unix() {
			t.Fatalf("%s mtime = %v，期望 %v", rel, fi.ModTime(), old)
		}
	}
	for _, rel := range []string{"dest/src", "dest/src/nested"} {
		fi, err := os.Stat(filepath.Join(root, rel))
		if err != nil {
			t.Fatal(err)
		}
		if fi.ModTime().Unix() != old.Unix() {
			t.Fatalf("目录 %s mtime = %v，期望 %v", rel, fi.ModTime(), old)
		}
	}

	// 目录不能复制到自身子目录。
	rec = manageReq(t, srv, token, http.MethodPost, "/api/copy", map[string]any{"paths": []string{"/src"}, "dir": "/src/nested"})
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("复制到自身子目录状态码 = %d，期望 400", rec.Code)
	}
}

func TestMoveIntoSelfRejectedAndBatchLimit(t *testing.T) {
	srv, root, token := newManageTestServer(t)
	if err := os.MkdirAll(filepath.Join(root, "a", "b"), 0o755); err != nil {
		t.Fatal(err)
	}
	rec := manageReq(t, srv, token, http.MethodPost, "/api/move", map[string]any{"paths": []string{"/a"}, "dir": "/a/b"})
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("移动到自身子目录状态码 = %d，期望 400", rec.Code)
	}

	paths := make([]string, manageMaxItems+1)
	for i := range paths {
		paths[i] = "/x"
	}
	rec = manageReq(t, srv, token, http.MethodPost, "/api/delete", map[string]any{"paths": paths})
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("超过批量上限状态码 = %d，期望 400", rec.Code)
	}
}

func TestRenameValidationAndConflict(t *testing.T) {
	srv, root, token := newManageTestServer(t)
	writeTestFile(t, filepath.Join(root, "a.txt"), "a")
	writeTestFile(t, filepath.Join(root, "b.txt"), "b")

	for _, name := range []string{"", "x/y.txt", ".hidden", "../z"} {
		rec := manageReq(t, srv, token, http.MethodPost, "/api/rename", map[string]any{"path": "/a.txt", "name": name})
		if rec.Code != http.StatusBadRequest {
			t.Fatalf("非法名称 %q 状态码 = %d，期望 400", name, rec.Code)
		}
	}
	rec := manageReq(t, srv, token, http.MethodPost, "/api/rename", map[string]any{"path": "/a.txt", "name": "b.txt"})
	if rec.Code != http.StatusConflict {
		t.Fatalf("重名状态码 = %d，期望 409", rec.Code)
	}
	rec = manageReq(t, srv, token, http.MethodPost, "/api/rename", map[string]any{"path": "/a.txt", "name": "c.txt"})
	if rec.Code != http.StatusOK {
		t.Fatalf("重命名状态码 = %d body=%s", rec.Code, rec.Body.String())
	}
	if got, _ := os.ReadFile(filepath.Join(root, "c.txt")); string(got) != "a" {
		t.Fatalf("重命名后内容 = %q", got)
	}
}
