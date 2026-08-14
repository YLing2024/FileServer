//go:build !windows

package main

import (
	"os/exec"
)

func setConsoleUTF8() {}

func openBrowser(url string) {
	exec.Command("xdg-open", url).Start()
}
