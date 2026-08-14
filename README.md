# FileServer —— 局域网文件服务器（单 exe，双击即用）

一个轻量的 Windows 局域网文件服务器：双击 `FileServer.exe`，控制台自动显示局域网地址并打开浏览器，同一 WiFi 下的手机/电脑访问该地址即可**只读**浏览、预览、下载 exe 所在文件夹的全部文件。

## 特性

- 🚀 **单文件**：约 10MB 的单 exe，零安装、零依赖、双击即用
- 🌐 **自动出地址**：启动自动检测局域网 IP 并打印访问地址，自动打开浏览器
- 🖼️ **图片缩略图**：jpg/png/gif/webp/bmp/tiff/svg 服务端生成缩略图（带缓存）
- 🎬 **视频首帧缩略图**：浏览器端抽帧，零额外体积；放入 ffmpeg 后自动升级为服务端全格式抽帧
- 🎞️ **自绘播放器**：进度拖动、倍速、音量、全屏、快捷键
- 🖼️ **相册级灯箱**：缩放/旋转/键盘/触屏切换、预加载
- 📄 **在线预览**：视频 / 音频 / PDF / 文本 / 代码
- 📦 **目录打包下载**：任意文件夹一键 zip
- 🔍 **文件搜索**：递归搜索当前目录
- 🎨 **现代界面**：暗色为主（可切亮色），网格/列表视图，手机响应式
- 🛡️ **只读 + 路径安全**：无任何写接口，防目录穿越、防符号链接逃逸；可选口令保护

## 快速开始

1. 解压后将 `FileServer.exe` 放到想共享的文件夹（也可以放任意位置后用 `--dir` 指定）
2. 双击运行
3. 首次运行如弹出 **Windows 防火墙提示，请勾选"专用网络"并允许**（局域网访问必需）
4. 手机/其他电脑连接同一 WiFi，在浏览器打开控制台显示的 `http://192.168.x.x:8080` 地址

停止服务：关闭控制台窗口，或按 `Ctrl+C`。

## 命令行参数

| 参数 | 说明 |
|---|---|
| `--port 9000` | 指定端口（默认 8080，被占用自动递增） |
| `--dir D:\共享` | 指定服务目录（默认 exe 所在目录） |
| `--no-browser` | 不自动打开浏览器 |
| `--hidden` | 显示隐藏文件（点开头） |
| `--auth user:pass` | 启用简单访问口令（Basic Auth） |
| `-v` | 显示访问日志 |

## 视频缩略图：可选 ffmpeg 增强

默认视频缩略图由**浏览器端**生成（零体积）。若需要 hevc/mkv/avi 等全格式覆盖，把 `ffmpeg.exe` 和 `ffprobe.exe` 放入以下任一位置（程序启动时自动检测）：

1. exe 同目录的 `ffmpeg\` 子文件夹
2. `%LOCALAPPDATA%\FileServer\ffmpeg\`（推荐：不污染共享目录）

```
# 方式一：exe 同目录
你的文件夹\
├── FileServer.exe
└── ffmpeg\
    ├── ffmpeg.exe
    └── ffprobe.exe

# 方式二：系统用户目录（推荐）
%LOCALAPPDATA%\FileServer\ffmpeg\
    ├── ffmpeg.exe
    └── ffprobe.exe
```

启用后视频缩略图由服务端生成，支持所有常见格式。
ffmpeg 官方构建下载：<https://www.gyan.dev/ffmpeg/builds/>（选 release-essentials）

## 从源码构建

需要 Windows + 网络（首次自动下载 Go 工具链到 `.tools\`，免安装）：

```
build.bat
```

产物在 `dist\FileServer.exe`。手动构建：

```
.tools\go\bin\go build -trimpath -ldflags "-s -w" -o dist\FileServer.exe .
```

## 测试

```
# 后端单元/集成测试
.tools\go\bin\go test ./...

# 前端冒烟测试（桌面端 10 项、移动端 4 项、bug 回归 2 项）
# 需要 Python + playwright，且先启动服务：dist\FileServer.exe --dir .\testdata --port 8099 --no-browser
python scripts\smoke_test.py
python scripts\mobile_test.py
python scripts\regression_test.py
```

## 项目结构

```
web\          前端（HTML/CSS/JS，零依赖，嵌入 exe）
*.go          后端（Go 标准库 + golang.org/x/image）
fetch-tools.ps1   下载 Go 工具链
build.bat     一键构建
```

## 常见问题

**Q: 手机打不开？** 确认手机与电脑在同一网络；确认防火墙已允许（首次运行弹窗勾选"专用网络"）；部分路由器开启"AP 隔离"会阻止设备互访，需在路由器设置中关闭。

**Q: 端口被占用？** 程序会自动尝试 8080 之后的端口，控制台会显示实际地址。

**Q: 杀毒软件报警？** Go 编译的静态程序误报率极低；若个别杀软误报，添加信任即可。

**Q: 想换个端口/目录？** 创建快捷方式，在目标后加参数，如：`"D:\FileServer.exe" --port 9000 --dir "D:\共享"`。

**Q: hevc/mkv 视频没有缩略图？** 浏览器不支持解码这些格式，无法前端抽帧；放入 ffmpeg 组件后即可（见上文）。
