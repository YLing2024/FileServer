package server

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"
	"unicode"
)

// ============================================================
// 分享链接：管理端接口 + 提取码限速/cookie。
//
// 公开访问接口在 share_public.go；本文件只处理「谁能创建/查看/撤销分享」，
// 以及公开接口共用的授权件（提取码 cookie、失败限速）。
// ============================================================

const (
	// shareCookieTTL 提取码通过后的短期 cookie 有效期（刷新免重输）。
	shareCookieTTL = 30 * time.Minute
	// 同一 IP 每分钟最多 10 次提取码失败；超过即 429。
	shareCodeWindow      = time.Minute
	shareCodeMaxFailures = 10
	// shareMaxNoteRunes 分享备注名长度上限（仅展示用，不参与路径解析）。
	shareMaxNoteRunes = 60
)

// shareCreateReq POST /api/share 请求体。天数为指针以区分「未传」与「0（永久）」。
type shareCreateReq struct {
	Path         string `json:"path"`
	Days         *int   `json:"days"`
	Code         string `json:"code"`
	MaxDownloads *int   `json:"max_downloads"`
	AllowPreview *bool  `json:"allow_preview"`
	Note         string `json:"note"`
}

// shareCreateResp 创建成功的响应。Code 只在本次创建时返回，服务端不保存明文。
type shareCreateResp struct {
	Token        string `json:"token"`
	URL          string `json:"url"`
	Code         string `json:"code,omitempty"`
	Name         string `json:"name"`
	IsDir        bool   `json:"is_dir"`
	Note         string `json:"note"`
	Created      int64  `json:"created"`
	Expires      int64  `json:"expires"`
	Downloads    int    `json:"downloads"`
	CodeSet      bool   `json:"code_set"`
	Status       string `json:"status"`
	AllowPreview bool   `json:"allow_preview"`
	MaxDownloads int    `json:"max_downloads"`
}

// shareListResp 分享列表。管理页只需要展示信息，不返回记录里的相对路径。
type shareListResp struct {
	Shares []shareListItem `json:"shares"`
}

// shareListItem 管理列表中的一条分享。
type shareListItem struct {
	Token        string `json:"token"`
	URL          string `json:"url"`
	Name         string `json:"name"`
	IsDir        bool   `json:"is_dir"`
	Note         string `json:"note"`
	Created      int64  `json:"created"`
	Expires      int64  `json:"expires"`
	Downloads    int    `json:"downloads"`
	MaxDownloads int    `json:"max_downloads"`
	AllowPreview bool   `json:"allow_preview"`
	CodeSet      bool   `json:"code_set"`
	Status       string `json:"status"`
}

// shareManageGate 分享管理门禁：认证未启用时沿用「全站敞开」语义；启用认证后
// 必须是有效登录会话（不能匿名创建/撤销公开链接）。
func (s *Server) shareManageGate(w http.ResponseWriter, r *http.Request) bool {
	if !s.authEnabled() {
		return true
	}
	_, ok := s.manageSession(w, r)
	return ok
}

// decodeShareBody 解析分享请求体（64 KiB 上限；字段少，不需要更大）。
func decodeShareBody(w http.ResponseWriter, r *http.Request, out any) bool {
	r.Body = http.MaxBytesReader(w, r.Body, 64*1024)
	if err := json.NewDecoder(r.Body).Decode(out); err != nil {
		var tooLarge *http.MaxBytesError
		if errors.As(err, &tooLarge) {
			httpError(w, http.StatusRequestEntityTooLarge, "请求体过大")
			return false
		}
		httpError(w, http.StatusBadRequest, "参数格式错误")
		return false
	}
	return true
}

// shareURL 返回相对链接；前端按 window.location.origin 拼成绝对地址，
// 服务端不硬编码域名/端口。
func shareURL(token string) string { return "/s/" + token }

// validateShareCode 提取码必须是 4 位 ASCII 字母或数字（区分大小写）。
func validateShareCode(code string) bool {
	if len(code) != 4 {
		return false
	}
	for i := 0; i < len(code); i++ {
		c := code[i]
		if (c >= '0' && c <= '9') || (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') {
			continue
		}
		return false
	}
	return true
}

// validShareNote 备注名只用于展示：去空白后不超过 60 个字符，不含控制字符。
func validShareNote(note string) bool {
	if note == "" {
		return true
	}
	if len([]rune(note)) > shareMaxNoteRunes {
		return false
	}
	for _, r := range note {
		if unicode.IsControl(r) {
			return false
		}
	}
	return true
}

// handleShareCreate POST /api/share 创建文件/目录分享。
func (s *Server) handleShareCreate(w http.ResponseWriter, r *http.Request) {
	if !s.shareManageGate(w, r) {
		return
	}
	var req shareCreateReq
	if !decodeShareBody(w, r, &req) {
		return
	}
	days := 7
	if req.Days != nil {
		days = *req.Days
	}
	if days != 0 && days != 1 && days != 7 && days != 30 {
		httpError(w, http.StatusBadRequest, "有效期只能是 1/7/30 天或永久")
		return
	}
	code := strings.TrimSpace(req.Code)
	if code != "" && !validateShareCode(code) {
		httpError(w, http.StatusBadRequest, "提取码必须是 4 位字母或数字")
		return
	}
	maxDownloads := 0
	if req.MaxDownloads != nil {
		maxDownloads = *req.MaxDownloads
	}
	if maxDownloads != 0 && maxDownloads != 10 && maxDownloads != 100 {
		httpError(w, http.StatusBadRequest, "下载次数上限只能是不限/10/100")
		return
	}
	preview := true
	if req.AllowPreview != nil {
		preview = *req.AllowPreview
	}
	note := strings.TrimSpace(req.Note)
	if note == "" {
		note = shareDefaultNote
	}
	if !validShareNote(note) {
		httpError(w, http.StatusBadRequest, "备注名过长或包含控制字符")
		return
	}
	if strings.TrimSpace(req.Path) == "" {
		httpError(w, http.StatusBadRequest, "路径不能为空")
		return
	}
	abs, err := s.safePath(req.Path)
	if err != nil {
		httpError(w, http.StatusBadRequest, "路径不合法")
		return
	}
	if s.hiddenBlocked(abs) {
		httpError(w, http.StatusForbidden, "该路径不可分享")
		return
	}
	fi, err := os.Stat(abs)
	if err != nil {
		httpError(w, http.StatusBadRequest, "路径不合法")
		return
	}

	now := time.Now()
	rec := shareRecord{
		Path:         relOf(s.cfg.root, abs),
		IsDir:        fi.IsDir(),
		Note:         note,
		Created:      now.Unix(),
		AllowPreview: preview,
		MaxDownloads: maxDownloads,
	}
	if days > 0 {
		rec.Expires = now.Add(time.Duration(days) * 24 * time.Hour).Unix()
	}
	created, err := s.shares.create(rec, code)
	if err != nil {
		slog.Error("创建分享失败", "err", err)
		httpError(w, http.StatusInternalServerError, "创建分享失败")
		return
	}

	slog.Info("share created",
		"token", shareTokenPrefix(created.Token),
		"dir", created.IsDir,
		"days", days,
		"max_downloads", maxDownloads,
		"code", created.CodeHash != "",
		"ip", s.clientIP(r),
	)
	writeJSON(w, http.StatusOK, shareCreateResp{
		Token:        created.Token,
		URL:          shareURL(created.Token),
		Code:         code,
		Name:         filepath.Base(created.Path),
		IsDir:        created.IsDir,
		Note:         created.Note,
		Created:      created.Created,
		Expires:      created.Expires,
		Downloads:    created.Downloads,
		CodeSet:      created.CodeHash != "",
		Status:       created.status(now),
		AllowPreview: created.AllowPreview,
		MaxDownloads: created.MaxDownloads,
	})
}

// handleShareList GET /api/share 分享列表（新建在前）。
func (s *Server) handleShareList(w http.ResponseWriter, r *http.Request) {
	if !s.shareManageGate(w, r) {
		return
	}
	now := time.Now()
	recs := s.shares.list()
	items := make([]shareListItem, 0, len(recs))
	for _, rec := range recs {
		items = append(items, shareListItem{
			Token:        rec.Token,
			URL:          shareURL(rec.Token),
			Name:         filepath.Base(rec.Path),
			IsDir:        rec.IsDir,
			Note:         rec.Note,
			Created:      rec.Created,
			Expires:      rec.Expires,
			Downloads:    rec.Downloads,
			MaxDownloads: rec.MaxDownloads,
			AllowPreview: rec.AllowPreview,
			CodeSet:      rec.CodeHash != "",
			Status:       rec.status(now),
		})
	}
	writeJSON(w, http.StatusOK, shareListResp{Shares: items})
}

// handleShareRevoke POST /api/share/revoke 撤销分享：记录保留，链接立即失效。
func (s *Server) handleShareRevoke(w http.ResponseWriter, r *http.Request) {
	if !s.shareManageGate(w, r) {
		return
	}
	var body struct {
		Token string `json:"token"`
	}
	if !decodeShareBody(w, r, &body) {
		return
	}
	token := strings.TrimSpace(body.Token)
	if token == "" {
		httpError(w, http.StatusBadRequest, "缺少 token")
		return
	}
	if _, err := s.shares.revoke(token); err != nil {
		if errors.Is(err, errShareNotFound) {
			httpError(w, http.StatusNotFound, "分享不存在")
			return
		}
		slog.Error("撤销分享失败", "token", shareTokenPrefix(token), "err", err)
		httpError(w, http.StatusInternalServerError, "撤销失败")
		return
	}
	slog.Info("share revoked", "token", shareTokenPrefix(token), "ip", s.clientIP(r))
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

// handleShareDelete DELETE /api/share/record 删除分享记录（不影响源文件）。
func (s *Server) handleShareDelete(w http.ResponseWriter, r *http.Request) {
	if !s.shareManageGate(w, r) {
		return
	}
	var body struct {
		Token string `json:"token"`
	}
	if !decodeShareBody(w, r, &body) {
		return
	}
	token := strings.TrimSpace(body.Token)
	if token == "" {
		httpError(w, http.StatusBadRequest, "缺少 token")
		return
	}
	ok, err := s.shares.delete(token)
	if err != nil {
		slog.Error("删除分享记录失败", "token", shareTokenPrefix(token), "err", err)
		httpError(w, http.StatusInternalServerError, "删除失败")
		return
	}
	if !ok {
		httpError(w, http.StatusNotFound, "分享不存在")
		return
	}
	slog.Info("share record deleted", "token", shareTokenPrefix(token), "ip", s.clientIP(r))
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

// ---- 提取码 cookie ----

// newShareCookieSecret 生成进程级 HMAC 密钥；重启后旧 cookie 失效，用户重新输入
// 提取码即可（比把密钥落盘更简单，也避免多一份需要保护的凭据）。
func newShareCookieSecret() []byte {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		// 极端情况下退化为按时间派生：仍然是随机量，不影响正确性。
		sum := sha256.Sum256([]byte(strconv.FormatInt(time.Now().UnixNano(), 10)))
		return sum[:]
	}
	return b
}

// shareCookieName 每条分享独立 cookie 名，配合 Path 限定，避免多分享互相覆盖。
func (s *Server) shareCookieName(token string) string { return "fs_share_" + token }

// shareCookieValue 生成绑定 token 与时间戳的签名值。
func (s *Server) shareCookieValue(token string, now time.Time) string {
	ts := strconv.FormatInt(now.Unix(), 10)
	mac := hmac.New(sha256.New, s.shareSecret)
	mac.Write([]byte(token + "|" + ts))
	return ts + "." + hex.EncodeToString(mac.Sum(nil))
}

// shareCookieValid 校验 cookie 签名与 30 分钟有效期。只认签名，不认客户端
// 声明的过期时间，避免改 cookie 延长授权。
func (s *Server) shareCookieValid(r *http.Request, token string, now time.Time) bool {
	c, err := r.Cookie(s.shareCookieName(token))
	if err != nil || c.Value == "" {
		return false
	}
	tsPart, sigPart, ok := strings.Cut(c.Value, ".")
	if !ok {
		return false
	}
	ts, err := strconv.ParseInt(tsPart, 10, 64)
	if err != nil {
		return false
	}
	issued := time.Unix(ts, 0)
	if issued.After(now.Add(time.Minute)) || now.Sub(issued) > shareCookieTTL {
		return false
	}
	mac := hmac.New(sha256.New, s.shareSecret)
	mac.Write([]byte(token + "|" + tsPart))
	want, err := hex.DecodeString(sigPart)
	if err != nil || !hmac.Equal(want, mac.Sum(nil)) {
		return false
	}
	return true
}

// setShareCookie 写入短期 HttpOnly cookie。Path 限定到该分享的公开接口，
// 其他分享或管理接口不会收到它。
func (s *Server) setShareCookie(w http.ResponseWriter, r *http.Request, token string, now time.Time) {
	http.SetCookie(w, &http.Cookie{
		Name:     s.shareCookieName(token),
		Value:    s.shareCookieValue(token, now),
		Path:     "/api/public/share/" + token,
		MaxAge:   int(shareCookieTTL / time.Second),
		Expires:  now.Add(shareCookieTTL),
		HttpOnly: true,
		SameSite: http.SameSiteLaxMode,
		Secure:   s.cookieSecure(r),
	})
}

// ---- 提取码失败限速 ----

// shareLimiter 按 IP 记录提取码失败时间戳（滑动 1 分钟窗口）。只对失败计数，
// 成功即清零，避免正常用户被历史错误拖累。
type shareLimiter struct {
	mu    sync.Mutex
	fails map[string][]time.Time
}

// newShareLimiter 创建限速器。
func newShareLimiter() *shareLimiter {
	return &shareLimiter{fails: make(map[string][]time.Time)}
}

// pruneLocked 丢弃窗口外的失败记录并顺带清理空 IP，防止地图无限增长。
func (l *shareLimiter) pruneLocked(ip string, now time.Time) {
	cut := now.Add(-shareCodeWindow)
	old := l.fails[ip]
	kept := old[:0]
	for _, t := range old {
		if t.After(cut) {
			kept = append(kept, t)
		}
	}
	if len(kept) == 0 {
		delete(l.fails, ip)
		return
	}
	l.fails[ip] = kept
}

// blocked 是否已达到失败上限。
func (l *shareLimiter) blocked(ip string, now time.Time) bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.pruneLocked(ip, now)
	return len(l.fails[ip]) >= shareCodeMaxFailures
}

// fail 记录一次失败（窗口外的旧记录不计）。
func (l *shareLimiter) fail(ip string, now time.Time) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.pruneLocked(ip, now)
	l.fails[ip] = append(l.fails[ip], now)
}

// reset 成功后清零该 IP 的失败记录。
func (l *shareLimiter) reset(ip string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	delete(l.fails, ip)
}
