# 缓存体系

**原则：所有缓存都在服务目录内的隐藏文件夹 `.FileServer\`，绝不写系统目录。**
（服务目录不可写时才回退系统临时目录。）

```
服务目录\.FileServer\
├── thumb\      图片缩略图（jpg，磁盘持久化；视频缩略图为浏览器抽帧，不落盘）
├── hls\        HLS 转码/重封装会话（index.m3u8 + seg_*.m4s，3 天清理）
└── faststart\  MP4 moov 前置重封装缓存（7 天清理）
```

删除 `.FileServer` 文件夹即清空全部缓存，不影响任何功能；已在 `.gitignore` 排除。

## 1. 图片缩略图缓存（thumb/ + 内存）

**图片**缩略图两层缓存（Go 原生解码；视频缩略图已 100% 浏览器抽帧，不落盘）：

- **内存**：`ThumbCache`（LRU，200MB 上限），键 = `path|size|mtime|w|h` 的 SHA1。
- **磁盘**：`.FileServer\thumb\<key>.jpg`，长期保留（7 天清理）。

`singleflight`（`Do`）：同一图片的并发生成请求只执行一次。

**MP4 顶层布局缓存**（`mp4LayoutCached`）：thumb-src 的 moov 定位结果缓存
1 小时（moov 尾部扫描要遍历全部 mdat 头，碎片盘上代价高，同一文件多次
抽帧不应重复扫描）。

## 2. HLS 会话缓存（hls/）

- 会话目录：`.FileServer\hls\<key>\`（index.m3u8 + seg_*.m4s）。
- 完整转码结果**缓存 3 天**：同一视频再次点开直接读缓存，秒开不重转。
- 会话生命周期：首片生成 → 播放 → abandon/空闲 10 分钟/停滞 60s → 终止；
  转码完成（ENDLIST 已写）后保留为纯缓存。
- 启动时清理 3 天前的目录。

## 3. faststart 缓存（faststart/）

- 直链播放的 MP4 重封装缓存：`.FileServer\faststart\<key>.mp4`。
- key = `path|size|mtime` 的 SHA1（文件变更自动失效）。
- **不限文件大小**：大文件首次播放后后台重封装（低优先级、播放时让路），
  完成后二次打开秒开（实测 1.1~1.9s）。
- 7 天清理（`cleanupOldFiles`，启动时执行）。

## 4. 内存缓存

| 缓存 | 上限 | 用途 |
|---|---|---|
| 目录列表 `listCache` | 2s TTL | 返回/翻页秒开 |
| 媒体信息 `ff.infos` | 4096 条 | video-info 快速响应 |
| 时长 `ff.durations` | 4096 条 | 缩略图 seek 位置选择 |
| 前端缩略图 `thumbCache` | 200 条（会话级） | 同一会话内重复浏览免请求 |
| 前端 video-info `videoInfoCache` | 60 条（会话级） | 重复点开同一视频免往返探测 |

## 5. 磁盘占用预估（实测参考）

- 缩略图：300×300 jpg 每张 ~15-40KB；146 视频目录全量 ~5MB。
- HLS：分片 m4s 与源大小同量级（copy 模式 ≈ 源文件），3 天自动清理。
- faststart：与源文件等大（重封装不改内容），7 天自动清理。
