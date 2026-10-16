// Package auth 实现 FileServer 的访问认证：口令哈希（自研 Argon2id）、TOTP
// 双因子、服务端会话与「记住我」。
//
// 设计约束：仓库不引入第三方依赖（唯一例外 golang.org/x/image），因此口令
// 派生与 TOTP 全部基于标准库自研；Argon2id 正确性以 RFC 9106 测试向量校验
// （见 argon2id_test.go），BLAKE2b 以公开测试向量校验（blake2b_test.go）。
package auth

import (
	"encoding/binary"
	"math/bits"
)

// BLAKE2b 常量（RFC 7693）。
var blake2bIV = [8]uint64{
	0x6a09e667f3bcc908, 0xbb67ae8584caa73b, 0x3c6ef372fe94f82b, 0xa54ff53a5f1d36f1,
	0x510e527fade682d1, 0x9b05688c2b3e6c1f, 0x1f83d9abfb41bd6b, 0x5be0cd19137e2179,
}

// blake2bSigma 轮置换表（12 轮，最后两轮复用前两行）。
var blake2bSigma = [12][16]byte{
	{0, 1, 2, 3, 4, 5, 6, 7, 8, 9, 10, 11, 12, 13, 14, 15},
	{14, 10, 4, 8, 9, 15, 13, 6, 1, 12, 0, 2, 11, 7, 5, 3},
	{11, 8, 12, 0, 5, 2, 15, 13, 10, 14, 3, 6, 7, 1, 9, 4},
	{7, 9, 3, 1, 13, 12, 11, 14, 2, 6, 5, 10, 4, 0, 15, 8},
	{9, 0, 5, 7, 2, 4, 10, 15, 14, 1, 11, 12, 6, 8, 3, 13},
	{2, 12, 6, 10, 0, 11, 8, 3, 4, 13, 7, 5, 15, 14, 1, 9},
	{12, 5, 1, 15, 14, 13, 4, 10, 0, 7, 6, 3, 9, 2, 8, 11},
	{13, 11, 7, 14, 12, 1, 3, 9, 5, 0, 15, 4, 8, 6, 2, 10},
	{6, 15, 14, 9, 11, 3, 0, 8, 12, 2, 13, 7, 1, 4, 10, 5},
	{10, 2, 8, 4, 7, 6, 1, 5, 15, 11, 9, 14, 3, 12, 13, 0},
	{0, 1, 2, 3, 4, 5, 6, 7, 8, 9, 10, 11, 12, 13, 14, 15},
	{14, 10, 4, 8, 9, 15, 13, 6, 1, 12, 0, 2, 11, 7, 5, 3},
}

// blake2bG 一轮内的混合函数。
func blake2bG(v *[16]uint64, a, b, c, d int, x, y uint64) {
	v[a] = v[a] + v[b] + x
	v[d] = bits.RotateLeft64(v[d]^v[a], -32)
	v[c] = v[c] + v[d]
	v[b] = bits.RotateLeft64(v[b]^v[c], -24)
	v[a] = v[a] + v[b] + y
	v[d] = bits.RotateLeft64(v[d]^v[a], -16)
	v[c] = v[c] + v[d]
	v[b] = bits.RotateLeft64(v[b]^v[c], -63)
}

// blake2bCompress 压缩一个 128 字节块；final 标记末块（决定 v[14] 取反）。
func blake2bCompress(h *[8]uint64, block []byte, t uint64, final bool) {
	var m [16]uint64
	for i := 0; i < 16; i++ {
		m[i] = binary.LittleEndian.Uint64(block[i*8:])
	}
	var v [16]uint64
	copy(v[:8], h[:])
	copy(v[8:], blake2bIV[:])
	v[12] ^= t
	if final {
		v[14] ^= 0xffffffffffffffff
	}
	for r := 0; r < 12; r++ {
		s := blake2bSigma[r]
		blake2bG(&v, 0, 4, 8, 12, m[s[0]], m[s[1]])
		blake2bG(&v, 1, 5, 9, 13, m[s[2]], m[s[3]])
		blake2bG(&v, 2, 6, 10, 14, m[s[4]], m[s[5]])
		blake2bG(&v, 3, 7, 11, 15, m[s[6]], m[s[7]])
		blake2bG(&v, 0, 5, 10, 15, m[s[8]], m[s[9]])
		blake2bG(&v, 1, 6, 11, 12, m[s[10]], m[s[11]])
		blake2bG(&v, 2, 7, 8, 13, m[s[12]], m[s[13]])
		blake2bG(&v, 3, 4, 9, 14, m[s[14]], m[s[15]])
	}
	for i := 0; i < 8; i++ {
		h[i] ^= v[i] ^ v[i+8]
	}
}

// blake2b 计算 BLAKE2b 摘要（outLen 1..64，无密钥）。
func blake2b(outLen int, data []byte) []byte {
	if outLen < 1 || outLen > 64 {
		panic("blake2b: invalid output length")
	}
	h := blake2bIV
	h[0] ^= 0x01010000 ^ uint64(outLen)

	var t uint64
	// 处理所有非末块：仅当剩余 > 128 时才可继续，确保最后一块（哪怕正好 128 字节）
	// 一定作为末块压缩（BLAKE2b 的 last-block 语义）。
	for len(data) > 128 {
		t += 128
		blake2bCompress(&h, data[:128], t, false)
		data = data[128:]
	}
	var last [128]byte
	copy(last[:], data)
	t += uint64(len(data))
	blake2bCompress(&h, last[:], t, true)

	out := make([]byte, outLen)
	for i := 0; i < outLen; i++ {
		out[i] = byte(h[i/8] >> (8 * (i % 8)))
	}
	return out
}
