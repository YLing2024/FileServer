package server

import (
	"bytes"
	"encoding/binary"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

// ============================================================
// moov 预读预热：目录页浏览时把 MP4 的 moov 区域顺序读进 OS 文件缓存。
// 怪封装片（5.9MB 巨 moov + 数千 mdat）在机械硬盘上冷读一次十几秒，
// 用户点开时才让 ffmpeg 去读就得干等；提前预热后首片毫秒级产出。
// 任何 HLS 会话运行期间立即让路（播放优先，绝不抢播放 IO）。
// ============================================================

const (
	prewarmChunk = 512 << 10             // 每次读 512KB
	prewarmPause = 30 * time.Millisecond // 块间暂停：约 8MB/s 限速
	prewarmMax   = 8 << 20             // 头部/尾部各自最多读 8MB
	prewarmDedup = 30 * time.Minute    // 同一文件 30 分钟内只预热一次
	prewarmQueue = 32                  // 忙时最多积压的待预热文件数
	prewarmConcurrent = 1              // 单路预热：机械盘上多路预热会与播放抢磁头
)

type prewarmReq struct {
	key string
	abs string
}

// playbackState 直链播放活动跟踪：浏览器直链播放（direct 模式）时发出连续的
// Range 请求，服务端记录最近一次活动时间。机械硬盘上任何并行的顺序读者
// （moov 预热/缩略图抽帧/faststart 重封装）都会与播放抢磁头，把起播拖慢甚至
// 卡死——播放绝对优先，其余任务一律让路。
type playbackState struct {
	mu         sync.Mutex
	lastActive time.Time
}

// markVideoRead 直链视频被读取（带 Range 的流式请求）时调用
func (s *Server) markVideoRead() {
	s.pb.mu.Lock()
	s.pb.lastActive = time.Now()
	s.pb.mu.Unlock()
}

// directPlaying 直链播放是否活跃：最近 10 秒内有视频 Range 读取。
// 浏览器起播/续传的 Range 请求之间常有数秒间隙（缓冲播放中），用窗口覆盖，
// 期间预热/抽帧让路不会影响可用性（稍后自动恢复）。
func (s *Server) directPlaying() bool {
	s.pb.mu.Lock()
	defer s.pb.mu.Unlock()
	return time.Since(s.pb.lastActive) < 10*time.Second
}

type prewarmState struct {
	mu      sync.Mutex
	warmed  map[string]time.Time
	pending []prewarmReq // FIFO：按请求顺序（视口浏览顺序）预热，先看到的文件先热
	busy    int          // 正在预热的文件数（上限 prewarmConcurrent）
}

// handlePrewarm GET /api/prewarm?path= 把目标 MP4 的 moov 区域读进 OS 缓存
func (s *Server) handlePrewarm(w http.ResponseWriter, r *http.Request) {
	abs, err := s.safePath(r.URL.Query().Get("path"))
	if err != nil || s.hiddenBlocked(abs) {
		w.WriteHeader(http.StatusNoContent)
		return
	}
	switch strings.ToLower(filepath.Ext(abs)) {
	case ".mp4", ".m4v", ".mov":
	default:
		w.WriteHeader(http.StatusNoContent)
		return
	}
	fi, err := os.Stat(abs)
	if err != nil || fi.IsDir() || fi.Size() < 4<<20 {
		w.WriteHeader(http.StatusNoContent)
		return
	}
	key := mediaKey(abs, fi)
	now := time.Now()
	if s.hls.Active() || s.directPlaying() {
		// 播放进行中：不接受新的预热（让路）
		w.WriteHeader(http.StatusNoContent)
		return
	}
	s.pw.mu.Lock()
	if t, ok := s.pw.warmed[key]; ok && now.Sub(t) < prewarmDedup {
		s.pw.mu.Unlock()
		w.WriteHeader(http.StatusNoContent)
		return
	}
	if s.pw.busy >= prewarmConcurrent {
		if len(s.pw.pending) < prewarmQueue {
			s.pw.pending = append(s.pw.pending, prewarmReq{key, abs})
		}
		s.pw.mu.Unlock()
		w.WriteHeader(http.StatusNoContent)
		return
	}
	s.pw.busy++
	s.pw.warmed[key] = now
	s.pw.mu.Unlock()
	go s.prewarmFile(key, abs)
	w.WriteHeader(http.StatusNoContent)
}

// prewarmFile 顺序读 moov 区域；头部没找到 moov 就再读尾部兜底。
func (s *Server) prewarmFile(key, abs string) {
	defer func() {
		s.pw.mu.Lock()
		s.pw.busy--
		s.pw.mu.Unlock()
		s.prewarmNext()
	}()
	fi, err := os.Stat(abs)
	if err != nil {
		return
	}
	f, err := os.Open(abs)
	if err != nil {
		return
	}
	defer f.Close()
	buf := make([]byte, prewarmChunk)
	yield := func() {
		// 播放（HLS 转码或直链 Range 流）进行中：让路（等待，不退出队列）。
		// 机械硬盘上多个读者同时 seek 会把各自都拖慢几倍，播放优先。
		for s.hls.Active() || s.directPlaying() {
			time.Sleep(300 * time.Millisecond)
		}
		time.Sleep(prewarmPause) // 块间限速：预热是后台任务，约 8~17MB/s 节奏
	}

	// 头部：moov 前置（下载/回放工具录的怪封装片几乎都是这种）
	if s.readMoovRegion(f, buf, 0, prewarmMax, fi.Size(), yield) {
		return
	}
	// 尾部：moov 后置（正常录制的片）
	s.readMoovRegion(f, buf, fi.Size()-prewarmMax, prewarmMax, fi.Size(), yield)
}

// readMoovRegion 顺序读 [off, off+length) 区域（512KB 一块），把碎片化 moov 的
// 随机冷读变成顺序读：机械盘上顺序流 ~80MB/s，随机逐块读要 5~15 秒。
// yield 在每块之间调用（播放中等待让路 + 块间限速）。
// 返回该区域是否完整覆盖 moov（未覆盖则由调用方读另一区域兜底）。
func (s *Server) readMoovRegion(f *os.File, buf []byte, off, length, fileSize int64, yield func()) bool {
	if off < 0 {
		off = 0
	}
	if off+length > fileSize {
		length = fileSize - off
	}
	if length <= 0 {
		return false
	}
	var moovOff, moovSize int64 = -1, 0
	for pos := off; pos < off+length; pos += prewarmChunk {
		if yield != nil {
			yield()
		}
		n := int64(prewarmChunk)
		if off+length-pos < n {
			n = off + length - pos
		}
		if _, err := f.ReadAt(buf[:n], pos); err != nil && err != io.EOF {
			return false
		}
		if o, sz := moovInChunk(buf[:n], pos, fileSize); o >= 0 {
			moovOff, moovSize = o, sz
		}
	}
	// 该区域已覆盖 moov 全程才算成功（否则再去读尾部）
	return moovOff >= 0 && moovOff+moovSize <= off+length
}

// prewarmNext 补充并行路数（prewarmConcurrent 上限，FIFO 顺序）
func (s *Server) prewarmNext() {
	if s.hls.Active() {
		// 有转码在跑：清空积压，播放优先
		s.pw.mu.Lock()
		s.pw.pending = nil
		s.pw.mu.Unlock()
		return
	}
	s.pw.mu.Lock()
	if s.directPlaying() {
		s.pw.mu.Unlock()
		// 直链播放中：不启动新预热（积压保留），播放结束自动恢复。
		// prewarmNext 唯一调用方是 prewarmFile 的收尾 defer，若此刻无
		// 正在预热的文件，播放结束后需要有人重新唤醒队列。
		time.AfterFunc(10*time.Second, s.prewarmNext)
		return
	}
	for s.pw.busy < prewarmConcurrent && len(s.pw.pending) > 0 {
		req := s.pw.pending[0]
		s.pw.pending = s.pw.pending[1:]
		s.pw.busy++
		s.pw.warmed[req.key] = time.Now()
		go s.prewarmFile(req.key, req.abs)
	}
	s.pw.mu.Unlock()
}

// moovInChunk 在 chunk 中找顶层 moov box；返回绝对偏移与大小，未找到返回 -1。
func moovInChunk(chunk []byte, base, fileSize int64) (int64, int64) {
	i := 0
	for {
		j := bytes.Index(chunk[i:], []byte("moov"))
		if j < 0 {
			return -1, 0
		}
		idx := i + j
		if idx >= 4 {
			boxStart := base + int64(idx) - 4 // size 字段在 4CC 前 4 字节
			sz := int64(binary.BigEndian.Uint32(chunk[idx-4 : idx]))
			if sz >= 8 && boxStart+sz <= fileSize {
				return boxStart, sz
			}
		}
		i = idx + 4
	}
}
