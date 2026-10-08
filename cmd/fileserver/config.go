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
	Port    int    // FILESERVER_PORT
	Dir     string // FILESERVER_DIR
	Browser bool
	Hidden  bool   // FILESERVER_HIDDEN
	FFmpeg  bool   // FILESERVER_FFMPEG
	Auth    string // FILESERVER_AUTH
	Verbose bool
	NoQR    bool
	Version bool
}

// parseConfig 解析命令行与环境变量。使用独立 FlagSet，--help/--version 文案统一在此。
func parseConfig(args []string) *Config {
	cfg := &Config{}
	fs := flag.NewFlagSet("FileServer", flag.ExitOnError)
	fs.IntVar(&cfg.Port, "port", envInt("FILESERVER_PORT", 0),
		"监听端口（默认 8080，被占用自动递增；环境变量 FILESERVER_PORT）")
	fs.StringVar(&cfg.Dir, "dir", os.Getenv("FILESERVER_DIR"),
		"服务目录（默认 exe 所在目录；环境变量 FILESERVER_DIR）")
	fs.BoolVar(&cfg.Browser, "browser", false,
		"启动后自动打开默认浏览器（默认关闭）")
	fs.BoolVar(&cfg.Hidden, "hidden", envBool("FILESERVER_HIDDEN"),
		"显示隐藏文件（点开头；环境变量 FILESERVER_HIDDEN）")
	fs.BoolVar(&cfg.FFmpeg, "ffmpeg", envBool("FILESERVER_FFMPEG"),
		"开启冷门格式（MKV/RMVB/HEVC 等）在线转码播放与服务端缩略图（需 exe 旁 ffmpeg；有性能代价；环境变量 FILESERVER_FFMPEG）")
	fs.StringVar(&cfg.Auth, "auth", os.Getenv("FILESERVER_AUTH"),
		"可选访问口令 user:pass（环境变量 FILESERVER_AUTH）")
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
