package server

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

// Ffmpeg 服务端视频抽帧（可选增强：exe 同目录 ffmpeg\ 或 PATH 中存在 ffmpeg 时启用）
type Ffmpeg struct {
	ffmpegPath  string
	ffprobePath string
	sem         chan struct{} // 并发限流
}

const (
	ffmpegTimeout = 15 * time.Second
	ffmpegProbeTO = 5 * time.Second
	ffmpegMaxConc = 2
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
		fp = ff // 部分构建只有 ffmpeg：ffmpeg 也可读时长（-i 输出探测信息）
	}
	return &Ffmpeg{ffmpegPath: ff, ffprobePath: fp, sem: make(chan struct{}, ffmpegMaxConc)}
}

// Available 是否可用
func (f *Ffmpeg) Available() bool { return f != nil && f.ffmpegPath != "" }

// serveVideoThumb 视频缩略图：使用 ffmpeg 服务端抽帧（可选增强；未安装时前端自行抽帧）
func (s *Server) serveVideoThumb(w http.ResponseWriter, r *http.Request, abs string, fi os.FileInfo, wq, hq int) {
	if s.ff == nil {
		writeErr(w, http.StatusNotFound, "服务端未启用视频缩略图（前端会自动抽帧）")
		return
	}
	key := thumbKey(abs, fi, wq, hq)
	if data, ok := s.thumbs.Get(key); ok {
		serveCached(w, r, key, data, "image/jpeg")
		return
	}

	// singleflight：同一视频的并发抽帧请求只执行一次；ffmpeg 本身还有并发信号量兜底
	data, ok := s.thumbs.Do(key, func() ([]byte, bool) {
		if data, ok := s.thumbs.Get(key); ok {
			return data, true
		}
		tmp, err := os.CreateTemp("", "fsvthumb-*.jpg")
		if err != nil {
			return nil, false
		}
		tmpPath := tmp.Name()
		tmp.Close()
		defer os.Remove(tmpPath)

		if err := s.ff.Thumb(context.Background(), abs, tmpPath, wq, hq); err != nil {
			return nil, false
		}
		data, err := os.ReadFile(tmpPath)
		if err != nil {
			return nil, false
		}
		s.thumbs.Put(key, data)
		return data, true
	})
	if !ok {
		writeErr(w, http.StatusNotFound, "视频抽帧失败")
		return
	}
	serveCached(w, r, key, data, "image/jpeg")
}

// probeDuration 用 ffprobe 探测视频时长（秒）
func (f *Ffmpeg) probeDuration(ctx context.Context, path string) (float64, error) {
	args := []string{"-v", "error", "-show_entries", "format=duration", "-of", "json", path}
	ctx, cancel := context.WithTimeout(ctx, ffmpegProbeTO)
	defer cancel()
	out, err := exec.CommandContext(ctx, f.ffprobePath, args...).Output()
	if err != nil {
		return 0, err
	}
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

// Thumb 抽一帧生成 w×h 方形缩略图写入 outPath（JPEG）
func (f *Ffmpeg) Thumb(ctx context.Context, videoPath, outPath string, w, h int) error {
	f.sem <- struct{}{}
	defer func() { <-f.sem }()

	dur, err := f.probeDuration(ctx, videoPath)
	if err != nil {
		dur = 0
	}
	ctx, cancel := context.WithTimeout(ctx, ffmpegTimeout)
	defer cancel()

	// 取 1 秒处或 10% 时长处（避开片头黑屏）
	seek := 1.0
	if dur > 0 {
		seek = dur * 0.1
		if seek < 0.1 {
			seek = 0.1
		}
	}
	args := []string{
		"-hide_banner", "-loglevel", "error", "-y",
		"-ss", strconv.FormatFloat(seek, 'f', 2, 64),
		"-i", videoPath,
		"-frames:v", "1",
		"-vf", fmt.Sprintf("scale=%d:%d:force_original_aspect_ratio=increase,crop=%d:%d", w, h, w, h),
		"-q:v", "4",
		"-f", "image2", outPath,
	}
	cmd := exec.CommandContext(ctx, f.ffmpegPath, args...)
	if out, err := cmd.CombinedOutput(); err != nil {
		if ctx.Err() != nil {
			return fmt.Errorf("抽帧超时")
		}
		return fmt.Errorf("抽帧失败: %s", strings.TrimSpace(string(out)))
	}
	return nil
}
