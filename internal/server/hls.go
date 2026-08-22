package server

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"time"

	"fileserver/internal/platform"
)

// ============================================================
// HLS 转码/重封装：让浏览器无法原生播放的视频（MKV/AVI/WMV/HEVC 等）
// 以「边转边播」的方式在线观看。
//
// 流程：请求 /api/hls 时按「路径|大小|mtime」建会话，ffmpeg 在会话目录里
// 连续生成分片 MP4（fmp4）与 index.m3u8（EVENT 型播放列表，边生成边可见），
// 前端 hls.js 拉取播放列表后立即起播（首片 2~4s 内产出），转码完成后
// 播放列表追加 ENDLIST 变成完整 VOD，全程可拖进度条。
//
// 编码器：优先 GPU（NVENC/AMF/QSV，启动时探测），不可用回退 libx264 CPU。
// 源已是 H.264 时用 -c copy 重封装（零画质损失、极快）。
// ============================================================

// hlsMaxAge 会话缓存保留时长；超时清理
const hlsMaxAge = 3 * 24 * time.Hour

// hlsMaxConc 全局同时转码的会话数上限（GPU/CPU 编码长任务，2 路足够）
const hlsMaxConc = 2

// hlsCopyMaxConc copy 重封装并发上限：磁盘速任务（百兆/秒），4 路并发
// 让连续点开多个视频时后面的不需要排长队（重封装远快于播放）
const hlsCopyMaxConc = 4

// hlsSegTime 单分片时长（秒）。小分片起播快、seek 粒度细；过大则首片等待久。
const hlsSegTime = 3

// hlsStallTimeout 转码停滞判定：目录无任何产出超过该时长视为卡死，终止进程
const hlsStallTimeout = 60 * time.Second

// hlsIdleCancel 转码运行中连续无播放请求超过该时长则取消（防「点开就关」白烧 GPU/CPU；
// 正常播放每几秒就有分片请求，暂停/离开 10 分钟视为放弃观看）
const hlsIdleCancel = 10 * time.Minute

// hlsFirstSegTimeout 等待首片产出的上限（含全局并发排队时间）
const hlsFirstSegTimeout = 60 * time.Second

// hlsSegWaitTimeout seek 超前时等待分片生成的上限。
// 必须与前端 hls.js 的 fragLoadingTimeOut 对齐（略短）：
// 服务端先超时返回 404，hls.js 立刻得知并决定重试，而不是双方互相死等。
const hlsSegWaitTimeout = 28 * time.Second

// HlsManager 管理所有 HLS 转码会话
type HlsManager struct {
	dir      string
	mu       sync.Mutex
	sessions map[string]*hlsSession // key（路径|大小|mtime SHA1） -> 会话
	copySem  chan struct{}          // copy 重封装并发（磁盘速任务，秒级完成）
	transSem chan struct{}          // 转码并发（GPU/CPU 长任务）
	closed   bool
}

type hlsSession struct {
	key     string
	src     string // 源文件绝对路径
	dir     string // 会话目录（index.m3u8 + seg_*.m4s）
	started time.Time

	done chan struct{} // 转码完成（或失败）时关闭
	err  error

	cmd     *exec.Cmd
	cancel  context.CancelFunc
	stopped bool // 被主动终止（服务器关闭/超时）

	reqMu   sync.Mutex
	lastReq time.Time // 最近一次分片/列表请求时间（防「点开就关」浪费转码）
}

func (s *hlsSession) touch() {
	s.reqMu.Lock()
	s.lastReq = time.Now()
	s.reqMu.Unlock()
}

func (s *hlsSession) idleFor() time.Duration {
	s.reqMu.Lock()
	defer s.reqMu.Unlock()
	if s.lastReq.IsZero() {
		return 0
	}
	return time.Since(s.lastReq)
}

// NewHlsManager 创建 HLS 管理器。
// 缓存目录：共享根目录下的隐藏文件夹 .FileServer\hls（不污染系统目录）。
// 共享根目录不可写时回退系统临时目录。
func NewHlsManager(root string) *HlsManager {
	dir := filepath.Join(root, cacheDirName, "hls")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		dir = filepath.Join(os.TempDir(), "FileServer", "hls")
		os.MkdirAll(dir, 0o755)
	}
	m := &HlsManager{
		dir:      dir,
		sessions: make(map[string]*hlsSession),
		copySem:  make(chan struct{}, hlsCopyMaxConc),
		transSem: make(chan struct{}, hlsMaxConc),
	}
	// 启动时清理历史残留（上次异常退出遗留的半成品/过期缓存）
	m.cleanupStale()
	go m.cleanupLoop()
	return m
}

// cleanupStale 启动时删除超过 hlsMaxAge 的会话目录（含上次异常退出残留）
func (m *HlsManager) cleanupStale() {
	cutoff := time.Now().Add(-hlsMaxAge)
	entries, err := os.ReadDir(m.dir)
	if err != nil {
		return
	}
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		p := filepath.Join(m.dir, e.Name())
		if info, err := e.Info(); err == nil && info.ModTime().Before(cutoff) {
			os.RemoveAll(p)
		}
	}
}

func (m *HlsManager) cleanupLoop() {
	for {
		time.Sleep(time.Hour)
		if m.closed {
			return
		}
		cutoff := time.Now().Add(-hlsMaxAge)
		m.mu.Lock()
		for k, s := range m.sessions {
			// 已完成或过期的会话：目录按 mtime 判定
			if s.started.Before(cutoff) {
				os.RemoveAll(s.dir)
				delete(m.sessions, k)
			}
		}
		m.mu.Unlock()
	}
}

// Close 关闭所有会话并终止转码进程（服务器退出时调用）
func (m *HlsManager) Close() {
	m.mu.Lock()
	m.closed = true
	for _, s := range m.sessions {
		if s.cancel != nil && !s.stopped {
			s.stopped = true
			s.cancel()
		}
	}
	m.mu.Unlock()
}

// Active 是否有正在运行的转码会话（预热 IO 让路判定用）
func (m *HlsManager) Active() bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, s := range m.sessions {
		select {
		case <-s.done:
		default:
			return true
		}
	}
	return false
}

// Abandon 终止 key 对应会话的转码进程（前端关闭播放器时调用）。
// 已完成的会话不受影响（其缓存文件保留，下次点开秒开）。
func (m *HlsManager) Abandon(key string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	s, ok := m.sessions[key]
	if !ok {
		return
	}
	select {
	case <-s.done:
		return // 已完成/已结束
	default:
	}
	if s.cancel != nil && !s.stopped {
		s.stopped = true
		s.cancel()
	}
}

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
	os.RemoveAll(s.dir)
	os.MkdirAll(s.dir, 0o755)

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
			os.RemoveAll(s.dir)
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
		if ctx.Err() != nil {
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
	if !spec.copyMode && spec.hwaccel != "" {
		// 硬件解码：-hwaccel 必须在 -i 之前；失败由上层降级重试兜底
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
				exec.Command("taskkill", "/F", "/T", "/PID", strconvItoa(cmd.Process.Pid)).Run()
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

// serveHlsFile 从会话目录按文件名安全地提供文件（index.m3u8 / seg_*.m4s）
func (s *Server) serveHlsFile(w http.ResponseWriter, r *http.Request, session *hlsSession, fname string) {
	if !segNameRe.MatchString(fname) || strings.Contains(fname, "..") {
		writeErr(w, http.StatusBadRequest, "非法文件名")
		return
	}
	session.touch() // 记录访问时间（空闲取消判定）
	fp := filepath.Join(session.dir, fname)
	fi, err := os.Stat(fp)
	if err != nil {
		// 分片尚未生成（seek 超前于转码进度 / 首片未出）：
		// 阻塞等待该分片生成——copy 重封装是磁盘速任务（秒级），
		// 转码则等待到会话完成或超时。避免 hls.js 404 后放弃。
		if strings.HasSuffix(fname, ".m4s") || fname == "init.mp4" {
			deadline := time.Now().Add(hlsSegWaitTimeout)
			for {
				if fi2, serr := os.Stat(fp); serr == nil && fi2.Size() > 0 {
					fi = fi2
					break
				}
				select {
				case <-session.done:
					writeErr(w, http.StatusNotFound, "分片不可用（转码已结束）")
					return
				case <-time.After(200 * time.Millisecond):
				}
				if time.Now().After(deadline) {
					writeErr(w, http.StatusNotFound, "分片尚未生成（转码中，请稍后拖动）")
					return
				}
			}
		} else {
			writeErr(w, http.StatusNotFound, "播放列表尚未生成")
			return
		}
	}
	if strings.HasSuffix(fname, ".m3u8") {
		// 播放列表随时增长：禁止缓存，hls.js 按需重载
		w.Header().Set("Content-Type", "application/vnd.apple.mpegurl")
		w.Header().Set("Cache-Control", "no-store")
		data, err := os.ReadFile(fp)
		if err != nil {
			writeErr(w, http.StatusInternalServerError, "读取播放列表失败")
			return
		}
		w.Write(rewritePlaylistURIs(data, r))
		return
	}
	// 分片内容不变：可缓存
	f, err := os.Open(fp)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "打开分片失败")
		return
	}
	defer f.Close()
	w.Header().Set("Content-Type", "video/mp4")
	w.Header().Set("Cache-Control", "public, max-age=3600")
	http.ServeContent(w, r, fname, fi.ModTime(), f)
}

// rewritePlaylistURIs 改写播放列表：
// 1) 相对 URI（init.mp4 / seg_*.m4s）→ 指向 /api/hls 的绝对 URL（浏览器/hls.js
//    按播放列表 URL 目录解析相对 URI 会 404）；
// 2) 输出「VOD 快照」语义：去掉 EXT-X-PLAYLIST-TYPE:EVENT、末尾追加 ENDLIST。
//    服务端转码快于实时，但 hls.js 对 EVENT（live）播放列表会追直播边缘、
//    大 GOP 源起播要等几十秒；VOD 快照让它拿到首片立即起播，
//    前端播放中定时重载 manifest 获取随转码增长的新分片。
func rewritePlaylistURIs(data []byte, r *http.Request) []byte {
	path := r.URL.Query().Get("path")
	base := "/api/hls?path=" + url.QueryEscape(path) + "&f="
	lines := strings.Split(string(data), "\n")
	out := make([]string, 0, len(lines)+2)
	hasEnd := false
	for _, ln := range lines {
		trimmed := strings.TrimSpace(ln)
		if trimmed == "" {
			continue
		}
		if strings.HasPrefix(trimmed, "#EXT-X-PLAYLIST-TYPE:") {
			continue // 移除 live/EVENT 声明 → VOD 快照
		}
		if trimmed == "#EXT-X-ENDLIST" {
			hasEnd = true
			out = append(out, trimmed)
			continue
		}
		if strings.HasPrefix(trimmed, "#EXT-X-MAP:") {
			if idx := strings.Index(trimmed, `URI="`); idx >= 0 {
				rest := trimmed[idx+len(`URI="`):]
				if end := strings.Index(rest, `"`); end > 0 {
					uri := rest[:end]
					if !strings.Contains(uri, "://") && !strings.HasPrefix(uri, "/") {
						out = append(out, trimmed[:idx]+`URI="`+base+uri+`"`)
						continue
					}
				}
			}
			out = append(out, trimmed)
			continue
		}
		if strings.HasPrefix(trimmed, "#") {
			out = append(out, trimmed)
			continue
		}
		// 纯分片文件名行（无路径分隔符）→ 绝对 URL
		if !strings.Contains(trimmed, "/") && !strings.Contains(trimmed, "://") {
			out = append(out, base+trimmed)
		} else {
			out = append(out, trimmed)
		}
	}
	if !hasEnd {
		out = append(out, "#EXT-X-ENDLIST")
	}
	return []byte(strings.Join(out, "\n"))
}
