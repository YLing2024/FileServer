package server

import (
	"context"
	"crypto/sha1"
	"encoding/hex"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

// Ffmpeg 服务端视频能力（可选增强：exe 同目录 ffmpeg\ 或 PATH 中存在 ffmpeg 时启用）。
// 负责：媒体元数据探测（ffprobe）、HLS 转码/重封装（GPU 优先）。
// 注：视频缩略图已 100% 改为浏览器抽帧，服务端不再生成视频缩略图。
type Ffmpeg struct {
	ffmpegPath  string
	ffprobePath string

	durMu     sync.RWMutex
	durations map[string]float64 // 时长元数据缓存：键 = 路径|大小|mtime 的 SHA1

	infoMu  sync.RWMutex
	infos   map[string]*MediaInfo // 媒体信息缓存
	probing map[string]bool       // 探测进行中（单飞）

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
	FPS        float64 `json:"fps"`    // 帧率（r_frame_rate，如 29.97/23.976/30）
	VideoCodec string  `json:"vcodec"` // h264 / hevc / vp9 / av1 / mpeg4 / wmv3 ...
	AudioCodec string  `json:"acodec"` // aac / mp3 / ac3 / opus / vorbis / ""（无音轨）
	PixFmt     string  `json:"pixfmt"` // yuv420p / yuv420p10le ...（用于 HDR 判定）
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
