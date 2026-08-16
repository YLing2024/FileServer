// Package qrcode 提供一个精简、零第三方依赖的 QR Code 生成器。
//
// 仅用于命令行场景：把短文本（如 URL）编码为 QR 码并渲染成终端可显示的字符图。
// 支持字节模式、纠错等级 M（约 15% 纠错能力）、Version 1~40 自适应。
package qrcode

import "errors"

// ErrTooLong 表示内容超出 QR 码最大容量。
var ErrTooLong = errors.New("qrcode: 内容超出 QR 码最大容量")

// Level 表示纠错等级。
type Level int

const (
	// Low ~7% 纠错能力，容量最大。
	Low Level = iota
	// Medium ~15% 纠错能力。默认值，兼顾容量与抗污损。
	Medium
	// Quartile ~25% 纠错能力。
	Quartile
	// High ~30% 纠错能力，容量最小。
	High
)

// Encode 生成一个 QR 码网格，返回 size x size 的布尔矩阵（true = 深色模块）。
// data 是原始字节；l 为纠错等级。容量不足时返回 ErrTooLong。
func Encode(data []byte, l Level) ([][]bool, error) {
	q, err := build(data, l)
	if err != nil {
		return nil, err
	}
	return q.modules, nil
}

// qr 是编码过程中的内部状态。
type qr struct {
	version      int
	level        Level
	size         int
	modules      [][]bool // 模块颜色
	occupied     [][]bool // 功能图形占位（不可写数据）
	allCodewords []byte   // 交错后的完整码字（数据 + 纠错）
}

// sizeOf 返回某版本的边长（模块数）。
func sizeOf(version int) int { return 17 + 4*version }

// build 完成一次完整编码。
func build(data []byte, l Level) (*qr, error) {
	version, err := chooseVersion(len(data), l)
	if err != nil {
		return nil, err
	}
	q := &qr{
		version:  version,
		level:    l,
		size:     sizeOf(version),
		modules:  alloc(version),
		occupied: alloc(version),
	}

	// 1. 生成含纠错的完整码字流
	bits, err := encodeBits(data, version, l)
	if err != nil {
		return nil, err
	}
	q.allCodewords, err = rsEncode(bits, version, l)
	if err != nil {
		return nil, err
	}

	// 2. 绘制功能图形（finder/timing/alignment/format 占位/version）
	q.drawFinder()
	q.drawTiming()
	q.drawAlignment()
	q.drawReservedFormat()
	q.drawVersion()

	// 3. 选择最佳掩码并最终放置
	best := q.chooseMask()
	q.placeData(q.allCodewords, best)
	q.drawFormat(best)
	return q, nil
}

func alloc(version int) [][]bool {
	s := sizeOf(version)
	m := make([][]bool, s)
	for i := range m {
		m[i] = make([]bool, s)
	}
	return m
}

// snapshot 复制一份「功能图形完成、数据未放置」的干净矩阵状态。
func (q *qr) snapshot() *qr {
	c := &qr{
		version:      q.version,
		level:        q.level,
		size:         q.size,
		modules:      alloc(q.version),
		occupied:     alloc(q.version),
		allCodewords: q.allCodewords,
	}
	copyGrid(c.modules, q.modules)
	copyGrid(c.occupied, q.occupied)
	return c
}

func copyGrid(dst, src [][]bool) {
	for i := range src {
		copy(dst[i], src[i])
	}
}
