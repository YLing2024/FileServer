package server

import (
	"archive/zip"
	"bytes"
	"encoding/base32"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

// ---- 测试辅助 ----

// newShareTestServer 启动一个不要求认证的测试服务（分享管理在无认证部署下敞开）。
func newShareTestServer(t *testing.T) (*Server, string) {
	t.Helper()
	root := t.TempDir()
	return New(root, Options{}), root
}

// shareCreate 通过 API 创建分享并返回响应。
func shareCreate(t *testing.T, srv *Server, token string, body map[string]any) (int, shareCreateResp) {
	t.Helper()
	rec := manageReq(t, srv, token, http.MethodPost, "/api/share", body)
	var out shareCreateResp
	if rec.Code == http.StatusOK {
		if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
			t.Fatalf("解析创建分享响应失败: %v body=%s", err, rec.Body.String())
		}
	}
	return rec.Code, out
}

// shareGet 发起一次公开 GET；cookie 为空表示不携带提取码 cookie。
func shareGet(t *testing.T, srv *Server, target string, cookie *http.Cookie) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, target, nil)
	if cookie != nil {
		req.AddCookie(cookie)
	}
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)
	return rec
}

// sharePost 发起一次公开 POST。
func sharePost(t *testing.T, srv *Server, target, body string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, target, strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)
	return rec
}

// setShareCookie 从 verify 响应中取出该分享的 cookie。
func setShareCookie(t *testing.T, rec *httptest.ResponseRecorder, token string) *http.Cookie {
	t.Helper()
	for _, c := range rec.Result().Cookies() {
		if c.Name == "fs_share_"+token {
			return c
		}
	}
	t.Fatalf("verify 响应没有下发分享 cookie，头=%v", rec.Header())
	return nil
}

// expireShare 直接把记录改成已过期（模拟时间流逝，不等待）。
func expireShare(t *testing.T, srv *Server, token string) {
	t.Helper()
	srv.shares.mu.Lock()
	defer srv.shares.mu.Unlock()
	rec, ok := srv.shares.records[token]
	if !ok {
		t.Fatalf("记录 %s 不存在", token)
	}
	rec.Expires = time.Now().Add(-time.Minute).Unix()
	srv.shares.records[token] = rec
	if err := srv.shares.saveLocked(); err != nil {
		t.Fatal(err)
	}
}

func TestShareTokenRandomAndUnique(t *testing.T) {
	seen := make(map[string]bool)
	enc := base32.StdEncoding.WithPadding(base32.NoPadding)
	for i := 0; i < 2000; i++ {
		token, err := newShareToken()
		if err != nil {
			t.Fatal(err)
		}
		if len(token) != 26 {
			t.Fatalf("token 长度 = %d，期望 26（16 字节 base32）", len(token))
		}
		raw, err := enc.DecodeString(strings.ToUpper(token))
		if err != nil {
			t.Fatalf("token 不是合法 base32: %v", err)
		}
		if len(raw) != shareTokenBytes {
			t.Fatalf("token 解码后 = %d 字节，期望 %d", len(raw), shareTokenBytes)
		}
		if seen[token] {
			t.Fatalf("生成重复 token: %s", token)
		}
		seen[token] = true
	}
}

func TestShareCreateValidation(t *testing.T) {
	srv, root := newShareTestServer(t)
	writeTestFile(t, filepath.Join(root, "a.txt"), "hello")
	writeTestFile(t, filepath.Join(root, ".hidden"), "x")

	cases := []struct {
		name string
		body map[string]any
		want int
	}{
		{"默认参数", map[string]any{"path": "/a.txt"}, http.StatusOK},
		{"永久", map[string]any{"path": "/a.txt", "days": 0}, http.StatusOK},
		{"非法天数", map[string]any{"path": "/a.txt", "days": 2}, http.StatusBadRequest},
		{"非法提取码", map[string]any{"path": "/a.txt", "code": "abc"}, http.StatusBadRequest},
		{"非字母数字提取码", map[string]any{"path": "/a.txt", "code": "A-12"}, http.StatusBadRequest},
		{"非法下载上限", map[string]any{"path": "/a.txt", "max_downloads": 5}, http.StatusBadRequest},
		{"越界路径", map[string]any{"path": "../a.txt"}, http.StatusBadRequest},
		{"不存在的路径", map[string]any{"path": "/nope.txt"}, http.StatusBadRequest},
		{"隐藏文件", map[string]any{"path": "/.hidden"}, http.StatusForbidden},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			code, resp := shareCreate(t, srv, "", tc.body)
			if code != tc.want {
				t.Fatalf("状态码 = %d，期望 %d", code, tc.want)
			}
			if tc.want == http.StatusOK && !strings.HasPrefix(resp.URL, "/s/") {
				t.Fatalf("url = %q，期望 /s/ 前缀", resp.URL)
			}
		})
	}
}

func TestShareCodeVerifyAndCookie(t *testing.T) {
	srv, root := newShareTestServer(t)
	writeTestFile(t, filepath.Join(root, "a.txt"), "hello-share")

	code, created := shareCreate(t, srv, "", map[string]any{
		"path": "/a.txt", "days": 7, "code": "AB12", "allow_preview": true,
	})
	if code != http.StatusOK || created.Code != "AB12" || !created.CodeSet {
		t.Fatalf("创建带提取码分享失败: %d %+v", code, created)
	}
	token := created.Token

	// 未验证时 info 不泄露文件名/大小，list 被拒绝。
	rec := shareGet(t, srv, "/api/public/share/"+token+"/info", nil)
	var info publicShareInfo
	if err := json.Unmarshal(rec.Body.Bytes(), &info); err != nil {
		t.Fatal(err)
	}
	if rec.Code != http.StatusOK || info.Verified || !info.HasCode || info.Name != "" || info.Size != 0 {
		t.Fatalf("未验证 info 泄露了内容或状态异常: %d %+v", rec.Code, info)
	}
	if rec := shareGet(t, srv, "/api/public/share/"+token+"/list", nil); rec.Code != http.StatusForbidden {
		t.Fatalf("未验证 list 应 403，得到 %d", rec.Code)
	}

	// 错码提示统一为「提取码不正确」。
	wrong := sharePost(t, srv, "/api/public/share/"+token+"/verify", `{"code":"ZZZZ"}`)
	if wrong.Code != http.StatusForbidden || !strings.Contains(wrong.Body.String(), "提取码不正确") {
		t.Fatalf("错码响应 = %d %s", wrong.Code, wrong.Body.String())
	}

	ok := sharePost(t, srv, "/api/public/share/"+token+"/verify", `{"code":"AB12"}`)
	if ok.Code != http.StatusOK {
		t.Fatalf("正确提取码应 200，得到 %d %s", ok.Code, ok.Body.String())
	}
	cookie := setShareCookie(t, ok, token)
	if cookie.MaxAge != int(shareCookieTTL/time.Second) || !cookie.HttpOnly {
		t.Fatalf("cookie 属性异常: %+v", cookie)
	}

	rec = shareGet(t, srv, "/api/public/share/"+token+"/info", cookie)
	info = publicShareInfo{}
	if err := json.Unmarshal(rec.Body.Bytes(), &info); err != nil {
		t.Fatal(err)
	}
	if !info.Verified || info.Name != "a.txt" || info.Size != int64(len("hello-share")) {
		t.Fatalf("验证后 info 异常: %d %+v", rec.Code, info)
	}
	dl := shareGet(t, srv, "/api/public/share/"+token+"/download", cookie)
	if dl.Code != http.StatusOK || dl.Body.String() != "hello-share" {
		t.Fatalf("下载失败: %d %q", dl.Code, dl.Body.String())
	}
	if cd := dl.Header().Get("Content-Disposition"); !strings.HasPrefix(cd, "attachment") {
		t.Fatalf("下载 Content-Disposition = %q，期望 attachment", cd)
	}
}

func TestShareCodeRateLimit(t *testing.T) {
	srv, root := newShareTestServer(t)
	writeTestFile(t, filepath.Join(root, "a.txt"), "x")
	_, created := shareCreate(t, srv, "", map[string]any{"path": "/a.txt", "code": "AB12"})
	token := created.Token

	for i := 0; i < shareCodeMaxFailures; i++ {
		rec := sharePost(t, srv, "/api/public/share/"+token+"/verify", `{"code":"ZZZZ"}`)
		if i < shareCodeMaxFailures-1 && rec.Code != http.StatusForbidden {
			t.Fatalf("第 %d 次错码 = %d，期望 403", i+1, rec.Code)
		}
		if i == shareCodeMaxFailures-1 && rec.Code != http.StatusTooManyRequests {
			t.Fatalf("第 %d 次错码 = %d，期望 429", i+1, rec.Code)
		}
		if !strings.Contains(rec.Body.String(), "提取码不正确") && rec.Code == http.StatusForbidden {
			t.Fatalf("错码文案应统一: %s", rec.Body.String())
		}
	}
	// 达到上限后，即使提取码正确也拒绝。
	rec := sharePost(t, srv, "/api/public/share/"+token+"/verify", `{"code":"AB12"}`)
	if rec.Code != http.StatusTooManyRequests {
		t.Fatalf("限速后正确码也应 429，得到 %d", rec.Code)
	}
}

func TestShareExpiredRevokedExhausted(t *testing.T) {
	srv, root := newShareTestServer(t)
	writeTestFile(t, filepath.Join(root, "a.txt"), "x")

	// 过期
	_, expired := shareCreate(t, srv, "", map[string]any{"path": "/a.txt", "days": 1})
	expireShare(t, srv, expired.Token)
	if rec := shareGet(t, srv, "/api/public/share/"+expired.Token+"/info", nil); rec.Code != http.StatusForbidden ||
		!strings.Contains(rec.Body.String(), `"code":"expired"`) {
		t.Fatalf("过期 info = %d %s", rec.Code, rec.Body.String())
	}
	if rec := shareGet(t, srv, "/api/public/share/"+expired.Token+"/download", nil); rec.Code != http.StatusForbidden {
		t.Fatalf("过期下载 = %d，期望 403", rec.Code)
	}

	// 撤销
	_, revoked := shareCreate(t, srv, "", map[string]any{"path": "/a.txt"})
	rev := manageReq(t, srv, "", http.MethodPost, "/api/share/revoke", map[string]any{"token": revoked.Token})
	if rev.Code != http.StatusOK {
		t.Fatalf("撤销 = %d", rev.Code)
	}
	if rec := shareGet(t, srv, "/api/public/share/"+revoked.Token+"/info", nil); rec.Code != http.StatusForbidden ||
		!strings.Contains(rec.Body.String(), `"code":"revoked"`) {
		t.Fatalf("撤销后 info = %d %s", rec.Code, rec.Body.String())
	}
	if rec := shareGet(t, srv, "/api/public/share/"+revoked.Token+"/download", nil); rec.Code != http.StatusForbidden {
		t.Fatalf("撤销后下载 = %d，期望 403", rec.Code)
	}

	// 次数上限：第一次成功，第二次与之后 403 exhausted
	_, limited := shareCreate(t, srv, "", map[string]any{"path": "/a.txt", "max_downloads": 10})
	if limited.MaxDownloads != 10 {
		t.Fatalf("max_downloads = %d", limited.MaxDownloads)
	}
	srv.shares.mu.Lock()
	rec := srv.shares.records[limited.Token]
	rec.MaxDownloads = 1
	srv.shares.records[limited.Token] = rec
	if err := srv.shares.saveLocked(); err != nil {
		t.Fatal(err)
	}
	srv.shares.mu.Unlock()
	if dl := shareGet(t, srv, "/api/public/share/"+limited.Token+"/download", nil); dl.Code != http.StatusOK {
		t.Fatalf("首次下载 = %d", dl.Code)
	}
	if dl := shareGet(t, srv, "/api/public/share/"+limited.Token+"/download", nil); dl.Code != http.StatusForbidden ||
		!strings.Contains(dl.Body.String(), `"code":"exhausted"`) {
		t.Fatalf("超次下载 = %d %s", dl.Code, dl.Body.String())
	}

	// 删除记录后公开页 404
	del := manageReq(t, srv, "", http.MethodDelete, "/api/share/record", map[string]any{"token": revoked.Token})
	if del.Code != http.StatusOK {
		t.Fatalf("删除记录 = %d", del.Code)
	}
	if rec := shareGet(t, srv, "/api/public/share/"+revoked.Token+"/info", nil); rec.Code != http.StatusNotFound {
		t.Fatalf("删除后 info = %d，期望 404", rec.Code)
	}
}

func TestSharePathTraversal(t *testing.T) {
	srv, root := newShareTestServer(t)
	shareDir := filepath.Join(root, "pub")
	writeTestFile(t, filepath.Join(shareDir, "ok.txt"), "inside")
	writeTestFile(t, filepath.Join(shareDir, "sub", "b.txt"), "inside-b")
	_, created := shareCreate(t, srv, "", map[string]any{"path": "/pub"})

	for _, p := range []string{"../../etc/passwd", "..%2F..%2Fetc%2Fpasswd", "/../../etc/passwd"} {
		rec := shareGet(t, srv, "/api/public/share/"+created.Token+"/download?path="+p, nil)
		if rec.Code == http.StatusOK {
			t.Fatalf("越界路径 %q 不应成功", p)
		}
		if strings.Contains(rec.Body.String(), "root:") || strings.Contains(rec.Body.String(), root) {
			t.Fatalf("越界响应泄露了路径/文件内容: %s", rec.Body.String())
		}
	}
	rec := shareGet(t, srv, "/api/public/share/"+created.Token+"/list?path=..", nil)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("越界 list = %d，期望 400", rec.Code)
	}

	if runtime.GOOS != "windows" {
		outside := t.TempDir()
		writeTestFile(t, filepath.Join(outside, "secret.txt"), "top-secret")
		link := filepath.Join(shareDir, "link")
		if err := os.Symlink(outside, link); err != nil {
			t.Skipf("创建符号链接失败: %v", err)
		}
		rec := shareGet(t, srv, "/api/public/share/"+created.Token+"/download?path=/link/secret.txt", nil)
		if rec.Code == http.StatusOK || strings.Contains(rec.Body.String(), "top-secret") {
			t.Fatalf("越界符号链接不应可读: %d %s", rec.Code, rec.Body.String())
		}
	}

	// 文件分享：任何非自身路径都拒绝。
	writeTestFile(t, filepath.Join(root, "single.txt"), "single")
	_, fileShare := shareCreate(t, srv, "", map[string]any{"path": "/single.txt"})
	rec = shareGet(t, srv, "/api/public/share/"+fileShare.Token+"/download?path=../../etc/passwd", nil)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("文件分享越界 = %d，期望 400", rec.Code)
	}
}

func TestShareZipStructure(t *testing.T) {
	srv, root := newShareTestServer(t)
	shareDir := filepath.Join(root, "pub")
	writeTestFile(t, filepath.Join(shareDir, "a.txt"), "A")
	writeTestFile(t, filepath.Join(shareDir, "sub", "b.txt"), "B")
	writeTestFile(t, filepath.Join(shareDir, ".hidden"), "H")
	if err := os.MkdirAll(filepath.Join(shareDir, "empty"), 0o755); err != nil {
		t.Fatal(err)
	}
	_, created := shareCreate(t, srv, "", map[string]any{"path": "/pub", "max_downloads": 10})

	rec := shareGet(t, srv, "/api/public/share/"+created.Token+"/zip", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("zip = %d %s", rec.Code, rec.Body.String())
	}
	if cd := rec.Header().Get("Content-Disposition"); !strings.HasPrefix(cd, "attachment") {
		t.Fatalf("zip Content-Disposition = %q", cd)
	}
	zr, err := zip.NewReader(bytes.NewReader(rec.Body.Bytes()), int64(rec.Body.Len()))
	if err != nil {
		t.Fatalf("解析 zip 失败: %v", err)
	}
	names := map[string]string{}
	for _, f := range zr.File {
		if strings.HasSuffix(f.Name, "/") {
			names[f.Name] = ""
			continue
		}
		rc, err := f.Open()
		if err != nil {
			t.Fatal(err)
		}
		data, _ := io.ReadAll(rc)
		_ = rc.Close()
		names[f.Name] = string(data)
	}
	for _, want := range []string{"pub/a.txt", "pub/sub/b.txt", "pub/empty/"} {
		if _, ok := names[want]; !ok {
			t.Fatalf("zip 缺少 %q，实际=%v", want, names)
		}
	}
	if names["pub/a.txt"] != "A" || names["pub/sub/b.txt"] != "B" {
		t.Fatalf("zip 内容错误: %v", names)
	}
	if _, ok := names["pub/.hidden"]; ok {
		t.Fatalf("未开启 --hidden 时不应打包隐藏文件")
	}
}

func TestSharePublicAndLoginIsolation(t *testing.T) {
	root := t.TempDir()
	writeTestFile(t, filepath.Join(root, "public.txt"), "public-data")
	srv := New(root, Options{AuthMode: "password", Auth: "user:secretpass"})
	if srv.cfg.auth == nil {
		t.Fatal("认证未初始化")
	}
	session, _, err := srv.cfg.auth.Login("secretpass", "", true, "share-test", "127.0.0.1")
	if err != nil {
		t.Fatal(err)
	}
	_, created := shareCreate(t, srv, session, map[string]any{"path": "/public.txt"})

	// 无登录会话：管理接口 401，公开接口与页面可访问。
	if rec := manageReq(t, srv, "", http.MethodGet, "/api/share", nil); rec.Code != http.StatusUnauthorized {
		t.Fatalf("匿名 /api/share = %d，期望 401", rec.Code)
	}
	if rec := manageReq(t, srv, "", http.MethodGet, "/api/list?path=/", nil); rec.Code != http.StatusUnauthorized {
		t.Fatalf("匿名 /api/list = %d，期望 401", rec.Code)
	}
	if rec := shareGet(t, srv, "/api/public/share/"+created.Token+"/info", nil); rec.Code != http.StatusOK {
		t.Fatalf("匿名公开 info = %d", rec.Code)
	}
	if rec := shareGet(t, srv, "/s/"+created.Token, nil); rec.Code != http.StatusOK {
		t.Fatalf("匿名公开页 = %d", rec.Code)
	}
	if rec := shareGet(t, srv, "/api/public/share/"+created.Token+"/download", nil); rec.Code != http.StatusOK || rec.Body.String() != "public-data" {
		t.Fatalf("匿名公开下载 = %d %q", rec.Code, rec.Body.String())
	}
}

func TestSharePublicHeadersAndPreview(t *testing.T) {
	srv, root := newShareTestServer(t)
	writeTestFile(t, filepath.Join(root, "a.txt"), "hello")
	_, created := shareCreate(t, srv, "", map[string]any{"path": "/a.txt", "allow_preview": true})

	page := shareGet(t, srv, "/s/"+created.Token, nil)
	if page.Code != http.StatusOK || !strings.Contains(page.Header().Get("X-Robots-Tag"), "noindex") {
		t.Fatalf("公开页头异常: %d %v", page.Code, page.Header())
	}
	if !strings.Contains(page.Header().Get("Cache-Control"), "no-store") {
		t.Fatalf("公开页应禁用缓存: %v", page.Header())
	}
	preview := shareGet(t, srv, "/api/public/share/"+created.Token+"/preview", nil)
	if preview.Code != http.StatusOK || preview.Body.String() != "hello" {
		t.Fatalf("预览 = %d %q", preview.Code, preview.Body.String())
	}
	if cd := preview.Header().Get("Content-Disposition"); !strings.HasPrefix(cd, "inline") {
		t.Fatalf("预览 Content-Disposition = %q，期望 inline", cd)
	}
	if !strings.Contains(preview.Header().Get("X-Robots-Tag"), "noindex") {
		t.Fatalf("预览应带 noindex: %v", preview.Header())
	}

	_, noPreview := shareCreate(t, srv, "", map[string]any{"path": "/a.txt", "allow_preview": false})
	if rec := shareGet(t, srv, "/api/public/share/"+noPreview.Token+"/preview", nil); rec.Code != http.StatusForbidden {
		t.Fatalf("禁止预览时 preview = %d，期望 403", rec.Code)
	}
}

func TestSharePersistedAcrossRestart(t *testing.T) {
	root := t.TempDir()
	writeTestFile(t, filepath.Join(root, "a.txt"), "x")
	srv := New(root, Options{})
	_, created := shareCreate(t, srv, "", map[string]any{"path": "/a.txt", "note": "给同事"})

	restarted := New(root, Options{})
	rec, ok := restarted.shares.get(created.Token)
	if !ok {
		t.Fatal("重启后分享记录丢失")
	}
	if rec.Note != "给同事" || rec.Path != "/a.txt" {
		t.Fatalf("重启后记录异常: %+v", rec)
	}
	if got := shareGet(t, restarted, "/api/public/share/"+created.Token+"/info", nil); got.Code != http.StatusOK {
		t.Fatalf("重启后公开 info = %d", got.Code)
	}
	// shares.json 必须存在且不可被其他用户读（0600）。
	fi, err := os.Stat(filepath.Join(root, cacheDirName, shareFileName))
	if err != nil {
		t.Fatal(err)
	}
	if runtime.GOOS != "windows" && fi.Mode().Perm() != 0o600 {
		t.Fatalf("shares.json 权限 = %v，期望 0600", fi.Mode().Perm())
	}
}

func TestShareRecordPersistShape(t *testing.T) {
	srv, root := newShareTestServer(t)
	writeTestFile(t, filepath.Join(root, "a.txt"), "x")
	_, created := shareCreate(t, srv, "", map[string]any{"path": "/a.txt", "code": "AB12"})
	data, err := os.ReadFile(filepath.Join(root, cacheDirName, shareFileName))
	if err != nil {
		t.Fatal(err)
	}
	var f sharesFile
	if err := json.Unmarshal(data, &f); err != nil {
		t.Fatal(err)
	}
	if len(f.Shares) != 1 || f.Shares[0].Token != created.Token {
		t.Fatalf("shares.json 内容异常: %+v", f)
	}
	if strings.Contains(string(data), `"AB12"`) {
		t.Fatal("shares.json 不应保存提取码明文")
	}
	if !strings.Contains(string(data), "code_hash") {
		t.Fatal("shares.json 应保存提取码哈希")
	}
}
