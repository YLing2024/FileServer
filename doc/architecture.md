# 总体架构

## 1. 定位

FileServer 是一个面向局域网的单文件文件服务器，核心场景是**在线视频播放**：
用户把下载好的视频放进共享目录，用手机/电脑浏览器直接点开播放，无需拷贝、无需转码等待。

硬约束（决定了大量设计决策）：

- **单 Windows exe**：无安装、无服务注册，双击即用；ffmpeg 为可选旁挂组件。
- **机械硬盘**：目标磁盘是 5400rpm 机械盘 + 严重碎片化的大视频（数百个 mdat 块、
  数 MB 巨 moov、数百万条 stco 表项）。物理寻道 5~15ms、moov 冷读一次 5~15s，
  是"起播慢、加载不出来"的根本原因。
- **播放体验优先**：目录浏览（缩略图/预热）绝不能饿死用户点开的视频。

## 2. 模块划分

```
cmd/fileserver/main.go      入口：参数解析、单实例、启动
internal/server/
├── server.go       HTTP 路由、目录列表/文件下载/zip/搜索、视频播放决策、冷门格式开关、faststart 缓存
├── hls.go          HLS 会话管理（copy/转码、分片、EVENT→VOD、abandon、进程强杀）
├── ffmpeg.go       ffmpeg 查找、GPU 编码器探测、服务端抽帧、faststart 重封装、媒体探测
├── thumb.go        缩略图缓存（内存 LRU + 磁盘 + 失败记忆 + 忙碌计数）、冷门格式服务端抽帧
├── normalize.go    怪封装规整化（任务队列/备份/恢复/删除/进度）
└── web/            内嵌前端（原生 HTML/CSS/JS，无构建；Go embed.FS）
internal/platform/  Windows 平台设施（Job Object 防孤儿进程）
internal/qrcode/    终端二维码
```

## 3. 进程模型

- 单个 `FileServer.exe` 进程，Go net/http 标准库。
- ffmpeg/ffprobe 以子进程方式按需启动：
  - 冷门格式缩略图抽帧：`BELOW_NORMAL_PRIORITY_CLASS` 低优先级，20s 超时；
  - HLS 转码：GPU 编码器优先（nvenc/amf/qsv 启动时实测），CPU 兜底；
  - 怪封装规整化：低优先级、单飞（`-c copy +faststart`，可能耗时数分钟）；
  - faststart 重封装：大文件低优先级、10 分钟超时；
  - **防孤儿**：子进程挂到 Windows Job Object（`KillOnParentExit`，句柄不可继承），
    服务退出即连带终止；异常情况再以 `taskkill /F /T` 兜底（仅 ctx 取消后 5s 未退时，
    或 abandon 时立即强杀）。
- 单实例：端口占用失败即提示退出（端口默认 8080，被占用自动 +1 递增）。

## 4. 请求数据流

```
浏览器
 ├─ GET /api/list?path=        目录列表（带 2s 短缓存）+ 怪封装标记
 ├─ GET /api/weird?path=       目录怪封装扫描（流式提前停，布局缓存）
 ├─ GET /api/thumb-src?path=   视频抽帧源（截短的合法 MP4：moov+样本区，≤16MB）
 ├─ GET /api/thumb?path=..     图片缩略图 + 冷门格式服务端抽帧 JPEG
 ├─ GET /api/video-info?path=  播放决策（毫秒级，不碰 ffprobe）
 ├─ GET /api/file?path=&fs=1   直链播放（Range 断点续传）
 ├─ GET /api/hls?path=&f=...   HLS 分片（copy/转码，EVENT→VOD）
 ├─ POST /api/normalize?path=  加入怪封装规整队列
 ├─ GET  /api/normalize/status|backups  规整进度/备份列表
 ├─ POST /api/normalize/restore|delete-backup  恢复/删除备份
 ├─ POST /api/settings/ffmpeg  冷门格式支持动态开关（前端工具栏按钮）
 └─ GET /api/zip?path=         目录打包下载
```

## 5. 磁盘 IO 优先级（核心设计）

机械硬盘上**任何并行的顺序读者都会互相拖慢**（磁头来回寻道）。全部磁盘任务按优先级：

| 任务 | 并发 | 让路条件 |
|---|---|---|
| 视频播放（直链 Range 流） | — | 最高优先级，无需让路 |
| HLS 转码/分片 | copy 4 / 转码 2 | 最高优先级 |
| 视频缩略图（常规 MP4/WebM 浏览器抽帧） | 浏览器内 3 路 | 点开视频瞬间前端中止在途抽帧 + 暂停新任务 |
| 冷门格式服务端抽帧 | 2 路（低优先级） | 不抢播放 |
| 怪封装规整化 | 1 路单飞（低优先级） | 不抢播放 |
| faststart 重封装 | 每文件单飞 | 播放进行时等待 |
| 图片缩略图（Go 原生解码） | 4 路 | — |

实现要点：

- `playbackState.directPlaying()`：直链播放活动跟踪。`/api/file` 收到视频/音频的
  Range 请求即记录时间戳，10s 窗口内视为"播放中"——重封装据此让路。
- 前端 `thumbPaused` + 抽帧中止：点开视频的瞬间中止所有在途抽帧 video 元素
  （`activeGrabsSet` 统一清空）、暂停新任务；返回列表自动恢复。
- **视频缩略图分工**：常规 MP4/WebM 用浏览器抽帧（服务端只提供 `/api/thumb-src`
  截短源，≤16MB，避免 Chromium 开区间 Range 整段下载）；冷门格式（MKV/RMVB/
  HEVC 等）在冷门格式支持开启时由服务端 ffmpeg 抽帧 JPEG（低优先级、2 路并发）。

## 6. 关键设计决策

1. **播放决策毫秒级返回**：绝不等待 ffprobe（异常源探测一次几十秒）。
   决策仅依赖扩展名 + MP4 头部 box 解析（`mp4HasHEVC`/`isWeird`）+ 文件大小。
2. **浏览器原生可播直链 + 冷门格式转码**：H.264 MP4/WebM 等直接给浏览器直链
   （Range 流式 + GPU 硬解）；冷门格式（MKV/RMVB/HEVC 等）在冷门格式支持开启时
   走 HLS 转码（GPU 优先），关闭时仅可下载。硬件解码（`-hwaccel cuda`）仅用于
   H.264/HEVC——RMVB 等 CUDA 无法硬解，强行硬解会卡死（曾实测残留进程）。
3. **怪封装 MP4**：mdat 碎片化严重（大量碎块）导致 Chrome 解析 moov 慢（起播
   十几秒~三十秒）。两种解法：
   - 冷门格式支持开启：自动走 HLS copy 重封装流（`-c copy` 零画质损失），起播约 1 秒；
   - 手动规整化：`-c copy +faststart` 永久整理为 mdat 单块，之后永远秒开；
     规整前自动备份原文件（可恢复/删除）。
4. **缩略图分工**：常规 MP4/WebM 用浏览器抽帧（零服务端成本）；冷门格式在开启
   冷门格式支持时由服务端 ffmpeg 抽帧（低优先级、2 路并发、磁盘缓存），
   关闭时保持图标（不下载、不占资源）。
5. **缓存全在服务目录内**：`.FileServer\`（thumb/ hls/ faststart/ backup/），不写系统目录。
6. **前端无构建**：原生 JS + embed.FS，改前端即改 Go 代码重新编译。
7. **冷门格式支持可动态切换**：`transcodeEnabled` 原子变量，前端工具栏 🎬 按钮
   调 `/api/settings/ffmpeg` 即时开关并记忆选择（localStorage），无需重启服务。
