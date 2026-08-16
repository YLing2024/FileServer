package qrcode

import "strings"

// ANSI 颜色常量：标准「黑码白底」，扫描器识别最快。
// 深模块 → 白底(47) + 黑前景(30)（黑色块）；浅模块/留白 → 白底(47) + 白前景(37)（白色）。
const (
	ansiReset = "\x1b[0m"
	ansiDark  = "\x1b[30;47m" // 黑字白底 → 深色模块（黑块）
	ansiLight = "\x1b[37;47m" // 白字白底 → 浅色模块/留白（白色）
)

// Render 把二维码矩阵渲染为终端可显示的字符串（Unicode 半块字符 + ANSI 颜色）。
//
// 整体效果是一个「白色矩形画框 + 黑码」：四周留白（quiet zone）与浅色模块共同
// 构成白底，深色模块是黑块，形成标准黑码白底，且白色严格限于二维码矩形内
// （每行结尾与整体结束都明确 reset，不向行尾/后续文本泄漏）。
//
// 需终端支持 ANSI 转义（Windows 10+ / Windows Terminal，由 platform.SetConsoleUTF8
// 启用 VT 处理）。输出重定向到文件时含颜色码，如需纯文本用 RenderPlain。
//
// quiet 为四周留白模块数（建议 ≥2，通常用 4）。返回的多行字符串无尾部换行。
func Render(modules [][]bool, quiet int) string {
	n := len(modules)
	if n == 0 {
		return ""
	}
	rows := (n + 1) / 2
	full := n + 2*quiet // 每行总宽度（模块列数）

	var b strings.Builder
	// 顶部留白：白底（白色矩形上边框）
	for i := 0; i < quiet; i++ {
		b.WriteString(ansiLight)
		writeBlank(&b, full)
		b.WriteString(ansiReset)
		b.WriteByte('\n')
	}
	// 内容行
	for r := 0; r < rows; r++ {
		// 左侧留白：白底
		b.WriteString(ansiLight)
		writeBlank(&b, quiet)
		for c := 0; c < n; c++ {
			top := modules[2*r][c]
			var bottom bool
			if 2*r+1 < n {
				bottom = modules[2*r+1][c]
			}
			if top || bottom {
				b.WriteString(ansiDark) // 深模块：白底黑块
			} else {
				b.WriteString(ansiLight) // 浅模块：白底
			}
			b.WriteString(renderChar(top, bottom))
		}
		// 右侧留白：白底
		b.WriteString(ansiLight)
		writeBlank(&b, quiet)
		b.WriteString(ansiReset)
		b.WriteByte('\n')
	}
	// 底部留白：白底（白色矩形下边框）
	for i := 0; i < quiet; i++ {
		b.WriteString(ansiLight)
		writeBlank(&b, full)
		b.WriteString(ansiReset)
		b.WriteByte('\n')
	}
	return strings.TrimRight(b.String(), "\n")
}

// renderChar 半块字符渲染；颜色由调用方上下文决定（深/浅切换）。
// 上深下深 → '█'；上深下浅 → '▀'；上浅下深 → '▄'；全浅 → ' '。
//（深=黑、浅=白，由外层在打印深/浅模块前切换 ansiDark/ansiLight。）
func renderChar(top, bottom bool) string {
	switch {
	case top && bottom:
		return "█"
	case top && !bottom:
		return "▀"
	case !top && bottom:
		return "▄"
	default:
		return " "
	}
}

// RenderPlain 纯文本渲染（无 ANSI 颜色码），深模块用 '█' 实块、浅模块用空格。
// 供无颜色终端或输出到文件/日志时使用。
func RenderPlain(modules [][]bool, quiet int) string {
	n := len(modules)
	if n == 0 {
		return ""
	}
	rows := (n + 1) / 2
	var b strings.Builder
	for i := 0; i < quiet; i++ {
		writeBlankRow(&b, n+2*quiet)
	}
	for r := 0; r < rows; r++ {
		writeBlank(&b, quiet)
		for c := 0; c < n; c++ {
			top := modules[2*r][c]
			var bottom bool
			if 2*r+1 < n {
				bottom = modules[2*r+1][c]
			}
			b.WriteString(plainChar(top, bottom))
		}
		writeBlank(&b, quiet)
		b.WriteByte('\n')
	}
	for i := 0; i < quiet; i++ {
		writeBlankRow(&b, n+2*quiet)
	}
	return strings.TrimRight(b.String(), "\n")
}

// plainChar 纯文本字符：深模块实块、浅模块空格（无颜色，依赖终端前景/背景对比）。
func plainChar(top, bottom bool) string {
	if top || bottom {
		if top && bottom {
			return "█"
		}
		if top {
			return "▀"
		}
		return "▄"
	}
	return " "
}

func writeBlank(b *strings.Builder, n int) {
	for i := 0; i < n; i++ {
		b.WriteByte(' ')
	}
}

func writeBlankRow(b *strings.Builder, n int) {
	writeBlank(b, n)
	b.WriteByte('\n')
}
