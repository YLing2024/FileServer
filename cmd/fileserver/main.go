// FileServer 局域网文件服务器入口
//
// 双击 exe 即用：自动检测局域网 IP 并打印访问地址，自动打开浏览器。
// 局域网内任意设备可只读浏览/预览/下载当前目录（或 --dir 指定目录）的文件。
package main

import (
	"errors"
	"flag"
	"fmt"
	"log"
	"net"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"

	"fileserver/internal/platform"
	"fileserver/internal/qrcode"
	"fileserver/internal/server"
)

func main() {
	port := flag.Int("port", 0, "监听端口（默认 8080，被占用自动递增）")
	dir := flag.String("dir", "", "服务目录（默认 exe 所在目录）")
	noBrowser := flag.Bool("no-browser", false, "不自动打开浏览器")
	hidden := flag.Bool("hidden", false, "显示隐藏文件（点开头）")
	auth := flag.String("auth", "", "可选访问口令 user:pass")
	verbose := flag.Bool("v", false, "详细访问日志")
	noQR := flag.Bool("no-qr", false, "不在终端显示地址二维码")
	flag.Parse()

	platform.SetConsoleUTF8()

	// 服务根目录：默认 exe 所在目录（双击场景）
	root := *dir
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

	// 监听端口（占用自动递增）
	startPort := *port
	if startPort <= 0 {
		startPort = 8080
	}
	ln, actualPort, err := listenLoop(startPort)
	if err != nil {
		log.Fatalf("无法监听端口: %v", err)
	}

	srv := server.New(rootAbs, server.Options{Hidden: *hidden, Auth: *auth, Verbose: *verbose})
	handler := srv.Handler()

	// ---- 控制台输出 ----
	fmt.Println("┌──────────────────────────────────────────────┐")
	fmt.Println("│  文件服务器 FileServer                         │")
	fmt.Println("└──────────────────────────────────────────────┘")
	fmt.Printf("服务目录: %s\n", rootAbs)
	fmt.Printf("本机访问: http://127.0.0.1:%d\n", actualPort)
	ips := server.LanIPs()
	if len(ips) == 0 {
		fmt.Println("局域网访问: 未检测到局域网 IP（请检查网络连接）")
	} else {
		fmt.Println("局域网访问:")
		for _, ip := range ips {
			fmt.Printf("  →  http://%s:%d\n", ip, actualPort)
		}
	}
	fmt.Println("------------------------------------------------------------------")
	fmt.Println("  手机/其他电脑连接同一 WiFi 后，在浏览器打开上面的局域网地址即可")
	fmt.Println("  首次运行如弹出 Windows 防火墙提示，请勾选“专用网络”并允许")
	fmt.Println("  按 Ctrl+C 或关闭本窗口即可停止服务")
	fmt.Println("------------------------------------------------------------------")

	// ---- 终端二维码 ----
	if !*noQR {
		printQRs(actualPort, ips)
	}

	if !*noBrowser {
		go platform.OpenBrowser(fmt.Sprintf("http://127.0.0.1:%d", actualPort))
	}

	log.Printf("服务已启动: %s (port %d)", rootAbs, actualPort)

	httpSrv := &http.Server{Handler: handler}
	// Ctrl+C / 关闭控制台窗口：优雅退出，终止 HLS 转码子进程（避免遗留 ffmpeg 空转）
	go func() {
		sig := make(chan os.Signal, 1)
		signal.Notify(sig, os.Interrupt, syscall.SIGTERM)
		<-sig
		log.Println("正在停止服务…")
		httpSrv.Close()
		srv.Close()
		ln.Close()
	}()

	if err := httpSrv.Serve(ln); err != nil && !errors.Is(err, http.ErrServerClosed) {
		log.Fatal(err)
	}
}

// listenLoop 从 startPort 开始尝试监听，被占用则递增
func listenLoop(startPort int) (net.Listener, int, error) {
	for i := 0; i < 20; i++ {
		ln, err := net.Listen("tcp", fmt.Sprintf(":%d", startPort+i))
		if err == nil {
			return ln, startPort + i, nil
		}
	}
	return nil, 0, fmt.Errorf("%d-%d 端口均被占用", startPort, startPort+19)
}

// printQRs 为每个访问地址在终端渲染二维码（本机 + 所有局域网地址）。
// 使用 Unicode 半块字符，手机扫屏即可打开对应地址。
func printQRs(port int, ips []string) {
	type addr struct {
		label string
		url   string
	}
	var list []addr
	list = append(list, addr{"本机", fmt.Sprintf("http://127.0.0.1:%d", port)})
	for _, ip := range ips {
		list = append(list, addr{"局域网 " + ip, fmt.Sprintf("http://%s:%d", ip, port)})
	}

	fmt.Println()
	fmt.Println("  ── 地址二维码（手机相机扫一扫即可打开） ──")
	for _, a := range list {
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
