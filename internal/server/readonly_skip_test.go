package server

import (
	"runtime"
	"testing"
)

// skipIfReadOnlyDirUnsupported 跳过“只读目录阻止写入”这类用例。
//
// Windows 的目录权限位不表达写权限：把目录 chmod 成 0o555 只会给它加上
// FILE_ATTRIBUTE_READONLY，仍然可以在其中创建文件，因此这些断言在 Windows
// 上不成立。Linux/macOS 上照常执行。
func skipIfReadOnlyDirUnsupported(t *testing.T) {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("Windows 目录权限位不阻止创建子项")
	}
}
