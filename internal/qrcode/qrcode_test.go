package qrcode

import "testing"

// 验证 GF(256) 乘法的正确性（用几个已知结果）。
func TestGFMul(t *testing.T) {
	cases := []struct {
		a, b, want byte
	}{
		{2, 3, 6},
		{4, 5, 20},
		{0, 155, 0},
		{155, 0, 0},
		{1, 200, 200},
	}
	for _, c := range cases {
		if got := gfMul(c.a, c.b); got != c.want {
			t.Errorf("gfMul(%d,%d)=%d, want %d", c.a, c.b, got, c.want)
		}
	}
	// 组合性质：a*b = b*a
	for a := 1; a < 256; a++ {
		for b := a; b < 256; b++ {
			if gfMul(byte(a), byte(b)) != gfMul(byte(b), byte(a)) {
				t.Fatalf("gfMul 不满足交换律: %d,%d", a, b)
			}
		}
	}
}

// 验证 RS 编码可被自洽校验：把数据+纠错码字作为多项式，其在生成根处求值应为 0。
func TestRSEncode(t *testing.T) {
	data := []byte{0x10, 0x20, 0x0C, 0x56, 0x61, 0x80, 0xEC, 0x11, 0xEC, 0x11, 0xEC, 0x11, 0xEC, 0x11, 0xEC, 0x11}
	ecLen := 10
	ecc := rsEncodeBytes(data, ecLen)
	full := append(append([]byte{}, data...), ecc...)

	// full 多项式在 α^0..α^(ecLen-1) 处求值应为 0
	for i := 0; i < ecLen; i++ {
		alpha := gfExp[i]
		// Horner 法求值 full(α^i)
		var acc byte
		for _, coef := range full {
			acc = gfMul(acc, alpha) ^ coef
		}
		if acc != 0 {
			t.Fatalf("RS 校验失败：根 α^%d 处求值 = %d", i, acc)
		}
	}
}

// 验证格式信息的 BCH 编码：去掉 XOR 掩码后，15 位码字对生成多项式 0x537 求余应为 0。
func TestFormatBits(t *testing.T) {
	for _, l := range []Level{Low, Medium, Quartile, High} {
		for m := 0; m < 8; m++ {
			full := formatBits(l, m)
			unmasked := full ^ 0x5412
			// BCH 校验：unmasked 对 g=0x537 取余应为 0
			g := 0x537
			rem := unmasked
			for i := 14; i >= 10; i-- {
				if rem>>uint(i)&1 == 1 {
					rem ^= g << uint(i-10)
				}
			}
			if rem != 0 {
				t.Errorf("formatBits(%v,%d)=0x%X 不满足 BCH 校验，余数 0x%X", l, m, full, rem)
			}
		}
	}
}

// 验证版本信息的 BCH 编码：18 位码字对生成多项式 0x1F25 求余应为 0。
func TestVersionBits(t *testing.T) {
	for v := 7; v <= 40; v++ {
		full := versionBits(v)
		g := 0x1F25
		rem := full
		for i := 17; i >= 12; i-- {
			if rem>>uint(i)&1 == 1 {
				rem ^= g << uint(i-12)
			}
		}
		if rem != 0 {
			t.Errorf("versionBits(%d)=0x%X 不满足 BCH 校验，余数 0x%X", v, full, rem)
		}
	}
}

// 端到端：编码一个短文本，校验矩阵基本结构（三个 finder 图案的边角为深色）。
func TestEncodeStructure(t *testing.T) {
	m, err := Encode([]byte("https://example.com"), Medium)
	if err != nil {
		t.Fatal(err)
	}
	s := len(m)
	if s < 21 || s%4 != 1 {
		t.Fatalf("边长异常: %d", s)
	}
	// 左上 finder 四角应为深色
	for _, p := range [][2]int{{0, 0}, {0, 6}, {6, 0}, {6, 6}} {
		if !m[p[0]][p[1]] {
			t.Errorf("左上 finder 角 (%d,%d) 应为深色", p[0], p[1])
		}
	}
	// finder 中心 3x3 应为深色
	for r := 2; r <= 4; r++ {
		for c := 2; c <= 4; c++ {
			if !m[r][c] {
				t.Errorf("finder 中心 (%d,%d) 应为深色", r, c)
			}
		}
	}
}

// 验证容量与版本选择。
func TestChooseVersion(t *testing.T) {
	v, err := chooseVersion(14, Medium)
	if err != nil || v != 1 {
		t.Errorf("15 字节 M 级应选 version 1, got v=%d err=%v", v, err)
	}
	v, _ = chooseVersion(15, Medium)
	if v != 2 {
		t.Errorf("15 字节 M 级应选 version 2 (M 级 v1 容量 14), got v=%d", v)
	}
	if _, err := chooseVersion(3000, High); err == nil {
		t.Errorf("3000 字节 H 级应超容量")
	}
}
