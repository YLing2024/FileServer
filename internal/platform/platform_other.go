//go:build !windows

// Package platform 提供跨平台系统能力（控制台编码、打开浏览器、子进程生命周期）。
package platform

import (
	"errors"
	"os"
	"os/exec"
	"runtime"
	"syscall"
)

// errLinkCountUnsupported 表示当前平台无法读取硬链接计数。
var errLinkCountUnsupported = errors.New("link count unsupported")

// SetConsoleUTF8 非 Windows 平台无操作。
func SetConsoleUTF8() {}

// KillOnParentExit 非 Windows 平台无操作：POSIX 没有 Job Object，
// 子进程的「随父进程终止」由进程组 + 启动时遗留清理共同兜底
// （见 SetProcessGroup / KillProcessGroup 与 internal/server 的启动清理）。
func KillOnParentExit(cmd *exec.Cmd) {}

// SetLowPriority 非 Windows 平台无操作（不做进程优先级调整）。
// Windows 生产路径见 platform_windows.go：后台任务以 BELOW_NORMAL 运行，
// 给用户播放链路让路；其余平台交由系统调度器处理。
func SetLowPriority(cmd *exec.Cmd) {}

// SetProcessGroup 让子进程在独立的进程组中运行（POSIX 专有）：
// 组的 pgid 等于子进程 pid，于是可以在终止时用 kill(-pgid) 一次性带走
// ffmpeg 及其可能派生的子进程，等价于 Windows Job Object 的「一同终止」语义。
// 必须在 cmd.Start() 之前调用。
func SetProcessGroup(cmd *exec.Cmd) {
	if cmd == nil {
		return
	}
	if cmd.SysProcAttr == nil {
		cmd.SysProcAttr = &syscall.SysProcAttr{}
	}
	cmd.SysProcAttr.Setpgid = true
}

// KillProcessGroup 向进程组发送 SIGKILL（pid 即组 id）；组不存在时回退只杀该 pid。
func KillProcessGroup(pid int) error {
	if pid <= 0 {
		return nil
	}
	if err := syscall.Kill(-pid, syscall.SIGKILL); err != nil {
		return syscall.Kill(pid, syscall.SIGKILL)
	}
	return nil
}

// TerminateTree 强杀 cmd 及其所在进程组（POSIX 下的 taskkill 等价物）。
func TerminateTree(cmd *exec.Cmd) {
	if cmd == nil || cmd.Process == nil {
		return
	}
	_ = KillProcessGroup(cmd.Process.Pid)
}

// OpenBrowser 使用系统默认浏览器打开 URL。
// macOS 用 open，其余 POSIX 用 xdg-open。
func OpenBrowser(url string) {
	opener := "xdg-open"
	if runtime.GOOS == "darwin" {
		opener = "open"
	}
	_ = exec.Command(opener, url).Start()
}

// LinkCount 返回路径对应 inode 的硬链接计数（POSIX：stat.Nlink）。
// blob 回收据此判断「除索引本身外是否还有用户文件链接」。
func LinkCount(path string) (uint64, error) {
	fi, err := os.Stat(path)
	if err != nil {
		return 0, err
	}
	st, ok := fi.Sys().(*syscall.Stat_t)
	if !ok {
		return 0, errLinkCountUnsupported
	}
	return uint64(st.Nlink), nil
}
