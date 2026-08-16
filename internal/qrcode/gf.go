package qrcode

// Reed-Solomon 纠错编码，工作在 GF(2^8)，生成多项式根为 α^0..α^(n-1)。
// 本原多项式 x^8 + x^4 + x^3 + x^2 + 1 = 0x11D。

// gfExp / gfLog 是 GF(256) 的指数表与对数表（以生成元 α=2 为底）。
var gfExp = [512]byte{}
var gfLog = [256]byte{}

func init() {
	// 构建指数/对数表
	x := 1
	for i := 0; i < 255; i++ {
		gfExp[i] = byte(x)
		gfLog[x] = byte(i)
		x <<= 1
		if x&0x100 != 0 {
			x ^= 0x11D
		}
	}
	// 复制前 255 项到后半段，避免求逆/乘法时取模
	for i := 255; i < 512; i++ {
		gfExp[i] = gfExp[i-255]
	}
}

// gfMul 乘法：a、b 为 0 时返回 0，否则查表。
func gfMul(a, b byte) byte {
	if a == 0 || b == 0 {
		return 0
	}
	return gfExp[int(gfLog[a])+int(gfLog[b])]
}

// rsGenerator 生成次数为 degree 的纠错生成多项式，返回系数（高次在前）。
// g(x) = ∏_{i=0}^{degree-1} (x - α^i)，为首一（monic）多项式：
// 系数按降序存储，g[0] = 1（x^degree 的系数），g[degree] = 常数项。
func rsGenerator(degree int) []byte {
	// g 初始为常数多项式 1（次数 0）
	g := []byte{1}
	for i := 0; i < degree; i++ {
		// 乘以 (x - α^i) = (x + α^i)（GF(2) 下减法即 XOR）
		alpha := gfExp[i]
		// 结果次数 +1：r[0] = g[0]（首一不变），
		// r[k] = g[k] ^ α*g[k-1]（k=1..len(g)-1），r[len(g)] = α*g[len(g)-1]
		next := make([]byte, len(g)+1)
		next[0] = g[0]
		for k := 1; k < len(g); k++ {
			next[k] = g[k] ^ gfMul(alpha, g[k-1])
		}
		next[len(g)] = gfMul(alpha, g[len(g)-1])
		g = next
	}
	return g
}

// rsEncode 对 dataBytes 进行 RS 编码，追加 ecLen 个纠错码字，返回完整码字。
func rsEncodeBytes(dataBytes []byte, ecLen int) []byte {
	gen := rsGenerator(ecLen)
	res := make([]byte, ecLen)
	for _, b := range dataBytes {
		factor := b ^ res[0]
		copy(res, res[1:])
		res[ecLen-1] = 0
		for i := 0; i < ecLen; i++ {
			res[i] ^= gfMul(gen[i+1], factor)
		}
	}
	return res
}
