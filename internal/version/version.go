// Package version 持有构建期注入的版本信息。
//
// 发布构建通过 -ldflags -X 注入以下变量（见 Makefile `dist` 目标与 release.ps1）：
//
//	-X github.com/YLing2024/FileServer/internal/version.Version=<版本>
//	-X github.com/YLing2024/FileServer/internal/version.Commit=<git 短哈希>
//	-X github.com/YLing2024/FileServer/internal/version.BuildDate=<构建时间>
//
// 未注入时保持 "dev" / 空，便于开发构建自我识别。
package version

import (
	"fmt"
	"runtime"
)

var (
	// Version 版本号（构建期注入，默认 dev）。
	Version = "dev"
	// Commit git 提交哈希（构建期注入，默认空）。
	Commit = ""
	// BuildDate 构建时间（构建期注入，默认空）。
	BuildDate = ""
)

// GoVersion 返回编译该二进制所用的 Go 版本，如 "go1.25.0"。
func GoVersion() string { return runtime.Version() }

// Platform 返回 GOOS/GOARCH，如 "windows/amd64"。
func Platform() string { return runtime.GOOS + "/" + runtime.GOARCH }

// String 返回人类可读的多行版本信息（供 --version 控制台输出）。
func String() string {
	commit := Commit
	if commit == "" {
		commit = "(unknown)"
	}
	date := BuildDate
	if date == "" {
		date = "(unknown)"
	}
	return fmt.Sprintf("FileServer %s\ncommit:     %s\nbuilt:      %s\ngo:         %s\nplatform:   %s",
		Version, commit, date, GoVersion(), Platform())
}
