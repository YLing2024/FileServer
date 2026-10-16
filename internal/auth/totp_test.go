package auth

import (
	"testing"
	"time"
)

// TestTOTPRFC6238Vectors 用 RFC 6238 附录 B 的 SHA1 测试向量校验 HOTP 截断。
// 秘钥为 ASCII "12345678901234567890"，位数 8。
func TestTOTPRFC6238Vectors(t *testing.T) {
	secret := []byte("12345678901234567890")
	cases := []struct {
		unix int64
		want string
	}{
		{59, "94287082"},
		{1111111109, "07081804"},
		{1111111111, "14050471"},
		{1234567890, "89005924"},
		{2000000000, "69279037"},
		{20000000000, "65353130"},
	}
	for _, c := range cases {
		step := uint64(c.unix / totpPeriod)
		got := totpCode(secret, step, 8)
		if got != mustAtoi(c.want) {
			t.Errorf("t=%d: totpCode = %08d, want %s", c.unix, got, c.want)
		}
	}
}

// TestVerifyTOTPAndReplay 覆盖 ±1 步窗口与重放拒绝。
func TestVerifyTOTPAndReplay(t *testing.T) {
	secret, err := generateTOTPSecret()
	if err != nil {
		t.Fatal(err)
	}
	raw, err := decodeTOTPSecret(secret)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Unix(1_700_000_000, 0)
	code := codeAt(raw, now.Unix()/totpPeriod)

	step, ok := verifyTOTP(raw, code, now, -1)
	if !ok {
		t.Fatal("正确验证码应通过")
	}
	// 同一码重放：上次消费步已知，必须拒绝
	if _, ok := verifyTOTP(raw, code, now, step); ok {
		t.Fatal("同一验证码重放应被拒绝")
	}
	// 时钟前进 30s 后，新步的码应通过
	next := codeAt(raw, now.Unix()/totpPeriod+1)
	if _, ok := verifyTOTP(raw, next, now.Add(30*time.Second), step); !ok {
		t.Fatal("下一个时间步的验证码应通过")
	}
	// 错误码
	if _, ok := verifyTOTP(raw, "000000", now, -1); ok {
		t.Fatal("错误验证码不应通过")
	}
	if _, ok := verifyTOTP(raw, "12345", now, -1); ok {
		t.Fatal("位数不足不应通过")
	}
}

// TestTOTPSkewWindow 断言 ±1 步窗口：当前步的码在前一步/后一步的时刻仍可接受。
func TestTOTPSkewWindow(t *testing.T) {
	secret := []byte("12345678901234567890")
	now := time.Unix(1_700_000_000, 0)
	cur := now.Unix() / totpPeriod
	for _, d := range []int64{-1, 0, 1} {
		code := codeAt(secret, cur+d)
		if _, ok := verifyTOTP(secret, code, now, -1); !ok {
			t.Errorf("±1 步窗口内的码（偏移 %d）应被接受", d)
		}
	}
	// 超出窗口（±2 步）不接受（取更早一步的码，且 lastStep=-1 不拦截）
	code := codeAt(secret, cur+2)
	if _, ok := verifyTOTP(secret, code, now, -1); ok {
		t.Error("窗口外的码不应被接受")
	}
}

func codeAt(secret []byte, step int64) string {
	return formatCode(totpCode(secret, uint64(step), totpDigits))
}

func formatCode(v uint32) string {
	return pad6(v)
}

func pad6(v uint32) string {
	s := mustItoa(v)
	for len(s) < 6 {
		s = "0" + s
	}
	return s
}

func mustItoa(v uint32) string {
	if v == 0 {
		return "0"
	}
	var b [12]byte
	i := len(b)
	for v > 0 {
		i--
		b[i] = byte('0' + v%10)
		v /= 10
	}
	return string(b[i:])
}

func mustAtoi(s string) uint32 {
	var n uint32
	for _, c := range s {
		n = n*10 + uint32(c-'0')
	}
	return n
}
