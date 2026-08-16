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
├── server.go       HTTP 路由、目录列表/文件下载/zip/搜索、视频播放决策、faststart 缓存
├── hls.go          HLS 会话管理（copy/转码、分片、VOD 快照、abandon）
├── ffmpeg.go       ffmpeg 查找、GPU 编码器探测、缩略图抽帧、faststart 重封装、媒体探测
├── thumb.go        缩略图缓存（内存 LRU + 磁盘 + 失败记忆 + 忙碌计数）
├── prewarm.go      moov 预读预热（浏览时把 moov 读进 OS 缓存）+ 直链播放活动跟踪
└── web/            内嵌前端（原生 HTML/CSS/JS，无构建；Go 1.16 embed.FS）
internal/platform/  Windows 平台设施（Job Object 防孤儿进程）
internal/qrcode/    终端二维码
```

## 3. 进程模型

- 单个 `FileServer.exe` 进程，Go net/http 标准库。
- ffmpeg/ffprobe 以子进程方式按需启动：
  - 缩略图抽帧：`BELOW_NORMAL_PRIORITY_CLASS` 低优先级，15s 超时；
  - HLS 转码：GPU 编码器优先（nvenc/amf/qsv 启动时实测），CPU 兜底；
  - faststart 重封装：大文件低优先级、10 分钟超时；
  - **防孤儿**：子进程挂到 Windows Job Object（`KillOnParentExit`，句柄不可继承），
    服务退出即连带终止；异常情况再以 `taskkill /F /T` 兜底（仅 ctx 取消后 5s 未退时）。
- 单实例：端口占用失败即提示退出（端口默认 8080，被占用自动 +1 递增）。

## 4. 请求数据流

```
浏览器
 ├─ GET /api/list?path=        目录列表（带 2s 短缓存）
 ├─ GET /api/thumb-src?path=   视频抽帧源（截短的合法 MP4：moov+样本区，≤16MB）
 ├─ GET /api/thumb?path=..     图片缩略图（Go 原生解码；视频已 404）
 ├─ GET /api/video-info?path=  播放决策（毫秒级，不碰 ffprobe）
 ├─ GET /api/file?path=&fs=1  直链播放（Range 断点续传）
 ├─ GET /api/hls?path=&f=...  HLS 分片（copy/转码）
 ├─ GET /api/prewarm?path=    moov 预读预热（浏览时自动发出）
 └─ GET /api/zip?path=        目录打包下载
```

## 5. 磁盘 IO 优先级（核心设计）

机械硬盘上**任何并行的顺序读者都会互相拖慢**（磁头来回寻道）。全部磁盘任务按优先级：

| 任务 | 并发 | 让路条件 |
|---|---|---|
| 视频播放（直链 Range 流） | — | 最高优先级，无需让路 |
| HLS 转码/分片 | copy 4 / 转码 2 | 最高优先级 |
| 视频缩略图（**浏览器抽帧**） | 浏览器内 3 路 | 点开视频瞬间前端中止在途抽帧 + 暂停新任务 |
| moov 预热 | **1** | 播放（HLS 或直链）进行时暂停推进 |
| faststart 重封装 | 每文件单飞 | 播放进行时等待 |
| 图片缩略图（Go 原生解码） | 4 路 | — |

实现要点：

- `playbackState.directPlaying()`：直链播放活动跟踪。`/api/file` 收到视频/音频的
  Range 请求即记录时间戳，10s 窗口内视为"播放中"——预热/重封装据此让路。
- 前端 `thumbPaused` + 抽帧中止：点开视频的瞬间中止所有在途抽帧 video 元素
  （`activeGrabsSet` 统一清空）、暂停新任务；返回列表自动恢复。
- **视频缩略图 100% 浏览器抽帧**：服务端不再生成视频缩略图（ffmpeg 抽帧已废弃——
  怪封装 moov 索引建表在机械盘上太贵）。服务端只提供 `/api/thumb-src` 截短
  抽帧源（moov + 开头样本区，≤16MB 的合法 MP4），避免 Chromium 对 video 源
  的开区间 Range 导致整段下载。

## 6. 关键设计决策

1. **播放决策毫秒级返回**：绝不等待 ffprobe（异常源探测一次 10s+）。
   决策仅靠扩展名 + MP4 头部 box 解析（`mp4IsFastStart`/`mp4HasHEVC`）。
2. **direct-first 播放**：moov 在头部（含怪封装巨 moov）的 MP4 直接给浏览器直链——
   Chrome 顺序下载 moov 即可解析起播；ffmpeg 对碎片化 moov 建索引反而要 10s+。
   moov 在中/尾的大文件才走 HLS。
3. **缩略图不做服务端**：抽一帧本身微不足道，贵的是 ffmpeg 为怪封装 moov 建
   5~6M 条样本索引前的碎片化随机读（机械盘 5~15s/张，多路并发直接打爆磁盘）。
   把成本摊给浏览器（Range 小读 + 硬件解码），服务端只提供一个截短源——
   传输/读取量 ≤16MB/张，实测首屏秒铺。
4. **缓存全在服务目录内**：`.FileServer\`（thumb/ hls/ faststart/），不写系统目录。
5. **前端无构建**：原生 JS + embed.FS，改前端即改 Go 代码重新编译。
