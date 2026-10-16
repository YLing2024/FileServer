package server

import (
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"strings"
	"time"

	"github.com/YLing2024/FileServer/internal/auth"
	"github.com/YLing2024/FileServer/internal/qrcode"
)

// ============================================================
// 认证 HTTP 层：引导页 / 登录 / 登出 / 会话管理 + 全接口门禁。
// 状态机与凭据存储见 internal/auth；这里只负责 cookie、请求解析与响应。
// ============================================================

// sessionCookie 会话 cookie 名。
const sessionCookie = "fs_session"

// requireAuth 全接口门禁：认证启用时，除 /api/auth/* 外的 /api/* 必须带有效会话。
// 非 /api/ 的静态资源放行（前端需要加载才能渲染登录/引导页），静态资源不含
// 任何服务目录内容，故不泄漏。
func (s *Server) requireAuth(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !s.authEnabled() || strings.HasPrefix(r.URL.Path, "/api/auth/") || !strings.HasPrefix(r.URL.Path, "/api/") {
			next.ServeHTTP(w, r)
			return
		}
		if _, ok := s.sessionFromRequest(r); ok {
			next.ServeHTTP(w, r)
			return
		}
		httpError(w, http.StatusUnauthorized, "需要访问口令")
	})
}

// authEnabled 是否要求认证（认证启用即要求；Manager 初始化失败也保持失败关闭）。
func (s *Server) authEnabled() bool { return s.cfg.authRequired }

// authUnavailable 认证已要求但状态机不可用（失败关闭）：写出 503 并返回 true。
func (s *Server) authUnavailable(w http.ResponseWriter) bool {
	if s.cfg.auth == nil {
		httpError(w, http.StatusServiceUnavailable, "认证不可用")
		return true
	}
	return false
}

// sessionFromRequest 读取并校验请求 cookie 中的会话。
func (s *Server) sessionFromRequest(r *http.Request) (auth.SessionInfo, bool) {
	if s.cfg.auth == nil {
		return auth.SessionInfo{}, false
	}
	c, err := r.Cookie(sessionCookie)
	if err != nil || c.Value == "" {
		return auth.SessionInfo{}, false
	}
	return s.cfg.auth.Validate(c.Value)
}

// clientIP 解析客户端 IP：默认用 TCP 对端；--trust-proxy 时取 X-Forwarded-For
// **最左**一个（原始客户端）。方向写死为最左，文档中说明。
func (s *Server) clientIP(r *http.Request) string {
	if s.cfg.trustProxy {
		if xff := r.Header.Get("X-Forwarded-For"); xff != "" {
			if i := strings.IndexByte(xff, ','); i >= 0 {
				return strings.TrimSpace(xff[:i])
			}
			return strings.TrimSpace(xff)
		}
	}
	if host, _, err := net.SplitHostPort(r.RemoteAddr); err == nil {
		return host
	}
	return r.RemoteAddr
}

// cookieSecure 是否给 cookie 加 Secure：显式配置、直连 https，或信任代理且
// X-Forwarded-Proto=https 时为真。
func (s *Server) cookieSecure(r *http.Request) bool {
	if s.cfg.cookieSecure || r.TLS != nil {
		return true
	}
	return s.cfg.trustProxy && strings.EqualFold(r.Header.Get("X-Forwarded-Proto"), "https")
}

// setSessionCookie 下发会话 cookie：HttpOnly + SameSite=Lax + Path=/；
// remember=true 时带 Max-Age/Expires（持久），否则为会话 cookie（关浏览器即失效）。
func (s *Server) setSessionCookie(w http.ResponseWriter, r *http.Request, token string, expires time.Time, remember bool) {
	c := &http.Cookie{
		Name:     sessionCookie,
		Value:    token,
		Path:     "/",
		HttpOnly: true,
		SameSite: http.SameSiteLaxMode,
		Secure:   s.cookieSecure(r),
	}
	if remember {
		c.Expires = expires
		if secs := int(time.Until(expires).Seconds()); secs > 0 {
			c.MaxAge = secs
		}
	}
	http.SetCookie(w, c)
}

// clearSessionCookie 清除会话 cookie（登出）。
func (s *Server) clearSessionCookie(w http.ResponseWriter, r *http.Request) {
	http.SetCookie(w, &http.Cookie{
		Name:     sessionCookie,
		Value:    "",
		Path:     "/",
		HttpOnly: true,
		SameSite: http.SameSiteLaxMode,
		Secure:   s.cookieSecure(r),
		MaxAge:   -1,
	})
}

// handleAuthStatus GET /api/auth/status 认证状态（公开）。
func (s *Server) handleAuthStatus(w http.ResponseWriter, r *http.Request) {
	if !s.authEnabled() {
		writeJSON(w, http.StatusOK, map[string]any{"enabled": false, "mode": "none", "authenticated": true, "needs_setup": false})
		return
	}
	if s.cfg.auth == nil {
		// 失败关闭：认证已要求但状态机不可用，明确告知前端不可登录
		writeJSON(w, http.StatusOK, map[string]any{"enabled": true, "authenticated": false, "needs_setup": false, "unavailable": true})
		return
	}
	_, authed := s.sessionFromRequest(r)
	writeJSON(w, http.StatusOK, map[string]any{
		"enabled":       true,
		"mode":          string(s.cfg.auth.Mode()),
		"authenticated": authed,
		"needs_setup":   s.cfg.auth.NeedsSetup(),
		"configured":    s.cfg.auth.Configured(),
		"need_password": s.cfg.auth.NeedPassword(),
		"need_totp":     s.cfg.auth.NeedTOTP(),
		"password_set":  s.cfg.auth.PasswordSet(),
		"totp_bound":    s.cfg.auth.TOTPBound(),
	})
}

// handleAuthSetupGet GET /api/auth/setup 引导页初始状态（生成待绑定 TOTP 密钥）。
func (s *Server) handleAuthSetupGet(w http.ResponseWriter, r *http.Request) {
	if !s.authEnabled() {
		httpError(w, http.StatusNotFound, "认证未启用")
		return
	}
	if s.authUnavailable(w) {
		return
	}
	secret, url, needPassword, needTOTP, err := s.cfg.auth.BeginSetup()
	if err != nil {
		httpError(w, http.StatusConflict, "已完成配置")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"mode":          string(s.cfg.auth.Mode()),
		"need_password": needPassword,
		"need_totp":     needTOTP,
		"totp_secret":   secret,
		"otpauth_url":   url,
	})
}

// authSetupReq 引导提交请求体。
type authSetupReq struct {
	Password string `json:"password"`
	Code     string `json:"code"`
	SkipTOTP bool   `json:"skip_totp"`
}

// handleAuthSetupPost POST /api/auth/setup 完成引导。
func (s *Server) handleAuthSetupPost(w http.ResponseWriter, r *http.Request) {
	if !s.authEnabled() {
		httpError(w, http.StatusNotFound, "认证未启用")
		return
	}
	if s.authUnavailable(w) {
		return
	}
	var req authSetupReq
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 64*1024)).Decode(&req); err != nil {
		httpError(w, http.StatusBadRequest, "参数格式错误")
		return
	}
	switch err := s.cfg.auth.CompleteSetup(req.Password, req.Code, req.SkipTOTP); {
	case err == nil:
		writeJSON(w, http.StatusOK, map[string]any{"ok": true})
	case errors.Is(err, auth.ErrWeakPassword):
		httpError(w, http.StatusBadRequest, "口令长度至少 8 位")
	case errors.Is(err, auth.ErrInvalidTOTP):
		httpError(w, http.StatusBadRequest, "验证码不正确")
	case errors.Is(err, auth.ErrAlreadyConfigured):
		httpError(w, http.StatusConflict, "已完成配置")
	default:
		httpError(w, http.StatusInternalServerError, "配置失败")
	}
}

// handleAuthTOTPQR GET /api/auth/totp-qr 返回待绑定 TOTP 的二维码（SVG，自研 qrcode）。
func (s *Server) handleAuthTOTPQR(w http.ResponseWriter, r *http.Request) {
	if !s.authEnabled() {
		httpError(w, http.StatusNotFound, "认证未启用")
		return
	}
	if s.authUnavailable(w) {
		return
	}
	_, url, _, needTOTP, err := s.cfg.auth.BeginSetup()
	if err != nil || !needTOTP {
		httpError(w, http.StatusNotFound, "无需绑定 TOTP")
		return
	}
	m, err := qrcode.Encode([]byte(url), qrcode.Medium)
	if err != nil {
		httpError(w, http.StatusInternalServerError, "二维码生成失败")
		return
	}
	w.Header().Set("Content-Type", "image/svg+xml; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	_, _ = w.Write(qrSVG(m))
}

// authLoginReq 登录请求体。
type authLoginReq struct {
	Password string `json:"password"`
	Code     string `json:"code"`
	Remember bool   `json:"remember"`
}

// handleAuthLogin POST /api/auth/login 登录并下发会话 cookie。
func (s *Server) handleAuthLogin(w http.ResponseWriter, r *http.Request) {
	if !s.authEnabled() {
		httpError(w, http.StatusNotFound, "认证未启用")
		return
	}
	if s.authUnavailable(w) {
		return
	}
	var req authLoginReq
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 64*1024)).Decode(&req); err != nil {
		httpError(w, http.StatusBadRequest, "参数格式错误")
		return
	}
	token, expires, err := s.cfg.auth.Login(req.Password, req.Code, req.Remember, r.UserAgent(), s.clientIP(r))
	switch {
	case err == nil:
		s.setSessionCookie(w, r, token, expires, req.Remember)
		writeJSON(w, http.StatusOK, map[string]any{"ok": true})
	case errors.Is(err, auth.ErrLocked):
		// 统一措辞，不泄露锁定阈值细节
		httpError(w, http.StatusTooManyRequests, "尝试过于频繁，请稍后再试")
	default:
		httpError(w, http.StatusUnauthorized, "访问凭据无效")
	}
}

// handleAuthLogout POST /api/auth/logout 使当前会话失效。
func (s *Server) handleAuthLogout(w http.ResponseWriter, r *http.Request) {
	if s.authEnabled() {
		if c, err := r.Cookie(sessionCookie); err == nil && c.Value != "" {
			s.cfg.auth.Logout(c.Value)
		}
	}
	s.clearSessionCookie(w, r)
	w.WriteHeader(http.StatusNoContent)
}

// handleAuthSessions GET /api/auth/sessions 列出已登录会话（不含 token）。
func (s *Server) handleAuthSessions(w http.ResponseWriter, r *http.Request) {
	if !s.authEnabled() {
		httpError(w, http.StatusNotFound, "认证未启用")
		return
	}
	if s.authUnavailable(w) {
		return
	}
	if _, ok := s.sessionFromRequest(r); !ok {
		httpError(w, http.StatusUnauthorized, "需要访问口令")
		return
	}
	tok := ""
	if c, err := r.Cookie(sessionCookie); err == nil {
		tok = c.Value
	}
	writeJSON(w, http.StatusOK, map[string]any{"sessions": s.cfg.auth.Sessions(tok)})
}

// handleAuthSessionRevoke POST /api/auth/sessions/revoke 撤销指定会话。
func (s *Server) handleAuthSessionRevoke(w http.ResponseWriter, r *http.Request) {
	if !s.authEnabled() {
		httpError(w, http.StatusNotFound, "认证未启用")
		return
	}
	if s.authUnavailable(w) {
		return
	}
	var body struct {
		ID string `json:"id"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 8*1024)).Decode(&body); err != nil || body.ID == "" {
		httpError(w, http.StatusBadRequest, "参数格式错误")
		return
	}
	if !s.cfg.auth.Revoke(body.ID) {
		httpError(w, http.StatusNotFound, "会话不存在")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

// qrSVG 把 QR 模块矩阵渲染为 SVG（quiet 为静默区模块数）。
func qrSVG(modules [][]bool) []byte {
	const quiet = 2
	n := len(modules)
	size := n + quiet*2
	var b strings.Builder
	fmt.Fprintf(&b, `<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 %d %d" shape-rendering="crispEdges">`, size, size)
	b.WriteString(`<rect width="100%" height="100%" fill="#fff"/>`)
	b.WriteString(`<g fill="#000">`)
	for r := 0; r < n; r++ {
		for c := 0; c < n; c++ {
			if modules[r][c] {
				fmt.Fprintf(&b, `<rect x="%d" y="%d" width="1" height="1"/>`, c+quiet, r+quiet)
			}
		}
	}
	b.WriteString(`</g></svg>`)
	return []byte(b.String())
}
