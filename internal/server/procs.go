package server

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"sync"

	"github.com/YLing2024/FileServer/internal/platform"
)

// procTracker 记录本服务启动的 ffmpeg/ffprobe 进程组，用于服务异常退出
// （SIGKILL / 崩溃，信号处理来不及跑）后，下次启动时清理遗留的孤儿进程。
//
// Windows 侧不持久化跟踪：子进程由不可继承句柄的 Job Object 保证随父进程终止
// （见 platform.KillOnParentExit），再按 PID 清理反而有 PID 复用误杀风险。
type procTracker struct {
	mu   sync.Mutex
	dir  string // 跟踪文件所在目录（服务缓存根）
	pids map[int]bool
}

// procListPath 进程组跟踪文件路径（位于服务缓存目录内，随缓存一起被忽略）。
func procListPath(baseDir string) string { return filepath.Join(baseDir, "procs.pids") }

// newProcTracker 创建跟踪器；POSIX 上载入上次运行遗留的进程组。
func newProcTracker(baseDir string) *procTracker {
	t := &procTracker{dir: baseDir, pids: make(map[int]bool)}
	if runtime.GOOS == "windows" {
		return t
	}
	t.load()
	return t
}

// load 读取上次运行记录的进程组 ID（找不到文件视为无遗留）。
func (t *procTracker) load() {
	data, err := os.ReadFile(procListPath(t.dir))
	if err != nil {
		return
	}
	for _, line := range strings.Split(string(data), "\n") {
		if pid, err := strconv.Atoi(strings.TrimSpace(line)); err == nil && pid > 0 {
			t.pids[pid] = true
		}
	}
}

// save 原子写跟踪文件（临时文件 + rename），失败静默——跟踪是 best-effort，
// 不影响转码主流程。
func (t *procTracker) save() {
	lines := make([]string, 0, len(t.pids))
	for pid := range t.pids {
		lines = append(lines, strconv.Itoa(pid))
	}
	tmp := procListPath(t.dir) + ".tmp"
	if err := os.WriteFile(tmp, []byte(strings.Join(lines, "\n")), 0o600); err != nil {
		return
	}
	_ = os.Rename(tmp, procListPath(t.dir))
}

// register 记录一个刚启动的进程组（pgid == pid，见 platform.SetProcessGroup）。
func (t *procTracker) register(pid int) {
	if t == nil || pid <= 0 || runtime.GOOS == "windows" {
		return
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	t.pids[pid] = true
	t.save()
}

// unregister 进程正常退出后移除记录。
func (t *procTracker) unregister(pid int) {
	if t == nil || pid <= 0 || runtime.GOOS == "windows" {
		return
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	delete(t.pids, pid)
	t.save()
}

// reap 清理上次运行遗留的孤儿进程组：只强杀确认仍存活且可执行文件为
// ffmpeg/ffprobe 的进程（避免 PID 复用误杀无关进程），随后清空跟踪文件。
func (t *procTracker) reap() {
	if t == nil || runtime.GOOS == "windows" {
		return
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	for pid := range t.pids {
		if isOrphanedFfmpeg(pid) {
			_ = platform.KillProcessGroup(pid)
		}
	}
	t.pids = make(map[int]bool)
	t.save()
}

// isOrphanedFfmpeg 判断 pid 是否为遗留的 ffmpeg/ffprobe 进程：用 ps 读取
// 可执行文件短名（comm）。进程已退出/无法读取时返回 false，不做任何动作。
func isOrphanedFfmpeg(pid int) bool {
	out, err := exec.Command("ps", "-o", "comm=", "-p", strconv.Itoa(pid)).Output()
	if err != nil {
		return false
	}
	comm := strings.ToLower(strings.TrimSpace(string(out)))
	return strings.Contains(comm, "ffmpeg") || strings.Contains(comm, "ffprobe")
}
