//go:build !windows

// Package platform 提供跨平台系统能力（控制台编码、打开浏览器）
package platform

import "os/exec"

// SetConsoleUTF8 非 Windows 平台无操作
func SetConsoleUTF8() {}

// OpenBrowser 使用系统默认浏览器打开 URL
func OpenBrowser(url string) {
	exec.Command("xdg-open", url).Start()
}
