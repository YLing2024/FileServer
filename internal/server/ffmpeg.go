package server

import (
	"bytes"
	"context"
	"crypto/sha1"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	"fileserver/internal/platform"
)

// Ffmpeg 服务端视频能力（可选增强：exe 同目录 ffmpeg\ 或 PATH 中存在 ffmpeg 时启用）。
// 负责：媒体元数据探测（ffprobe）、HLS 转码/重封装（GPU 优先）。
// 注：视频缩略图已 100% 改为浏览器抽帧，服务端不再生成视频缩略图。
type Ffmpeg struct {
	ffmpegPath  string
	ffprobePath string

	durMu     sync.RWMutex
	durations map[string]float64 // 时长元数据缓存：键 = 路径|大小|mtime 的 SHA1

	infoMu sync.RWMutex
	infos  map[string]*MediaInfo // 媒体信息缓存
	probing map[string]bool      // 探测进行中（单飞）

	encMu      sync.RWMutex
	encoder    string   // 选定的 H.264 编码器：h264_nvenc / h264_amf / h264_qsv / libx264
	gpu        bool     // 是否 GPU 编码
	encArgs    []string // 编码器附加参数（preset 等）
	hwaccel    string   // 与编码器配对的硬件解码加速：cuda / d3d11va / qsv / ""（CPU 编码时不启用）
	hasTonemap bool     // zscale+tonemap 滤镜可用（HDR 色调映射）
}

// MediaInfo 视频媒体元数据（供播放决策与前端展示）
type MediaInfo struct {
	Duration   float64 `json:"duration"`
	Width      int     `json:"width"`
	Height     int     `json:"height"`
	FPS        float64 `json:"fps"`     // 帧率（r_frame_rate，如 29.97/23.976/30）
	VideoCodec string  `json:"vcodec"`  // h264 / hevc / vp9 / av1 / mpeg4 / wmv3 ...
	AudioCodec string  `json:"acodec"`  // aac / mp3 / ac3 / opus / vorbis / ""（无音轨）
	PixFmt     string  `json:"pixfmt"`  // yuv420p / yuv420p10le ...（用于 HDR 判定）
	HasAudio   bool    `json:"has_audio"`
}

// Playable 浏览器可否直接原生播放（无需转码）。
// 规则：MP4/MOV 容器 + H.264 视频 + AAC/MP3 音频；WebM 容器 + VP8/9/AV1 + Opus/Vorbis。
// 其余（MKV/AVI/WMV/HEVC 等）一律走 HLS 转码。
func (m *MediaInfo) Playable(ext string) bool {
	e := strings.ToLower(ext)
	switch e {
	case ".mp4", ".m4v", ".mov":
		return m.VideoCodec == "h264" && (m.AudioCodec == "" || m.AudioCodec == "aac" || m.AudioCodec == "mp3")
	case ".webm":
		return (m.VideoCodec == "vp8" || m.VideoCodec == "vp9" || m.VideoCodec == "av1") &&
			(m.AudioCodec == "" || m.AudioCodec == "opus" || m.AudioCodec == "vorbis")
	}
	return false
}

// Copyable HLS 重封装（-c copy，零转码开销）是否可行：
// 视频 H.264 且音频 AAC/无音轨（MP3 需转音频，视频可 copy）。
func (m *MediaInfo) Copyable() bool {
	return m.VideoCodec == "h264"
}

const (
	ffmpegTimeout = 15 * time.Second
	ffmpegProbeTO = 5 * time.Second
	// 时长/媒体信息缓存条数上限（防无限增长）
	durationCacheMax = 4096
	mediaInfoMax     = 4096
	// mp4HeadProbe 顺序读取的头部区域大小：用于解析 MP4 顶层 box、识别怪封装
	// （怪封装的 moov 通常在头部，mdat 碎片也集中在前部；顺序读此区域即可
	// 在内存中解析，避免逐 box 随机 seek 在机械盘上耗时数秒）。
	mp4HeadProbe = 8 << 20
)

// FindFfmpeg 查找 ffmpeg/ffprobe：优先 exe 同目录的 ffmpeg\ 子目录，其次 PATH
func FindFfmpeg() *Ffmpeg {
	find := func(name string) string {
		// 候选位置：exe 同目录 ffmpeg\、exe 同目录、%LOCALAPPDATA%\FileServer\ffmpeg
		var dirs []string
		if exe, err := os.Executable(); err == nil {
			d := filepath.Dir(exe)
			dirs = append(dirs, filepath.Join(d, "ffmpeg"), d)
		}
		if la := os.Getenv("LOCALAPPDATA"); la != "" {
			dirs = append(dirs, filepath.Join(la, "FileServer", "ffmpeg"))
		}
		for _, dir := range dirs {
			p := filepath.Join(dir, name+".exe")
			if info, err := os.Stat(p); err == nil && !info.IsDir() {
				return p
			}
		}
		// PATH
		if p, err := exec.LookPath(name); err == nil {
			return p
		}
		return ""
	}
	ff := find("ffmpeg")
	fp := find("ffprobe")
	if ff == "" {
		return nil
	}
	if fp == "" {
		// 仅安装 ffmpeg、未装 ffprobe：时长探测回退到
		// `ffmpeg -i <file>` 并解析 stderr 的 Duration 字段（见 probeDuration）。
		fp = ff
	}
	f := &Ffmpeg{
		ffmpegPath:  ff,
		ffprobePath: fp,
		durations:   make(map[string]float64),
		infos:       make(map[string]*MediaInfo),
		encoder:     "libx264", // 默认 CPU 编码，探测到可用 GPU 后升级
	}
	// GPU 编码器探测在后台执行（约 1~3s，期间先用 libx264）
	go f.probeEncoder()
	return f
}

// probeEncoder 探测可用的 H.264 硬件编码器（启动时实测一次，全平台通用）：
// 依次试 h264_nvenc（NVIDIA）→ h264_amf（AMD）→ h264_qsv（Intel），
// 每个候选先确认 ffmpeg 构建包含该编码器、再跑一次极短的真实编码验证
// （仅列出编码器不等于可用：驱动/显卡缺失时打开会失败）。
// 全部失败回退 libx264（CPU，任何机器都能跑）。
// 同时探测 zscale/tonemap 滤镜（HDR 色调映射）可用性。
func (f *Ffmpeg) probeEncoder() {
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()

	out, err := exec.CommandContext(ctx, f.ffmpegPath, "-hide_banner", "-encoders").Output()
	if err != nil {
		return // 保持 libx264
	}
	encoders := string(out)
	candidates := []struct {
		name    string
		args    []string
		hwaccel string
	}{
		{"h264_nvenc", []string{"-preset", "p4", "-tune", "hq"}, "cuda"},
		{"h264_amf", []string{"-quality", "balanced"}, "d3d11va"},
		{"h264_qsv", []string{"-preset", "medium"}, "qsv"},
	}
	for _, c := range candidates {
		if !strings.Contains(encoders, c.name) {
			continue
		}
		args := append([]string{
			"-hide_banner", "-loglevel", "error", "-y",
			"-f", "lavfi", "-i", "testsrc2=s=320x180:d=0.5:r=30",
			"-an", "-c:v", c.name,
		}, c.args...)
		args = append(args, "-b:v", "800k", "-frames:v", "10", "-f", "null", "-")
		cmd := exec.CommandContext(ctx, f.ffmpegPath, args...)
		if err := cmd.Run(); err == nil {
			f.encMu.Lock()
			f.encoder = c.name
			f.gpu = true
			f.encArgs = c.args
			f.hwaccel = c.hwaccel
			f.encMu.Unlock()
			break
		}
		if ctx.Err() != nil {
			return
		}
	}

	// zscale/tonemap 滤镜探测（HDR→SDR 色调映射，无此滤镜时 HDR 视频跳过色调映射）
	tonemapArgs := []string{
		"-hide_banner", "-loglevel", "error", "-y",
		"-f", "lavfi", "-i", "testsrc2=s=320x180:d=0.3:r=30",
		"-vf", "zscale=t=linear:npl=100,format=gbrpf32le,zscale=p=bt709,tonemap=hable:desat=0,zscale=t=bt709:m=bt709:r=tv,format=yuv420p",
		"-frames:v", "3", "-f", "null", "-",
	}
	if cmd := exec.CommandContext(ctx, f.ffmpegPath, tonemapArgs...); cmd.Run() == nil {
		f.encMu.Lock()
		f.hasTonemap = true
		f.encMu.Unlock()
	}

	// 兜底：一切 GPU 尝试都失败时用 CPU
	f.encMu.Lock()
	if f.encoder == "" {
		f.encoder = "libx264"
		f.gpu = false
		f.encArgs = []string{"-preset", "veryfast"}
	}
	f.encMu.Unlock()
}

// EncoderInfo 当前选定的编码器、硬件解码加速与参数（供 HLS 转码/缩略图使用）
func (f *Ffmpeg) EncoderInfo() (name string, gpu bool, hwaccel string, args []string) {
	f.encMu.RLock()
	defer f.encMu.RUnlock()
	return f.encoder, f.gpu, f.hwaccel, append([]string(nil), f.encArgs...)
}

// HasTonemap zscale/tonemap 滤镜是否可用（HDR 色调映射）
func (f *Ffmpeg) HasTonemap() bool {
	f.encMu.RLock()
	defer f.encMu.RUnlock()
	return f.hasTonemap
}

// Available 是否可用
func (f *Ffmpeg) Available() bool { return f != nil && f.ffmpegPath != "" }

// mediaKey 媒体信息缓存键：路径|大小|mtime 的 SHA1
func mediaKey(abs string, fi os.FileInfo) string {
	h := sha1.Sum([]byte(fmt.Sprintf("%s|%d|%d", abs, fi.Size(), fi.ModTime().UnixNano())))
	return hex.EncodeToString(h[:])
}

// ProbeMediaCached 只查内存缓存的媒体信息，绝不发起 ffprobe。
// 供 video-info 快速响应使用（未命中时由调用方决定是否后台探测）。
func (f *Ffmpeg) ProbeMediaCached(abs string, fi os.FileInfo) (*MediaInfo, error) {
	key := mediaKey(abs, fi)
	f.infoMu.RLock()
	m, ok := f.infos[key]
	f.infoMu.RUnlock()
	if !ok {
		return nil, fmt.Errorf("媒体信息未缓存")
	}
	return m, nil
}

// ProbeMedia 探测视频媒体信息（ffprobe JSON），带内存缓存与并发单飞。
// 一次探测拿到：时长、视频编码、分辨率、像素格式、音频编码。
// 个别源探测极慢（moov 结构异常，一次十几秒）：设 2 秒超时——正常文件
// moov 结构良好 0.3s 内完成；超时失败由调用方降级（copy 兜底/转码默认参数），
// 绝不阻塞播放链路。
func (f *Ffmpeg) ProbeMedia(ctx context.Context, abs string, fi os.FileInfo) (*MediaInfo, error) {
	key := mediaKey(abs, fi)
	f.infoMu.RLock()
	if m, ok := f.infos[key]; ok {
		f.infoMu.RUnlock()
		return m, nil
	}
	f.infoMu.RUnlock()

	// 单飞：同一文件探测进行中不再重复发起——等待其完成共享结果。
	// 等待方（如 HLS 会话）宁可多等 2~3 秒拿到真实编码信息，
	// 也不能拿到「探测中」后误判为可 copy（HEVC copy 出来 Chrome 播不了）。
	f.infoMu.Lock()
	if f.probing == nil {
		f.probing = make(map[string]bool)
	}
	if f.probing[key] {
		f.infoMu.Unlock()
		deadline := time.Now().Add(3 * time.Second)
		for time.Now().Before(deadline) {
			time.Sleep(50 * time.Millisecond)
			f.infoMu.RLock()
			if m, ok := f.infos[key]; ok {
				f.infoMu.RUnlock()
				return m, nil
			}
			f.infoMu.RUnlock()
		}
		return nil, fmt.Errorf("探测超时")
	}
	f.probing[key] = true
	f.infoMu.Unlock()
	defer func() {
		f.infoMu.Lock()
		delete(f.probing, key)
		f.infoMu.Unlock()
	}()

	cctx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	var raw struct {
		Format struct {
			Duration string `json:"duration"`
		} `json:"format"`
		Streams []struct {
			CodecType  string `json:"codec_type"`
			CodecName  string `json:"codec_name"`
			Width      int    `json:"width"`
			Height     int    `json:"height"`
			PixFmt     string `json:"pix_fmt"`
			FrameRate  string `json:"r_frame_rate"`
			AvgFps     string `json:"avg_frame_rate"`
		} `json:"streams"`
	}
	out, err := exec.CommandContext(cctx, f.ffprobePath,
		"-v", "error", "-show_entries",
		"stream=codec_type,codec_name,width,height,pix_fmt,r_frame_rate,avg_frame_rate:format=duration",
		"-of", "json", abs).Output()
	if err != nil {
		// ffprobe 不可用（ffprobePath==ffmpegPath）：回退 duration + 空编解码信息
		dur, derr := f.probeDurationUncached(cctx, abs)
		if derr != nil {
			return nil, fmt.Errorf("探测失败: %v", derr)
		}
		m := &MediaInfo{Duration: dur}
		f.infoMu.Lock()
		if len(f.infos) >= mediaInfoMax {
			f.infos = make(map[string]*MediaInfo, mediaInfoMax)
		}
		f.infos[key] = m
		f.infoMu.Unlock()
		return m, nil
	}
	if err := json.Unmarshal(out, &raw); err != nil {
		return nil, fmt.Errorf("解析 ffprobe 输出失败: %v", err)
	}
	m := &MediaInfo{}
	m.Duration, _ = strconv.ParseFloat(raw.Format.Duration, 64)
	for _, st := range raw.Streams {
		switch st.CodecType {
		case "video":
			if m.VideoCodec == "" {
				m.VideoCodec = st.CodecName
				m.Width = st.Width
				m.Height = st.Height
				m.PixFmt = st.PixFmt
				m.FPS = parseFPS(st.FrameRate)
				if m.FPS <= 0 {
					m.FPS = parseFPS(st.AvgFps)
				}
			}
		case "audio":
			if m.AudioCodec == "" {
				m.AudioCodec = st.CodecName
				m.HasAudio = true
			}
		}
	}
	if m.VideoCodec == "" {
		return nil, fmt.Errorf("文件中未找到视频流")
	}
	f.infoMu.Lock()
	if len(f.infos) >= mediaInfoMax {
		f.infos = make(map[string]*MediaInfo, mediaInfoMax)
	}
	f.infos[key] = m
	f.infoMu.Unlock()
	return m, nil
}

// parseFPS 解析 ffprobe 的 "num/den" 形式帧率
func parseFPS(s string) float64 {
	parts := strings.Split(s, "/")
	if len(parts) != 2 {
		v, _ := strconv.ParseFloat(s, 64)
		return v
	}
	num, err1 := strconv.ParseFloat(parts[0], 64)
	den, err2 := strconv.ParseFloat(parts[1], 64)
	if err1 != nil || err2 != nil || den == 0 {
		return 0
	}
	return num / den
}

// probeDurationCached 只查内存缓存中的时长，绝不发起 ffprobe。
// 供缩略图等高频短任务使用（探测留给 video-info/播放链路，探测一次缓存共用）。
func (f *Ffmpeg) probeDurationCached(path string) (float64, error) {
	key, ok := f.durationKey(path)
	if !ok {
		return 0, fmt.Errorf("无法获取文件信息")
	}
	if d, hit := f.getDuration(key); hit {
		return d, nil
	}
	return 0, fmt.Errorf("时长未缓存")
}

// probeDuration 探测视频时长（秒）。优先 ffprobe（JSON 输出）；
// 当 ffprobe 缺失（ffprobePath==ffmpegPath）或 ffprobe 失败时，
// 回退到 `ffmpeg -i <file>` 并解析 stderr 中的 Duration 字段。
// 结果按「路径|大小|mtime」缓存，避免每张缩略图都重起一个 ffprobe 进程。
func (f *Ffmpeg) probeDuration(ctx context.Context, path string) (float64, error) {
	key, ok := f.durationKey(path)
	if ok {
		if d, hit := f.getDuration(key); hit {
			return d, nil
		}
	}

	ctx, cancel := context.WithTimeout(ctx, ffmpegProbeTO)
	defer cancel()
	dur, err := f.probeDurationUncached(ctx, path)
	if ok && err == nil {
		f.putDuration(key, dur)
	}
	return dur, err
}

// probeDurationUncached 实际执行时长探测（不查/不写缓存）
func (f *Ffmpeg) probeDurationUncached(ctx context.Context, path string) (float64, error) {
	if f.ffprobePath != f.ffmpegPath {
		out, err := exec.CommandContext(ctx, f.ffprobePath,
			"-v", "error", "-show_entries", "format=duration", "-of", "json", path).Output()
		if err == nil {
			if dur, derr := parseDurationJSON(out); derr == nil {
				return dur, nil
			}
		}
	}
	// 回退路径：ffmpeg -i 无 -show_entries 等 ffprobe 专有参数；
	// ffmpeg 对不存在的输入会以非零退出并打印 Duration 到 stderr。
	out, err := exec.CommandContext(ctx, f.ffmpegPath, "-hide_banner", "-i", path).CombinedOutput()
	if err == nil {
		return 0, fmt.Errorf("无时长信息")
	}
	return parseDurationFromStderr(out)
}

// durationKey 计算时长缓存键；文件不存在/不可 stat 时返回 ok=false（不缓存）
func (f *Ffmpeg) durationKey(path string) (string, bool) {
	fi, err := os.Stat(path)
	if err != nil {
		return "", false
	}
	h := sha1.Sum([]byte(fmt.Sprintf("%s|%d|%d", path, fi.Size(), fi.ModTime().UnixNano())))
	return hex.EncodeToString(h[:]), true
}

func (f *Ffmpeg) getDuration(key string) (float64, bool) {
	f.durMu.RLock()
	d, ok := f.durations[key]
	f.durMu.RUnlock()
	return d, ok
}

func (f *Ffmpeg) putDuration(key string, d float64) {
	f.durMu.Lock()
	if len(f.durations) >= durationCacheMax {
		f.durations = make(map[string]float64, durationCacheMax)
	}
	f.durations[key] = d
	f.durMu.Unlock()
}

// parseDurationJSON 解析 ffprobe JSON 输出中的 format.duration
func parseDurationJSON(out []byte) (float64, error) {
	var v struct {
		Format struct {
			Duration string `json:"duration"`
		} `json:"format"`
	}
	if err := json.Unmarshal(out, &v); err != nil || v.Format.Duration == "" {
		return 0, fmt.Errorf("无法解析时长")
	}
	return strconv.ParseFloat(v.Format.Duration, 64)
}

// parseDurationFromStderr 从 ffmpeg -i 的 stderr 输出解析 Duration: HH:MM:SS.ff
func parseDurationFromStderr(b []byte) (float64, error) {
	idx := bytes.Index(b, []byte("Duration:"))
	if idx < 0 {
		return 0, fmt.Errorf("未找到 Duration")
	}
	rest := b[idx+len("Duration:"):]
	rest = bytes.TrimLeft(rest, " ")
	if comma := bytes.IndexByte(rest, ','); comma >= 0 {
		rest = rest[:comma]
	}
	parts := bytes.Split(rest, []byte(":"))
	if len(parts) != 3 {
		return 0, fmt.Errorf("无法解析时长")
	}
	h, err1 := strconv.Atoi(string(parts[0]))
	m, err2 := strconv.Atoi(string(parts[1]))
	s, err3 := strconv.ParseFloat(string(parts[2]), 64)
	if err1 != nil || err2 != nil || err3 != nil {
		return 0, fmt.Errorf("无法解析时长")
	}
	return float64(h)*3600 + float64(m)*60 + s, nil
}

// runFFmpeg 启动 ffmpeg 并等待结束；子进程随主进程终止（防孤儿）。
// lowPriority=true 时以低于正常优先级启动（缩略图抽帧等后台任务，
// 避免与用户点开的播放/转码链路抢 CPU/IO）。
func runFFmpeg(ctx context.Context, path string, args []string, lowPriority bool) error {
	cmd := exec.CommandContext(ctx, path, args...)
	if lowPriority && runtime.GOOS == "windows" {
		// BELOW_NORMAL_PRIORITY_CLASS
		cmd.SysProcAttr = &syscall.SysProcAttr{CreationFlags: 0x00004000}
	}
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	if err := cmd.Start(); err != nil {
		return fmt.Errorf("启动失败: %v", err)
	}
	platform.KillOnParentExit(cmd)
	// 超时/取消后 CommandContext 会 Kill；个别情况 Kill 不生效，
	// 待 ctx 取消后再等 5 秒仍未退出才 taskkill /F /T 强制终止（防孤儿抽帧占盘）。
	// 注意：兜底必须在 ctx.Done() 之后——盲目 5 秒强杀会把正常慢文件误杀。
	waitCh := make(chan error, 1)
	go func() { waitCh <- cmd.Wait() }()
	var waitErr error
	select {
	case waitErr = <-waitCh:
	case <-ctx.Done():
		select {
		case waitErr = <-waitCh:
		case <-time.After(5 * time.Second):
			if cmd.Process != nil {
				exec.Command("taskkill", "/F", "/T", "/PID", strconv.Itoa(cmd.Process.Pid)).Run()
			}
			waitErr = <-waitCh
		}
	}
	if waitErr != nil {
		return fmt.Errorf("%s", strings.TrimSpace(stderr.String()))
	}
	return nil
}

// ExtractFrame 用 ffmpeg 从任意视频（含 MKV/RMVB/HEVC 等冷门格式）抽取一帧写为 JPEG。
// 供冷门格式的服务端缩略图使用（--ffmpeg 开启时）：
//   - 低优先级运行（BELOW_NORMAL），不抢用户播放/浏览；
//   - 超时 20s（个别冷门格式 seek 慢）；
//   - Job Object 随主进程终止（防孤儿 ffmpeg 占资源）。
// 帧位置：跳过开头黑场/片头（-ss 1），质量 4（较清晰）。
func (f *Ffmpeg) ExtractFrame(ctx context.Context, src, dst string) error {
	if f == nil || f.ffmpegPath == "" {
		return fmt.Errorf("ffmpeg 不可用")
	}
	ctx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	args := []string{
		"-hide_banner", "-loglevel", "error", "-y",
		"-ss", "1", // 跳过开头黑场/片头
		"-i", src,
		"-frames:v", "1",
		"-q:v", "4",
		"-f", "image2", dst,
	}
	if err := runFFmpeg(ctx, f.ffmpegPath, args, true); err != nil {
		os.Remove(dst)
		return err
	}
	if fi, err := os.Stat(dst); err != nil || fi.Size() == 0 {
		os.Remove(dst)
		return fmt.Errorf("抽帧结果为空")
	}
	return nil
}

// Faststart 把 MP4（moov 在尾部）重封装为 moov 前置的 MP4 写入 dst。
// 小文件几十毫秒完成；大文件（>32MB）为磁盘速任务，放宽超时到 10 分钟、
// 以低于正常优先级运行（不抢正在播放的直链链路）。-c copy 无损。
func (f *Ffmpeg) Faststart(ctx context.Context, src, dst string, size int64) error {
	timeout := 30 * time.Second
	lowPri := false
	if size > 32*1024*1024 {
		timeout = 10 * time.Minute
		lowPri = true
	}
	cctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	tmp := dst + ".tmp"
	args := []string{
		"-hide_banner", "-loglevel", "error", "-y",
		"-analyzeduration", "0", "-probesize", "32",
		"-i", src,
		"-map", "0",
		"-c", "copy",
		"-movflags", "+faststart",
		"-f", "mp4", tmp,
	}
	if err := runFFmpeg(cctx, f.ffmpegPath, args, lowPri); err != nil {
		os.Remove(tmp)
		return err
	}
	if err := os.Rename(tmp, dst); err != nil {
		os.Remove(tmp)
		return err
	}
	return nil
}

// mp4Layout 描述 MP4 顶层结构：moov 位置/大小与 mdat 块数量
type mp4Layout struct {
	moovOffset int64
	moovSize   int64
	mdatCount  int
}

// mp4LayoutOf 解析 MP4 顶层 box。关键性能点：顺序读文件头部一块区域后
// 在内存中解析 box，而不是每个 box 一次 ReadAt 随机寻道——怪封装文件有
// 几百到几千个 mdat 块，逐块 seek 在机械盘上要几秒（实测 4s），而顺序读
// 头部区域只需 ~150ms。
// 头部未找到 moov 时再读尾部区域找 moov（moov 在尾部的正常录制片）——
// 供 thumb-src 正确识别「moov 后置」文件，避免误判为解析失败而整段返回。
func mp4LayoutOf(abs string) mp4Layout {
	var l mp4Layout
	l.moovOffset = -1
	f, err := os.Open(abs)
	if err != nil {
		return l
	}
	defer f.Close()
	st, err := f.Stat()
	if err != nil || st.Size() < 1024 {
		return l
	}
	fileSize := st.Size()
	buf := make([]byte, mp4HeadProbe)
	n, _ := f.Read(buf) // 顺序读头部（机械盘友好）
	buf = buf[:n]
	var pos int64 = 0
	const maxBoxes = 5000
	for i := 0; i < maxBoxes && pos+8 <= int64(len(buf)); i++ {
		boxSize := int64(buf[pos])<<24 | int64(buf[pos+1])<<16 | int64(buf[pos+2])<<8 | int64(buf[pos+3])
		typ := string(buf[pos+4 : pos+8])
		if typ == "moov" {
			// 记录 moov 位置但不返回：怪封装可能是 moov 在头 + 大量 mdat 碎块在后，
			// 必须继续遍历才能统计 mdat 块数（这是「怪封装」的真正判据）。
			l.moovOffset = pos
			l.moovSize = boxSize
		} else if typ == "mdat" {
			l.mdatCount++
			if boxSize == 0 {
				break // mdat 延伸到 EOF，其后不再有顶层 box
			}
		}
		if boxSize == 1 {
			if pos+16 > int64(len(buf)) {
				break
			}
			boxSize = int64(buf[pos+8])<<56 | int64(buf[pos+9])<<48 | int64(buf[pos+10])<<40 | int64(buf[pos+11])<<32 |
				int64(buf[pos+12])<<24 | int64(buf[pos+13])<<16 | int64(buf[pos+14])<<8 | int64(buf[pos+15])
		}
		if boxSize < 8 {
			break
		}
		pos += boxSize
	}

	// 头部未找到 moov：读尾部区域找 moov（moov 在尾部的正常录制片）
	if l.moovOffset < 0 {
		tailStart := fileSize - mp4HeadProbe
		if tailStart < 0 {
			tailStart = 0
		}
		tbuf := make([]byte, mp4HeadProbe)
		tn, _ := f.ReadAt(tbuf, tailStart)
		tbuf = tbuf[:tn]
		var tpos int64 = 0
		for i := 0; i < maxBoxes && tpos+8 <= int64(len(tbuf)); i++ {
			boxSize := int64(tbuf[tpos])<<24 | int64(tbuf[tpos+1])<<16 | int64(tbuf[tpos+2])<<8 | int64(tbuf[tpos+3])
			typ := string(tbuf[tpos+4 : tpos+8])
			if typ == "moov" {
				l.moovOffset = tailStart + tpos
				l.moovSize = boxSize
				return l
			}
			if boxSize == 1 {
				if tpos+16 > int64(len(tbuf)) {
					break
				}
				boxSize = int64(tbuf[tpos+8])<<56 | int64(tbuf[tpos+9])<<48 | int64(tbuf[tpos+10])<<40 | int64(tbuf[tpos+11])<<32 |
					int64(tbuf[tpos+12])<<24 | int64(tbuf[tpos+13])<<16 | int64(tbuf[tpos+14])<<8 | int64(tbuf[tpos+15])
			}
			if boxSize < 8 {
				break
			}
			tpos += boxSize
		}
	}
	return l
}

// mp4MoovOffset 解析 MP4 顶层 box 结构，返回 moov box 的起始偏移。
// 返回 -1 表示未找到 moov（可能为分片 MP4 或无 moov）。
func mp4MoovOffset(f *os.File, size int64) int64 {
	buf := make([]byte, 8)
	var pos int64 = 0
	const maxBoxes = 64
	for i := 0; i < maxBoxes && pos+8 <= size; i++ {
		if _, err := f.ReadAt(buf, pos); err != nil {
			return -1
		}
		boxSize := int64(buf[0])<<24 | int64(buf[1])<<16 | int64(buf[2])<<8 | int64(buf[3])
		typ := string(buf[4:8])
		if typ == "moov" {
			return pos
		}
		if typ == "mdat" && boxSize == 0 {
			// mdat 延伸到文件末尾，其后不再有顶层 moov（moov 缺失或位于 mdat 前）
			return -1
		}
		// 64 位扩展 box 尺寸（size==1）：必须在 boxSize<8 判定之前处理，
		// 否则 size==1 会被误判为畸形而提前返回 -1
		if boxSize == 1 {
			var ext [8]byte
			if _, err := f.ReadAt(ext[:], pos+8); err != nil {
				return -1
			}
			boxSize = int64(ext[0])<<56 | int64(ext[1])<<48 | int64(ext[2])<<40 | int64(ext[3])<<32 |
				int64(ext[4])<<24 | int64(ext[5])<<16 | int64(ext[6])<<8 | int64(ext[7])
		}
		if boxSize < 8 {
			return -1
		}
		pos += boxSize
	}
	return -1
}

// mp4IsFastStart 判断 MP4 是否已 faststart（moov 在文件头部，浏览器可直接起播）。
func mp4IsFastStart(abs string) bool {
	l := mp4LayoutOf(abs)
	// moov 在开头 256KB 内视为已 faststart（正常情况 moov 紧跟 ftyp，几十 KB 内）
	return l.moovOffset >= 0 && l.moovOffset <= 256*1024
}

// mp4IsWeirdLayout 判断 MP4 是否为「怪封装」：mdat 碎片化严重（大量小 mdat 块交错）。
// 注意：不以 moov 大小为判据——规整化后的文件 moov 更大但 mdat 单块、解析飞快。
// 是否「值得规整」还需结合文件体积（见 isWeird：≥256MB 才有明显收益）。
func mp4IsWeirdLayout(abs string) bool {
	l := mp4LayoutOf(abs)
	return l.mdatCount > 4
}
