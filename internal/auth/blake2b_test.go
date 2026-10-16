package auth

import (
	"bytes"
	"testing"
)

// TestBlake2bVectors 断言 BLAKE2b 与公开测试向量一致（RFC 7693 / 官方向量）。
func TestBlake2bVectors(t *testing.T) {
	cases := []struct {
		name   string
		outLen int
		in     []byte
		want   string
	}{
		{
			name:   "512/abc",
			outLen: 64,
			in:     []byte("abc"),
			want:   "ba80a53f981c4d0d6a2797b69f12f6e94c212f14685ac4b74b12bb6fdbffa2d17d87c5392aab792dc252d5de4533cc9518d38aa8dbf1925ab92386edd4009923",
		},
		{
			name:   "512/empty",
			outLen: 64,
			in:     nil,
			want:   "786a02f742015903c6c6fd852552d272912f4740e15847618a86e217f71f5419d25e1031afee585313896444934eb04b903a685b1448b755d56f701afe9be2ce",
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := hexEncode(blake2b(c.outLen, c.in))
			if got != c.want {
				t.Fatalf("blake2b = %s\nwant      %s", got, c.want)
			}
		})
	}
}

// TestBlake2bLongInput 覆盖跨多块（>128B）与恰为块整数倍的情形。
func TestBlake2bLongInput(t *testing.T) {
	data := bytes.Repeat([]byte{0xab}, 128*3) // 恰为 3 块
	got := blake2b(64, data)
	if len(got) != 64 {
		t.Fatalf("输出长度 = %d, want 64", len(got))
	}
	// 改变最后一块任意一字节，摘要必须变化（末块被纳入压缩）
	data2 := append([]byte(nil), data...)
	data2[len(data2)-1] ^= 0x01
	if bytes.Equal(got, blake2b(64, data2)) {
		t.Fatal("末块变化未影响摘要，末块压缩语义有误")
	}
}

func hexEncode(b []byte) string {
	const hexdigits = "0123456789abcdef"
	out := make([]byte, len(b)*2)
	for i, v := range b {
		out[i*2] = hexdigits[v>>4]
		out[i*2+1] = hexdigits[v&0xf]
	}
	return string(out)
}
