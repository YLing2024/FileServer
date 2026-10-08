package server

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/YLing2024/FileServer/internal/platform"
)

var segNameRe = regexp.MustCompile(`^[A-Za-z0-9_.-]+$`)

// Get 获取（或创建）path 对应的 HLS 会话。会话不存在时创建并阻塞等待首片产出
// （保证 /api/hls 首次响应就有播放列表可拉）。已存在的会话直接返回——
// 分片按当前转码进度服务（serveHlsFile 对未生成分片单独阻塞等待），
// 绝不能等转码全部完成（大文件重封装几十秒，会让每个分片请求都卡住，
// 表现为「加载中」几十秒——这就是点开视频要等很久的根因）。
func (m *HlsManager) Get(ctx context.Context, abs string, fi os.FileInfo, ff *Ffmpeg) (*hlsSession, error) {
	key := mediaKey(abs, fi)
	m.mu.Lock()
	if m.closed {
		m.mu.Unlock()
		return nil, fmt.Errorf("服务正在关闭")
	}
	if s, ok := m.sessions[key]; ok {
		m.mu.Unlock()
		return s, nil
	}
	s := &hlsSession{
		key:     key,
		src:     abs,
		dir:     filepath.Join(m.dir, key),
		started: time.Now(),
		done:    make(chan struct{}),
	}
	m.sessions[key] = s
	// 新会话开始：其他「已无请求」的运行中会话让位（原页面已关/刷新离开）。
	// 它们在本机磁盘上的持续读写会拖慢新会话首片产出。
	// 播放中的页面每几秒就有分片请求、每 8s 一次 manifest 轮询，
	// idle 超过 10s 即视为已离开，正常播放不会被误杀。
	for _, s2 := range m.sessions {
		if s2 == s {
			continue
		}
		select {
		case <-s2.done:
			continue
		default:
		}
		if s2.idleFor() > 10*time.Second && s2.cancel != nil && !s2.stopped {
			s2.stopped = true
			s2.cancel()
		}
	}
	m.mu.Unlock()

	// 会话目录已存在且完整（上次运行生成）：直接复用，不再重转
	if fi2, err := os.Stat(filepath.Join(s.dir, "index.m3u8")); err == nil && fi2.Size() > 0 {
		if data, err := os.ReadFile(filepath.Join(s.dir, "index.m3u8")); err == nil && strings.Contains(string(data), "#EXT-X-ENDLIST") {
			close(s.done)
			return s, nil
		}
	}
	// 否则重建目录并启动转码
	_ = os.RemoveAll(s.dir)
	_ = os.MkdirAll(s.dir, 0o755)

	sctx, cancel := context.WithCancel(context.Background())
	s.cancel = cancel

	// 转码/copy 并发槽位在 run() 内按规格获取（copy 与转码分开限流）
	go func() {
		defer close(s.done)
		s.err = m.run(sctx, s, ff, fi)
		if s.err != nil {
			// 失败会话从表里移除，下次请求重试；目录残留由启动清理兜底
			m.mu.Lock()
			if m.sessions[s.key] == s {
				delete(m.sessions, s.key)
			}
			m.mu.Unlock()
			_ = os.RemoveAll(s.dir)
		}
	}()
	// 等待首片（保证 /api/hls 首次响应就有播放列表可拉）
	waitCtx := ctx
	var cancelWait context.CancelFunc
	if waitCtx == nil {
		waitCtx, cancelWait = context.WithTimeout(context.Background(), hlsFirstSegTimeout)
		defer cancelWait()
	}
	deadline := time.Now().Add(hlsFirstSegTimeout)
	for {
		if fi2, err := os.Stat(filepath.Join(s.dir, "index.m3u8")); err == nil && fi2.Size() > 64 {
			return s, nil
		}
		select {
		case <-waitCtx.Done():
			return nil, fmt.Errorf("等待首片超时")
		case <-s.done:
			if s.err != nil {
				return nil, s.err
			}
			return s, nil
		case <-time.After(200 * time.Millisecond):
			if time.Now().After(deadline) {
				return nil, fmt.Errorf("等待首片超时")
			}
		}
	}
}

// transcodeSpec 一次 ffmpeg 尝试的参数规格
type transcodeSpec struct {
	copyMode bool
	hwaccel  string
	encoder  string
	encArgs  []string
}

// run 启动 ffmpeg 生成 HLS（EVENT 播放列表 + fmp4 分片），带停滞/空闲看门狗。
// 尝试序列（任一成功即返回）：
//  1. copy 重封装（零画质损失、极快；探测失败时也先尝试——常见容器 copy 即可播）
//  2. GPU/CPU 首选编码器 + 硬件解码（4K/HEVC 提速明显）
//  3. 降级兜底：libx264 CPU、无硬件解码、最简参数（应对个别源兼容问题）
func (m *HlsManager) run(ctx context.Context, s *hlsSession, ff *Ffmpeg, fi os.FileInfo) error {
	var info *MediaInfo
	noCopy := false // 已确认源不可 copy（HEVC）：探测失败也绝不能走 copy
	ext := strings.ToLower(filepath.Ext(s.src))
	if (ext == ".mp4" || ext == ".m4v" || ext == ".mov") && !mp4HasHEVC(s.src) {
		// H.264 MP4 → 直接 copy，跳过探测。
		// 怪封装文件的 ffprobe 一次十几秒，探测会白白拖慢首片。
		info = nil
	} else {
		// 其他容器或 HEVC：探测编码决定 copy 还是转码
		var err error
		info, err = ff.ProbeMedia(ctx, s.src, fi)
		if err != nil {
			info = nil // 探测失败/超时：走无信息兜底路径
		}
		if info == nil && (ext == ".mp4" || ext == ".m4v" || ext == ".mov") && mp4HasHEVC(s.src) {
			// 探测失败但确认 HEVC：禁止 copy（Chrome 无法解码 HEVC fMP4）
			noCopy = true
		}
	}

	encName, _, hwaccel, encArgs := ff.EncoderInfo()
	specs := make([]transcodeSpec, 0, 3)
	// copy 优先：有信息时要求 H.264 可 copy；无信息时也先试 copy（ffmpeg 自行解析，
	// 音频一律转 AAC 保证可播；copy 失败自动进入下方转码兜底）
	if (info == nil && !noCopy) || (info != nil && info.Copyable()) {
		specs = append(specs, transcodeSpec{copyMode: true})
	}
	if (info != nil && !info.Copyable()) || noCopy {
		specs = append(specs, transcodeSpec{hwaccel: hwaccel, encoder: encName, encArgs: encArgs})
	}
	// 最后兜底：CPU + 无 hwaccel + veryfast（应对个别源的兼容问题）
	fallback := transcodeSpec{encoder: "libx264", encArgs: []string{"-preset", "veryfast"}}
	if !sameSpec(specs[len(specs)-1], fallback) {
		specs = append(specs, fallback)
	}

	var lastErr error
	for _, spec := range specs {
		if ctx.Err() != nil {
			return fmt.Errorf("转码已取消")
		}
		// copy（磁盘速任务）与转码（长任务）分开限流：
		// 点开一个非 faststart MP4 不会被正在跑的 4K 转码长任务挡住
		sem := m.transSem
		if spec.copyMode {
			sem = m.copySem
		}
		select {
		case sem <- struct{}{}:
		case <-ctx.Done():
			return fmt.Errorf("转码已取消")
		}
		lastErr = m.runOnce(ctx, s, ff, info, spec)
		<-sem
		if lastErr == nil {
			return nil
		}
		if ctx.Err() != nil || s.stopped {
			return fmt.Errorf("转码已取消")
		}
	}
	return lastErr
}

// sameSpec 两个转码规格等价（避免无 GPU 时重复跑两次相同的 CPU 转码）
func sameSpec(a, b transcodeSpec) bool {
	return a.copyMode == b.copyMode && a.hwaccel == b.hwaccel &&
		a.encoder == b.encoder && strings.Join(a.encArgs, " ") == strings.Join(b.encArgs, " ")
}

// runOnce 按单个规格跑一次 ffmpeg HLS 生成。
// info 可为 nil（探测失败/进行中）：copy 模式音频一律转 AAC 保证可播；
// 转码模式用默认码率与通用像素格式。
func (m *HlsManager) runOnce(ctx context.Context, s *hlsSession, ff *Ffmpeg, info *MediaInfo, spec transcodeSpec) error {
	hasAudio := info != nil && info.HasAudio
	args := []string{"-hide_banner", "-loglevel", "error", "-y"}
	ext := strings.ToLower(filepath.Ext(s.src))
	if spec.copyMode && (ext == ".mp4" || ext == ".m4v" || ext == ".mov") {
		// MP4 的全部流信息都在 moov 里，跳过 ffmpeg 探测阶段：
		// 默认探测会顺序读文件前 ~5MB（怪封装片是 5.9MB 巨 moov + 数千 mdat），
		// 机械硬盘冷读一次十几秒，直接把首片产出拖到十几秒。
		args = append(args, "-analyzeduration", "0", "-probesize", "32")
	}
	if !spec.copyMode && spec.hwaccel != "" && hwaccelSupported(info) {
		// 硬件解码：-hwaccel 必须在 -i 之前；失败由上层降级重试兜底。
		// 仅对 H.264/HEVC 使用——CUDA 硬解不支持 RMVB(RealVideo)/VP9 等编码，
		// 强行 -hwaccel cuda 会卡死（进程挂起、永不产出分片，实测残留）。
		args = append(args, "-hwaccel", spec.hwaccel)
	}
	args = append(args, "-i", s.src, "-map", "0:v:0")
	if hasAudio || info == nil {
		args = append(args, "-map", "0:a:0?") // 探测未知时尝试映射音轨（无音轨则忽略）
	}
	args = append(args, "-sn", "-dn") // 丢弃字幕与数据流

	if spec.copyMode {
		// 视频直接 copy；音频仅在明确为 AAC 时 copy，否则转 AAC
		// （info==nil 时一律转 AAC：AC3/DTS 等 copy 进 fmp4 浏览器播不了）
		args = append(args, "-c:v", "copy")
		if info != nil && info.HasAudio && info.AudioCodec == "aac" {
			args = append(args, "-c:a", "copy")
		} else if hasAudio || info == nil {
			args = append(args, "-c:a", "aac", "-b:a", "128k", "-ar", "48000", "-ac", "2")
		} else {
			args = append(args, "-an")
		}
	} else {
		// 转码：H.264 + AAC
		args = append(args, "-c:v", spec.encoder)
		args = append(args, spec.encArgs...)
		br := bitrateFor(info)
		args = append(args, "-b:v", fmt.Sprintf("%dk", br))
		// 流控：限制瞬时码率峰值（HLS 分片内波动过大易卡顿）
		args = append(args, "-maxrate", fmt.Sprintf("%dk", br*3/2), "-bufsize", fmt.Sprintf("%dk", br*2))
		// 关键帧间隔 = 帧率 × 分片时长：HLS 分片边界对齐关键帧，
		// 保证首片快速产出（3s 内容即可起播）且 seek 精确。
		// 用 -g（GOP）而非 -force_key_frames expr：NVENC 对后者支持不稳
		// （实测首个关键帧会拖到其内部 GOP 默认值，导致首片等待过长）。
		if info != nil && info.FPS > 0 && info.FPS < 240 {
			gop := int(info.FPS*hlsSegTime + 0.5)
			if gop < 12 {
				gop = 12
			}
			args = append(args, "-g", strconvItoa(gop))
		}
		// HDR 10bit 源：色调映射到 SDR，避免转码后发灰
		// （hasTonemap 由启动探测确认滤镜存在；无滤镜时跳过映射保证可播）
		if info != nil && isHDR(info) && ff.HasTonemap() {
			args = append(args, "-vf",
				"zscale=t=linear:npl=100,format=gbrpf32le,zscale=p=bt709,tonemap=hable:desat=0,zscale=t=bt709:m=bt709:r=tv,format=yuv420p")
		} else if info != nil && !strings.HasPrefix(info.PixFmt, "yuv420p") && info.PixFmt != "" {
			args = append(args, "-vf", "format=yuv420p")
		} else if info == nil {
			// 探测未知：通用输出像素格式，保证任何输入都能转出标准 H.264
			args = append(args, "-pix_fmt", "yuv420p")
		}
		if hasAudio || info == nil {
			args = append(args, "-c:a", "aac", "-b:a", "128k", "-ar", "48000", "-ac", "2")
		} else {
			args = append(args, "-an")
		}
	}

	args = append(args,
		"-hls_time", strconvItoa(hlsSegTime),
		"-hls_list_size", "0",
		"-hls_playlist_type", "event",
		"-hls_segment_type", "fmp4",
		"-hls_flags", "independent_segments",
		"-hls_segment_filename", filepath.Join(s.dir, "seg_%06d.m4s"),
		filepath.Join(s.dir, "index.m3u8"),
	)

	cmd := exec.CommandContext(ctx, ff.ffmpegPath, args...)
	cmd.Dir = s.dir
	s.cmd = cmd
	var stderr strings.Builder
	cmd.Stderr = &stderr

	// 看门狗：
	// 1) 停滞检测——hlsStallTimeout 内目录无产出（ffmpeg 卡死）则终止；
	// 2) 空闲检测——转码运行中 hlsIdleCancel 无播放请求（点开就关）则终止。
	stopWatch := make(chan struct{})
	go func() {
		ticker := time.NewTicker(5 * time.Second)
		defer ticker.Stop()
		var lastSize int64 = -1
		var lastGrowth time.Time
		for {
			select {
			case <-stopWatch:
				return
			case <-ticker.C:
				if s.idleFor() > hlsIdleCancel {
					s.cancel()
					return
				}
				size := dirSize(s.dir)
				if lastSize >= 0 && size == lastSize {
					if time.Since(lastGrowth) > hlsStallTimeout {
						s.cancel()
						return
					}
				} else {
					lastGrowth = time.Now()
					lastSize = size
				}
			}
		}
	}()

	if err := cmd.Start(); err != nil {
		close(stopWatch)
		return fmt.Errorf("启动转码进程失败: %v", err)
	}
	// 主进程被强杀（关窗口 X）时 ffmpeg 子进程一并终止，不留孤儿
	platform.KillOnParentExit(cmd)
	// 取消/超时后 CommandContext 会 Kill 子进程；个别情况（进程卡死磁盘 IO）
	// Kill 可能不生效，待 ctx 取消后再等 5 秒仍未退出才 taskkill /F /T 强杀。
	// 注意：兜底必须在 ctx.Done() 之后——转码是长任务，不能盲目限时。
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
				_ = exec.Command("taskkill", "/F", "/T", "/PID", strconvItoa(cmd.Process.Pid)).Run()
			}
			waitErr = <-waitCh
		}
	}
	close(stopWatch)
	if ctx.Err() != nil && s.stopped {
		return fmt.Errorf("转码已停止")
	}
	if waitErr != nil {
		if ctx.Err() != nil {
			return fmt.Errorf("转码中断")
		}
		return fmt.Errorf("HLS 生成失败: %s", strings.TrimSpace(stderr.String()))
	}
	return nil
}

func strconvItoa(n int) string { return fmt.Sprintf("%d", n) }

// hwaccelSupported 该媒体是否适合硬件解码（-hwaccel cuda 等）：
// 仅 H.264/HEVC 有成熟的 GPU 硬解；RMVB(RealVideo)/VP8/9/AV1 等用 CUDA 硬解
// 会挂起（实测 nvenc+cuda 转 RMVB 进程卡死不产分片）。info 为 nil（未探测到）
// 时保守不用硬解（CPU 解码兜底，仅 GPU 编码）。
func hwaccelSupported(info *MediaInfo) bool {
	if info == nil {
		return false
	}
	switch info.VideoCodec {
	case "h264", "hevc":
		return true
	}
	return false
}

// dirSize 目录内所有文件总大小（看门狗进度判据）
func dirSize(dir string) int64 {
	var total int64
	entries, err := os.ReadDir(dir)
	if err != nil {
		return 0
	}
	for _, e := range entries {
		if info, err := e.Info(); err == nil {
			total += info.Size()
		}
	}
	return total
}

// bitrateFor 按分辨率估算合理码率（Kbps，LAN 播放质量优先）；info 为 nil 时用 1080p 默认
func bitrateFor(m *MediaInfo) int {
	h := 0
	if m != nil {
		h = m.Height
	}
	if h == 0 {
		h = 1080
	}
	switch {
	case h >= 1440:
		return 14000
	case h >= 1080:
		return 6000
	case h >= 720:
		return 3500
	case h >= 480:
		return 2000
	default:
		return 1000
	}
}

// isHDR 判断源是否为 HDR（10bit 像素格式或 PQ/HLG 传递函数）
func isHDR(m *MediaInfo) bool {
	if strings.Contains(m.PixFmt, "10le") || strings.Contains(m.PixFmt, "12le") {
		return true
	}
	return false
}
