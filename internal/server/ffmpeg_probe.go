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
	"strconv"
	"strings"
	"time"
)

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
			CodecType string `json:"codec_type"`
			CodecName string `json:"codec_name"`
			Width     int    `json:"width"`
			Height    int    `json:"height"`
			PixFmt    string `json:"pix_fmt"`
			FrameRate string `json:"r_frame_rate"`
			AvgFps    string `json:"avg_frame_rate"`
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
