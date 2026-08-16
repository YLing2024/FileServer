# 前端架构

原生 HTML/CSS/JS，无构建步骤，经 `embed.FS` 内嵌进 exe（`internal/server/web/`）。
改前端 = 改源码重新编译（静态资源响应 `Cache-Control: no-store`，浏览器不缓存旧版）。

## 1. 文件

```
web/
├── index.html   单页壳：浏览视图 + 预览视图 + 播放器骨架
├── app.js       全部逻辑（~1500 行，模块化注释分区）
├── style.css    深浅双主题
└── vendor/hls.min.js  hls.js（内嵌 ~415KB，首次转码前预加载）
```

## 2. 状态与导航

- `state`：path/view/sort/order/searching/entries/hasMore/hls 等。
- **URL 驱动导航**：`?path=` 浏览目录、`?view=` 预览文件；`pushState` + `popstate`
  统一处理——刷新/前进/后退/手机返回手势都能恢复视图。
- 列表滚动位置在离开前记忆（`rememberScroll`），返回时恢复。

## 3. 视频缩略图（纯浏览器抽帧）

**视频缩略图 100% 由浏览器生成**（服务端不再生成视频缩略图；图片缩略图仍走
服务端 Go 原生解码）。

### 3.1 抽帧源（`/api/thumb-src`）

- 关键背景：Chromium 对 `<video>` 源发出**开区间 Range**（`bytes=0-`）——
  直接给原文件会把整个 1.4GB 视频全传一遍（磁盘/网络/内存打爆，曾实测
  单张抽帧整段下载 500MB+）。
- 服务端返回**截短的合法 MP4**（≤16MB）：moov 在头部的文件截头部 16MB；
  moov 在尾部的文件返回头部 16MB + moov 拼接（样本偏移不变仍合法）。
- 浏览器把截短文件当完整文件解析：元数据秒读、seek 到 `min(1, dur*0.1)`，
  样本命中 → canvas 截帧 → dataURL（`thumbCache` 会话 LRU，200 条）。

### 3.2 队列与并发

- 卡片进入视口（IntersectionObserver，400px 预加载区）→ `enqueueFrontThumb`
  入队；`pumpVideoThumbs` 按 **3 路并发**消费。
- 每个任务：隐藏 `<video preload=metadata>` + 20s 超时；解码不支持
  （HEVC/MKV）/损坏文件 → 保持图标，绝不硬拉；偶发网络错误重试一次。

### 3.3 播放绝对优先（核心体验）

- `thumbPaused` 全局开关：点开视频的瞬间置 true，不再启动新抽帧；
- `pauseThumbs`：**中止所有在途抽帧 video 元素**（`activeGrabsSet` 统一
  `src=''`，立即释放连接与磁盘 IO 给播放）；
- 返回列表（`showBrowse`）：置 false，重新渲染的卡片自动重新抽帧。

## 4. 播放器

- 视频决策：`video-info`（会话级缓存）→ direct（`<video src>`）或 hls（hls.js）。
- **不自动播放**：点卡片进预览，用户点大播放键（或空格）起播——避免误触发
  与自动播放策略问题；播放意图在源就绪前记录（`__wantPlay`），就绪后自动续播。
- HLS：
  - `attachHls`：VOD 快照流 + 8s manifest 轮询（`startLoad`）+ seek 收敛
    （超前 seek 收敛到已生成范围并提示）；
  - hls.js 参数与服务端分片等待上限对齐（30s 超时、4 次重试）；
  - 离开预览页：`abandon=1` 通知服务端终止转码，`hls.destroy()` + 释放媒体资源。
- 快捷键：空格播放/暂停、←→ ±5s、↑↓ 音量、M 静音、F 全屏。
- 进度条：拖拽 seek、缓冲进度显示、tooltip 预览时间；HLS 转码初期时长未知时
  用服务端 ffprobe 时长兜底显示。
- 音频：一律直链（浏览器原生）。
- 图片：灯箱预览；PDF/文本：内嵌查看；其余：下载。

## 5. 搜索

- 输入 320ms 防抖 → `/api/search`；结果条目可点击进入所在目录。
- 搜索模式下点视频同样走预览流程（含缩略图暂停）。

## 6. 移动端适配

- 响应式：网格列数自适应（`auto-fill, minmax`）；触屏手势（swipe 返回、进度条拖拽）。
- 播放器全屏按钮：iOS Safari 用 `webkitEnterFullscreen`，其余 `requestFullscreen`。
- 物理返回键：popstate 统一处理（预览 → 列表 → 上级目录）。
- 列表/网格视图切换记忆在 localStorage（`fs.view`/`fs.sort`/`fs.order`）。

## 7. 性能注意

- 大目录（上千文件）卡片惰性渲染 + IntersectionObserver 懒抽帧，
  只有进入视口的卡片才发 `thumb-src` 请求（每张 ≤16MB，3 路并发）。
- 前端缩略图缓存 LRU 防内存膨胀；重复浏览同一目录秒显（dataURL 命中）。
