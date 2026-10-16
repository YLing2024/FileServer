package server

import (
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"testing"
	"time"

	"github.com/YLing2024/FileServer/internal/platform"
)

// TestProcTrackerReapOrphanPOSIX 断言启动时能清理上次遗留的 ffmpeg 孤儿进程：
// 把 sleep 二进制复制成名为 ffmpeg 的可执行文件（comm 即 ffmpeg，用于通过
// 进程名安全校验），登记其进程组后用新跟踪器 reap，进程必须被强杀。
func TestProcTrackerReapOrphanPOSIX(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Windows 由 Job Object 覆盖，不做持久化跟踪")
	}
	sleepBin, err := exec.LookPath("sleep")
	if err != nil {
		t.Skip("无 sleep 命令")
	}
	dir := t.TempDir()
	fake := filepath.Join(dir, "ffmpeg")
	if err := copyExecutable(sleepBin, fake); err != nil {
		t.Fatalf("复制测试进程失败: %v", err)
	}

	cmd := exec.Command(fake, "300")
	platform.SetProcessGroup(cmd)
	if err := cmd.Start(); err != nil {
		t.Fatalf("启动测试进程失败: %v", err)
	}
	// 确保清理（reap 失败时测试进程不被泄漏）
	t.Cleanup(func() {
		_ = cmd.Process.Kill()
		_, _ = cmd.Process.Wait()
	})

	first := newProcTracker(dir)
	first.register(cmd.Process.Pid)

	second := newProcTracker(dir) // 模拟下次启动：载入遗留记录
	second.reap()

	done := make(chan struct{})
	go func() { _ = cmd.Wait(); close(done) }()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("reap 未能清理遗留的孤儿进程")
	}
}

// copyExecutable 复制可执行文件并保留可执行位（测试辅助）。
func copyExecutable(src, dst string) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer func() { _ = in.Close() }()
	out, err := os.OpenFile(dst, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o755)
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, in); err != nil {
		_ = out.Close()
		return err
	}
	return out.Close()
}
