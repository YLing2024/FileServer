package server

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"time"

	"github.com/YLing2024/FileServer/internal/platform"
)

// runFFmpeg 启动 ffmpeg 并等待结束；子进程随主进程终止（防孤儿）。
// lowPriority=true 时以低于正常优先级启动（缩略图抽帧等后台任务，
// 避免与用户点开的播放/转码链路抢 CPU/IO）。
func runFFmpeg(ctx context.Context, path string, args []string, lowPriority bool) error {
	cmd := exec.CommandContext(ctx, path, args...)
	if lowPriority {
		// 平台差异隔离在 internal/platform：Windows 上设 BELOW_NORMAL，
		// 其余平台为 no-op（见 platform.SetLowPriority）。
		platform.SetLowPriority(cmd)
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
				_ = exec.Command("taskkill", "/F", "/T", "/PID", strconv.Itoa(cmd.Process.Pid)).Run()
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
//
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
		_ = os.Remove(dst)
		return err
	}
	if fi, err := os.Stat(dst); err != nil || fi.Size() == 0 {
		_ = os.Remove(dst)
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
		_ = os.Remove(tmp)
		return err
	}
	if err := os.Rename(tmp, dst); err != nil {
		_ = os.Remove(tmp)
		return err
	}
	return nil
}
