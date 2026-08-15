package server

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
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

	// 取 1 秒处或 10% 时长处（避开片头黑屏）；抽帧缩略图只需一个代表性画面，
	// 用相对靠前的位置即可，避免长视频 seek 太深。
	seek := 1.0
	if dur > 0 {
		seek = dur * 0.05
		if seek < 0.1 {
			seek = 0.1
		}
		if seek > 30 {
			seek = 30
		}
	}

	// 关键：-ss 必须放在 -i 之后做「输入关键帧 seek」。
	// 之前的写法把 -ss 放在 -i 之前（输出 seek），ffmpeg 会从文件头开始逐帧解码
	// 到目标时间点，大视频/Range 文件解码整段流，极慢；放到 -i 之后则直接跳到
	// 最近的关键帧瞬时定位，配合 -frames:v 1 只解码 1 帧。
	// -noaccurate_seek 进一步跳过关键帧之间的精细定位，加速取帧。
	args := []string{
		"-hide_banner", "-loglevel", "error", "-y",
		"-i", videoPath,
		"-ss", strconv.FormatFloat(seek, 'f', 3, 64),
		"-noaccurate_seek",
		"-map", "0:v:0",
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

// mp4MoovOffset 解析 MP4 顶层 box 结构，返回 moov box 的起始偏移。
// 返回 -1 表示未找到 moov（可能为分片 MP4 或无 moov，需要 remux 才能播）。
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
		if boxSize < 8 {
			return -1
		}
		// 64 位扩展 box 尺寸（size==1）
		if boxSize == 1 {
			var ext [8]byte
			if _, err := f.ReadAt(ext[:], pos+8); err != nil {
				return -1
			}
			boxSize = int64(ext[0])<<56 | int64(ext[1])<<48 | int64(ext[2])<<40 | int64(ext[3])<<32 |
				int64(ext[4])<<24 | int64(ext[5])<<16 | int64(ext[6])<<8 | int64(ext[7])
		}
		pos += boxSize
	}
	return -1
}

// needsFastStart 判断 MP4 是否需要服务端 faststart 重封装才能即时播放：
// moov 不在文件头部（< 前 1MB 或不在前几个 box）时，浏览器必须抓取整个/末端索引才能起播，
// 表现为「大视频初始加载很久」。moov 缺失/在 mdat 之后同样需要。
func mp4NeedsFastStart(abs string) bool {
	f, err := os.Open(abs)
	if err != nil {
		return false
	}
	defer f.Close()
	st, err := f.Stat()
	if err != nil || st.Size() < 1024 {
		return false
	}
	off := mp4MoovOffset(f, st.Size())
	if off < 0 {
		return true // 无 moov 或 mdat 排在 moov 前，无法即时流式
	}
	// moov 在开头 256KB 内视为已 faststart（正常情况 moov 紧跟 ftyp，几十 KB 内）
	return off > 256*1024
}

// RemuxFastStart 用 ffmpeg 将输入视频以 faststart（moov 前移）+ 分片 MP4 方式
// 重封装（-c copy 不重编码，GPU/CPU 都几乎零开销）写入 w，使浏览器可边下载边播放。
// 返回实际 MIME 类型与错误。
func (f *Ffmpeg) RemuxFastStart(ctx context.Context, videoPath string, w io.Writer) error {
	f.sem <- struct{}{}
	defer func() { <-f.sem }()

	ctx, cancel := context.WithTimeout(ctx, ffmpegTimeout)
	defer cancel()

	// -movflags frag_keyframe+empty_moov+default_base_moof：输出分片 MP4，
	// 每个关键帧一个 moof，moov 极小且位于开头，浏览器立即起播并可逐段拉取。
	// -c copy 不重编码；-f mp4 输出格式。
	args := []string{
		"-hide_banner", "-loglevel", "error", "-y",
		"-i", videoPath,
		"-map", "0",
		"-c", "copy",
		"-movflags", "frag_keyframe+empty_moov+default_base_moof",
		"-f", "mp4",
		"pipe:1",
	}
	cmd := exec.CommandContext(ctx, f.ffmpegPath, args...)
	cmd.Stdout = w
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		if ctx.Err() != nil {
			return fmt.Errorf("转码超时")
		}
		return fmt.Errorf("faststart 失败: %s", strings.TrimSpace(stderr.String()))
	}
	return nil
}
