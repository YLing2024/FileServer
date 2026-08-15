package server

import (
	"bytes"
	"context"
	"encoding/binary"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// mp4Box 构造一个 MP4 顶层 box（size + type + payload）
func mp4Box(typ string, payload []byte) []byte {
	b := make([]byte, 8+len(payload))
	binary.BigEndian.PutUint32(b[0:4], uint32(8+len(payload)))
	copy(b[4:8], typ)
	copy(b[8:], payload)
	return b
}

// mp4Box64 构造 64 位扩展尺寸的 box（size==1 + largesize）
func mp4Box64(typ string, payload []byte, largeSize uint64) []byte {
	b := make([]byte, 16+len(payload))
	binary.BigEndian.PutUint32(b[0:4], 1)
	copy(b[4:8], typ)
	binary.BigEndian.PutUint64(b[8:16], largeSize)
	copy(b[16:], payload)
	return b
}

func writeMP4(t *testing.T, name string, boxes ...[]byte) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), name)
	var buf bytes.Buffer
	for _, b := range boxes {
		buf.Write(b)
	}
	if err := os.WriteFile(p, buf.Bytes(), 0o644); err != nil {
		t.Fatal(err)
	}
	return p
}

func mp4File(t *testing.T, name string, data []byte) *os.File {
	t.Helper()
	p := filepath.Join(t.TempDir(), name)
	if err := os.WriteFile(p, data, 0o644); err != nil {
		t.Fatal(err)
	}
	f, err := os.Open(p)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { f.Close() })
	return f
}

// TestMP4MoovOffset 覆盖 moov 定位的边界情况（5.1）：
// 首 box 即 moov、正常 moov、64 位扩展 box、mdat size=0、boxSize<8、无 moov
func TestMP4MoovOffset(t *testing.T) {
	moov := mp4Box("moov", []byte("xxxx"))

	cases := []struct {
		name string
		mp4  []byte
		want int64
	}{
		{
			"首 box 即 moov",
			moov,
			0,
		},
		{
			"ftyp 后跟 moov",
			append(mp4Box("ftyp", make([]byte, 8)), moov...),
			16,
		},
		{
			"ftyp+free+moov（moov 在中间）",
			append(append(mp4Box("ftyp", make([]byte, 8)), mp4Box("free", make([]byte, 100))...), moov...),
			16 + 108, // ftyp(16) + free(8+100=108)
		},
		{
			"无 moov",
			append(mp4Box("ftyp", make([]byte, 8)), mp4Box("mdat", make([]byte, 32))...),
			-1,
		},
		{
			"mdat size=0（延伸到 EOF）",
			append(mp4Box("ftyp", make([]byte, 8)), []byte{0, 0, 0, 0, 'm', 'd', 'a', 't'}...),
			-1,
		},
		{
			"boxSize<8（畸形）",
			[]byte{0, 0, 0, 4, 'f', 't', 'y', 'p'},
			-1,
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			f := mp4File(t, "x.mp4", c.mp4)
			if got := mp4MoovOffset(f, int64(len(c.mp4))); got != c.want {
				t.Errorf("mp4MoovOffset = %d, want %d", got, c.want)
			}
		})
	}

	// 64 位扩展 box：ftyp 用 largesize=16+2000，其后 moov
	t.Run("64位扩展 box", func(t *testing.T) {
		big := mp4Box64("mdat", make([]byte, 2000), 16+2000)
		data := append(append([]byte{}, big...), moov...)
		f := mp4File(t, "big.mp4", data)
		if got := mp4MoovOffset(f, int64(len(data))); got != int64(len(big)) {
			t.Errorf("mp4MoovOffset = %d, want %d", got, len(big))
		}
	})

	// 空文件 / 文件过短
	t.Run("短文件", func(t *testing.T) {
		f := mp4File(t, "short.mp4", []byte{0, 0, 0, 8})
		if got := mp4MoovOffset(f, 4); got != -1 {
			t.Errorf("短文件应为 -1, 得到 %d", got)
		}
	})
}

// TestMP4NeedsFastStart moov 位置决定是否需要 faststart 重封装
func TestMP4NeedsFastStart(t *testing.T) {
	moov := mp4Box("moov", []byte("xxxx"))
	ftyp := mp4Box("ftyp", make([]byte, 8))

	t.Run("文件过小不需处理", func(t *testing.T) {
		p := writeMP4(t, "tiny.mp4", []byte{0, 0, 0, 8, 'f', 't', 'y', 'p'})
		if mp4NeedsFastStart(p) {
			t.Error("小于 1KB 的文件不应判定为需 faststart")
		}
	})

	t.Run("moov 在开头不需处理", func(t *testing.T) {
		data := append(append([]byte{}, ftyp...), moov...)
		p := writeMP4(t, "ok.mp4", data)
		// 补充 padding 到超过 1KB，但 moov 仍在前部
		buf := make([]byte, 1200)
		copy(buf, data)
		if err := os.WriteFile(p, buf, 0o644); err != nil {
			t.Fatal(err)
		}
		if mp4NeedsFastStart(p) {
			t.Error("moov 在前部不应判定为需 faststart")
		}
	})

	t.Run("moov 在尾部需处理", func(t *testing.T) {
		// ftyp + 300KB 填充 + moov（moov 偏移 > 256KB）
		pad := make([]byte, 300*1024)
		copy(pad, ftyp)
		data := append(pad, moov...)
		p := writeMP4(t, "tail.mp4", data)
		if !mp4NeedsFastStart(p) {
			t.Error("moov 在尾部应判定为需 faststart")
		}
	})

	t.Run("无 moov 需处理", func(t *testing.T) {
		pad := make([]byte, 1200)
		copy(pad, append(append([]byte{}, ftyp...), mp4Box("mdat", make([]byte, 64))...))
		p := writeMP4(t, "nomoov.mp4", pad)
		if !mp4NeedsFastStart(p) {
			t.Error("无 moov 应判定为需 faststart")
		}
	})
}

// TestParseDurationFromStderr ffmpeg -i stderr 的 Duration 解析（2.2 回退路径）
func TestParseDurationFromStderr(t *testing.T) {
	cases := []struct {
		in   string
		want float64
		ok   bool
	}{
		{"Duration: 00:01:23.45, start: 0.000000, bitrate: 123 kb/s", 83.45, true},
		{"  Duration: 00:00:05.00, start: 0.000000", 5.0, true},
		{"Duration: 01:00:00, start: 0.000000", 3600.0, true},
		{"Input #0, mov,mp4,m4a,3gp,3g2,mj2:\n  Duration: 00:00:02.50", 2.5, true},
		{"no duration here", 0, false},
		{"Duration: N/A, start: 0.000000", 0, false},
		{"Duration: 12:34", 0, false},
	}
	for _, c := range cases {
		got, err := parseDurationFromStderr([]byte(c.in))
		if c.ok != (err == nil) {
			t.Errorf("parseDurationFromStderr(%q) err=%v, want ok=%v", c.in, err, c.ok)
			continue
		}
		if c.ok && got != c.want {
			t.Errorf("parseDurationFromStderr(%q) = %v, want %v", c.in, got, c.want)
		}
	}
}

// TestParseDurationJSON ffprobe JSON 输出解析
func TestParseDurationJSON(t *testing.T) {
	got, err := parseDurationJSON([]byte(`{"format":{"duration":"12.500000"}}`))
	if err != nil || got != 12.5 {
		t.Errorf("解析失败: got=%v err=%v", got, err)
	}
	if _, err := parseDurationJSON([]byte(`{"format":{}}`)); err == nil {
		t.Error("空 duration 应报错")
	}
	if _, err := parseDurationJSON([]byte(`not json`)); err == nil {
		t.Error("非法 JSON 应报错")
	}
}

// TestContainsFold 大小写不敏感包含匹配（3.4）
func TestContainsFold(t *testing.T) {
	cases := []struct {
		s, sub string
		want   bool
	}{
		{"HelloWorld.mp4", "hello", true},
		{"HelloWorld.mp4", "WORLD", true},
		{"HelloWorld.mp4", "xyz", false},
		{"中文测试", "测试", true},
		{"Sample.MP4", "mp4", true},
		{"abc", "", true},
	}
	for _, c := range cases {
		if got := containsFold(c.s, c.sub); got != c.want {
			t.Errorf("containsFold(%q,%q)=%v want %v", c.s, c.sub, got, c.want)
		}
	}
}

// TestRemuxFastStartIntegration 用真实 ffmpeg 验证重封装首包与停滞看门狗（2.1）。
// 无 ffmpeg 环境自动跳过。
func TestRemuxFastStartIntegration(t *testing.T) {
	ff := FindFfmpeg()
	if ff == nil {
		t.Skip("ffmpeg 不可用，跳过集成测试")
	}
	// 生成一个 2 秒测试视频（lavfi 色彩源，无需外部素材）
	src := filepath.Join(t.TempDir(), "src.mp4")
	gen := exec.Command(ff.ffmpegPath, "-hide_banner", "-loglevel", "error", "-y",
		"-f", "lavfi", "-i", "color=c=red:s=128x128:d=2",
		"-c:v", "libx264", "-pix_fmt", "yuv420p", src)
	if out, err := gen.CombinedOutput(); err != nil {
		t.Skipf("无法生成测试视频: %v (%s)", err, strings.TrimSpace(string(out)))
	}
	if fi, err := os.Stat(src); err != nil || fi.Size() == 0 {
		t.Skip("测试视频生成异常，跳过")
	}

	// 大输出缓冲（不触发客户端背压），验证可在远超原 15s 的场景下正常完成
	var buf bytes.Buffer
	if err := ff.RemuxFastStart(context.Background(), src, &buf); err != nil {
		t.Fatalf("remux 失败: %v", err)
	}
	if buf.Len() < 128 {
		t.Fatalf("remux 输出过短: %d 字节", buf.Len())
	}
	// 输出应为合法 mp4（ftyp 位于首 box 的 type 字段）
	if len(buf.Bytes()) < 8 || string(buf.Bytes()[4:8]) != "ftyp" {
		t.Fatalf("remux 输出不是 MP4: 头部 %q", buf.Bytes()[:8])
	}

	// 写入中途失败（等价于客户端断开、http 写回错误）：必须能快速终止而非永久挂起
	lim := &limitedWriter{max: 128}
	start := time.Now()
	if err := ff.RemuxFastStart(context.Background(), src, lim); err == nil {
		t.Error("客户端断开应返回错误")
	}
	if d := time.Since(start); d > ffmpegTimeout {
		t.Fatalf("写入失败后未及时终止: %v", d)
	}
}

// limitedWriter 写入超过 max 后返回 os.ErrClosed，模拟客户端断开（http 写回错误）
type limitedWriter struct{ max int }

func (l *limitedWriter) Write(p []byte) (int, error) {
	if l.max <= 0 {
		return 0, os.ErrClosed
	}
	n := len(p)
	if n > l.max {
		n = l.max
	}
	l.max -= n
	return n, nil
}

// TestFastStartServingIntegration 验证 2.3：非 faststart 视频的「初始请求」与
// 后续 seek 的「Range 请求」都从同一 faststart 缓存文件服务，字节布局一致，
// 不再出现「流式 remux 输出 + 原始文件 Range」混用导致的字节错位。
func TestFastStartServingIntegration(t *testing.T) {
	ff := FindFfmpeg()
	if ff == nil {
		t.Skip("ffmpeg 不可用，跳过集成测试")
	}
	root := t.TempDir()
	src := filepath.Join(root, "v.mp4")
	gen := exec.Command(ff.ffmpegPath, "-hide_banner", "-loglevel", "error", "-y",
		"-f", "lavfi", "-i", "testsrc2=s=320x240:d=8",
		"-c:v", "libx264", "-preset", "ultrafast", "-pix_fmt", "yuv420p", src)
	if out, err := gen.CombinedOutput(); err != nil {
		t.Skipf("无法生成测试视频: %v (%s)", err, strings.TrimSpace(string(out)))
	}
	if !mp4NeedsFastStart(src) {
		t.Skip("生成的视频已是 faststart，跳过本用例")
	}

	srv := New(root, Options{})
	ts := httptest.NewServer(srv.Handler())
	defer ts.Close()

	// 初始请求（无 Range）：走 faststart 缓存，返回完整 remux 输出
	resp := get(t, ts.URL+"/api/file?path=/v.mp4")
	full, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != 200 || len(full) == 0 {
		t.Fatalf("初始请求失败: status=%d bytes=%d", resp.StatusCode, len(full))
	}

	// 中途 Range 请求（模拟浏览器 seek）：必须与初始请求同字节布局
	req, _ := http.NewRequest("GET", ts.URL+"/api/file?path=/v.mp4", nil)
	req.Header.Set("Range", "bytes=1000-1999")
	resp2, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	part, _ := io.ReadAll(resp2.Body)
	resp2.Body.Close()
	if resp2.StatusCode != 206 {
		t.Fatalf("Range 应返回 206, 得到 %d", resp2.StatusCode)
	}
	if string(part) != string(full[1000:2000]) {
		t.Fatal("seek 字节与初始响应不一致（remux 与 Range 混用导致错位）")
	}
}

// TestRemuxFastStartIntegrationStall 用极短的停滞阈值验证看门狗逻辑。
// 由于 ffmpegTimeout 是包级常量（15s），此测试直接构造 progressWriter 验证其判定。
func TestProgressWriterStall(t *testing.T) {
	pw := &progressWriter{w: &bytes.Buffer{}, last: time.Now().Add(-2 * time.Second)}
	if !pw.stalled(time.Second) {
		t.Error("超过阈值无写入应判定停滞")
	}
	if _, err := pw.Write([]byte("x")); err != nil {
		t.Fatal(err)
	}
	if pw.stalled(time.Second) {
		t.Error("有写入后不应判定停滞")
	}
}
