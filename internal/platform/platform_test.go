package platform

import (
	"os/exec"
	"runtime"
	"testing"
	"time"
)

// TestHelpersDoNotPanic 覆盖平台的 best-effort 辅助函数：
// 非 Windows 上 SetConsoleUTF8/SetLowPriority/KillOnParentExit 均为安全 no-op；
// Windows 上 SetLowPriority 只设置 SysProcAttr（不启动进程）。
func TestHelpersDoNotPanic(t *testing.T) {
	SetConsoleUTF8()
	cmd := exec.Command("go", "version")
	SetLowPriority(cmd)
	KillOnParentExit(cmd) // cmd.Process == nil 时必须安全返回
	SetProcessGroup(cmd)  // 未 Start 时只设置属性，不得 panic
	TerminateTree(cmd)    // cmd.Process == nil 时必须安全返回
}

// TestTerminateProcessGroupPOSIX 断言 POSIX 上进程组终止函数真能带走
// 独立进程组里的子进程（Windows 由 Job Object 覆盖，跳过）。
func TestTerminateProcessGroupPOSIX(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX 进程组语义")
	}
	cmd := exec.Command("sleep", "300")
	SetProcessGroup(cmd)
	if err := cmd.Start(); err != nil {
		t.Fatalf("启动测试进程失败: %v", err)
	}
	TerminateTree(cmd)
	done := make(chan struct{})
	go func() { _ = cmd.Wait(); close(done) }()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		_ = cmd.Process.Kill()
		t.Fatal("进程组未被终止（仍存活）")
	}
}
