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

// sessionCookie 旧版会话 cookie 名。新流程不再下发 cookie，这里仅用于兼容读取
// 历史会话（只读，不再 Set-Cookie）。
const sessionCookie = "fs_session"

// ticketTTL 短票有效期：媒体/下载/缩略图/HLS 等浏览器原生 GET 请求经 ?t= 鉴权，
// 短票绑定会话、只对 GET 生效；前端在剩余寿命不足 60s 时自动续签。
const ticketTTL = 5 * time.Minute

// bearerPrefix Authorization: Bearer 前缀。
const bearerPrefix = "Bearer "

// requireAuth 全接口门禁：认证启用时，除 /api/auth/* 外的 /api/* 必须带有效会话。
// 非 /api/ 的静态资源放行（前端需要加载才能渲染登录/引导页），静态资源不含
// 任何服务目录内容，故不泄漏。
func (s *Server) requireAuth(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !s.authEnabled() || !strings.HasPrefix(r.URL.Path, "/api/") || s.authExempt(r) {
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

// authExempt 免鉴权路径：
//   - /api/auth/*：登录/引导/状态自身，必须匿名可达；
//   - /api/public/*：分享链接的公开访问接口，只凭 token + 可选提取码授权，
//     不能再叠加登录门禁（否则「别人无需登录即可下载」不成立）；
//   - GET /api/health：运维探活（看门狗、反代健康检查、容器 healthcheck）
//     需要匿名可达，且响应体只有进程/版本信息，不含目录/文件/会话内容。
//
// 其余 /api/*（含 /api/info、/api/list、/api/share）仍要求有效会话。
func (s *Server) authExempt(r *http.Request) bool {
	if strings.HasPrefix(r.URL.Path, "/api/auth/") || strings.HasPrefix(r.URL.Path, "/api/public/") {
		return true
	}
	return r.Method == http.MethodGet && r.URL.Path == "/api/health"
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

// bearerToken 提取 Authorization: Bearer <token> 中的 token（无则空串）。
func bearerToken(r *http.Request) string {
	h := r.Header.Get("Authorization")
	if len(h) > len(bearerPrefix) && strings.EqualFold(h[:len(bearerPrefix)], bearerPrefix) {
		return strings.TrimSpace(h[len(bearerPrefix):])
	}
	return ""
}

// tokenFromRequest 取请求携带的会话 token：Authorization: Bearer 优先，其次兼容
// 读取旧版 cookie。用于登出/会话列表/短票签发等需要 token 本体的处理。
func tokenFromRequest(r *http.Request) string {
	if t := bearerToken(r); t != "" {
		return t
	}
	if c, err := r.Cookie(sessionCookie); err == nil && c.Value != "" {
		return c.Value
	}
	return ""
}

// sessionFromRequest 校验请求凭据，顺序：短票 ?t=（仅 GET）→ Authorization: Bearer
// → 旧版 cookie（只读兼容，不再下发）。短票用于浏览器原生媒体请求；无效凭据返回 false。
func (s *Server) sessionFromRequest(r *http.Request) (auth.SessionInfo, bool) {
	if s.cfg.auth == nil {
		return auth.SessionInfo{}, false
	}
	if r.Method == http.MethodGet {
		if t := r.URL.Query().Get("t"); t != "" {
			if info, ok := s.cfg.auth.ValidateTicket(t); ok {
				return info, true
			}
		}
	}
	if tok := bearerToken(r); tok != "" {
		return s.cfg.auth.Validate(tok)
	}
	// 旧版 cookie 只读兼容：--cookie-secure 时与旧版一致，要求安全传输
	// （旧 cookie 本就是 Secure，浏览器不会在明文连接上发送）。
	if c, err := r.Cookie(sessionCookie); err == nil && c.Value != "" {
		if !s.cfg.cookieSecure || s.cookieSecure(r) {
			return s.cfg.auth.Validate(c.Value)
		}
	}
	return auth.SessionInfo{}, false
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

// cookieSecure 是否视为安全传输：显式配置、直连 https，或信任代理且
// X-Forwarded-Proto=https 时为真。新流程不再下发 cookie，仅用于旧版 cookie
// 兼容路径的传输要求（--cookie-secure 语义的历史延续）。
func (s *Server) cookieSecure(r *http.Request) bool {
	if s.cfg.cookieSecure || r.TLS != nil {
		return true
	}
	return s.cfg.trustProxy && strings.EqualFold(r.Header.Get("X-Forwarded-Proto"), "https")
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

// handleAuthLogin POST /api/auth/login 校验凭据并签发会话 token。token 明文只经
// 响应体交给前端，前端据 remember 决定存 sessionStorage（不勾）或 localStorage（勾选）；
// 服务端不再下发 cookie。
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
		writeJSON(w, http.StatusOK, map[string]any{
			"ok":       true,
			"token":    token,
			"expires":  expires.Unix(),
			"remember": req.Remember,
		})
	case errors.Is(err, auth.ErrLocked):
		// 统一措辞，不泄露锁定阈值细节
		httpError(w, http.StatusTooManyRequests, "尝试过于频繁，请稍后再试")
	default:
		httpError(w, http.StatusUnauthorized, "访问凭据无效")
	}
}

// handleAuthTicket POST /api/auth/ticket 为当前会话签发短票（需已登录）。
// 短票绑定该会话、只对 GET 生效，供浏览器原生媒体/下载/缩略图/HLS 请求经 ?t=
// 鉴权（这些请求无法携带 Authorization 头）；同会话可重复签发。
func (s *Server) handleAuthTicket(w http.ResponseWriter, r *http.Request) {
	if !s.authEnabled() {
		httpError(w, http.StatusNotFound, "认证未启用")
		return
	}
	if s.authUnavailable(w) {
		return
	}
	tok := tokenFromRequest(r)
	if tok == "" {
		httpError(w, http.StatusUnauthorized, "需要访问口令")
		return
	}
	if _, ok := s.cfg.auth.Validate(tok); !ok {
		httpError(w, http.StatusUnauthorized, "需要访问口令")
		return
	}
	ticket, expires, err := s.cfg.auth.IssueTicket(tok, ticketTTL)
	if err != nil {
		httpError(w, http.StatusUnauthorized, "需要访问口令")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ticket": ticket, "expires": expires.Unix()})
}

// handleAuthLogout POST /api/auth/logout 使当前会话 token 失效（前端同时清本地凭据）。
func (s *Server) handleAuthLogout(w http.ResponseWriter, r *http.Request) {
	if s.authEnabled() {
		if tok := tokenFromRequest(r); tok != "" {
			s.cfg.auth.Logout(tok)
		}
	}
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
	writeJSON(w, http.StatusOK, map[string]any{"sessions": s.cfg.auth.Sessions(tokenFromRequest(r))})
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
