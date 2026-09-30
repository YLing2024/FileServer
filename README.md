# FileServer

Windows 局域网文件服务器：双击单个 exe，同一 WiFi 下的手机与电脑即可只读浏览、预览、下载指定目录。

## 它能做什么

- **单文件运行**：编译为一个约 8MB 的 exe，双击启动，无需安装运行时；发布包可选捆绑 ffmpeg 组件
- **自动给出地址**：启动时探测本机局域网 IP，在控制台打印每个访问地址（`--browser` 同时打开浏览器）
- **终端二维码**：为每个地址渲染二维码，手机扫码直达（`--no-qr` 关闭）
- **视频在线播放**：原生格式直链流式播放；开启冷门格式支持后，MKV/RMVB/AVI/WMV/HEVC 等实时转 H.264，边转边播
- **怪封装 MP4 处理**：识别 mdat 碎片化 / moov 过大的 MP4，可手动规整化，之后稳定秒开
- **视频缩略图**：常规 MP4/WebM 由浏览器抽帧，服务端零成本；冷门格式在开启支持后由服务端抽帧
- **自绘播放器**：进度拖动、倍速、音量、全屏，快捷键空格 / 方向键 / M / F
- **图片灯箱与在线预览**：灯箱支持缩放、旋转、键盘与触屏；可预览视频、音频、PDF、文本、代码
- **目录打包下载**：任意文件夹流式 zip；**递归搜索**：带深度与数量上限
- **浏览即读、缓存隔离**：只浏览、预览、下载，不写用户文件（唯一写入是规整化，就地改写并留备份）；所有缓存位于服务目录下的隐藏文件夹 `.FileServer\`
- **路径安全**：防目录穿越与符号链接逃逸；可选访问口令

## 快速开始

1. 解压，把 `FileServer.exe` 放到要共享的文件夹（也可放别处，用 `--dir` 指定目录），双击运行
2. 首次运行若弹出 Windows 防火墙提示，勾选“专用网络”并允许
3. 手机或电脑连接同一 WiFi，在浏览器打开控制台打印的地址，形如 `http://<局域网地址>:8080`

默认不启用口令，同一局域网内任何设备都可打开；需要限制访问时用 `--auth user:pass`。

停止服务：关闭控制台窗口，或按 `Ctrl+C`（优雅退出并终止在跑的转码进程）。

## 命令行参数

| 参数 | 说明 |
|---|---|
| `--port 9000` | 监听端口，默认 8080；被占用时自动递增（依次尝试 20 个） |
| `--dir D:\共享` | 服务目录，默认 exe 所在目录 |
| `--browser` | 启动后打开默认浏览器，默认关闭 |
| `--hidden` | 显示隐藏文件（点开头）。默认关闭时，隐藏文件在列表、搜索、直链下载、缩略图、zip 中均不可见/不可访问；`.FileServer` 为保留名，无论开关始终不可见/不可访问 |
| `--auth user:pass` | 启用 Basic Auth 访问口令，默认不启用。口令明文走 HTTP，仅限可信局域网 |
| `--ffmpeg` | 开启冷门格式在线转码播放与服务端抽帧缩略图（需 ffmpeg；有 CPU/GPU 代价，默认关闭），也可在网页工具栏动态开关 |
| `--no-qr` | 不在终端显示地址二维码 |
| `-v` | 输出访问日志 |

本项目不读取自定义环境变量；查找 ffmpeg 时会参考 `%LOCALAPPDATA%\FileServer\ffmpeg`。

## 视频在线播放

点开视频后前端询问 `/api/video-info`，服务端据文件扩展名与 MP4 头部毫秒级决策（不等 ffprobe）。

冷门格式支持关闭（默认）：原生格式（H.264 MP4、WebM 等）直链播放（Range 流式 + 硬解）；MKV/RMVB/AVI/WMV/HEVC 等不可在线播放，可下载。

冷门格式支持开启后：原生格式仍直链播放；怪封装 MP4 走 ffmpeg `-c copy` 重封装为 HLS 分片（零转码、零画质损失）；冷门格式实时转 H.264（GPU 优先，CPU 兜底），边转边播。

共性机制：

- **编码器实测优选**：启动时依次实测 h264_nvenc / h264_amf / h264_qsv，全部不可用回退 libx264；无独显也能播
- **硬件解码按需**：仅 H.264/HEVC 使用配套 `-hwaccel`（cuda / d3d11va / qsv）；RMVB 等用 CUDA 硬解会挂起，自动回退 CPU 解码
- **会话管理**：分片 3 秒；copy 4 路并发、转码 2 路并发；首片等待上限 60s、分片请求阻塞上限 28s；停滞 60s 或空闲 10 分钟自动终止；离开播放页即通知服务端终止（`abandon`，taskkill 兜底）
- **进程安全**：ffmpeg 子进程挂 Job Object，服务退出或崩溃时连带终止，不留孤儿进程
- **播放列表**：转码中返回 EVENT 列表（暂不写 ENDLIST），完成后转 VOD；进度条按服务端下发的真实总时长显示
- **seek 收敛与缓存复用**：拖到尚未生成的位置时收敛到已生成范围，转码跟上后可继续后拖；完整转码结果缓存 3 天

## 怪封装识别与规整化

- **判定**：mdat 块数 > 4 且文件 ≥ 256MB 视为怪封装（结果带缓存，毫秒级）
- **标识**：怪封装视频卡片显示「怪封装」标记与「规整化」按钮，列表视图同样显示
- **规整化**：用 ffmpeg `-c copy +faststart` 合并碎片 mdat、前置 moov，永久秒开
- **备份**：覆盖前先把原文件移到 `.FileServer\backup\`（按相对路径）；任一步失败可回滚，不留半成品
- **备份管理**：面板可查看、一键恢复或彻底删除备份
- **依赖**：规整化不受冷门格式开关影响，但需要服务端存在 ffmpeg；无 ffmpeg 时接口返回「无法规整」

## 视频缩略图

**常规 MP4/WebM：浏览器抽帧，始终如此（服务端零成本）**

浏览器用隐藏 `<video>` 加载 `/api/thumb-src` 返回的截短抽帧源，seek 后 canvas 截帧：

- 不给原文件的原因：Chromium 对 video 源发开区间 Range（`bytes=0-`），整段回源会把大文件全传一遍
- 抽帧源：服务端只返回头部样本区 + moov（≤16MB 的合法 MP4），浏览器元数据秒读、seek 命中样本
- 懒加载与播放优先：卡片进入视口才抽（3 路并发、20 秒兜底超时，解码不支持或损坏时保持图标）；点开视频即中止在途抽帧，返回列表后恢复

**冷门格式（MKV/RMVB/AVI/WMV/HEVC 等）：**

- 开启支持：服务端 ffmpeg 抽一帧 JPEG（取 1s 处，跳过黑场），缓存到 `.FileServer\thumb\`（7 天清理），2 路并发且低优先级
- 未开启：保持文件图标，不产生服务端开销

## 缓存目录

```
服务目录\.FileServer\
├── thumb\      图片缩略图 + 冷门格式服务端抽帧缩略图（jpg，7 天清理）
├── hls\        HLS 转码/重封装会话（index.m3u8 + seg_*.m4s，3 天清理）
├── faststart\  MP4 moov 前置重封装缓存（7 天清理）
└── backup\     规整化前的原文件备份（可恢复/删除）
```

- 不写系统目录；服务目录不可写时才回退系统临时目录
- 删除 `.FileServer` 即清空全部缓存，不影响功能；无论是否 `--hidden`，它都不出现在列表、搜索、zip 中，也无法通过直链、缩略图、抽帧源访问（含大小写变体）

## 构建与测试

需要 Windows 与网络（首次自动下载 Go 工具链到 `.tools\`，免安装）：

```
build.bat                  # 构建到 dist\FileServer.exe
release.ps1 -Version 1.2.3 # 打包 dist\FileServer-lite-<版本>.zip 与 -full-<版本>.zip
```

手动构建与测试：

```
.tools\go\bin\go.exe vet ./...
.tools\go\bin\go.exe build -trimpath -ldflags "-s -w" -o dist\FileServer.exe .\cmd\fileserver
.tools\go\bin\go.exe test ./internal/...
```

端到端测试（Python 3.12 + Playwright，需先启动服务）：

```
dist\FileServer.exe --dir .\testdata --port 8099 --no-qr
python scripts\smoke_test.py        # 冒烟
python scripts\regression_test.py   # 回归
python scripts\mobile_test.py       # 移动端 + 灯箱 + zip
python scripts\nav_test.py          # 导航/历史
python scripts\special_e2e_test.py  # 特殊文件端到端
python scripts\hls_e2e_test.py      # HLS 端到端
python scripts\frontthumb_test.py   # 前端抽帧（需无 ffmpeg 服务）
```

## 项目结构

```
cmd/fileserver/          入口：参数解析、局域网 IP 探测、控制台输出与二维码、优雅退出
internal/server/         HTTP 服务：路由、文件与视频、播放决策、缩略图、HLS、规整化、安全与路径
internal/server/web/     内嵌前端（index.html / style.css / app.js / hls.min.js）
internal/qrcode/         纯 Go 二维码编码与终端渲染
internal/platform/       OS 差异（控制台编码、打开浏览器、子进程回收）
scripts/                 Playwright 测试脚本
testdata/ + doc/         测试样本（*.mp4 不入库）；项目文档
build.bat + release.ps1  构建与打包
```

## 常见问题

- **手机打不开**：确认在同一网络、防火墙已放行；路由器若开启 AP 隔离，需关闭
- **端口被占用**：程序在 8080 之后自动尝试，控制台会显示实际地址
- **哪些视频要转码、开了冷门格式还不能播**：原生格式直链播放，MKV/RMVB/AVI/WMV/HEVC 等需开启冷门格式支持；开启后仍不能播时，确认服务端能找到 ffmpeg（exe 同目录 `ffmpeg\`、exe 同目录或 PATH）
- **大视频第一次要等 / 没有缩略图**：机械盘上碎片化的 MP4 冷读需数秒到十几秒，完整播放一次后会生成缓存，怪封装 MP4 可点「规整化」；关闭冷门格式支持时 HEVC/MKV 无缩略图、保持图标属正常

## 许可证

MIT，见 `LICENSE`。
