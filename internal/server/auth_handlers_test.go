package server

import (
	"crypto/hmac"
	"crypto/sha1"
	"encoding/base32"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// ---- 测试辅助 ----

func newAuthServer(t *testing.T, opts Options) (*httptest.Server, *http.Client) {
	t.Helper()
	srv := New(t.TempDir(), opts)
	ts := httptest.NewServer(srv.Handler())
	t.Cleanup(ts.Close)
	jar, _ := cookiejar.New(nil)
	return ts, &http.Client{Jar: jar}
}

type authStatusResp struct {
	Enabled       bool   `json:"enabled"`
	Mode          string `json:"mode"`
	Authenticated bool   `json:"authenticated"`
	NeedsSetup    bool   `json:"needs_setup"`
	NeedPassword  bool   `json:"need_password"`
	NeedTOTP      bool   `json:"need_totp"`
	Configured    bool   `json:"configured"`
	PasswordSet   bool   `json:"password_set"`
	TOTPBound     bool   `json:"totp_bound"`
}

func authStatus(t *testing.T, base string) authStatusResp {
	t.Helper()
	r, err := http.Get(base + "/api/auth/status")
	if err != nil {
		t.Fatal(err)
	}
	defer r.Body.Close()
	var st authStatusResp
	if err := json.NewDecoder(r.Body).Decode(&st); err != nil {
		t.Fatal(err)
	}
	return st
}

func authLogin(t *testing.T, client *http.Client, base, password, code string, remember bool) int {
	t.Helper()
	body, _ := json.Marshal(map[string]any{"password": password, "code": code, "remember": remember})
	r, err := client.Post(base+"/api/auth/login", "application/json", strings.NewReader(string(body)))
	if err != nil {
		t.Fatal(err)
	}
	io.Copy(io.Discard, r.Body)
	r.Body.Close()
	return r.StatusCode
}

func postJSONClient(t *testing.T, client *http.Client, url, body string) *http.Response {
	t.Helper()
	r, err := client.Post(url, "application/json", strings.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	return r
}

func getWithClient(t *testing.T, client *http.Client, url string) *http.Response {
	t.Helper()
	r, err := client.Get(url)
	if err != nil {
		t.Fatal(err)
	}
	return r
}

// totpCodeNow 用标准库按当前时间计算 6 位 TOTP（RFC 6238），供端到端登录测试用。
func totpCodeNow(t *testing.T, secretB32 string) string {
	t.Helper()
	return totpCodeAtStep(t, secretB32, time.Now().Unix()/30)
}

// totpCodeNext 计算相对当前时间步 +1 的验证码：引导绑定刚消费了当前步，
// 端到端测试用下一步的新码验证正常登录（避开重放防护）。
func totpCodeNext(t *testing.T, secretB32 string) string {
	t.Helper()
	return totpCodeAtStep(t, secretB32, time.Now().Unix()/30+1)
}

// totpCodeAtStep 计算指定时间步的 6 位 TOTP。
func totpCodeAtStep(t *testing.T, secretB32 string, step int64) string {
	t.Helper()
	s := strings.ToUpper(strings.TrimRight(strings.ReplaceAll(secretB32, " ", ""), "="))
	key, err := base32.StdEncoding.WithPadding(base32.NoPadding).DecodeString(s)
	if err != nil {
		t.Fatalf("解码 TOTP 密钥失败: %v", err)
	}
	var buf [8]byte
	binary.BigEndian.PutUint64(buf[:], uint64(step))
	mac := hmac.New(sha1.New, key)
	mac.Write(buf[:])
	sum := mac.Sum(nil)
	off := sum[len(sum)-1] & 0x0f
	bin := (uint32(sum[off])&0x7f)<<24 | uint32(sum[off+1])<<16 | uint32(sum[off+2])<<8 | uint32(sum[off+3])
	return fmt.Sprintf("%06d", bin%1000000)
}

// ---- 测试 ----

// TestAuthDisabledByDefault 不传认证参数时行为与改前一致（敞开、无登录页）。
func TestAuthDisabledByDefault(t *testing.T) {
	ts, _ := newAuthServer(t, Options{})
	st := authStatus(t, ts.URL)
	if st.Enabled {
		t.Fatalf("默认不应启用认证: %+v", st)
	}
	r := get(t, ts.URL+"/api/list?path=/")
	r.Body.Close()
	if r.StatusCode != 200 {
		t.Fatalf("默认应敞开, 得到 %d", r.StatusCode)
	}
}

// TestAuthPasswordFlow 引导 → 登录 → 访问 → 登出 全流程。
func TestAuthPasswordFlow(t *testing.T) {
	ts, client := newAuthServer(t, Options{AuthMode: "password"})

	st := authStatus(t, ts.URL)
	if !st.Enabled || !st.NeedsSetup || st.Mode != "password" {
		t.Fatalf("引导状态异常: %+v", st)
	}
	// 未认证：API 401
	r := get(t, ts.URL+"/api/list")
	r.Body.Close()
	if r.StatusCode != 401 {
		t.Fatalf("引导前 API 应 401, 得到 %d", r.StatusCode)
	}
	// 弱口令被拒
	w := postJSONClient(t, client, ts.URL+"/api/auth/setup", `{"password":"short"}`)
	w.Body.Close()
	if w.StatusCode != 400 {
		t.Fatalf("弱口令应 400, 得到 %d", w.StatusCode)
	}
	// 设置口令
	s := postJSONClient(t, client, ts.URL+"/api/auth/setup", `{"password":"password123"}`)
	s.Body.Close()
	if s.StatusCode != 200 {
		t.Fatalf("设置口令应 200, 得到 %d", s.StatusCode)
	}
	st = authStatus(t, ts.URL)
	if st.NeedsSetup || st.Authenticated {
		t.Fatalf("设置后应转入登录态: %+v", st)
	}
	// 错误口令 401
	if code := authLogin(t, client, ts.URL, "wrongpass", "", false); code != http.StatusUnauthorized {
		t.Fatalf("错误口令应 401, 得到 %d", code)
	}
	// 正确口令 → cookie → 访问
	if code := authLogin(t, client, ts.URL, "password123", "", false); code != http.StatusOK {
		t.Fatalf("正确口令应 200, 得到 %d", code)
	}
	lr := getWithClient(t, client, ts.URL+"/api/list?path=/")
	lr.Body.Close()
	if lr.StatusCode != 200 {
		t.Fatalf("登录后应可访问, 得到 %d", lr.StatusCode)
	}
	// 登出 → 旧 token 失效
	lo, _ := client.Post(ts.URL+"/api/auth/logout", "application/json", nil)
	lo.Body.Close()
	if lo.StatusCode != http.StatusNoContent {
		t.Fatalf("登出应 204, 得到 %d", lo.StatusCode)
	}
	after := getWithClient(t, client, ts.URL+"/api/list")
	after.Body.Close()
	if after.StatusCode != 401 {
		t.Fatalf("登出后应 401, 得到 %d", after.StatusCode)
	}
}

// TestAuthTOTPFlow 仅 TOTP：必须绑定、登录只填验证码、重放被拒。
func TestAuthTOTPFlow(t *testing.T) {
	ts, client := newAuthServer(t, Options{AuthMode: "totp"})

	gr := getWithClient(t, client, ts.URL+"/api/auth/setup")
	var setup struct {
		NeedTOTP   bool   `json:"need_totp"`
		NeedPw     bool   `json:"need_password"`
		Secret     string `json:"totp_secret"`
		OTPAuthURL string `json:"otpauth_url"`
	}
	json.NewDecoder(gr.Body).Decode(&setup)
	gr.Body.Close()
	if !setup.NeedTOTP || setup.NeedPw || setup.Secret == "" || setup.OTPAuthURL == "" {
		t.Fatalf("totp 引导状态异常: %+v", setup)
	}
	// 跳过 TOTP 必须被拒
	skip := postJSONClient(t, client, ts.URL+"/api/auth/setup", `{"skip_totp":true}`)
	skip.Body.Close()
	if skip.StatusCode != 400 {
		t.Fatalf("totp 模式跳过应 400, 得到 %d", skip.StatusCode)
	}
	// 正确码绑定
	code := totpCodeNow(t, setup.Secret)
	bind := postJSONClient(t, client, ts.URL+"/api/auth/setup", `{"code":"`+code+`"}`)
	bind.Body.Close()
	if bind.StatusCode != 200 {
		t.Fatalf("绑定 TOTP 应 200, 得到 %d", bind.StatusCode)
	}
	// 登录只填验证码（无密码字段）。绑定刚消费了当前时间步，故用下一步的新码
	// （同一码重放被拒是 F3 的预期行为，见 TestAuthTOTPSetupConsumesCode）。
	if code := authLogin(t, client, ts.URL, "", totpCodeNext(t, setup.Secret), false); code != http.StatusOK {
		t.Fatalf("仅验证码登录应 200, 得到 %d", code)
	}
}

// TestAuthSetupAtomic 引导提交的原子性（F1）：password+totp 下验证码错误返回
// 400，且不得留下任何部分状态（status 仍 needs_setup=true、password_set=false），
// 随后仅凭口令登录必须 401；用正确码重试才成功。
func TestAuthSetupAtomic(t *testing.T) {
	ts, client := newAuthServer(t, Options{AuthMode: "password+totp"})

	gr := getWithClient(t, client, ts.URL+"/api/auth/setup")
	var setup struct {
		Secret   string `json:"totp_secret"`
		NeedPw   bool   `json:"need_password"`
		NeedTotp bool   `json:"need_totp"`
	}
	if err := json.NewDecoder(gr.Body).Decode(&setup); err != nil {
		t.Fatal(err)
	}
	gr.Body.Close()
	if !setup.NeedPw || !setup.NeedTotp || setup.Secret == "" {
		t.Fatalf("引导状态异常: %+v", setup)
	}
	// 口令合法 + 错误验证码：整笔拒绝
	body, _ := json.Marshal(map[string]any{"password": "password123", "code": "000000"})
	r := postJSONClient(t, client, ts.URL+"/api/auth/setup", string(body))
	r.Body.Close()
	if r.StatusCode != http.StatusBadRequest {
		t.Fatalf("错误验证码应 400, 得到 %d", r.StatusCode)
	}
	// 不得留下部分状态
	st := authStatus(t, ts.URL)
	if !st.NeedsSetup || st.PasswordSet || st.TOTPBound || st.Configured {
		t.Fatalf("失败后不得留下部分状态: %+v", st)
	}
	// 仅凭口令登录必须 401
	if code := authLogin(t, client, ts.URL, "password123", "", false); code != http.StatusUnauthorized {
		t.Fatalf("未完成双因素配置时仅口令登录应 401, 得到 %d", code)
	}
	// 用正确码重试：原子成功
	bind := postJSONClient(t, client, ts.URL+"/api/auth/setup",
		`{"password":"password123","code":"`+totpCodeNow(t, setup.Secret)+`"}`)
	bind.Body.Close()
	if bind.StatusCode != http.StatusOK {
		t.Fatalf("正确验证码重试应 200, 得到 %d", bind.StatusCode)
	}
	st = authStatus(t, ts.URL)
	if st.NeedsSetup || !st.PasswordSet || !st.TOTPBound {
		t.Fatalf("重试成功后双因素应就位: %+v", st)
	}
}

// TestAuthTOTPSetupConsumesCode 引导绑定用过的验证码在同一时间步内再次用于
// 登录必须 401（F3）。
func TestAuthTOTPSetupConsumesCode(t *testing.T) {
	ts, client := newAuthServer(t, Options{AuthMode: "totp"})

	gr := getWithClient(t, client, ts.URL+"/api/auth/setup")
	var setup struct {
		Secret string `json:"totp_secret"`
	}
	if err := json.NewDecoder(gr.Body).Decode(&setup); err != nil {
		t.Fatal(err)
	}
	gr.Body.Close()
	if setup.Secret == "" {
		t.Fatal("应返回待绑定 TOTP 密钥")
	}
	code := totpCodeNow(t, setup.Secret)
	bind := postJSONClient(t, client, ts.URL+"/api/auth/setup", `{"code":"`+code+`"}`)
	bind.Body.Close()
	if bind.StatusCode != http.StatusOK {
		t.Fatalf("绑定应 200, 得到 %d", bind.StatusCode)
	}
	// 同一个码立即登录：无论是否跨时间步都命中重放集合，必须 401
	if got := authLogin(t, client, ts.URL, "", code, false); got != http.StatusUnauthorized {
		t.Fatalf("绑定用过的码登录应 401, 得到 %d", got)
	}
}

// TestHealthPublicWhenAuthEnabled 认证开启时 GET /api/health 免鉴权匿名 200
// （F2），响应体不含目录/文件/会话信息；其余 /api/* 维持 401。
func TestHealthPublicWhenAuthEnabled(t *testing.T) {
	ts, _ := newAuthServer(t, Options{Auth: "user:password123"})

	hr := get(t, ts.URL+"/api/health")
	body, _ := io.ReadAll(hr.Body)
	hr.Body.Close()
	if hr.StatusCode != http.StatusOK {
		t.Fatalf("匿名 /api/health 应 200, 得到 %d", hr.StatusCode)
	}
	for _, leaked := range []string{"entries", "sessions", "totp", "path"} {
		if strings.Contains(string(body), leaked) {
			t.Errorf("/api/health 泄漏字段 %q: %s", leaked, body)
		}
	}
	// 其余 /api/* 仍 401
	for _, p := range []string{"/api/info", "/api/list", "/api/search?q=x"} {
		r := get(t, ts.URL+p)
		r.Body.Close()
		if r.StatusCode != http.StatusUnauthorized {
			t.Errorf("匿名 %s 应 401, 得到 %d", p, r.StatusCode)
		}
	}
}

// TestAuthLockout 连续失败触发限速（429）。
func TestAuthLockout(t *testing.T) {
	ts, client := newAuthServer(t, Options{Auth: "user:password123"})
	for i := 0; i < 6; i++ {
		if code := authLogin(t, client, ts.URL, "nope", "", false); code != http.StatusUnauthorized && code != http.StatusTooManyRequests {
			t.Fatalf("第 %d 次应 401/429, 得到 %d", i+1, code)
		}
	}
	if code := authLogin(t, client, ts.URL, "password123", "", false); code != http.StatusTooManyRequests {
		t.Fatalf("锁定后即使正确口令也应 429, 得到 %d", code)
	}
}

// TestAuthCookieFlags 会话 cookie 属性：HttpOnly/SameSite/Path 恒有；
// 勾选记住我带 Max-Age，不勾则无；CookieSecure 时带 Secure。
func TestAuthCookieFlags(t *testing.T) {
	ts, client := newAuthServer(t, Options{Auth: "user:password123", CookieSecure: true})

	// 不勾记住我：会话 cookie（无 Max-Age/Expires），带 Secure
	r := postJSONClient(t, client, ts.URL+"/api/auth/login", `{"password":"password123","remember":false}`)
	r.Body.Close()
	c := findCookie(t, r, "fs_session")
	if c == nil || !c.HttpOnly || c.SameSite != http.SameSiteLaxMode || c.Path != "/" || !c.Secure {
		t.Fatalf("会话 cookie 属性异常: %+v", c)
	}
	if c.MaxAge != 0 || !c.Expires.IsZero() {
		t.Errorf("不勾记住我不应带持久期: MaxAge=%d Expires=%v", c.MaxAge, c.Expires)
	}

	// 勾选记住我：持久 cookie（Max-Age > 0）
	r2 := postJSONClient(t, client, ts.URL+"/api/auth/login", `{"password":"password123","remember":true}`)
	r2.Body.Close()
	c2 := findCookie(t, r2, "fs_session")
	if c2 == nil || c2.MaxAge <= 0 {
		t.Fatalf("记住我应带 Max-Age: %+v", c2)
	}
	if c2.MaxAge < 29*24*3600 {
		t.Errorf("记住我有效期应约 30 天, 得到 %d 秒", c2.MaxAge)
	}
}

func findCookie(t *testing.T, r *http.Response, name string) *http.Cookie {
	t.Helper()
	for _, c := range r.Cookies() {
		if c.Name == name {
			return c
		}
	}
	return nil
}

// TestAuthSessionsAndRevoke 会话列表与撤销。
func TestAuthSessionsAndRevoke(t *testing.T) {
	ts, client := newAuthServer(t, Options{Auth: "user:password123"})
	authLogin(t, client, ts.URL, "password123", "", false)

	// 用第二个独立客户端登录，产生第二个会话
	jar2, _ := cookiejar.New(nil)
	client2 := &http.Client{Jar: jar2}
	authLogin(t, client2, ts.URL, "password123", "", true)

	lr := getWithClient(t, client, ts.URL+"/api/auth/sessions")
	var out struct {
		Sessions []struct {
			ID      string `json:"id"`
			Current bool   `json:"current"`
			UA      string `json:"ua"`
		} `json:"sessions"`
	}
	json.NewDecoder(lr.Body).Decode(&out)
	lr.Body.Close()
	if len(out.Sessions) != 2 {
		t.Fatalf("应有 2 个会话, 得到 %d", len(out.Sessions))
	}
	var otherID string
	for _, s := range out.Sessions {
		if !s.Current {
			otherID = s.ID
		}
		if s.ID == "" {
			t.Error("会话 id 不应为空")
		}
	}
	// 撤销另一个会话后，其 token 失效
	rv := postJSONClient(t, client, ts.URL+"/api/auth/sessions/revoke", `{"id":"`+otherID+`"}`)
	rv.Body.Close()
	if rv.StatusCode != 200 {
		t.Fatalf("撤销应 200, 得到 %d", rv.StatusCode)
	}
	c2r := getWithClient(t, client2, ts.URL+"/api/list")
	c2r.Body.Close()
	if c2r.StatusCode != 401 {
		t.Fatalf("被撤销会话应 401, 得到 %d", c2r.StatusCode)
	}
}

// TestAuthSecurityHeaders 登录/引导页（及主页面）带严格安全头。
func TestAuthSecurityHeaders(t *testing.T) {
	ts, _ := newAuthServer(t, Options{AuthMode: "password"})
	r := get(t, ts.URL+"/")
	r.Body.Close()
	if r.Header.Get("X-Content-Type-Options") != "nosniff" {
		t.Error("缺少 nosniff")
	}
	if r.Header.Get("Referrer-Policy") != "no-referrer" {
		t.Error("缺少 Referrer-Policy: no-referrer")
	}
	csp := r.Header.Get("Content-Security-Policy")
	for _, want := range []string{"default-src 'self'", "frame-ancestors 'none'", "base-uri 'none'"} {
		if !strings.Contains(csp, want) {
			t.Errorf("CSP 缺少 %q: %s", want, csp)
		}
	}
	if cc := r.Header.Get("Cache-Control"); !strings.Contains(cc, "no-store") {
		t.Errorf("缺少 no-store: %q", cc)
	}
}

// TestAuthTOTPQR 二维码端点返回 SVG（自研 qrcode）。
func TestAuthTOTPQR(t *testing.T) {
	ts, client := newAuthServer(t, Options{AuthMode: "totp"})
	getWithClient(t, client, ts.URL+"/api/auth/setup").Body.Close() // 生成待绑定密钥
	r := get(t, ts.URL+"/api/auth/totp-qr")
	data, _ := io.ReadAll(r.Body)
	r.Body.Close()
	if r.StatusCode != 200 || !strings.Contains(r.Header.Get("Content-Type"), "image/svg+xml") {
		t.Fatalf("二维码端点异常: %d %s", r.StatusCode, r.Header.Get("Content-Type"))
	}
	if !strings.Contains(string(data), "<svg") {
		t.Fatalf("应返回 SVG: %s", data[:min(80, len(data))])
	}
}
