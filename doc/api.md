# HTTP API 参考

所有接口都是 `GET`，JSON 或文件字节。统一错误格式：`{"error": "..."}`。

## 公共约定

- 路径参数 `path` 必须 URL 编码（`encodeURIComponent`），服务端用 `safePath` 校验：
  拒绝 `..` 穿越、绝对路径逃逸；符号链接指向服务目录外一律拒绝（见 security.md）。
- 隐藏文件（`.开头`，且未开 `--hidden`）：列表、搜索、直链、缩略图、zip 全部拒绝。
- 前端静态资源响应头 `Cache-Control: no-store`（保证改版立即生效）。

## GET /api/info

能力探测（前端启动时调用，决定缩略图模式等）。

| 字段 | 说明 |
|---|---|
| `name` / `version` | 服务标识 |
| `ffmpeg` | 是否找到可用 ffmpeg（决定服务端缩略图/转码） |
| `hls` | 是否支持 HLS 转码 |
| `kinds` | 扩展名 → 类型映射（统一前端判断） |
| `list_limit` / `search_limit` | 列表/搜索上限 |

## GET /api/list?path=

目录列表。响应 `{"path":..., "entries":[{name,is_dir,size,mtime},...]}`。
- 服务端分页：默认上限 2000 条，`?limit=` 可调。
- 2 秒短缓存（`listCache`）：快速返回/翻页秒开。
- `mtime` 为 Unix 秒。

## GET /api/thumb?path=&w=&h=

缩略图。**图片**走 Go 原生解码缩放（视频缩略图已 100% 浏览器抽帧，视频请求一律 404）。

图片响应：200（image/jpeg）+ ETag/304；解析失败 404。4 路并发、巨图（>6000px）直接返回原图。

## GET /api/thumb-src?path=

**浏览器抽帧专用源**：返回一个"截短的合法 MP4"（moov + 开头样本区），
让 `<video preload=metadata>` 元数据秒读、seek 命中，且**绝不整段下载**。

- moov 在头部（含怪封装巨 moov）：返回 `[0, 16MB)`；
- moov 在尾部（正常录制片）：返回 `[0, 16MB) + [moov]` 拼接（样本偏移不变，仍合法）；
- 非 MP4 容器 / 解析失败 / 小文件：整段返回（带 Range 支持）。
- 响应为 200 整段流（不处理 Range——抽帧元素会缓冲全部 ≤16MB，seek 在缓冲内完成）；
- MP4 顶层布局带 1 小时缓存（`mp4LayoutCached`，避免 moov 尾部扫描的反复寻道）。

## GET /api/file?path=&dl=1

文件下载/直链预览，支持 Range 断点续传（`http.ServeContent`）。
- `dl=1`：强制附件下载。
- 视频/音频 Range 请求会触发 `markVideoRead()`（直链播放活动跟踪，
  moov 预热/缩略图抽帧据此让路）。
- HTML/SVG/JS 等可执行扩展名强制 attachment（防 XSS 渲染）。

## GET /api/video-info?path=

播放决策，**毫秒级返回，绝不等待 ffprobe**。

响应：`{mode, mime, duration?, width?, height?}`

`mode` 默认 `direct`；仅当**开启 `--ffmpeg`**（且找到 ffmpeg）且该扩展名浏览器
不能原生播放时才置 `hls`（详见 video-pipeline.md）：

| 情况 | mode |
|---|---|
| 未开启 `--ffmpeg`（或未找到 ffmpeg） | 一律 direct |
| 开启 `--ffmpeg`：`.webm` | direct |
| 开启 `--ffmpeg`：MP4 家族含 HEVC（hvc1/hev1，头尾 256KB 探测） | hls |
| 开启 `--ffmpeg`：MP4 家族且「怪封装」（≥256MB 且 mdat > 4 块） | hls（copy 重封装） |
| 开启 `--ffmpeg`：MP4 家族其余（H.264 等） | direct |
| 开启 `--ffmpeg`：音频 / `.ogv` | direct |
| 开启 `--ffmpeg`：其他容器（MKV/AVI/RMVB/…） | hls |

元数据：内存缓存命中直接返回完整信息；未命中后台 ffprobe（2s 超时），
前端 2 秒后二次查询补时长。

> MP4 的 faststart 重封装已移除；需要永久规整（moov 前置 / mdat 合并）请用独立
> 项目 [mp4norm](https://github.com/YLing2024/mp4norm)。

## GET /api/hls?path=&f=index.m3u8|seg_000001.m4s

HLS 分片服务。`f` 为播放列表或分片文件名。
- 首片等待上限 60s（copy 毫秒级，转码视源而定）；分片请求阻塞等待生成上限 28s
  （与前端 hls.js `fragLoadingTimeOut` 对齐）。
- 播放列表为 **VOD 快照**：EVENT 播放列表改写成 ENDLIST 形式，hls.js 首片即播。
- `&abandon=1`：离开播放页通知终止会话（立即杀转码进程，不浪费磁盘/GPU）。
- 会话空闲 10 分钟或停滞 60s 自动终止；`Get` 空闲 10s 取消等待。

## GET /api/prewarm?path=

moov 预读预热：把 MP4 头部/尾部最多 8MB 顺序读进 OS 缓存（512KB 块、块间 30ms 限速）。
- 目录页浏览时前端对可见视频自动发出（每会话最多 40 个）。
- 单路并发 + FIFO 积压（最多 32 个）；同一文件 30 分钟内只预热一次。
- 播放（HLS 或直链）或抽帧进行中：暂停推进、不启动新任务（播放绝对优先）。
- 仅 `.mp4/.m4v/.mov` 且 ≥4MB 的文件生效；其余返回 204。

## GET /api/search?q=&path=&limit=

搜索（文件名/路径包含匹配，服务端分页，默认上限 2000）。
- 目录搜索范围 = 当前目录及子目录；条目带 `_searchPath` 供前端定位。
- 前端 320ms 防抖后调用。

## GET /api/text?path=

读取文本内容与版本标识，供编辑态使用。仅对白名单扩展名开放。

- 白名单（小写，含点）与大小上限由 `/api/info` 的 `text_exts` / `text_max_bytes` 下发；
  默认上限 2 MiB（`--text-max-bytes` 可配）。
- 成功（可编辑）：`{path, content, size, mtime, version, editable:true, max_bytes}`
- 超过上限：`200 {editable:false, reason:"too_large", size, max_bytes}`（不返回内容）。
- 扩展名不在白名单：`403`；路径不存在/隐藏：`404`。
- `version` = `mtime(ns)-size`，写请求必须回传，用于并发防覆盖。

## POST /api/text

保存已有文本文件（原子写 + 版本校验）。

- 请求体：`{path, content, version}`。
- 成功：`200 {path, size, mtime, version}`。
- 版本不匹配（他人已改动）：`409`；扩展名不在白名单：`403`。
- 目录/文件只读（无写权限位）：`403`；内容超上限：`413`。
- 副作用：原子替换目标文件（同目录临时文件 + rename），保留原权限位；
  失败不留临时文件残留；成功后失效目录列表缓存。

## POST /api/text/new

在指定目录内新建文本文件，不覆盖已存在文件。

- 请求体：`{dir, name, content}`；`name` 不得含路径分隔符或 `..`。
- 成功：`200 {path, size, mtime, version}`；扩展名不在白名单：`403`。
- 同名文件已存在：`409`；目录只读：`403`；内容超上限：`413`。
- 副作用：`O_EXCL` 占位确保不覆盖，再原子写入。

## GET /api/zip?path=

目录打包下载（zip 流式，含子目录）。
- 隐藏文件（未开 `--hidden`）跳过。
- 大目录流式写，不占内存。
