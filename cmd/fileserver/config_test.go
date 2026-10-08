package main

import "testing"

func clearEnv(t *testing.T) {
	t.Helper()
	for _, k := range []string{
		"FILESERVER_PORT", "FILESERVER_DIR", "FILESERVER_AUTH",
		"FILESERVER_FFMPEG", "FILESERVER_HIDDEN",
	} {
		t.Setenv(k, "")
	}
}

// TestParseConfigDefaults 断言无参、无环境变量时默认行为与既有版本一致。
func TestParseConfigDefaults(t *testing.T) {
	clearEnv(t)
	cfg := parseConfig(nil)
	if cfg.Port != 0 || cfg.Dir != "" || cfg.Auth != "" ||
		cfg.FFmpeg || cfg.Hidden || cfg.Browser || cfg.Verbose || cfg.NoQR || cfg.Version {
		t.Fatalf("默认配置不符: %+v", cfg)
	}
}

// TestParseConfigEnvOverrides 断言环境变量作为默认值生效。
func TestParseConfigEnvOverrides(t *testing.T) {
	t.Setenv("FILESERVER_PORT", "9001")
	t.Setenv("FILESERVER_DIR", "/srv/data")
	t.Setenv("FILESERVER_AUTH", "user:pass")
	t.Setenv("FILESERVER_FFMPEG", "true")
	t.Setenv("FILESERVER_HIDDEN", "1")

	cfg := parseConfig(nil)
	if cfg.Port != 9001 || cfg.Dir != "/srv/data" || cfg.Auth != "user:pass" ||
		!cfg.FFmpeg || !cfg.Hidden {
		t.Fatalf("环境变量未生效: %+v", cfg)
	}
}

// TestParseConfigFlagBeatsEnv 断言命令行显式传参优先于环境变量。
func TestParseConfigFlagBeatsEnv(t *testing.T) {
	t.Setenv("FILESERVER_PORT", "9001")
	t.Setenv("FILESERVER_AUTH", "env:pass")

	cfg := parseConfig([]string{"-port", "9002", "-auth", "cli:pass", "-v", "-no-qr", "-version"})
	if cfg.Port != 9002 || cfg.Auth != "cli:pass" || !cfg.Verbose || !cfg.NoQR || !cfg.Version {
		t.Fatalf("命令行应覆盖环境变量: %+v", cfg)
	}
}
