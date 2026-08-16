package server

import (
	"bytes"
	"context"
	"encoding/binary"
	"encoding/json"
	"io"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
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

// TestMP4MoovOffset 覆盖 moov 定位的边界情况：
// 首 box 即 moov、正常 moov、64 位扩展 box、mdat size=0、boxSize<8、无 moov
func TestMP4MoovOffset(t *testing.T) {
	moov := mp4Box("moov", []byte("xxxx"))

	cases := []struct {
		name string
		mp4  []byte
		want int64
	}{
		{"首 box 即 moov", moov, 0},
		{
			"ftyp 后跟 moov",
			append(mp4Box("ftyp", make([]byte, 8)), moov...),
			16,
		},
		{
			"ftyp+free+moov（moov 在中间）",
			append(append(mp4Box("ftyp", make([]byte, 8)), mp4Box("free", make([]byte, 100))...), moov...),
			16 + 108,
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

	// 64 位扩展 box：mdat 用 largesize=16+2000，其后 moov
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

// TestMP4IsFastStart moov 位置决定浏览器是否可直链即时起播
func TestMP4IsFastStart(t *testing.T) {
	moov := mp4Box("moov", []byte("xxxx"))
	ftyp := mp4Box("ftyp", make([]byte, 8))

	t.Run("文件过小不算 faststart", func(t *testing.T) {
		p := writeMP4(t, "tiny.mp4", []byte{0, 0, 0, 8, 'f', 't', 'y', 'p'})
		if mp4IsFastStart(p) {
			t.Error("小于 1KB 的文件不应判定为 faststart")
		}
	})

	t.Run("moov 在开头即 faststart", func(t *testing.T) {
		data := append(append([]byte{}, ftyp...), moov...)
		p := writeMP4(t, "ok.mp4", data)
		buf := make([]byte, 1200)
		copy(buf, data)
		if err := os.WriteFile(p, buf, 0o644); err != nil {
			t.Fatal(err)
		}
		if !mp4IsFastStart(p) {
			t.Error("moov 在前部应判定为 faststart")
		}
	})

	t.Run("moov 在尾部不是 faststart", func(t *testing.T) {
		pad := make([]byte, 300*1024)
		copy(pad, ftyp)
		data := append(pad, moov...)
		p := writeMP4(t, "tail.mp4", data)
		if mp4IsFastStart(p) {
			t.Error("moov 在尾部不应判定为 faststart")
		}
	})

	t.Run("无 moov 不是 faststart", func(t *testing.T) {
		pad := make([]byte, 1200)
		copy(pad, append(append([]byte{}, ftyp...), mp4Box("mdat", make([]byte, 64))...))
		p := writeMP4(t, "nomoov.mp4", pad)
		if mp4IsFastStart(p) {
			t.Error("无 moov 不应判定为 faststart")
		}
	})
}

// TestMediaInfoPlayable 浏览器原生可播放性判定矩阵
func TestMediaInfoPlayable(t *testing.T) {
	cases := []struct {
		name string
		m    MediaInfo
		ext  string
		want bool
	}{
		{"mp4 h264 aac", MediaInfo{VideoCodec: "h264", AudioCodec: "aac"}, ".mp4", true},
		{"mp4 h264 无音轨", MediaInfo{VideoCodec: "h264"}, ".mp4", true},
		{"mp4 h264 mp3", MediaInfo{VideoCodec: "h264", AudioCodec: "mp3"}, ".mp4", true},
		{"mp4 hevc aac 不可播", MediaInfo{VideoCodec: "hevc", AudioCodec: "aac"}, ".mp4", false},
		{"mp4 h264 ac3 不可播", MediaInfo{VideoCodec: "h264", AudioCodec: "ac3"}, ".mp4", false},
		{"webm vp9 opus", MediaInfo{VideoCodec: "vp9", AudioCodec: "opus"}, ".webm", true},
		{"webm av1 vorbis", MediaInfo{VideoCodec: "av1", AudioCodec: "vorbis"}, ".webm", true},
		{"webm h264 不可播", MediaInfo{VideoCodec: "h264", AudioCodec: "opus"}, ".webm", false},
		{"mkv h264 aac 不可播（容器）", MediaInfo{VideoCodec: "h264", AudioCodec: "aac"}, ".mkv", false},
		{"avi mpeg4 不可播", MediaInfo{VideoCodec: "mpeg4", AudioCodec: "mp3"}, ".avi", false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := c.m.Playable(c.ext); got != c.want {
				t.Errorf("Playable(%s) = %v, want %v", c.ext, got, c.want)
			}
		})
	}
}

// TestBitrateFor 码率随分辨率递增
func TestBitrateFor(t *testing.T) {
	if bitrateFor(&MediaInfo{Height: 360}) == bitrateFor(&MediaInfo{Height: 2160}) {
		t.Error("不同分辨率码率应不同")
	}
	if bitrateFor(&MediaInfo{Height: 1080}) != 6000 {
		t.Errorf("1080p 码率应为 6000k, got %d", bitrateFor(&MediaInfo{Height: 1080}))
	}
}

// TestParseDurationFromStderr ffmpeg -i stderr 的 Duration 解析（回退路径）
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

// TestContainsFold 大小写不敏感包含匹配
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

// TestDurationCache 验证时长元数据缓存：同一文件第二次探测命中缓存，
// 不再起 ffprobe 进程（把 ffprobePath 指向必然失败的假路径，若第二次不命中
// 缓存而真的执行 ffprobe 会失败；命中缓存则直接返回上次结果）。
func TestDurationCache(t *testing.T) {
	if _, err := exec.LookPath("ffmpeg"); err != nil {
		t.Skip("ffmpeg 不可用，跳过集成测试")
	}
	ff := FindFfmpeg()
	if ff == nil {
		t.Skip("ffmpeg 不可用，跳过集成测试")
	}

	src := filepath.Join(t.TempDir(), "dur.mp4")
	gen := exec.Command(ff.ffmpegPath, "-hide_banner", "-loglevel", "error", "-y",
		"-f", "lavfi", "-i", "color=c=blue:s=64x64:d=3",
		"-c:v", "libx264", "-pix_fmt", "yuv420p", src)
	if out, err := gen.CombinedOutput(); err != nil {
		t.Skipf("无法生成测试视频: %v (%s)", err, strings.TrimSpace(string(out)))
	}

	d1, err := ff.probeDuration(context.Background(), src)
	if err != nil {
		t.Fatalf("首次探测失败: %v", err)
	}
	if d1 < 2.5 || d1 > 3.5 {
		t.Fatalf("首次探测时长异常: %v", d1)
	}
	realProbe := ff.ffprobePath
	ff.ffprobePath = filepath.Join(t.TempDir(), "nonexistent-ffprobe.exe")
	d2, err := ff.probeDuration(context.Background(), src)
	if err != nil {
		ff.ffprobePath = realProbe
		t.Fatalf("第二次探测应命中缓存，却失败: %v", err)
	}
	ff.ffprobePath = realProbe
	if d2 != d1 {
		t.Fatalf("缓存命中时长不一致: %v vs %v", d2, d1)
	}
}

// TestHlsManagerIntegration 用真实 ffmpeg 验证 HLS 转码全链路：
// MKV（浏览器不可播）→ 会话生成 index.m3u8 + fmp4 分片 → EVENT 播放列表 → 完成后 ENDLIST。
func TestHlsManagerIntegration(t *testing.T) {
	ff := FindFfmpeg()
	if ff == nil {
		t.Skip("ffmpeg 不可用，跳过集成测试")
	}
	root := t.TempDir()
	src := filepath.Join(root, "movie.mkv")
	gen := exec.Command(ff.ffmpegPath, "-hide_banner", "-loglevel", "error", "-y",
		"-f", "lavfi", "-i", "testsrc2=s=320x240:d=6:r=30",
		"-f", "lavfi", "-i", "sine=frequency=440:duration=6",
		"-c:v", "libx264", "-preset", "ultrafast", "-pix_fmt", "yuv420p",
		"-c:a", "aac", "-shortest", src)
	if out, err := gen.CombinedOutput(); err != nil {
		t.Skipf("无法生成测试视频: %v (%s)", err, strings.TrimSpace(string(out)))
	}

	fi, err := os.Stat(src)
	if err != nil {
		t.Fatal(err)
	}
	srv := New(root, Options{})
	defer srv.Close()
	mgr := srv.hls

	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
	defer cancel()
	sess, err := mgr.Get(ctx, src, fi, srv.ff)
	if err != nil {
		t.Fatalf("HLS 会话创建失败: %v", err)
	}

	// 等待转码完成（6s 视频很快），或至少等到播放列表有内容
	deadline := time.Now().Add(120 * time.Second)
	for {
		data, err := os.ReadFile(filepath.Join(sess.dir, "index.m3u8"))
		if err == nil && strings.Contains(string(data), "#EXTINF") &&
			strings.Contains(string(data), "#EXT-X-ENDLIST") {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("等待 HLS 转码完成超时")
		}
		time.Sleep(500 * time.Millisecond)
	}

	// 播放列表结构校验
	data, _ := os.ReadFile(filepath.Join(sess.dir, "index.m3u8"))
	pl := string(data)
	for _, want := range []string{"#EXTM3U", "#EXT-X-TARGETDURATION", "#EXTINF", "#EXT-X-ENDLIST"} {
		if !strings.Contains(pl, want) {
			t.Errorf("播放列表缺少 %s:\n%s", want, pl)
		}
	}
	// 分片文件存在且非空
	segs, _ := filepath.Glob(filepath.Join(sess.dir, "seg_*.m4s"))
	if len(segs) == 0 {
		t.Fatal("未生成任何分片")
	}
	for _, s := range segs {
		if info, err := os.Stat(s); err != nil || info.Size() == 0 {
			t.Errorf("分片 %s 为空或不可读", s)
		}
	}
}

// TestVideoInfoDecision 验证 /api/video-info 的 direct/hls 决策：
// faststart MP4 → direct；非 faststart MP4 → hls；MKV → hls；HEVC MP4 → hls。
func TestVideoInfoDecision(t *testing.T) {
	ff := FindFfmpeg()
	if ff == nil {
		t.Skip("ffmpeg 不可用，跳过集成测试")
	}
	root := t.TempDir()

	mkVideo := func(name string, extra ...string) string {
		p := filepath.Join(root, name)
		args := append([]string{"-hide_banner", "-loglevel", "error", "-y",
			"-f", "lavfi", "-i", "testsrc2=s=320x240:d=2:r=30",
			"-c:v", "libx264", "-preset", "ultrafast", "-pix_fmt", "yuv420p"}, extra...)
		args = append(args, p)
		cmd := exec.Command(ff.ffmpegPath, args...)
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("生成 %s 失败: %v", name, strings.TrimSpace(string(out)))
		}
		return p
	}
	mkVideo("fast.mp4", "-movflags", "+faststart")
	// 小非 faststart MP4（<32MB）→ direct + faststart 预热标志
	mkVideo("smallslow.mp4", "-movflags", "-faststart")
	// 非 faststart 且 >32MB（小文件走「服务端即时 faststart 化 + 直链」路径）：
	// 1280x720 15s @20Mbps ≈ 37MB，ultrafast 生成快
	slow := filepath.Join(root, "slow.mp4")
	slowGen := exec.Command(ff.ffmpegPath, "-hide_banner", "-loglevel", "error", "-y",
		"-f", "lavfi", "-i", "testsrc2=s=1280x720:d=15:r=30",
		"-c:v", "libx264", "-preset", "ultrafast", "-b:v", "20M", "-pix_fmt", "yuv420p", "-movflags", "-faststart", slow)
	if out, err := slowGen.CombinedOutput(); err != nil {
		t.Fatalf("生成 slow.mp4 失败: %s", strings.TrimSpace(string(out)))
	}
	if fi, err := os.Stat(slow); err != nil || fi.Size() <= 32*1024*1024 {
		t.Skip("slow.mp4 未超过 32MB，跳过本用例")
	}
	mkVideo("x.mkv", "-f", "matroska")

	hevc := filepath.Join(root, "hevc.mp4")
	genHevc := exec.Command(ff.ffmpegPath, "-hide_banner", "-loglevel", "error", "-y",
		"-f", "lavfi", "-i", "testsrc2=s=320x240:d=2:r=30",
		"-c:v", "libx265", "-preset", "ultrafast", "-pix_fmt", "yuv420p", hevc)
	_ = genHevc.Run() // 失败则跳过该用例

	srv := New(root, Options{})
	defer srv.Close()
	ts := httptest.NewServer(srv.Handler())
	defer ts.Close()

	cases := []struct {
		name     string
		path     string
		mode     string
		minDur   float64
		maxDur   float64
	}{
		{"faststart mp4 → direct", "/fast.mp4", "direct", 1.5, 2.5},
		{"小非 faststart mp4 → direct(预热)", "/smallslow.mp4", "direct", 1.5, 2.5},
		{"非 faststart 大 mp4 → hls", "/slow.mp4", "hls", 14, 16},
		{"mkv → hls", "/x.mkv", "hls", 1.5, 2.5},
		{"hevc mp4 → hls", "/hevc.mp4", "hls", 1.5, 2.5},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if c.path == "/hevc.mp4" {
				if _, err := os.Stat(hevc); err != nil {
					t.Skip("libx265 不可用，跳过")
				}
			}
			// 首次请求：断言模式（决策可能不等待探测，duration 允许缺失）
			resp := get(t, ts.URL+"/api/video-info?path="+c.path)
			if resp.StatusCode != 200 {
				t.Fatalf("状态码 %d", resp.StatusCode)
			}
			var v struct {
				Mode     string  `json:"mode"`
				Duration float64 `json:"duration"`
			}
			if err := json.NewDecoder(resp.Body).Decode(&v); err != nil {
				t.Fatal(err)
			}
			resp.Body.Close()
			if v.Mode != c.mode {
				t.Errorf("mode = %q, want %q", v.Mode, c.mode)
			}
			// 等待后台探测完成（探测为秒级内），二次请求断言时长
			time.Sleep(1200 * time.Millisecond)
			resp2 := get(t, ts.URL+"/api/video-info?path="+c.path)
			defer resp2.Body.Close()
			if err := json.NewDecoder(resp2.Body).Decode(&v); err != nil {
				t.Fatal(err)
			}
			if v.Duration < c.minDur || v.Duration > c.maxDur {
				t.Errorf("时长异常: %v (期望 %v~%v)", v.Duration, c.minDur, c.maxDur)
			}
		})
	}
}

// TestHlsEndpoint 验证 /api/hls 端到端：请求播放列表 → 200 + m3u8；请求分片 → 200 + video/mp4
func TestHlsEndpoint(t *testing.T) {
	ff := FindFfmpeg()
	if ff == nil {
		t.Skip("ffmpeg 不可用，跳过集成测试")
	}
	root := t.TempDir()
	src := filepath.Join(root, "ep.mkv")
	gen := exec.Command(ff.ffmpegPath, "-hide_banner", "-loglevel", "error", "-y",
		"-f", "lavfi", "-i", "testsrc2=s=320x240:d=4:r=30",
		"-c:v", "libx264", "-preset", "ultrafast", "-pix_fmt", "yuv420p", "-f", "matroska", src)
	if out, err := gen.CombinedOutput(); err != nil {
		t.Skipf("无法生成测试视频: %s", strings.TrimSpace(string(out)))
	}

	srv := New(root, Options{})
	defer srv.Close()
	ts := httptest.NewServer(srv.Handler())
	defer ts.Close()

	// 播放列表（首次请求会触发转码并等待首片）
	resp := get(t, ts.URL+"/api/hls?path=/ep.mkv&f=index.m3u8")
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		t.Fatalf("播放列表状态码 %d", resp.StatusCode)
	}
	pl, _ := io.ReadAll(resp.Body)
	if !strings.Contains(string(pl), "#EXTM3U") {
		t.Fatalf("不是合法播放列表: %s", pl)
	}
	if ct := resp.Header.Get("Content-Type"); ct != "application/vnd.apple.mpegurl" {
		t.Errorf("m3u8 Content-Type = %q", ct)
	}

	// 提取第一个分片名并请求
	re := regexp.MustCompile(`seg_\d+\.m4s`)
	name := re.FindString(string(pl))
	if name == "" {
		t.Skip("播放列表尚无分片（转码未完成）")
	}
	resp2 := get(t, ts.URL+"/api/hls?path=/ep.mkv&f="+name)
	defer resp2.Body.Close()
	if resp2.StatusCode != 200 {
		t.Fatalf("分片状态码 %d", resp2.StatusCode)
	}
	if ct := resp2.Header.Get("Content-Type"); ct != "video/mp4" {
		t.Errorf("分片 Content-Type = %q", ct)
	}
}

// TestHlsPathSafety 非法文件名必须被拒绝
func TestHlsPathSafety(t *testing.T) {
	ff := FindFfmpeg()
	if ff == nil {
		t.Skip("ffmpeg 不可用，跳过集成测试")
	}
	root := t.TempDir()
	src := filepath.Join(root, "s.mp4")
	gen := exec.Command(ff.ffmpegPath, "-hide_banner", "-loglevel", "error", "-y",
		"-f", "lavfi", "-i", "testsrc2=s=160x120:d=2:r=30",
		"-c:v", "libx264", "-preset", "ultrafast", "-pix_fmt", "yuv420p", src)
	if out, err := gen.CombinedOutput(); err != nil {
		t.Skipf("无法生成测试视频: %s", strings.TrimSpace(string(out)))
	}
	srv := New(root, Options{})
	defer srv.Close()
	ts := httptest.NewServer(srv.Handler())
	defer ts.Close()
	// 先触发会话
	get(t, ts.URL+"/api/hls?path=/s.mp4&f=index.m3u8").Body.Close()
	// 目录穿越尝试
	resp := get(t, ts.URL+"/api/hls?path=/s.mp4&f=..%2F..%2Fserver.go")
	defer resp.Body.Close()
	if resp.StatusCode != 400 {
		t.Errorf("非法文件名应 400, 得到 %d", resp.StatusCode)
	}
}
