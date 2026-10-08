package platform

import (
	"os/exec"
	"testing"
)

// TestHelpersDoNotPanic 覆盖平台的 best-effort 辅助函数：
// 非 Windows 上 SetConsoleUTF8/SetLowPriority/KillOnParentExit 均为安全 no-op；
// Windows 上 SetLowPriority 只设置 SysProcAttr（不启动进程）。
func TestHelpersDoNotPanic(t *testing.T) {
	SetConsoleUTF8()
	cmd := exec.Command("go", "version")
	SetLowPriority(cmd)
	KillOnParentExit(cmd) // cmd.Process == nil 时必须安全返回
}
