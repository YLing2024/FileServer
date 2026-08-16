package qrcode

// 本文件负责 QR 码矩阵的绘制：功能图形 + 数据放置 + 掩码。

// drawFinder 绘制三个定位图形（左上、右上、左下）。
func (q *qr) drawFinder() {
	for _, c := range [][2]int{{0, 0}, {q.size - 7, 0}, {0, q.size - 7}} {
		r0, c0 := c[0], c[1]
		for r := -1; r <= 7; r++ {
			for cc := -1; cc <= 7; cc++ {
				rr, ccc := r0+r, c0+cc
				if rr < 0 || rr >= q.size || ccc < 0 || ccc >= q.size {
					continue
				}
				// 分隔符（-1 或 7 行/列）为白色；内部 7x7 为靶心图案
				dark := r >= 0 && r <= 6 && cc >= 0 && cc <= 6 &&
					(r == 0 || r == 6 || cc == 0 || cc == 6 || (r >= 2 && r <= 4 && cc >= 2 && cc <= 4))
				q.modules[rr][ccc] = dark
				q.occupied[rr][ccc] = true
			}
		}
	}
}

// drawTiming 绘制时序图形（第 6 行与第 6 列，深色间隔）。
func (q *qr) drawTiming() {
	for i := 8; i < q.size-8; i++ {
		dark := i%2 == 0
		// 行 6
		if !q.occupied[6][i] {
			q.modules[6][i] = dark
			q.occupied[6][i] = true
		}
		// 列 6
		if !q.occupied[i][6] {
			q.modules[i][6] = dark
			q.occupied[i][6] = true
		}
	}
}

// alignmentPositions 返回某版本对齐图案的中心坐标列表。
func alignmentPositions(version int) []int {
	if version == 1 {
		return []int{}
	}
	n := version/7 + 2
	// 最后一个坐标固定为 size-7 = 4*version + 10
	last := 4*version + 10
	// 步长：均分，向下取整后保证为偶数
	step := (last - 6) / (n - 1)
	if step%2 == 1 {
		step--
	}
	pos := make([]int, 0, n)
	pos = append(pos, 6)
	for i := 1; i < n; i++ {
		pos = append(pos, 6+step*i)
	}
	return pos
}

// drawAlignment 绘制对齐图案。
func (q *qr) drawAlignment() {
	pos := alignmentPositions(q.version)
	for _, r := range pos {
		for _, c := range pos {
			// 跳过与定位图形重叠的三个角
			if (r == 6 && c == 6) || (r == 6 && c == q.size-7) || (r == q.size-7 && c == 6) {
				continue
			}
			for dr := -2; dr <= 2; dr++ {
				for dc := -2; dc <= 2; dc++ {
					rr, cc := r+dr, c+dc
					if rr < 0 || rr >= q.size || cc < 0 || cc >= q.size {
						continue
					}
					dark := dr == -2 || dr == 2 || dc == -2 || dc == 2 || (dr == 0 && dc == 0)
					q.modules[rr][cc] = dark
					q.occupied[rr][cc] = true
				}
			}
		}
	}
}

// drawReservedFormat 仅标记格式信息区域为占用（实际绘制在 drawFormat 中）。
// 格式信息共 15 位，分布于左上（第一副本）与右上/左下（第二副本），
// 另加一个固定暗模块。坐标均以 modules[行][列] 表示，与 Nayuki 参考实现一致。
func (q *qr) drawReservedFormat() {
	mark := func(r, c int) {
		if r >= 0 && r < q.size && c >= 0 && c < q.size {
			q.occupied[r][c] = true
		}
	}
	// 第一副本围绕左上 finder：列 8 行 0~8 与 行 8 列 0~8（行/列 6 为时序，跳过）
	for i := 0; i <= 8; i++ {
		if i != 6 {
			mark(i, 8)  // 列 8，行 i
			mark(8, i)  // 行 8，列 i
		}
	}
	// 第二副本 bit 0~7：行 8，列 size-1 .. size-8
	for i := 0; i < 8; i++ {
		mark(8, q.size-1-i)
	}
	// 第二副本 bit 8~14：列 8，行 size-8 .. size-14（即 size-15+i, i=8..14）
	for i := 8; i < 15; i++ {
		mark(q.size-15+i, 8)
	}
	// 暗模块：列 8，行 size-8
	mark(q.size-8, 8)
}

// drawFormat 绘制格式信息（含掩码）。data 为 15 位，bit14 为最高位，bit0 为最低位。
// 采用 Nayuki QR 库验证过的标准布局（modules[行][列]）：
// 第一副本：位 0~5 在列 8 行 0~5，位 6 在 (7,8)，位 7 在 (8,8)，位 8 在 (8,7)，位 9~14 在行 8 列 5~0。
// 第二副本：位 0~7 在行 8 列 size-1..size-8，位 8~14 在列 8 行 size-8..size-14。
// 暗模块固定在 (size-8, 8)（行 size-8、列 8）。
func (q *qr) drawFormat(mask int) {
	data := formatBits(q.level, mask)
	bit := func(i int) bool { return (data>>uint(i))&1 == 1 }

	// 第一副本：左上角
	for i := 0; i <= 5; i++ {
		q.modules[i][8] = bit(i) // (行 i, 列 8) = 位 0~5
	}
	q.modules[7][8] = bit(6) // 位 6
	q.modules[8][8] = bit(7) // 位 7
	q.modules[8][7] = bit(8) // 位 8
	for i := 9; i < 15; i++ {
		q.modules[8][14-i] = bit(i) // 行 8，列 5~0 = 位 9~14
	}

	// 第二副本：左下（行 8，列 size-1-i）
	for i := 0; i < 8; i++ {
		q.modules[8][q.size-1-i] = bit(i) // 位 0~7
	}
	// 第二副本：右上（列 8，行 size-15+i，i=8..14 → 行 10..16）
	for i := 8; i < 15; i++ {
		q.modules[q.size-15+i][8] = bit(i) // 位 8~14
	}

	// 暗模块（行 size-8，列 8）
	q.modules[q.size-8][8] = true
}

// drawVersion 绘制版本信息（仅 version >= 7 需要）。
func (q *qr) drawVersion() {
	if q.version < 7 {
		return
	}
	bits := versionBits(q.version)
	for i := 0; i < 18; i++ {
		bit := ((bits >> uint(i)) & 1) == 1
		a := q.size - 11 + i%3 // 行/列坐标（靠边）
		b := i / 3
		// 左下 3x6
		q.modules[a][b] = bit
		q.occupied[a][b] = true
		// 右上 6x3
		q.modules[b][a] = bit
		q.occupied[b][a] = true
	}
}

// placeData 按 QR 的之字形规则放置码字，并应用掩码。
func (q *qr) placeData(codewords []byte, mask int) {
	// 将码字拆成位（高位在前）
	var bits []bool
	for _, c := range codewords {
		for j := 7; j >= 0; j-- {
			bits = append(bits, c>>uint(j)&1 == 1)
		}
	}

	idx := 0
	// 之字形扫描：right 为每列对的右列索引，从 size-1 起每次减 2（24,22,...,8,6,4,2）。
	// 当 right<=6 时整对左移 1（6→5,4→3,2→1），从而跳过时序列（第 6 列），
	// 覆盖 (24,23),(22,21),...,(8,7),(5,4),(3,2),(1,0) 全部数据列，与 Nayuki 一致。
	for right := q.size - 1; right >= 1; right -= 2 {
		col := right
		if col <= 6 {
			col--
		}
		// 方向：Nayuki 的 (col+1)&2==0 判断（col 为减 1 后的右列索引）
		upward := (col+1)&2 == 0
		for i := 0; i < q.size; i++ {
			r := i
			if upward {
				r = q.size - 1 - i
			}
			for c := 0; c < 2; c++ {
				cc := col - c
				if cc < 0 {
					continue
				}
				if q.occupied[r][cc] {
					continue
				}
				var dark bool
				if idx < len(bits) {
					dark = bits[idx]
					idx++
				}
				if maskFn(mask, r, cc) {
					dark = !dark
				}
				q.modules[r][cc] = dark
			}
		}
	}
}

// maskFn 返回坐标为 (r, c) 时某掩码是否取反。
func maskFn(mask, r, c int) bool {
	switch mask {
	case 0:
		return (r+c)%2 == 0
	case 1:
		return r%2 == 0
	case 2:
		return c%3 == 0
	case 3:
		return (r+c)%3 == 0
	case 4:
		return (r/2+c/3)%2 == 0
	case 5:
		return (r*c)%2+(r*c)%3 == 0
	case 6:
		return ((r*c)%2+(r*c)%3)%2 == 0
	case 7:
		return ((r+c)%2+(r*c)%3)%2 == 0
	}
	return false
}
