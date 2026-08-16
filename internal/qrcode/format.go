package qrcode

// 格式信息与版本信息的 BCH 编码，以及掩码评分。

// levelMaskBits 各纠错等级的格式信息指示位（2 位）。
var levelMaskBits = map[Level]int{
	Low:      0x1,
	Medium:   0x0,
	Quartile: 0x3,
	High:     0x2,
}

// formatBits 生成 15 位格式信息（5 位数据 + 10 位 BCH，含固定 XOR 掩码 0x5412）。
func formatBits(l Level, mask int) int {
	data := levelMaskBits[l]<<3 | mask
	// BCH(15,5) 生成多项式 0x537
	g := 0x537
	rem := data << 10
	for i := 14; i >= 10; i-- {
		if rem>>uint(i)&1 == 1 {
			rem ^= g << uint(i-10)
		}
	}
	full := (data<<10 | rem) ^ 0x5412
	return full
}

// versionBits 生成 18 位版本信息（version >= 7）。
func versionBits(version int) int {
	// BCH(18,6) 生成多项式 0x1F25
	g := 0x1F25
	rem := version << 12
	for i := 17; i >= 12; i-- {
		if rem>>uint(i)&1 == 1 {
			rem ^= g << uint(i-12)
		}
	}
	return version<<12 | rem
}

// chooseMask 尝试 8 种掩码，返回评分最低者。
func (q *qr) chooseMask() int {
	base := q.snapshot()

	bestMask := 0
	bestScore := -1
	for m := 0; m < 8; m++ {
		c := base.snapshot()
		c.placeData(c.allCodewords, m)
		c.drawFormat(m)
		score := c.penalty()
		if bestScore == -1 || score < bestScore {
			bestScore = score
			bestMask = m
		}
	}
	return bestMask
}

// penalty 计算掩码罚分（N1~N4 规则）。
func (q *qr) penalty() int {
	n1 := q.penaltyN1()
	n2 := q.penaltyN2()
	n3 := q.penaltyN3()
	n4 := q.penaltyN4()
	return n1 + n2 + n3 + n4
}

// penaltyN1 行/列连续同色 ≥5 的罚分：3 + (长度-5)。
func (q *qr) penaltyN1() int {
	score := 0
	// 行
	for r := 0; r < q.size; r++ {
		run := 1
		for c := 1; c <= q.size; c++ {
			if c < q.size && q.modules[r][c] == q.modules[r][c-1] {
				run++
			} else {
				if run >= 5 {
					score += 3 + run - 5
				}
				run = 1
			}
		}
	}
	// 列
	for c := 0; c < q.size; c++ {
		run := 1
		for r := 1; r <= q.size; r++ {
			if r < q.size && q.modules[r][c] == q.modules[r-1][c] {
				run++
			} else {
				if run >= 5 {
					score += 3 + run - 5
				}
				run = 1
			}
		}
	}
	return score
}

// penaltyN2 2x2 同色块的罚分，每个 +3。
func (q *qr) penaltyN2() int {
	score := 0
	for r := 0; r < q.size-1; r++ {
		for c := 0; c < q.size-1; c++ {
			if q.modules[r][c] == q.modules[r][c+1] &&
				q.modules[r][c] == q.modules[r+1][c] &&
				q.modules[r][c] == q.modules[r+1][c+1] {
				score += 3
			}
		}
	}
	return score
}

// penaltyN3 检测 1:1:3:1:1 模式（两侧各 4 个浅色模块），每个 +40。
// 模式为 00001011101（深色在前）或 10111010000（深色在后）。
func (q *qr) penaltyN3() int {
	score := 0
	// 两种 11 位模式（true=深色）
	patDarkFirst := []bool{false, false, false, false, true, false, true, true, true, false, true}
	patDarkLast := []bool{true, false, true, true, true, false, true, false, false, false, false}

	check := func(m [][]bool, r, c int) bool {
		ok1 := true
		ok2 := true
		for i := 0; i < 11; i++ {
			if m[r][c+i] != patDarkFirst[i] {
				ok1 = false
			}
			if m[r][c+i] != patDarkLast[i] {
				ok2 = false
			}
		}
		return ok1 || ok2
	}

	for r := 0; r < q.size; r++ {
		for c := 0; c < q.size-10; c++ {
			if check(q.modules, r, c) {
				score += 40
			}
		}
	}
	for c := 0; c < q.size; c++ {
		col := make([]bool, q.size)
		for r := 0; r < q.size; r++ {
			col[r] = q.modules[r][c]
		}
		for r := 0; r < q.size-10; r++ {
			// 复用行检测逻辑：直接内联检查
			ok1 := true
			ok2 := true
			for i := 0; i < 11; i++ {
				if col[r+i] != patDarkFirst[i] {
					ok1 = false
				}
				if col[r+i] != patDarkLast[i] {
					ok2 = false
				}
			}
			if ok1 || ok2 {
				score += 40
			}
		}
	}
	return score
}

// penaltyN4 深色模块占比偏离 50% 的罚分。
func (q *qr) penaltyN4() int {
	dark := 0
	for r := 0; r < q.size; r++ {
		for c := 0; c < q.size; c++ {
			if q.modules[r][c] {
				dark++
			}
		}
	}
	total := q.size * q.size
	pct := dark * 100 / total
	prev := pct/5 - 10
	next := (pct+4)/5 - 10
	if prev < 0 {
		prev = -prev
	}
	if next < 0 {
		next = -next
	}
	if prev < next {
		return prev * 10
	}
	return next * 10
}
