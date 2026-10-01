# 视频播放管线

## 1. 播放决策（`handleVideoInfo`）

目标：**毫秒级**返回，绝不等待 ffprobe（异常源探测一次 10s+，会饿死播放链路）。

探测手段（全部本地快速读）：
- `mp4HasHEVC`：读文件头/尾各 256KB，查 `hvc1`/`hev1` 标志（HEVC 需 hls.js 转码）；
- `isWeird`：≥256MB 且顶层 `mdat` > 4 块（怪封装，copy 重封装起播更快）。

`mode` 默认 `direct`；仅当**开启 --ffmpeg**（且找到 ffmpeg）且浏览器不能原生
播放时才置 `hls`：

```
未开启 --ffmpeg（或未找到 ffmpeg）    → 一律 direct（前端提示不可在线播放）
.webm                                → direct（浏览器原生）
.mp4/.m4v/.mov + HEVC                → hls（浏览器不支持 HEVC 直链）
.mp4/.m4v/.mov + 怪封装(≥256MB,mdat>4) → hls（copy 重封装）
.mp4/.m4v/.mov 其余（H.264…）        → direct
音频 / .ogv                           → direct
其他容器（mkv/avi/rmvb/...）          → hls
```

注意：决策**不再按文件大小或 moov 位置**区分 direct / hls（旧文档的
「≤32MB 或 moov 在头部」判据已废止）。H.264 MP4 无论 moov 在哪都先 direct；
moov 位置只决定是否触发后台预热重封装。怪封装大文件在开启 --ffmpeg 时走 HLS
copy（ffmpeg 建索引虽慢但稳定流式），未开启时仍 direct（Chrome 顺序下载 moov
后即可解析起播，实测 1~3s）。

## 2. 直链播放（direct）

- 浏览器 `<video src="/api/file?path=...&fs=1">`，Range 断点续传。
- `fs=1`：faststart 重封装缓存（`faststartCachePath`）存在时读缓存——重封装后
  moov 前置、无碎片，二次打开秒开（实测 1.1~1.9s）；未就绪回退原文件。
- `/api/video-info` 对**直链、非 HEVC、原文件 moov 不在头部且尚无缓存**的 MP4
  后台触发一次 `warmFaststart`：`-c copy -movflags +faststart`，失败只记日志
  （best-effort，绝不影响本次播放）；大文件低于正常优先级、10 分钟超时。
- 触发只负责调度：`warmFaststart` 内单飞防重复（`fsBusy`/`fsMu`），且
  `hls.Active() || directPlaying()` 时先等待——播放/转码进行中让路，不抢磁盘。
- 直链 Range 请求 → `markVideoRead()`：预热/缩略图/重封装 10s 窗口内让路。

## 3. HLS 会话（`hls.go`）

### 3.1 会话生命周期

```
首次请求 index.m3u8 → 创建会话 → 启动 copy 或转码 → 分片持续生成
  → 用户离开（abandon=1）→ 立即终止
  → 或空闲 10 分钟 / 停滞 60s → 自动终止
  → 转码完成 → 会话保留（缓存 3 天，二次点开直接读）
```

- 会话键 = 文件路径；同文件并发复用同一会话。
- `Active()`：是否有活跃会话/转码在跑——moov 预热据此暂停推进。
- `Get()`：分片未就绪时阻塞等待生成（上限 28s）；空闲 10s 取消等待。

### 3.2 转码规格

- 分片 3 秒一片；`copy`（H.264 已封装直拷，毫秒级）与 `transcode`（CPU/GPU 编码）两路，
  互不阻塞：copy 4 路并发、转码 2 路并发。
- HEVC 判定（`mp4HasHEVC`）为真的文件直接走 copy 重封装（改封装不重编码，
  hls.js 解码 HEVC）；判定失败回退转码。
- GPU 编码器：nvenc（CUDA）→ amf（D3D11VA）→ qsv，启动时实测可用性；
  全部失败回退 libx264（CPU）。
- HDR 源：探测到 zscale/tonemap 滤镜时做 HDR→SDR 色调映射。

### 3.3 VOD 快照与 seek 收敛

- 服务端把 EVENT 播放列表改写成 **VOD 快照**（去 EXT-X-EVENT 段、加 EXT-X-ENDLIST）：
  hls.js 按 VOD 语义处理，首片即播、无直播追边缘问题。
- 前端每 8s 重载一次 manifest（`hls.startLoad()`）感知新分片；
  转码完成后列表趋于稳定。
- **seek 收敛**：拖到尚未生成的位置时，前端把目标收敛到当前已生成范围并 toast 提示，
  绝不卡死；转码赶上来后可继续往后拖。
- hls.js 参数：`fragLoadingTimeOut 30s`（与服务端分片等待上限对齐）、
  `fragLoadingMaxRetry 4`、`backBufferLength 30`。

## 4. 起播优化组合拳（机械硬盘专项）

1. **moov 预热**（浏览时）：目录页把可见视频的 moov 区域预读进 OS 缓存，
   点开时 ffmpeg/浏览器直接命中（详见 prewarm 章节）。
2. **跳过探测**：copy/重封装统一 `-analyzeduration 0 -probesize 32`
   （MP4 流信息全在 moov，跳过默认 5MB 探测）。
3. **direct-first**：浏览器能原生解码的文件（H.264 MP4/WebM 等）不经过 ffmpeg，
   浏览器直链即点即播——不再按 moov 位置决定是否转封装。
4. **播放绝对优先**：点开视频瞬间，前端中止在途抽帧（thumb-src 读取全部
   释放）+ 预热/重封装让路——磁盘 IO 全部让给播放。
5. **缓存复用**：HLS 完整转码缓存 3 天、faststart 缓存 7 天。

## 5. 实测数据（目标环境：5400rpm 机械盘 + 碎片化怪封装大文件）

| 场景 | 耗时 |
|---|---|
| 大目录（146 视频）浏览中，抽帧正忙时点开视频起播 | 1.0~1.1s |
| 同文件二次打开（faststart 缓存命中） | 1.1~1.9s |
| 首次冷打开（无任何缓存，moov 在头部） | ~2~10s（取决于 moov 冷读） |
| HLS 冷转码（moov 在中/尾） | 首片 3~20s（视 ffmpeg 索引耗时） |
| 浏览器抽帧（thumb-src 截短源，3 路并发） | 首张 ~1s，2 秒 15 张，48 张约 18s |
| 大目录首屏缩略图铺满 | 几秒（无服务端抽帧、无整段下载） |
