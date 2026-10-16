package auth

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"sort"
	"strconv"
	"sync"
	"time"
)

// Mode 认证模式。
type Mode string

// 四种认证形态：none / totp / password / password+totp。
const (
	ModeNone         Mode = "none"
	ModeTOTP         Mode = "totp"
	ModePassword     Mode = "password"
	ModePasswordTOTP Mode = "password+totp"
)

// ValidMode 判断字符串是否为合法认证模式。
func ValidMode(s string) bool {
	switch Mode(s) {
	case ModeNone, ModeTOTP, ModePassword, ModePasswordTOTP:
		return true
	}
	return false
}

// 认证错误（对外统一为同一文案，不区分具体原因）。
var (
	ErrInvalidCredentials = errors.New("invalid credentials")
	ErrLocked             = errors.New("too many attempts")
	ErrAlreadyConfigured  = errors.New("already configured")
	ErrWeakPassword       = errors.New("weak password")
	ErrInvalidTOTP        = errors.New("invalid totp")
)

// Config Manager 构造参数。
type Config struct {
	Mode           Mode
	Path           string        // auth.json 路径
	PresetPassword string        // --auth user:pass 的 pass 部分
	HasPreset      bool          // 是否传了 --auth
	SessionTTL     time.Duration // 不勾「记住我」时的会话有效期（默认 12h）
	RememberDays   int           // 勾选「记住我」的持久 cookie 天数（默认 30）
	TrustProxy     bool          // 是否信任 X-Forwarded-For（取最左）
}

// SessionInfo 对外暴露的会话信息（不含 token 明文/哈希）。
type SessionInfo struct {
	ID       string `json:"id"`
	Created  int64  `json:"created"`
	LastSeen int64  `json:"last_seen"`
	Expires  int64  `json:"expires"`
	Remember bool   `json:"remember"`
	Current  bool   `json:"current"`
	UA       string `json:"ua,omitempty"`
	IP       string `json:"ip,omitempty"`
}

// Manager 认证状态机：口令/TOTP 校验、会话签发与撤销、失败限速。
type Manager struct {
	mu   sync.Mutex
	cfg  Config
	data authFile

	pendingTOTP string // 引导页待确认的 TOTP 密钥（仅内存，确认后落盘）

	limiter *loginLimiter
	now     func() time.Time
}

// NewManager 载入（或初始化）认证状态；--auth 预置口令在首次启动时写入。
func NewManager(cfg Config) (*Manager, error) {
	if cfg.SessionTTL <= 0 {
		cfg.SessionTTL = 12 * time.Hour
	}
	if cfg.RememberDays <= 0 {
		cfg.RememberDays = 30
	}
	m := &Manager{cfg: cfg, limiter: newLoginLimiter(), now: time.Now}
	af, err := loadAuthFile(cfg.Path)
	if err != nil {
		return nil, err
	}
	m.data = af
	m.data.Mode = string(cfg.Mode)

	if cfg.HasPreset && cfg.PresetPassword != "" && m.data.Password == "" &&
		(cfg.Mode == ModePassword || cfg.Mode == ModePasswordTOTP) {
		h, herr := hashPassword(cfg.PresetPassword)
		if herr != nil {
			return nil, herr
		}
		m.data.Password = h
		if err := m.saveLocked(); err != nil {
			return nil, err
		}
	}
	return m, nil
}

// Mode 当前认证模式。
func (m *Manager) Mode() Mode { return Mode(m.data.Mode) }

// Enabled 是否启用认证（none 为关闭）。
func (m *Manager) Enabled() bool { return m.Mode() != ModeNone }

// PasswordSet 是否已设置口令。
func (m *Manager) PasswordSet() bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.data.Password != ""
}

// TOTPBound 是否已绑定 TOTP。
func (m *Manager) TOTPBound() bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.data.TOTPSecret != ""
}

// Configured 认证是否已配置完成（none 视为完成）。
func (m *Manager) Configured() bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.configuredLocked()
}

// NeedsSetup 是否仍需首次引导。
func (m *Manager) NeedsSetup() bool { return m.Enabled() && !m.Configured() }

// NeedPassword 当前是否需要设置口令（引导页）。
func (m *Manager) NeedPassword() bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.needPasswordLocked()
}

// NeedTOTP 当前是否需要绑定 TOTP（引导页）。配置完成后（例如 password+totp
// 跳过了 TOTP）不再要求，补绑属于后续设置项。
func (m *Manager) NeedTOTP() bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.needTOTPLocked() && !m.configuredLocked()
}

// configuredLocked 各模式的完成条件：totp 需密钥；password 需口令；
// password+totp 需口令（TOTP 可跳过，跳过后退化为单因素）。
func (m *Manager) configuredLocked() bool {
	switch m.Mode() {
	case ModeNone:
		return true
	case ModeTOTP:
		return m.data.TOTPSecret != ""
	case ModePassword:
		return m.data.Password != ""
	case ModePasswordTOTP:
		return m.data.Password != ""
	}
	return true
}

func (m *Manager) needPasswordLocked() bool {
	return (m.Mode() == ModePassword || m.Mode() == ModePasswordTOTP) && m.data.Password == ""
}

func (m *Manager) needTOTPLocked() bool {
	return (m.Mode() == ModeTOTP || m.Mode() == ModePasswordTOTP) && m.data.TOTPSecret == ""
}

// BeginSetup 生成（或复用）引导页待绑定的 TOTP 密钥，返回密钥、otpauth URL
// 以及当前需要口令/需要 TOTP 的状态。仅在未配置完成时可用。
func (m *Manager) BeginSetup() (secret, url string, needPassword, needTOTP bool, err error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.configuredLocked() {
		return "", "", false, false, ErrAlreadyConfigured
	}
	needPassword = m.needPasswordLocked()
	needTOTP = m.needTOTPLocked()
	if needTOTP {
		if m.pendingTOTP == "" {
			s, gerr := generateTOTPSecret()
			if gerr != nil {
				return "", "", false, false, gerr
			}
			m.pendingTOTP = s
		}
		secret = m.pendingTOTP
		url = otpauthURL(secret, "FileServer")
	}
	return secret, url, needPassword, needTOTP, nil
}

// CompleteSetup 完成引导：设置口令（如需）并绑定 TOTP（如需，可跳过）。
// totp 模式不可跳过；password+totp 跳过则退化为单因素。
func (m *Manager) CompleteSetup(password, code string, skipTOTP bool) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.configuredLocked() {
		return ErrAlreadyConfigured
	}
	needPassword := m.needPasswordLocked()
	needTOTP := m.needTOTPLocked()

	// 原子性：先跑完全部校验（口令强度、验证码），任一失败直接返回且不改动
	// m.data；全部通过后才一次性应用并落盘。若边校验边写 m.data，验证码失败时
	// 会留下「口令已设、TOTP 未绑」的半配置状态：status 显示 password_set=true、
	// needs_setup=false，仅凭口令即可登录，双因素实际并未启用。
	var newPasswordHash string
	if needPassword {
		if len([]rune(password)) < 8 {
			return ErrWeakPassword
		}
		h, err := hashPassword(password)
		if err != nil {
			return err
		}
		newPasswordHash = h
	}
	if needTOTP && skipTOTP && m.Mode() == ModeTOTP {
		return ErrInvalidTOTP // totp 形态必须绑定，不可跳过
	}
	bindTOTP := needTOTP && !skipTOTP
	var bindStep int64
	if bindTOTP {
		if m.pendingTOTP == "" {
			return ErrInvalidTOTP
		}
		raw, err := decodeTOTPSecret(m.pendingTOTP)
		if err != nil {
			return ErrInvalidTOTP
		}
		step, ok := verifyTOTP(raw, code, m.now(), m.data.LastTOTPStep)
		if !ok {
			return ErrInvalidTOTP
		}
		bindStep = step
	}

	// 全部校验通过：一次性应用并落盘。
	if newPasswordHash != "" {
		m.data.Password = newPasswordHash
	}
	if bindTOTP {
		m.data.TOTPSecret = m.pendingTOTP
		// 绑定同样是「一次成功的 TOTP 校验」，其时间步也必须计入已用集合，
		// 否则同一时间步内该码还能再次用于登录（重放）。
		m.data.LastTOTPStep = bindStep
	}
	m.pendingTOTP = ""
	return m.saveLocked()
}

// Login 校验凭据并签发会话 token。password 与 code 按模式取用；
// 统一返回 ErrInvalidCredentials（不区分具体原因），锁定返回 ErrLocked。
func (m *Manager) Login(password, code string, remember bool, ua, ip string) (token string, expires time.Time, err error) {
	if d, locked := m.limiter.locked(ip, m.now()); locked {
		_ = d
		return "", time.Time{}, ErrLocked
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if !m.configuredLocked() {
		m.limiter.fail(ip, m.now())
		return "", time.Time{}, ErrInvalidCredentials
	}
	nowT := m.now()

	if m.data.Password != "" {
		if !verifyPassword(password, m.data.Password) {
			m.limiter.fail(ip, nowT)
			return "", time.Time{}, ErrInvalidCredentials
		}
	}
	if m.data.TOTPSecret != "" {
		raw, derr := decodeTOTPSecret(m.data.TOTPSecret)
		if derr != nil {
			m.limiter.fail(ip, nowT)
			return "", time.Time{}, ErrInvalidCredentials
		}
		step, ok := verifyTOTP(raw, code, nowT, m.data.LastTOTPStep)
		if !ok {
			m.limiter.fail(ip, nowT)
			return "", time.Time{}, ErrInvalidCredentials
		}
		m.data.LastTOTPStep = step // 消费该时间步（重放防护）
	}

	token, rec, terr := m.newSessionLocked(remember, ua, ip, nowT)
	if terr != nil {
		return "", time.Time{}, terr
	}
	m.limiter.reset(ip)
	if err := m.saveLocked(); err != nil {
		return "", time.Time{}, err
	}
	return token, time.Unix(rec.Expires, 0), nil
}

// Validate 校验会话 token：命中且未过期返回会话信息，并（节流）刷新 last_seen。
func (m *Manager) Validate(token string) (SessionInfo, bool) {
	if token == "" {
		return SessionInfo{}, false
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	h := hashToken(token)
	nowT := m.now()
	changed := false
	for i := range m.data.Sessions {
		s := &m.data.Sessions[i]
		if s.TokenHash != h {
			continue
		}
		if nowT.Unix() >= s.Expires {
			m.data.Sessions = append(m.data.Sessions[:i], m.data.Sessions[i+1:]...)
			_ = m.saveLocked()
			return SessionInfo{}, false
		}
		// 节流：距上次刷新超过 60s 才写盘，避免每请求都落盘
		if nowT.Unix()-s.LastSeen > 60 {
			s.LastSeen = nowT.Unix()
			changed = true
		}
		info := toSessionInfo(*s)
		if changed {
			_ = m.saveLocked()
		}
		return info, true
	}
	return SessionInfo{}, false
}

// Logout 使指定 token 失效。
func (m *Manager) Logout(token string) {
	if token == "" {
		return
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	h := hashToken(token)
	out := m.data.Sessions[:0]
	for _, s := range m.data.Sessions {
		if s.TokenHash != h {
			out = append(out, s)
		}
	}
	m.data.Sessions = out
	_ = m.saveLocked()
}

// Sessions 列出全部有效会话（不含 token），按创建时间倒序；current 标记当前 token。
func (m *Manager) Sessions(currentToken string) []SessionInfo {
	m.mu.Lock()
	defer m.mu.Unlock()
	curHash := hashToken(currentToken)
	nowT := m.now().Unix()
	out := make([]SessionInfo, 0, len(m.data.Sessions))
	for _, s := range m.data.Sessions {
		if nowT >= s.Expires {
			continue
		}
		info := toSessionInfo(s)
		info.Current = s.TokenHash == curHash
		out = append(out, info)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Created > out[j].Created })
	return out
}

// Revoke 撤销指定 id（或当前会话）的 token；返回是否命中。
func (m *Manager) Revoke(id string) bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := m.data.Sessions[:0]
	found := false
	for _, s := range m.data.Sessions {
		if s.ID == id {
			found = true
			continue
		}
		out = append(out, s)
	}
	if found {
		m.data.Sessions = out
		_ = m.saveLocked()
	}
	return found
}

// RevokeToken 撤销指定 token 对应的会话（登出用）。
func (m *Manager) RevokeToken(token string) { m.Logout(token) }

// ResetTOTP 清除 TOTP 绑定（命令行自救：--reset-totp）。
func (m *Manager) ResetTOTP() error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.data.TOTPSecret = ""
	m.data.LastTOTPStep = 0
	m.pendingTOTP = ""
	return m.saveLocked()
}

// ResetAll 清除全部凭据与会话（命令行自救：--reset-auth）。
func (m *Manager) ResetAll() error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.data = authFile{Version: 1, Mode: m.data.Mode}
	m.pendingTOTP = ""
	return m.saveLocked()
}

// newSessionLocked 生成新会话：随机 token（仅返回明文，存储只留 sha256）。
func (m *Manager) newSessionLocked(remember bool, ua, ip string, nowT time.Time) (string, sessionRecord, error) {
	raw := make([]byte, 32)
	if _, err := rand.Read(raw); err != nil {
		return "", sessionRecord{}, err
	}
	token := base64.RawURLEncoding.EncodeToString(raw)
	ttl := m.cfg.SessionTTL
	if remember {
		ttl = time.Duration(m.cfg.RememberDays) * 24 * time.Hour
	}
	rec := sessionRecord{
		ID:        newID(),
		TokenHash: hashToken(token),
		Created:   nowT.Unix(),
		LastSeen:  nowT.Unix(),
		Expires:   nowT.Add(ttl).Unix(),
		Remember:  remember,
		UA:        truncate(ua, 200),
		IP:        ip,
	}
	// 清理过期会话 + 上限保护
	live := m.data.Sessions[:0]
	for _, s := range m.data.Sessions {
		if nowT.Unix() < s.Expires {
			live = append(live, s)
		}
	}
	m.data.Sessions = append(live, rec)
	const maxSessions = 100
	if len(m.data.Sessions) > maxSessions {
		m.data.Sessions = m.data.Sessions[len(m.data.Sessions)-maxSessions:]
	}
	return token, rec, nil
}

func (m *Manager) saveLocked() error {
	return saveAuthFile(m.cfg.Path, &m.data)
}

func toSessionInfo(s sessionRecord) SessionInfo {
	return SessionInfo{ID: s.ID, Created: s.Created, LastSeen: s.LastSeen, Expires: s.Expires, Remember: s.Remember, UA: s.UA, IP: s.IP}
}

// hashToken 会话 token 的 sha256（hex）。
func hashToken(token string) string {
	sum := sha256.Sum256([]byte(token))
	return hex.EncodeToString(sum[:])
}

// newID 会话 id（随机 16 字节 hex）。
func newID() string {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		return strconv.FormatInt(time.Now().UnixNano(), 16)
	}
	return hex.EncodeToString(b[:])
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n]
}

// loginLimiter 按客户端 IP 的阶梯锁定：连续失败达阈值后锁定，
// 逐级加长（60s → 300s → 900s，到顶后维持）。
type loginLimiter struct {
	mu sync.Mutex
	m  map[string]*limiterEntry
}

type limiterEntry struct {
	fails     int
	level     int
	lockUntil time.Time
}

const lockThreshold = 5

var lockLadder = []time.Duration{60 * time.Second, 300 * time.Second, 900 * time.Second}

func newLoginLimiter() *loginLimiter { return &loginLimiter{m: make(map[string]*limiterEntry)} }

// locked 返回该 IP 是否处于锁定及剩余时长。
func (l *loginLimiter) locked(ip string, now time.Time) (time.Duration, bool) {
	l.mu.Lock()
	defer l.mu.Unlock()
	e := l.m[ip]
	if e == nil || e.lockUntil.IsZero() || !now.Before(e.lockUntil) {
		return 0, false
	}
	return e.lockUntil.Sub(now), true
}

// fail 记一次失败；达阈值则按阶梯锁定。
func (l *loginLimiter) fail(ip string, now time.Time) {
	l.mu.Lock()
	defer l.mu.Unlock()
	e := l.m[ip]
	if e == nil {
		e = &limiterEntry{}
		l.m[ip] = e
	}
	if !e.lockUntil.IsZero() && !now.Before(e.lockUntil) {
		e.lockUntil = time.Time{}
	}
	e.fails++
	if e.fails >= lockThreshold {
		idx := e.level
		if idx >= len(lockLadder) {
			idx = len(lockLadder) - 1
		}
		e.lockUntil = now.Add(lockLadder[idx])
		e.level++
		e.fails = 0
	}
}

// reset 成功登录后清空该 IP 的失败记录。
func (l *loginLimiter) reset(ip string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	delete(l.m, ip)
}
