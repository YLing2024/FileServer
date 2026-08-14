//go:build windows

package main

import (
	"os/exec"
	"runtime"
	"syscall"
)

// setConsoleUTF8 将 Windows 控制台输出/输入代码页切换为 UTF-8，保证中文正常显示
func setConsoleUTF8() {
	if runtime.GOOS != "windows" {
		return
	}
	k32 := syscall.NewLazyDLL("kernel32.dll")
	setOut := k32.NewProc("SetConsoleOutputCP")
	setOut.Call(65001)
	setIn := k32.NewProc("SetConsoleCP")
	setIn.Call(65001)
}

// openBrowser 使用系统默认浏览器打开 URL
func openBrowser(url string) {
	exec.Command("rundll32", "url.dll,FileProtocolHandler", url).Start()
}
