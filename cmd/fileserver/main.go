// FileServer 局域网文件服务器入口
//
// 双击 exe 即用：自动检测局域网 IP 并打印访问地址，自动打开浏览器。
// 局域网内任意设备可只读浏览/预览/下载当前目录（或 --dir 指定目录）的文件。
package main

import (
	"errors"
	"fmt"
	"log"
	"log/slog"
	"net"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/YLing2024/FileServer/internal/platform"
	"github.com/YLing2024/FileServer/internal/qrcode"
	"github.com/YLing2024/FileServer/internal/server"
	"github.com/YLing2024/FileServer/internal/version"
)

func main() {
	cfg := parseConfig(os.Args[1:])
	setupLogger(cfg.Verbose)

	platform.SetConsoleUTF8()

	if cfg.Version {
		fmt.Println(version.String())
		return
	}

	// 服务根目录：默认 exe 所在目录（双击场景）
	root := cfg.Dir
	if root == "" {
		exe, err := os.Executable()
		if err != nil {
			log.Fatalf("无法定位程序目录: %v", err)
		}
		root = filepath.Dir(exe)
	}
	rootAbs, err := filepath.Abs(root)
	if err != nil {
		log.Fatalf("无法解析目录: %v", err)
	}
	if fi, err := os.Stat(rootAbs); err != nil || !fi.IsDir() {
		log.Fatalf("服务目录无效: %s", rootAbs)
	}

	// 监听地址（--host 默认 0.0.0.0：局域网扫码即用；127.0.0.1 时仅本机/反代可达）
	host := cfg.Host
	if host == "" {
		host = "0.0.0.0"
	}
	// 监听端口（占用自动递增）
	startPort := cfg.Port
	if startPort <= 0 {
		startPort = 8080
	}
	ln, actualPort, err := listenLoop(host, startPort)
	if err != nil {
		log.Fatalf("无法监听端口: %v", err)
	}

	// 仅通配地址才枚举局域网网卡；绑定具体地址时只公布该地址，
	// 避免打印一堆实际并未监听的地址。
	wildcard := isWildcardHost(host)
	var ips []string
	if wildcard {
		ips = server.LanIPs()
	}

	srv := server.New(rootAbs, server.Options{Hidden: cfg.Hidden, Auth: cfg.Auth, Verbose: cfg.Verbose, FFmpeg: cfg.FFmpeg, TextMaxBytes: cfg.TextMax})
	handler := srv.Handler()

	// ---- 控制台输出 ----
	fmt.Println("┌──────────────────────────────────────────────┐")
	fmt.Println("│  文件服务器 FileServer                         │")
	fmt.Println("└──────────────────────────────────────────────┘")
	fmt.Printf("服务目录: %s\n", rootAbs)
	if wildcard {
		fmt.Printf("本机访问: %s\n", httpURL("127.0.0.1", actualPort))
		if len(ips) == 0 {
			fmt.Println("局域网访问: 未检测到局域网 IP（请检查网络连接）")
		} else {
			fmt.Println("局域网访问:")
			for _, ip := range ips {
				fmt.Printf("  →  %s\n", httpURL(ip, actualPort))
			}
		}
	} else {
		fmt.Printf("监听地址: %s\n", host)
		fmt.Printf("访问地址: %s\n", httpURL(host, actualPort))
	}
	fmt.Println("------------------------------------------------------------------")
	fmt.Println("  手机/其他电脑连接同一 WiFi 后，在浏览器打开上面的局域网地址即可")
	if runtime.GOOS == "windows" {
		fmt.Println("  首次运行如弹出 Windows 防火墙提示，请勾选“专用网络”并允许")
	} else if runtime.GOOS == "darwin" {
		fmt.Println("  首次运行如弹出“允许传入连接”防火墙提示，请点击允许")
	}
	fmt.Println("  按 Ctrl+C 或关闭本窗口即可停止服务")
	fmt.Println("------------------------------------------------------------------")

	// ---- 终端二维码 ----
	if !cfg.NoQR {
		qrHost := browserHost(host)
		var addrs []qrAddr
		addrs = append(addrs, qrAddr{"本机", httpURL(qrHost, actualPort)})
		if wildcard {
			for _, ip := range ips {
				addrs = append(addrs, qrAddr{"局域网 " + ip, httpURL(ip, actualPort)})
			}
		}
		printQRs(addrs)
	}

	if cfg.Browser {
		go platform.OpenBrowser(httpURL(browserHost(host), actualPort))
	}

	slog.Info("服务已启动", "dir", rootAbs, "port", actualPort)

	// ReadHeaderTimeout：防慢客户端长期占用连接。
	// IdleTimeout：复用连接空闲过久后回收，避免半开连接堆积。
	// MaxHeaderBytes：限制请求头体积（默认 1MB 已够，这里显式写明）。
	// 明确不设 WriteTimeout/ReadTimeout：读写都是「按需」的，设了会掐断
	// 长视频 / 大文件的响应（整段下载可能持续数十分钟），故保持不限时。
	httpSrv := &http.Server{
		Handler:           handler,
		ReadHeaderTimeout: 10 * time.Second,
		IdleTimeout:       120 * time.Second,
		MaxHeaderBytes:    1 << 20,
	}
	// Ctrl+C / 关闭控制台窗口：优雅退出，终止 HLS 转码子进程（避免遗留 ffmpeg 空转）
	go func() {
		sig := make(chan os.Signal, 1)
		signal.Notify(sig, os.Interrupt, syscall.SIGTERM)
		<-sig
		slog.Info("正在停止服务")
		_ = httpSrv.Close()
		srv.Close()
		_ = ln.Close()
	}()

	if err := httpSrv.Serve(ln); err != nil && !errors.Is(err, http.ErrServerClosed) {
		log.Fatal(err)
	}
}

// setupLogger 按 -v 配置全局 slog 级别：-v=Debug，否则 Info。
// 输出走 TextHandler（人类可读文本，非 JSON），与既有控制台风格一致；
// 未开启 -v 时不打印 Debug 级请求日志，用户可见输出不变。
func setupLogger(verbose bool) {
	level := slog.LevelInfo
	if verbose {
		level = slog.LevelDebug
	}
	slog.SetDefault(slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: level})))
}

// listenLoop 在 host 上从 startPort 开始尝试监听，被占用则递增
func listenLoop(host string, startPort int) (net.Listener, int, error) {
	for i := 0; i < 20; i++ {
		ln, err := net.Listen("tcp", net.JoinHostPort(host, strconv.Itoa(startPort+i)))
		if err == nil {
			return ln, startPort + i, nil
		}
	}
	return nil, 0, fmt.Errorf("%d-%d 端口均被占用", startPort, startPort+19)
}

// isWildcardHost 是否绑定到全部网卡（此时才枚举并公布局域网地址）。
func isWildcardHost(host string) bool {
	switch host {
	case "", "0.0.0.0", "::", "[::]":
		return true
	}
	return false
}

// browserHost 供浏览器/二维码使用的可达主机：通配地址回落到回环。
func browserHost(host string) string {
	if isWildcardHost(host) {
		return "127.0.0.1"
	}
	return host
}

// httpURL 拼接 http URL；IPv6 字面量交由 net.JoinHostPort 加方括号。
func httpURL(host string, port int) string {
	return "http://" + net.JoinHostPort(host, strconv.Itoa(port))
}

// qrAddr 控制台二维码的一个地址条目。
type qrAddr struct {
	label string
	url   string
}

// printQRs 为每个访问地址在终端渲染二维码。
// 使用 Unicode 半块字符，手机扫屏即可打开对应地址。
func printQRs(addrs []qrAddr) {
	fmt.Println()
	fmt.Println("  ── 地址二维码（手机相机扫一扫即可打开） ──")
	for _, a := range addrs {
		m, err := qrcode.Encode([]byte(a.url), qrcode.Medium)
		if err != nil {
			fmt.Printf("  [%s] 二维码生成失败: %v\n", a.label, err)
			continue
		}
		art := qrcode.Render(m, 2)
		fmt.Printf("  · %s\n", a.label)
		// 每行加两个空格缩进，与标题对齐
		for _, line := range strings.Split(art, "\n") {
			fmt.Printf("  %s\n", line)
		}
		fmt.Printf("  %s\n\n", a.url)
	}
}
