package server

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"sync"
	"time"
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
		_ = os.MkdirAll(dir, 0o755)
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
			_ = os.RemoveAll(p)
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
				_ = os.RemoveAll(s.dir)
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
	s, ok := m.sessions[key]
	if !ok {
		m.mu.Unlock()
		return
	}
	select {
	case <-s.done:
		m.mu.Unlock()
		return // 已完成/已结束
	default:
	}
	if s.cancel != nil && !s.stopped {
		s.stopped = true
		s.cancel()
	}
	// 立即强杀该会话的 ffmpeg 进程（GPU 转码偶发卡死时 Kill 不生效，
	// 若等 runOnce 内的 5s taskkill 兜底，期间进程持续占资源；
	// 且 fallback 可能已起第二个进程——一并清理防残留）。
	cmd := s.cmd
	m.mu.Unlock()
	if cmd != nil && cmd.Process != nil {
		_ = exec.Command("taskkill", "/F", "/T", "/PID", strconvItoa(cmd.Process.Pid)).Run()
	}
}
