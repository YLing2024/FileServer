package auth

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha1"
	"crypto/subtle"
	"encoding/base32"
	"encoding/binary"
	"fmt"
	"net/url"
	"strings"
	"time"
)

// ============================================================
// TOTP（RFC 6238，SHA1 / 6 位 / 30 秒步长），基于标准库自研。
// 校验用恒定时间比较；时间步重放防护由 store 记录 lastTOTPStep 实现。
// ============================================================

// totpPeriod 时间步长（秒）。
const totpPeriod = 30

// totpDigits 验证码位数。
const totpDigits = 6

// totpSkewSteps 允许的时间步偏移（±1 步，容忍客户端时钟偏差）。
const totpSkewSteps = 1

// generateTOTPSecret 生成 20 字节随机密钥的 base32（无填充）编码。
func generateTOTPSecret() (string, error) {
	raw := make([]byte, 20)
	if _, err := rand.Read(raw); err != nil {
		return "", err
	}
	return base32.StdEncoding.WithPadding(base32.NoPadding).EncodeToString(raw), nil
}

// decodeTOTPSecret 解析用户/存储中的 base32 密钥（容忍空格、大小写与填充）。
func decodeTOTPSecret(secret string) ([]byte, error) {
	s := strings.ToUpper(strings.ReplaceAll(strings.TrimSpace(secret), " ", ""))
	s = strings.TrimRight(s, "=")
	return base32.StdEncoding.WithPadding(base32.NoPadding).DecodeString(s)
}

// otpauthURL 生成供认证器 App 扫码的 otpauth:// URL（label 为账号标识）。
func otpauthURL(secret, label string) string {
	q := url.Values{}
	q.Set("secret", secret)
	q.Set("issuer", "FileServer")
	q.Set("algorithm", "SHA1")
	q.Set("digits", fmt.Sprintf("%d", totpDigits))
	q.Set("period", fmt.Sprintf("%d", totpPeriod))
	return "otpauth://totp/" + url.PathEscape("FileServer:"+label) + "?" + q.Encode()
}

// totpCode 计算指定时间步的验证码（RFC 6238 的 HOTP 截断）。
func totpCode(secret []byte, counter uint64, digits int) uint32 {
	var buf [8]byte
	binary.BigEndian.PutUint64(buf[:], counter)
	mac := hmac.New(sha1.New, secret)
	mac.Write(buf[:])
	sum := mac.Sum(nil)
	off := sum[len(sum)-1] & 0x0f
	bin := (uint32(sum[off])&0x7f)<<24 | uint32(sum[off+1])<<16 | uint32(sum[off+2])<<8 | uint32(sum[off+3])
	mod := uint32(1)
	for i := 0; i < digits; i++ {
		mod *= 10
	}
	return bin % mod
}

// verifyTOTP 校验验证码：在 ±totpSkewSteps 步窗口内匹配，且时间步必须晚于
// lastStep（重放防护）。命中时返回该时间步。
func verifyTOTP(secret []byte, code string, now time.Time, lastStep int64) (int64, bool) {
	code = strings.TrimSpace(code)
	if len(code) != totpDigits {
		return 0, false
	}
	cur := now.Unix() / totpPeriod
	for d := int64(-totpSkewSteps); d <= totpSkewSteps; d++ {
		step := cur + d
		if step <= lastStep {
			continue // 该步（或更早）已被消费：拒重放
		}
		want := fmt.Sprintf("%0*d", totpDigits, totpCode(secret, uint64(step), totpDigits))
		if subtle.ConstantTimeCompare([]byte(want), []byte(code)) == 1 {
			return step, true
		}
	}
	return 0, false
}
