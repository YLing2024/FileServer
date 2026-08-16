//go:build windows

// Package platform 提供跨平台系统能力（控制台编码、打开浏览器）
package platform

import (
	"os/exec"
	"runtime"
	"sync"
	"syscall"
	"unsafe"
)

// SetConsoleUTF8 将 Windows 控制台输出/输入代码页切换为 UTF-8，保证中文正常显示；
// 并启用 VT（虚拟终端）处理，使 ANSI 颜色/反显转义生效（用于彩色二维码）。
func SetConsoleUTF8() {
	if runtime.GOOS != "windows" {
		return
	}
	k32 := syscall.NewLazyDLL("kernel32.dll")
	setOut := k32.NewProc("SetConsoleOutputCP")
	setOut.Call(65001)
	setIn := k32.NewProc("SetConsoleCP")
	setIn.Call(65001)

	// 启用 ENABLE_VIRTUAL_TERMINAL_PROCESSING (0x0004)，
	// 使 stdout 支持 ANSI 转义序列（Windows 10+ 支持）。
	getMode := k32.NewProc("GetConsoleMode")
	setMode := k32.NewProc("SetConsoleMode")
	getStdHandle := k32.NewProc("GetStdHandle")
	// STD_OUTPUT_HANDLE = -11
	handle, _, _ := getStdHandle.Call(uintptr(0xFFFFFFF5))
	var mode uint32
	if r, _, _ := getMode.Call(handle, uintptr(unsafe.Pointer(&mode))); r != 0 {
		setMode.Call(handle, uintptr(mode|0x0004))
	}
}

// ---- 子进程随父进程终止（Job Object）----
//
// 用户关闭控制台窗口（点 X）时 Windows 直接杀死 FileServer 主进程，
// 信号处理函数来不及执行，ffmpeg 转码子进程会变成孤儿继续空转烧 CPU。
// 把子进程挂到设置了 JOB_OBJECT_LIMIT_KILL_ON_JOB_CLOSE 的 Job Object：
// 主进程退出（无论正常/被杀/崩溃）时系统关闭 Job 句柄，所有子进程一并终止。
//
// 关键：Job 句柄必须【不可继承】！os/exec 的子进程默认继承所有可继承句柄，
// 若 ffmpeg 继承了 Job 句柄，主进程被杀后它仍持有句柄，
// KILL_ON_JOB_CLOSE 永不触发——这正是「强杀服务后 ffmpeg 变孤儿占盘」的根因。

const jobObjectExtendedLimitInformation = 9
const jobObjectLimitKillOnJobClose = 0x2000

type securityAttributes struct {
	nLength              uint32
	lpSecurityDescriptor uintptr
	bInheritHandle       uint32
}

type jobIoCounters struct {
	Read, Write, Other         uint64
	ReadOps, WriteOps, OtherOps uint64
}

type jobBasicLimitInfo struct {
	PerProcessUserTimeLimit int64
	PerJobUserTimeLimit     int64
	LimitFlags              uint32
	MinWorkingSetSize       uintptr
	MaxWorkingSetSize       uintptr
	ActiveProcessLimit      uint32
	Affinity                uintptr
	PriorityClass           uint32
	SchedulingClass         uint32
}

type jobExtendedLimitInfo struct {
	Basic              jobBasicLimitInfo
	Io                 jobIoCounters
	ProcessMemoryLimit uintptr
	JobMemoryLimit     uintptr
	PeakProcessMemory  uintptr
	PeakJobMemory      uintptr
}

var (
	jobOnce sync.Once
	jobH    uintptr // 全局 Job Object 句柄：进程存续期间永不关闭
)

// KillOnParentExit 将 cmd 的进程挂入「随父进程终止」的 Job Object。
// 必须在 cmd.Start() 之后调用（此时 cmd.Process 已就绪）。
func KillOnParentExit(cmd *exec.Cmd) {
	if runtime.GOOS != "windows" || cmd == nil || cmd.Process == nil {
		return
	}
	jobOnce.Do(func() {
		k32 := syscall.NewLazyDLL("kernel32.dll")
		createJob := k32.NewProc("CreateJobObjectW")
		// bInheritHandle=0：句柄不可继承（见上方注释，防子进程持有导致失效）
		sa := securityAttributes{nLength: uint32(unsafe.Sizeof(securityAttributes{})), bInheritHandle: 0}
		h, _, _ := createJob.Call(uintptr(unsafe.Pointer(&sa)), 0)
		if h == 0 {
			return
		}
		info := jobExtendedLimitInfo{}
		info.Basic.LimitFlags = jobObjectLimitKillOnJobClose
		setInfo := k32.NewProc("SetInformationJobObject")
		if r, _, _ := setInfo.Call(h, jobObjectExtendedLimitInformation,
			uintptr(unsafe.Pointer(&info)), uintptr(unsafe.Sizeof(info))); r == 0 {
			return // 设置失败：不挂载（宁可留句柄也不带错误语义）
		}
		jobH = h
	})
	if jobH == 0 {
		return
	}
	assign := syscall.NewLazyDLL("kernel32.dll").NewProc("AssignProcessToJobObject")
	// 挂载失败（个别系统策略）时子进程退化为普通进程：功能不受影响，
	// 仅失去「随父进程终止」保护（孤儿清理由 HLS 会话的强杀兜底覆盖）
	assign.Call(jobH, uintptr(cmd.Process.Pid))
}

// OpenBrowser 使用系统默认浏览器打开 URL
func OpenBrowser(url string) {
	exec.Command("rundll32", "url.dll,FileProtocolHandler", url).Start()
}
