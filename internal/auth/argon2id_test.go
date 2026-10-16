package auth

import (
	"bytes"
	"testing"
)

// TestArgon2idRFC9106Vector 用 RFC 9106 §5.3 的官方测试向量校验实现正确性。
// 该向量同时覆盖 secret 与 associated data 两个可选输入。
func TestArgon2idRFC9106Vector(t *testing.T) {
	password := bytes.Repeat([]byte{0x01}, 32)
	salt := bytes.Repeat([]byte{0x02}, 16)
	secret := bytes.Repeat([]byte{0x03}, 8)
	ad := bytes.Repeat([]byte{0x04}, 12)
	const want = "0d640df58d78766c08c037a34a8b53c9d01ef0452d75b65eb52520e96b01e659"
	got := argon2id(password, salt, secret, ad, 3, 32, 4, 32)
	if hexEncode(got) != want {
		t.Fatalf("argon2id = %s\nwant       %s", hexEncode(got), want)
	}
}

// TestPasswordHashRoundTrip 覆盖 PHC 编码与恒定时间校验。
func TestPasswordHashRoundTrip(t *testing.T) {
	h, err := hashPassword("correct horse battery staple")
	if err != nil {
		t.Fatal(err)
	}
	if !verifyPassword("correct horse battery staple", h) {
		t.Fatal("正确口令应通过校验")
	}
	if verifyPassword("wrong password", h) {
		t.Fatal("错误口令不应通过校验")
	}
	if verifyPassword("correct horse battery staple", "$argon2id$v=19$bad") {
		t.Fatal("非法哈希不应通过校验")
	}
}
