package main

import (
	"flag"
	"os"
	"strconv"
	"strings"
)

// Config 集中所有命令行参数。同名环境变量作为 flag 的默认值，
// 因此优先级为：命令行显式传参 > 环境变量 > 内置默认。
// 内置默认与既有版本完全一致（端口 8080 起自增、口令关、ffmpeg 关、二维码开）。
type Config struct {
	Port         int    // FILESERVER_PORT
	Host         string // FILESERVER_HOST
	Dir          string // FILESERVER_DIR
	Browser      bool
	Hidden       bool   // FILESERVER_HIDDEN
	FFmpeg       bool   // FILESERVER_FFMPEG
	Auth         string // FILESERVER_AUTH（兼容：等价 password 模式预置口令）
	AuthMode     string // FILESERVER_AUTH_MODE（none|totp|password|password+totp）
	RememberDays int    // FILESERVER_REMEMBER_DAYS
	SessionHours int    // FILESERVER_SESSION_HOURS
	TrustProxy   bool   // FILESERVER_TRUST_PROXY
	CookieSecure bool   // FILESERVER_COOKIE_SECURE
	ResetAuth    bool   // --reset-auth
	ResetTOTP    bool   // --reset-totp
	TextMax      int64  // FILESERVER_TEXT_MAX_BYTES
	Verbose      bool
	NoQR         bool
	Version      bool
}

// parseConfig 解析命令行与环境变量。使用独立 FlagSet，--help/--version 文案统一在此。
func parseConfig(args []string) *Config {
	cfg := &Config{}
	fs := flag.NewFlagSet("FileServer", flag.ExitOnError)
	fs.IntVar(&cfg.Port, "port", envInt("FILESERVER_PORT", 0),
		"监听端口（默认 8080，被占用自动递增；环境变量 FILESERVER_PORT）")
	fs.StringVar(&cfg.Host, "host", envString("FILESERVER_HOST", ""),
		"监听地址（默认 0.0.0.0，局域网可访问；设为 127.0.0.1 时仅本机/反代可访问；环境变量 FILESERVER_HOST）")
	fs.StringVar(&cfg.Dir, "dir", os.Getenv("FILESERVER_DIR"),
		"服务目录（默认 exe 所在目录；环境变量 FILESERVER_DIR）")
	fs.BoolVar(&cfg.Browser, "browser", false,
		"启动后自动打开默认浏览器（默认关闭）")
	fs.BoolVar(&cfg.Hidden, "hidden", envBool("FILESERVER_HIDDEN"),
		"显示隐藏文件（点开头；环境变量 FILESERVER_HIDDEN）")
	fs.BoolVar(&cfg.FFmpeg, "ffmpeg", envBool("FILESERVER_FFMPEG"),
		"开启冷门格式（MKV/RMVB/HEVC 等）在线转码播放与服务端缩略图（需 exe 旁 ffmpeg；有性能代价；环境变量 FILESERVER_FFMPEG）")
	fs.StringVar(&cfg.Auth, "auth", os.Getenv("FILESERVER_AUTH"),
		"兼容用法：--auth user:pass，等价于 --auth-mode password + 预置口令（环境变量 FILESERVER_AUTH）")
	fs.StringVar(&cfg.AuthMode, "auth-mode", envString("FILESERVER_AUTH_MODE", ""),
		"认证模式：none|totp|password|password+totp（默认 none=局域网敞开；环境变量 FILESERVER_AUTH_MODE）")
	fs.IntVar(&cfg.RememberDays, "remember-days", envInt("FILESERVER_REMEMBER_DAYS", 0),
		"勾选「记住我」的持久 cookie 天数（默认 30；环境变量 FILESERVER_REMEMBER_DAYS）")
	fs.IntVar(&cfg.SessionHours, "session-hours", envInt("FILESERVER_SESSION_HOURS", 0),
		"不勾「记住我」的会话有效期小时数（默认 12；环境变量 FILESERVER_SESSION_HOURS）")
	fs.BoolVar(&cfg.TrustProxy, "trust-proxy", envBool("FILESERVER_TRUST_PROXY"),
		"信任反向代理的 X-Forwarded-For（取最左为客户端 IP；默认不信任；环境变量 FILESERVER_TRUST_PROXY）")
	fs.BoolVar(&cfg.CookieSecure, "cookie-secure", envBool("FILESERVER_COOKIE_SECURE"),
		"强制认证 cookie 带 Secure（https 下自动开启；环境变量 FILESERVER_COOKIE_SECURE）")
	fs.BoolVar(&cfg.ResetAuth, "reset-auth", false,
		"清除全部认证凭据与会话后退出（服务未启动时使用，用于找回访问权限）")
	fs.BoolVar(&cfg.ResetTOTP, "reset-totp", false,
		"仅清除 TOTP 绑定后退出（服务未启动时使用，用于丢失验证器后的自救）")
	fs.Int64Var(&cfg.TextMax, "text-max-bytes", envInt64("FILESERVER_TEXT_MAX_BYTES", 0),
		"可编辑文本大小上限（字节，默认 2097152=2MiB；环境变量 FILESERVER_TEXT_MAX_BYTES）")
	fs.BoolVar(&cfg.Verbose, "v", false, "详细访问日志")
	fs.BoolVar(&cfg.NoQR, "no-qr", false, "不在终端显示地址二维码")
	fs.BoolVar(&cfg.Version, "version", false, "打印版本信息后退出")
	_ = fs.Parse(args)
	return cfg
}

// envInt 读取整型环境变量，未设置/非法时用默认值。
func envInt(name string, def int) int {
	v := strings.TrimSpace(os.Getenv(name))
	if v == "" {
		return def
	}
	n, err := strconv.Atoi(v)
	if err != nil {
		return def
	}
	return n
}

// envBool 读取布尔环境变量（1/true/yes/on 为真），其余含未设置为假。
func envBool(name string) bool {
	switch strings.ToLower(strings.TrimSpace(os.Getenv(name))) {
	case "1", "true", "yes", "on":
		return true
	default:
		return false
	}
}

// envInt64 读取 64 位整型环境变量，未设置/非法时用默认值。
func envInt64(name string, def int64) int64 {
	v := strings.TrimSpace(os.Getenv(name))
	if v == "" {
		return def
	}
	n, err := strconv.ParseInt(v, 10, 64)
	if err != nil {
		return def
	}
	return n
}

// envString 读取环境变量并去除首尾空白，未设置为空串。
func envString(name, def string) string {
	if v := strings.TrimSpace(os.Getenv(name)); v != "" {
		return v
	}
	return def
}
