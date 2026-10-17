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
| `text_exts` | 可新建/编辑的文本扩展名白名单 |
| `text_max_bytes` | 可编辑文本大小上限（字节） |

## GET /api/health

健康检查：返回进程存活与构建版本信息（version/commit/go/platform），
供监控/部署脚本确认服务与版本。无鉴权以外的副作用。

- **免鉴权**：认证开启（`--auth-mode` 非 `none`）时本接口仍可匿名访问并返回 `200`，
  供看门狗 / 反向代理健康检查 / 容器 healthcheck 使用（响应体只有上述进程/版本
  信息，不含任何目录、文件或会话内容）。其余 `/api/*` 仍要求有效会话（`401`）。

```bash
curl -s http://127.0.0.1:8080/api/health
# {"ok":true,"version":"dev","commit":"...","go":"go1.25.0","platform":"linux/amd64"}
```

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

## POST /api/settings/ffmpeg

动态开关冷门格式（MKV/RMVB/HEVC 等）在线转码播放 + 服务端抽帧缩略图。
前端工具栏按钮调用并记忆到 localStorage。切换不影响已在进行中的转码会话。

- 请求体：`{"enabled": true|false}`。
- `enabled=true` 但服务端无 ffmpeg：`502`。
- 成功：`200 {"hls": true|false}`；参数非法：`400`。
- 副作用：改变 `transcodeEnabled` 运行时开关（进程内，不持久化）。

## 认证端点

认证默认关闭（`--auth-mode none`）。启用后，除 `/api/auth/*` 与 `GET /api/health`
外的全部 `/api/*` 接口都需要有效凭据，未认证返回 `401 {"error":"需要访问口令"}`
（`GET /api/health` 免鉴权，详见该端点说明）。

凭据校验顺序（`requireAuth`）：短票 `?t=`（**仅 GET**）→ `Authorization: Bearer <token>`
→ 旧版 `fs_session` cookie（只读兼容，服务端已不再下发 cookie）。前端凭据真源为浏览器
存储：不勾「记住我」存 `sessionStorage`，勾选存 `localStorage`（键 `fs.token`）。

认证模式 `--auth-mode`：`none` / `totp` / `password` / `password+totp`。
兼容用法 `--auth user:pass` 等价于 `password` 模式 + 预置口令。
失败响应统一为 `401 访问凭据无效`（不区分原因）；触发限速返回 `429`。
服务端 token 有效期统一 7 天（`--token-days`，默认 7；勾/不勾「记住我」一致）。

> 浏览器原生请求（`<video>`/`<img>`/`<a download>`/HLS 分片）无法携带 `Authorization`
> 头，改用绑定会话的短票经 `?t=` 鉴权（见 `POST /api/auth/ticket`）；token 本体**绝不**
> 进入 URL/日志。

### GET /api/auth/status

认证状态（公开）。响应字段：`enabled`、`mode`、`authenticated`、`needs_setup`、
`configured`、`need_password`、`need_totp`、`password_set`、`totp_bound`。

### GET /api/auth/setup

引导页初始状态（公开）。未完成配置时返回 `{mode, need_password, need_totp,
totp_secret, otpauth_url}`（`need_totp` 时生成待绑定密钥）；已完成返回 `409`。

### POST /api/auth/setup

完成引导：设置口令（如需，长度 ≥ 8）并绑定 TOTP（如需，`skip_totp=true` 可跳过；
`totp` 模式不可跳过）。成功 `200 {"ok":true}`；弱口令/验证码错误 `400`；已完成 `409`。

- **原子提交**：先校验全部必需因素（口令强度、TOTP 验证码），全部通过后才一次性
  持久化口令哈希 + TOTP 绑定 + 配置状态。任一失败即整笔拒绝，**不留下部分状态**：
  `GET /api/auth/status` 仍为 `needs_setup=true`、`password_set=false`，
  此后仅凭口令登录仍返回 `401`（不会出现「看着像配好了、其实只有单因素」）。
- 绑定成功所用的验证码其时间步计入重放集合，同一时间步内再用该码登录返回 `401`
  （见 `doc/security.md` 的重放防护说明）。
- `--auth user:pass` / 环境变量预置口令属于预置路径，不经引导，不受本节影响。

### GET /api/auth/totp-qr

返回待绑定 TOTP 的二维码（`image/svg+xml`，服务端自研 qrcode 生成）。无需 TOTP
绑定时 `404`。

### POST /api/auth/login

校验凭据并签发会话 token。请求体 `{password, code, remember}`（按模式取用）。
成功 `200 {"ok":true, "token":"<明文 token>", "expires":<unix 秒>, "remember":bool}`；
**不再 `Set-Cookie`**（凭据由前端自行写入 `sessionStorage`/`localStorage`）。
`expires` 距当前约 7 天。凭据错误 `401`；触发限速 `429`。

```bash
curl -s -X POST http://127.0.0.1:8080/api/auth/login \
  -H 'Content-Type: application/json' \
  -d '{"password":"secret123","remember":true}'
# {"ok":true,"token":"...","expires":1750000000,"remember":true}
```

### POST /api/auth/ticket

为当前会话签发短票，供浏览器原生媒体/下载/缩略图/HLS 请求经 `?t=` 鉴权
（这些请求无法携带 `Authorization` 头）。**需已登录**：`Authorization: Bearer <token>`。

- 成功 `200 {"ticket":"<明文短票>", "expires":<unix 秒>}`；短票默认有效期 5 分钟。
- 短票**绑定签发它的会话**：会话被撤销/过期后其短票立即失效。
- 短票**只对 GET 生效**（POST/编辑类接口只认 `Authorization` 头）。
- 同一会话可重复签发；前端在剩余寿命不足 60s 时自动续签。
- 未登录/凭据无效 `401`。短票可出现在 URL（如 HLS 分片）中；token 本体不可。

```bash
curl -s -X POST http://127.0.0.1:8080/api/auth/ticket \
  -H 'Authorization: Bearer <token>'
# {"ticket":"...","expires":1750000300}
curl -s "http://127.0.0.1:8080/api/list?path=/&t=<ticket>"
```

### POST /api/auth/logout

使当前会话 token 失效（凭据经 `Authorization: Bearer` 或旧版 cookie 提供）。返回 `204`。
前端登出时同时清本地存储的 `fs.token`。

### GET /api/auth/sessions

列出已登录会话（不含 token 明文）：`{sessions:[{id,created,last_seen,expires,
remember,current,ua,ip}]}`。需已登录。

### POST /api/auth/sessions/revoke

撤销指定会话：`{"id": "..."}`。成功 `200 {"ok":true}`；不存在 `404`。需已登录。
