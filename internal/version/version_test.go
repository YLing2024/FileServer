package version

import (
	"strings"
	"testing"
)

func TestStringContainsInjectedValues(t *testing.T) {
	oldV, oldC, oldD := Version, Commit, BuildDate
	defer func() { Version, Commit, BuildDate = oldV, oldC, oldD }()

	Version = "9.9.9-test"
	Commit = "abc1234"
	BuildDate = "2026-10-08T00:00:00Z"

	s := String()
	for _, want := range []string{"9.9.9-test", "abc1234", "2026-10-08T00:00:00Z", GoVersion(), Platform()} {
		if !strings.Contains(s, want) {
			t.Errorf("String() 缺少 %q:\n%s", want, s)
		}
	}
}

func TestStringPlaceholdersWhenUninjected(t *testing.T) {
	oldV, oldC, oldD := Version, Commit, BuildDate
	defer func() { Version, Commit, BuildDate = oldV, oldC, oldD }()

	Version, Commit, BuildDate = "dev", "", ""
	if s := String(); !strings.Contains(s, "(unknown)") {
		t.Errorf("未注入 commit/build 时应显示占位符:\n%s", s)
	}
}

func TestPlatformAndGoVersion(t *testing.T) {
	if !strings.Contains(Platform(), "/") {
		t.Errorf("Platform() 应为 GOOS/GOARCH 形式, 得到 %q", Platform())
	}
	if GoVersion() == "" {
		t.Error("GoVersion() 不应为空")
	}
}
