//go:build windows

// Package platform 提供跨平台系统能力（控制台编码、打开浏览器）
package platform

import (
	"os/exec"
	"runtime"
	"syscall"
)

// SetConsoleUTF8 将 Windows 控制台输出/输入代码页切换为 UTF-8，保证中文正常显示
func SetConsoleUTF8() {
	if runtime.GOOS != "windows" {
		return
	}
	k32 := syscall.NewLazyDLL("kernel32.dll")
	setOut := k32.NewProc("SetConsoleOutputCP")
	setOut.Call(65001)
	setIn := k32.NewProc("SetConsoleCP")
	setIn.Call(65001)
}

// OpenBrowser 使用系统默认浏览器打开 URL
func OpenBrowser(url string) {
	exec.Command("rundll32", "url.dll,FileProtocolHandler", url).Start()
}
