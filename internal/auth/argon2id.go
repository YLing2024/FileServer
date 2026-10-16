package auth

import (
	"crypto/rand"
	"crypto/subtle"
	"encoding/base64"
	"encoding/binary"
	"fmt"
	"math/bits"
	"strings"
)

// ============================================================
// 自研 Argon2id（RFC 9106）。
//
// 为什么自研：仓库不引入第三方依赖，而 Argon2id/bcrypt 都不在 Go 标准库。
// 正确性以 RFC 9106 测试向量校验（argon2id_test.go），BLAKE2b 亦有独立向量。
// 仅在口令派生这一条路径使用；参数默认 m=64MiB, t=3, p=4, tagLen=32。
// ============================================================

const (
	argonVersion = 0x13 // Argon2 v1.3
	argonTypeID  = 2    // Argon2id
)

// argonBlock 1024 字节的工作块（128 个 64 位字，小端）。
type argonBlock [128]uint64

// gb 是 Argon2 的混合函数（比 BLAKE2b 的 G 多一个 32 位乘法项）。
func gb(v *[16]uint64, a, b, c, d int) {
	v[a] = v[a] + v[b] + 2*uint64(uint32(v[a]))*uint64(uint32(v[b]))
	v[d] = bits.RotateLeft64(v[d]^v[a], -32)
	v[c] = v[c] + v[d] + 2*uint64(uint32(v[c]))*uint64(uint32(v[d]))
	v[b] = bits.RotateLeft64(v[b]^v[c], -24)
	v[a] = v[a] + v[b] + 2*uint64(uint32(v[a]))*uint64(uint32(v[b]))
	v[d] = bits.RotateLeft64(v[d]^v[a], -16)
	v[c] = v[c] + v[d] + 2*uint64(uint32(v[c]))*uint64(uint32(v[d]))
	v[b] = bits.RotateLeft64(v[b]^v[c], -63)
}

// argonRound 对 16 个字的向量做一轮 Argon2 置换 P（8 次 gb）。
func argonRound(v *[16]uint64) {
	gb(v, 0, 4, 8, 12)
	gb(v, 1, 5, 9, 13)
	gb(v, 2, 6, 10, 14)
	gb(v, 3, 7, 11, 15)
	gb(v, 0, 5, 10, 15)
	gb(v, 1, 6, 11, 12)
	gb(v, 2, 7, 8, 13)
	gb(v, 3, 4, 9, 14)
}

// fillBlock 计算 next = G(prev, ref)（withXor 时再异或旧 next）：
// 先按行、再按列对 ref^prev 施加置换 P，最后异或回 ref^prev。
// next 可与 ref/prev 别名（先读后写）。
func fillBlock(prev, ref, next *argonBlock, withXor bool) {
	var r, tmp argonBlock
	for i := 0; i < 128; i++ {
		r[i] = ref[i] ^ prev[i]
	}
	tmp = r
	if withXor {
		for i := 0; i < 128; i++ {
			tmp[i] ^= next[i]
		}
	}
	for i := 0; i < 8; i++ {
		argonRound((*[16]uint64)(r[16*i : 16*i+16]))
	}
	colIdx := [16]int{0, 1, 16, 17, 32, 33, 48, 49, 64, 65, 80, 81, 96, 97, 112, 113}
	for i := 0; i < 8; i++ {
		var v [16]uint64
		for k := 0; k < 16; k++ {
			v[k] = r[2*i+colIdx[k]]
		}
		argonRound(&v)
		for k := 0; k < 16; k++ {
			r[2*i+colIdx[k]] = v[k]
		}
	}
	for i := 0; i < 128; i++ {
		next[i] = r[i] ^ tmp[i]
	}
}

// argonHPrime 变长哈希 H'（RFC 9106 3.3）。
func argonHPrime(in []byte, outLen int) []byte {
	var lenBuf [4]byte
	binary.LittleEndian.PutUint32(lenBuf[:], uint32(outLen))
	base := append(lenBuf[:], in...)
	if outLen <= 64 {
		return blake2b(outLen, base)
	}
	r := (outLen+31)/32 - 2
	out := make([]byte, 0, outLen)
	v := blake2b(64, base)
	out = append(out, v[:32]...)
	for i := 2; i <= r; i++ {
		v = blake2b(64, v)
		out = append(out, v[:32]...)
	}
	out = append(out, blake2b(outLen-32*r, v)...)
	return out
}

// argonH0 计算初始哈希（RFC 9106 3.2）。
func argonH0(password, salt, secret, ad []byte, t, m uint32, p uint8, tagLen uint32) []byte {
	var buf []byte
	le32 := func(v uint32) {
		var b [4]byte
		binary.LittleEndian.PutUint32(b[:], v)
		buf = append(buf, b[:]...)
	}
	le32(uint32(p))
	le32(tagLen)
	le32(m)
	le32(t)
	le32(argonVersion)
	le32(argonTypeID)
	le32(uint32(len(password)))
	buf = append(buf, password...)
	le32(uint32(len(salt)))
	buf = append(buf, salt...)
	le32(uint32(len(secret)))
	buf = append(buf, secret...)
	le32(uint32(len(ad)))
	buf = append(buf, ad...)
	return blake2b(64, buf)
}

// indexAlpha 计算参考块的索引（RFC 9106 3.4.1.2）。
func indexAlpha(pass, slice, index, laneLength, segmentLength uint32, sameLane bool, pseudoRand uint32) uint32 {
	var refArea uint32
	if pass == 0 {
		if slice == 0 {
			refArea = index - 1
		} else if sameLane {
			refArea = slice*segmentLength + index - 1
		} else if index == 0 {
			refArea = slice*segmentLength - 1
		} else {
			refArea = slice * segmentLength
		}
	} else if sameLane {
		refArea = laneLength - segmentLength + index - 1
	} else if index == 0 {
		refArea = laneLength - segmentLength - 1
	} else {
		refArea = laneLength - segmentLength
	}
	rel := uint64(pseudoRand)
	rel = rel * rel >> 32
	rel = uint64(refArea) - 1 - (uint64(refArea)*rel)>>32
	var start uint32
	if pass != 0 && slice != 3 {
		start = (slice + 1) * segmentLength
	}
	return (start + uint32(rel)) % laneLength
}

// argon2id 计算 Argon2id 标签。memory 单位 KiB，password/salt/secret/ad 均可为 nil。
func argon2id(password, salt, secret, ad []byte, t, memory uint32, p uint8, tagLen uint32) []byte {
	lanes := uint32(p)
	if lanes == 0 {
		lanes = 1
	}
	if memory < 8*lanes {
		memory = 8 * lanes
	}
	segmentLength := memory / (lanes * 4)
	memory = segmentLength * lanes * 4
	laneLength := segmentLength * 4

	h0 := argonH0(password, salt, secret, ad, t, memory, p, tagLen)
	blocks := make([]argonBlock, memory)
	for lane := uint32(0); lane < lanes; lane++ {
		var idx [8]byte
		binary.LittleEndian.PutUint32(idx[0:4], 0)
		binary.LittleEndian.PutUint32(idx[4:8], lane)
		first := argonHPrime(append(append([]byte{}, h0...), idx[:]...), 1024)
		binary.LittleEndian.PutUint32(idx[0:4], 1)
		second := argonHPrime(append(append([]byte{}, h0...), idx[:]...), 1024)
		setBlockLE(&blocks[lane*laneLength+0], first)
		setBlockLE(&blocks[lane*laneLength+1], second)
	}

	var zero argonBlock
	for pass := uint32(0); pass < t; pass++ {
		for slice := uint32(0); slice < 4; slice++ {
			dataIndependent := pass == 0 && slice < 2
			var address, input argonBlock
			if dataIndependent {
				input[0] = uint64(pass)
				input[1] = uint64(0)
				input[2] = uint64(slice)
				input[3] = uint64(memory)
				input[4] = uint64(t)
				input[5] = uint64(argonTypeID)
			}
			for lane := uint32(0); lane < lanes; lane++ {
				if dataIndependent {
					input[1] = uint64(lane)
					input[6] = 0
				}
				start := uint32(0)
				if pass == 0 && slice == 0 {
					start = 2
					if dataIndependent {
						nextAddresses(&address, &input, &zero)
					}
				}
				for index := start; index < segmentLength; index++ {
					if dataIndependent && index%128 == 0 {
						nextAddresses(&address, &input, &zero)
					}
					j := slice*segmentLength + index
					cur := lane*laneLength + j
					prev := cur - 1
					if j == 0 {
						prev = lane*laneLength + laneLength - 1
					}
					var pseudoRand uint64
					if dataIndependent {
						pseudoRand = address[index%128]
					} else {
						pseudoRand = blocks[prev][0]
					}
					j1 := uint32(pseudoRand)
					j2 := uint32(pseudoRand >> 32)
					refLane := j2 % lanes
					if pass == 0 && slice == 0 {
						refLane = lane
					}
					refIndex := indexAlpha(pass, slice, index, laneLength, segmentLength, refLane == lane, j1)
					ref := refLane*laneLength + refIndex
					fillBlock(&blocks[prev], &blocks[ref], &blocks[cur], pass != 0)
				}
			}
		}
	}

	var final argonBlock
	for lane := uint32(0); lane < lanes; lane++ {
		b := &blocks[lane*laneLength+laneLength-1]
		for i := 0; i < 128; i++ {
			final[i] ^= b[i]
		}
	}
	return argonHPrime(blockBytes(&final), int(tagLen))
}

// nextAddresses 生成下一个 1024 字节的地址块（数据无关寻址）。
func nextAddresses(address, input, zero *argonBlock) {
	input[6]++
	fillBlock(zero, input, address, false)
	fillBlock(zero, address, address, false)
}

// setBlockLE 将 1024 字节按小端写入工作块。
func setBlockLE(b *argonBlock, data []byte) {
	for i := 0; i < 128; i++ {
		b[i] = binary.LittleEndian.Uint64(data[i*8:])
	}
}

// blockBytes 将工作块序列化为 1024 字节（小端）。
func blockBytes(b *argonBlock) []byte {
	out := make([]byte, 1024)
	for i := 0; i < 128; i++ {
		binary.LittleEndian.PutUint64(out[i*8:], b[i])
	}
	return out
}

// ---- 口令哈希（PHC 字符串编码） ----

// argonParams 口令派生参数。
type argonParams struct {
	time    uint32
	memory  uint32 // KiB
	threads uint8
	keyLen  uint32
}

// defaultArgonParams 默认参数：64 MiB、t=3、p=4、32 字节标签。
var defaultArgonParams = argonParams{time: 3, memory: 64 * 1024, threads: 4, keyLen: 32}

// hashPassword 生成 PHC 格式的 Argon2id 口令哈希：$argon2id$v=19$m=..,t=..,p=..$salt$hash。
func hashPassword(password string) (string, error) {
	salt := make([]byte, 16)
	if _, err := rand.Read(salt); err != nil {
		return "", err
	}
	p := defaultArgonParams
	tag := argon2id([]byte(password), salt, nil, nil, p.time, p.memory, p.threads, p.keyLen)
	return fmt.Sprintf("$argon2id$v=%d$m=%d,t=%d,p=%d$%s$%s",
		argonVersion, p.memory, p.time, p.threads,
		base64.RawStdEncoding.EncodeToString(salt),
		base64.RawStdEncoding.EncodeToString(tag)), nil
}

// verifyPassword 校验口令与 PHC 哈希（恒定时间比较）。
func verifyPassword(password, encoded string) bool {
	parts := strings.Split(encoded, "$")
	if len(parts) != 6 || parts[1] != "argon2id" {
		return false
	}
	var version int
	if _, err := fmt.Sscanf(parts[2], "v=%d", &version); err != nil {
		return false
	}
	var p argonParams
	if _, err := fmt.Sscanf(parts[3], "m=%d,t=%d,p=%d", &p.memory, &p.time, &p.threads); err != nil {
		return false
	}
	salt, err := base64.RawStdEncoding.DecodeString(parts[4])
	if err != nil {
		return false
	}
	want, err := base64.RawStdEncoding.DecodeString(parts[5])
	if err != nil || len(want) == 0 {
		return false
	}
	got := argon2id([]byte(password), salt, nil, nil, p.time, p.memory, p.threads, uint32(len(want)))
	return subtle.ConstantTimeCompare(got, want) == 1
}
