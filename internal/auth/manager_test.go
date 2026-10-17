package auth

import (
	"path/filepath"
	"testing"
	"time"
)

func newTestManager(t *testing.T, mode Mode, preset string) *Manager {
	t.Helper()
	m, err := NewManager(Config{Mode: mode, Path: filepath.Join(t.TempDir(), "auth.json"), PresetPassword: preset, HasPreset: preset != ""})
	if err != nil {
		t.Fatal(err)
	}
	return m
}

// TestPasswordModeFlow 覆盖纯口令模式的引导、登录、登出、记住我。
func TestPasswordModeFlow(t *testing.T) {
	m := newTestManager(t, ModePassword, "")
	if !m.NeedsSetup() || !m.NeedPassword() || m.NeedTOTP() {
		t.Fatalf("初始引导状态异常: setup=%v pw=%v totp=%v", m.NeedsSetup(), m.NeedPassword(), m.NeedTOTP())
	}
	if err := m.CompleteSetup("short", "", true); err != ErrWeakPassword {
		t.Fatalf("弱口令应被拒绝, 得到 %v", err)
	}
	if err := m.CompleteSetup("password123", "", true); err != nil {
		t.Fatalf("设置口令失败: %v", err)
	}
	if m.NeedsSetup() {
		t.Fatal("设置口令后不应再需要引导")
	}
	// 错误口令
	if _, _, err := m.Login("wrongpass", "", false, "ua", "1.1.1.1"); err != ErrInvalidCredentials {
		t.Fatalf("错误口令应拒绝, 得到 %v", err)
	}
	// 正确口令 + 不勾记住我（token 有效期统一 7 天）
	tok, exp, err := m.Login("password123", "", false, "ua", "1.1.1.1")
	if err != nil {
		t.Fatalf("登录失败: %v", err)
	}
	if d := time.Until(exp); d > 7*24*time.Hour || d < 7*24*time.Hour-2*time.Minute {
		t.Errorf("会话有效期应约 7 天, 得到 %v", d)
	}
	if _, ok := m.Validate(tok); !ok {
		t.Fatal("token 应有效")
	}
	// 勾选记住我同样 7 天（客户端存储决定"是否还要重新输"，服务端 TTL 一致）
	tok2, exp2, _ := m.Login("password123", "", true, "ua", "1.1.1.1")
	if d := time.Until(exp2); d > 7*24*time.Hour || d < 7*24*time.Hour-2*time.Minute {
		t.Errorf("勾选记住我的 token 有效期也应约 7 天, 得到 %v", d)
	}
	// 会话列表 + 撤销
	list := m.Sessions(tok2)
	if len(list) != 2 {
		t.Fatalf("会话数 = %d, want 2", len(list))
	}
	m.RevokeToken(tok)
	if _, ok := m.Validate(tok); ok {
		t.Fatal("登出后 token 应失效")
	}
	if _, ok := m.Validate(tok2); !ok {
		t.Fatal("未登出的 token 仍应有效")
	}
}

// TestTOTPModeFlow 覆盖仅 TOTP 模式：必须绑定、登录只填验证码、重放拒绝。
func TestTOTPModeFlow(t *testing.T) {
	m := newTestManager(t, ModeTOTP, "")
	fixed := time.Unix(1_700_000_000, 0)
	m.now = func() time.Time { return fixed }

	if !m.NeedsSetup() || m.NeedPassword() || !m.NeedTOTP() {
		t.Fatalf("totp 模式初始状态异常")
	}
	secret, url, needPw, needTotp, err := m.BeginSetup()
	if err != nil || secret == "" || needPw || !needTotp {
		t.Fatalf("BeginSetup 异常: secret=%q err=%v", secret, err)
	}
	if url == "" {
		t.Fatal("应返回 otpauth URL")
	}
	raw, err := decodeTOTPSecret(secret)
	if err != nil {
		t.Fatal(err)
	}
	// 跳过 TOTP 必须被拒绝
	if err := m.CompleteSetup("", "", true); err != ErrInvalidTOTP {
		t.Fatalf("totp 模式不可跳过, 得到 %v", err)
	}
	// 错误验证码
	if err := m.CompleteSetup("", "000000", false); err != ErrInvalidTOTP {
		t.Fatalf("错误验证码应拒绝, 得到 %v", err)
	}
	// 正确验证码绑定
	step := fixed.Unix() / totpPeriod
	if err := m.CompleteSetup("", codeAt(raw, step), false); err != nil {
		t.Fatalf("绑定 TOTP 失败: %v", err)
	}
	if m.NeedsSetup() {
		t.Fatal("绑定后不应再需要引导")
	}
	// 登录只用验证码
	code := codeAt(raw, step+1)
	m.now = func() time.Time { return fixed.Add(30 * time.Second) }
	if _, _, err := m.Login("", code, false, "ua", "2.2.2.2"); err != nil {
		t.Fatalf("仅验证码登录失败: %v", err)
	}
	// 同一码重放
	if _, _, err := m.Login("", code, false, "ua", "2.2.2.2"); err != ErrInvalidCredentials {
		t.Fatalf("重放应被拒绝, 得到 %v", err)
	}
}

// TestCompleteSetupAtomicOnBadTOTP 断言 password+totp 引导提交的原子性（F1）：
// 验证码错误时整笔拒绝，不得留下任何部分状态——仍处于引导中、口令未设，
// 且随后仅凭口令登录必须失败（不能退化成"看起来配好了、其实只有单因素"）。
func TestCompleteSetupAtomicOnBadTOTP(t *testing.T) {
	m := newTestManager(t, ModePasswordTOTP, "")
	fixed := time.Unix(1_700_000_000, 0)
	m.now = func() time.Time { return fixed }

	secret, _, needPw, needTotp, err := m.BeginSetup()
	if err != nil || !needPw || !needTotp || secret == "" {
		t.Fatalf("BeginSetup 异常: err=%v need_pw=%v need_totp=%v", err, needPw, needTotp)
	}
	// 口令合法但验证码错误
	if err := m.CompleteSetup("password123", "000000", false); err != ErrInvalidTOTP {
		t.Fatalf("错误验证码应返回 ErrInvalidTOTP, 得到 %v", err)
	}
	// 无部分状态
	if !m.NeedsSetup() {
		t.Error("提交失败后应仍处于引导中 (needs_setup=true)")
	}
	if m.PasswordSet() || m.TOTPBound() || m.Configured() {
		t.Errorf("提交失败后不得留下部分状态: password_set=%v totp_bound=%v configured=%v",
			m.PasswordSet(), m.TOTPBound(), m.Configured())
	}
	// 仅凭口令登录必须被拒
	if _, _, err := m.Login("password123", "", false, "ua", "5.5.5.5"); err != ErrInvalidCredentials {
		t.Fatalf("未完成配置时仅口令登录应被拒, 得到 %v", err)
	}
	// 用正确码重试应原子成功，双因素就位
	raw, err := decodeTOTPSecret(secret)
	if err != nil {
		t.Fatal(err)
	}
	if err := m.CompleteSetup("password123", codeAt(raw, fixed.Unix()/totpPeriod), false); err != nil {
		t.Fatalf("正确验证码重试应成功: %v", err)
	}
	if !m.PasswordSet() || !m.TOTPBound() || m.NeedsSetup() {
		t.Fatalf("重试成功后双因素应就位: password_set=%v totp_bound=%v needs_setup=%v",
			m.PasswordSet(), m.TOTPBound(), m.NeedsSetup())
	}
	if _, _, err := m.Login("password123", "", false, "ua", "5.5.5.5"); err != ErrInvalidCredentials {
		t.Fatalf("双因素下仅口令登录应被拒, 得到 %v", err)
	}
}

// TestSetupConsumesTOTPStep 断言引导绑定用过的验证码计入重放集合（F3）：
// 同一步内该码再次登录必须被拒；窗口语义不变——±2 步拒绝、窗口内未用码可登录。
func TestSetupConsumesTOTPStep(t *testing.T) {
	m := newTestManager(t, ModeTOTP, "")
	fixed := time.Unix(1_700_000_000, 0)
	m.now = func() time.Time { return fixed }

	secret, _, _, _, err := m.BeginSetup()
	if err != nil {
		t.Fatal(err)
	}
	raw, err := decodeTOTPSecret(secret)
	if err != nil {
		t.Fatal(err)
	}
	cur := fixed.Unix() / totpPeriod
	if err := m.CompleteSetup("", codeAt(raw, cur), false); err != nil {
		t.Fatalf("绑定失败: %v", err)
	}
	// 绑定步已计入已用集合：同一步同码登录一律 401
	if _, _, err := m.Login("", codeAt(raw, cur), false, "ua", "7.7.7.7"); err != ErrInvalidCredentials {
		t.Fatalf("绑定用过的码再登录应被拒, 得到 %v", err)
	}
	// 窗口语义不变：±2 步（cur+2）在 now=cur 时拒绝
	if _, _, err := m.Login("", codeAt(raw, cur+2), false, "ua", "7.7.7.7"); err != ErrInvalidCredentials {
		t.Fatalf("窗口外（+2 步）的码应被拒, 得到 %v", err)
	}
	// 窗口内未用步（cur+1）可登录
	if _, _, err := m.Login("", codeAt(raw, cur+1), false, "ua", "7.7.7.7"); err != nil {
		t.Fatalf("窗口内未用码应可登录: %v", err)
	}
}

// TestPasswordTOTPSkip 覆盖 password+totp 的跳过路径（退化为单因素）。
func TestPasswordTOTPSkip(t *testing.T) {
	m := newTestManager(t, ModePasswordTOTP, "")
	if err := m.CompleteSetup("password123", "", true); err != nil {
		t.Fatalf("跳过 TOTP 失败: %v", err)
	}
	if m.NeedsSetup() {
		t.Fatal("设口令并跳过 TOTP 后视为配置完成")
	}
	if m.NeedTOTP() {
		t.Fatal("跳过后不应再要求 TOTP")
	}
	if _, _, err := m.Login("password123", "", false, "ua", "3.3.3.3"); err != nil {
		t.Fatalf("单因素登录失败: %v", err)
	}
}

// TestLoginLimiterLadder 连续失败触发阶梯锁定（60s → 300s → 900s）。
func TestLoginLimiterLadder(t *testing.T) {
	m := newTestManager(t, ModeTOTP, "")
	fixed := time.Unix(1_700_000_000, 0)
	now := fixed
	m.now = func() time.Time { return now }
	secret, _, _, _, _ := m.BeginSetup()
	raw, _ := decodeTOTPSecret(secret)
	step := fixed.Unix() / totpPeriod
	_ = m.CompleteSetup("", codeAt(raw, step), false)

	ip := "9.9.9.9"
	for i := 0; i < lockThreshold; i++ {
		if _, _, err := m.Login("", "000000", false, "ua", ip); err != ErrInvalidCredentials {
			t.Fatalf("第 %d 次失败应返回凭证错误, 得到 %v", i+1, err)
		}
	}
	// 达阈值后应锁定，即使给正确码也拒绝
	if _, _, err := m.Login("", codeAt(raw, step+1), false, "ua", ip); err != ErrLocked {
		t.Fatalf("应被锁定, 得到 %v", err)
	}
	if d, locked := m.limiter.locked(ip, now); !locked || d > 60*time.Second {
		t.Fatalf("首次锁定应为 60s, 得到 %v locked=%v", d, locked)
	}
	// 锁定过期后再次失败，进入第二级
	now = now.Add(61 * time.Second)
	for i := 0; i < lockThreshold; i++ {
		m.Login("", "000000", false, "ua", ip)
	}
	if d, locked := m.limiter.locked(ip, now); !locked || d > 300*time.Second || d < 299*time.Second {
		t.Fatalf("第二次锁定应为 300s, 得到 %v locked=%v", d, locked)
	}
	// 成功登录会清零
	now = now.Add(301 * time.Second)
	if _, _, err := m.Login("", codeAt(raw, now.Unix()/totpPeriod), false, "ua", ip); err != nil {
		t.Fatalf("锁定过期后正确码应登录成功: %v", err)
	}
	if _, locked := m.limiter.locked(ip, now); locked {
		t.Fatal("成功登录后不应仍锁定")
	}
}

// TestResetTOTPAndAll 覆盖命令行自救：重置 TOTP 与重置全部凭据。
func TestResetTOTPAndAll(t *testing.T) {
	m := newTestManager(t, ModeTOTP, "")
	fixed := time.Unix(1_700_000_000, 0)
	m.now = func() time.Time { return fixed }
	secret, _, _, _, _ := m.BeginSetup()
	raw, _ := decodeTOTPSecret(secret)
	if err := m.CompleteSetup("", codeAt(raw, fixed.Unix()/totpPeriod), false); err != nil {
		t.Fatal(err)
	}
	if err := m.ResetTOTP(); err != nil {
		t.Fatal(err)
	}
	if !m.NeedsSetup() {
		t.Fatal("重置 TOTP 后应回到引导")
	}
	if err := m.ResetAll(); err != nil {
		t.Fatal(err)
	}
	if !m.NeedsSetup() {
		t.Fatal("重置全部后应回到引导")
	}
}

// TestPresetPassword --auth user:pass 等价于预置口令（无需引导）。
func TestPresetPassword(t *testing.T) {
	m := newTestManager(t, ModePassword, "sekret")
	if m.NeedsSetup() {
		t.Fatal("预置口令后无需引导")
	}
	if _, _, err := m.Login("sekret", "", false, "ua", "4.4.4.4"); err != nil {
		t.Fatalf("预置口令登录失败: %v", err)
	}
}

// TestTokenTTLSevenDays 断言服务端 token 有效期统一 7 天（--token-days 默认），
// 勾/不勾「记住我」一致。
func TestTokenTTLSevenDays(t *testing.T) {
	m := newTestManager(t, ModePassword, "sekret")
	for _, remember := range []bool{false, true} {
		_, exp, err := m.Login("sekret", "", remember, "ua", "8.8.8.8")
		if err != nil {
			t.Fatalf("remember=%v 登录失败: %v", remember, err)
		}
		if d := time.Until(exp); d > 7*24*time.Hour || d < 7*24*time.Hour-time.Minute {
			t.Errorf("remember=%v token 有效期应约 7 天, 得到 %v", remember, d)
		}
	}
}

// TestTicketTTL 短票默认有效期 5 分钟：到期前有效、到期后失效。
func TestTicketTTL(t *testing.T) {
	m := newTestManager(t, ModePassword, "sekret")
	fixed := time.Unix(1_700_000_000, 0)
	m.now = func() time.Time { return fixed }
	tok, _, err := m.Login("sekret", "", false, "ua", "8.8.8.8")
	if err != nil {
		t.Fatal(err)
	}
	ticket, exp, err := m.IssueTicket(tok, 0)
	if err != nil {
		t.Fatalf("签发短票失败: %v", err)
	}
	if ticket == "" {
		t.Fatal("短票不应为空")
	}
	if d := exp.Sub(fixed); d != 5*time.Minute {
		t.Errorf("短票默认 TTL 应为 5 分钟, 得到 %v", d)
	}
	if _, ok := m.ValidateTicket(ticket); !ok {
		t.Fatal("未过期短票应有效")
	}
	m.now = func() time.Time { return fixed.Add(5 * time.Minute) }
	if _, ok := m.ValidateTicket(ticket); ok {
		t.Fatal("过期短票应失效")
	}
}

// TestTicketBindsSession 短票绑定会话：会话撤销/登出后其短票立即失效。
func TestTicketBindsSession(t *testing.T) {
	m := newTestManager(t, ModePassword, "sekret")
	tok, _, err := m.Login("sekret", "", false, "ua", "8.8.8.8")
	if err != nil {
		t.Fatal(err)
	}
	ticket, _, err := m.IssueTicket(tok, 0)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := m.ValidateTicket(ticket); !ok {
		t.Fatal("短票应有效")
	}
	m.RevokeToken(tok)
	if _, ok := m.ValidateTicket(ticket); ok {
		t.Fatal("会话撤销后短票应失效")
	}
	if _, ok := m.ValidateTicket("unknown-ticket"); ok {
		t.Fatal("未知短票应失效")
	}
	if _, ok := m.ValidateTicket(""); ok {
		t.Fatal("空短票应失效")
	}
}

// TestIssueTicketRequiresSession 无有效会话不能签发短票。
func TestIssueTicketRequiresSession(t *testing.T) {
	m := newTestManager(t, ModePassword, "sekret")
	if _, _, err := m.IssueTicket("nonexistent", 0); err != ErrInvalidCredentials {
		t.Fatalf("未知会话签发短票应失败, 得到 %v", err)
	}
	if _, _, err := m.IssueTicket("", 0); err != ErrInvalidCredentials {
		t.Fatalf("空会话签发短票应失败, 得到 %v", err)
	}
}
