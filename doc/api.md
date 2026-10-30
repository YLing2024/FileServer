# HTTP API 参考

接口以 `GET` 为主（读取/下载/媒体流），写入类为 `POST`（新建、编辑文本、上传、认证、设置），
响应为 JSON 或文件字节。统一错误格式：`{"error": "..."}`；上传冲突额外带
`code` / `existing`（见 `POST /api/upload`）。

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
| `allow_upload` | 是否允许上传（`FILESERVER_ALLOW_UPLOAD` / `--allow-upload`，默认 false） |
| `upload_max_bytes` | 单文件上传上限（字节，默认 1024 MiB） |
| `manage` | 是否可用文件管理（重命名/删除/移动/复制/回收站）；仅在启用认证时为 true |

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

在指定目录内新建文本文件，不覆盖已存在文件。保留为兼容入口，前端已切换到 `POST /api/create`。

- 请求体：`{dir, name, content}`；命名校验与 `POST /api/create` 完全一致（见下）。
- 成功：`200 {path, size, mtime, version}`；扩展名不在白名单：`403`。
- 同名文件已存在：`409`；目录只读：`403`；内容超上限：`413`。
- 副作用：`O_EXCL` 占位确保不覆盖，再原子写入。

## POST /api/create

在当前目录内新建**空文件**（扩展名不限）或**单层文件夹**；只创建，绝不覆盖。

- 请求体：`{dir, kind, name, content?}`。
  - `dir`：目标目录相对路径（可为 `/`）；必须已存在且为目录。
  - `kind`：`"file"`（默认语义）或 `"dir"`；其余值 `400`。
  - `name`：名称，须通过跨平台命名校验（见下）；不含路径分隔符。
  - `content`：可选，**仅** `kind="file"` 且扩展名在文本白名单内时写入；
    非白名单扩展名忽略该字段，只建 0 字节空文件。
- 成功：`200 {ok:true, kind, name, path, size, mtime, version}`。
- 命名校验（服务端为准，前端仅提示）：去首尾空白后非空；不得为 `.`/`..`；
  不含 `/ \` 与 NUL；不含控制字符；不含 Windows 非法字符 `< > : " | ? *`；
  不以 `.` 开头（隐藏）；不以 `.` 或空格结尾；非 Windows 保留设备名
  （`CON`/`PRN`/`AUX`/`NUL`/`COM1..9`/`LPT1..9`，含带扩展名形式，大小写不敏感）；
  UTF-8 字节长度 ≤ 255。
- 状态码：`400` 名称不合法 / `kind` 非法 / 目标不是目录 / 父目录不存在；
  `403` 路径穿越或符号链接逃逸 / 目录只读 / 隐藏名；`409` 同名文件或文件夹已存在。
- 副作用：文件 `O_CREATE|O_EXCL|O_WRONLY 0o644`；目录 `os.Mkdir 0o755`
  （**不**递归创建父目录）。失败不留半截产物；成功后失效目录列表缓存。

## POST /api/upload

上传一个或多个文件到指定目录。**默认关闭**：需启动时显式开启
`--allow-upload`（或环境变量 `FILESERVER_ALLOW_UPLOAD=1`），否则返回 `403`。
开启后前端工具栏才显示「上传」按钮，并支持整页拖拽投放。

### 请求

- 路径：`POST /api/upload?dir=<相对路径>&mode=ask|overwrite|rename`
  - `dir`（query，必填）：目标目录相对路径；`/` 表示根目录。必须已存在且为目录。
  - `mode`（query，可选，默认 `ask`）：同名冲突处理方式。
    - `ask`：不覆盖；有冲突时整批拒绝并返回 `409`，由前端弹窗询问。
    - `overwrite`：覆盖同名**普通文件**；目标是文件夹时返回 `409`（不覆盖目录）。
    - `rename`：自动改名为 `name (1).ext`、`name (2).ext`……（保留扩展名）。
  - `file`（multipart，1..64 个）：文件字段，可重复；文件名保留中文与空格。
- 请求体：`multipart/form-data`。单文件上限由 `--max-upload-mb`
  （默认 1024，环境变量 `FILESERVER_MAX_UPLOAD_MB`）配置，超限 `413`。
- 鉴权：与全站一致。认证启用时必须是有效会话（`Authorization: Bearer <token>`），
  未登录 `401`；**认证未启用（默认 `--auth-mode none`）时该端点与全站一样敞开**，
  因此公网/不可信网络部署必须同时启用认证。

```bash
# 先登录拿 token（认证启用时）
TOKEN=$(curl -s -X POST http://127.0.0.1:8080/api/auth/login \
  -H 'Content-Type: application/json' \
  -d '{"password":"secret123","remember":true}' | sed -n 's/.*"token":"\([^"]*\)".*/\1/p')

# 上传两个文件到 /docs（默认 ask，不覆盖）
curl -s -X POST 'http://127.0.0.1:8080/api/upload?dir=%2Fdocs&mode=ask' \
  -H "Authorization: Bearer $TOKEN" \
  -F 'file=@./a.txt' \
  -F 'file=@./报告 2026.pdf'
```

### 成功响应

`200`：

```json
{
  "uploaded": [
    {"name": "a.txt", "size": 123, "mtime": 1761782400},
    {"name": "报告 2026.pdf", "size": 456789, "mtime": 1761782401}
  ]
}
```

`name` 是最终落盘名（`rename` 模式下可能与请求文件名不同）；`size` 为字节数，
`mtime` 为 Unix 秒。

### 冲突响应（`409`）

```json
{"error":"同名文件已存在","code":"conflict","existing":["a.txt","b.txt"]}
```

`existing` 按字典序去重。前端据此弹窗，用户选择后带 `mode=overwrite` 或
`mode=rename` 重新提交整批。覆盖目录时 `code` 为 `"target_is_dir"`。

### 错误码

| 状态码 | 场景 | 响应 |
|---|---|---|
| `400` | `dir` 越界/绝对路径/不存在/不是目录、`mode` 非法、文件名为空或含 `/ \`、隐藏名（点开头）、Windows 保留名、无 `file` 字段、超过 64 个文件 | `{"error":"..."}` |
| `401` | 认证启用但未登录/会话无效 | `{"error":"需要访问口令"}` |
| `403` | 未开启上传开关、隐藏目录或 `.FileServer`、目录只读 | `{"error":"..."}` |
| `409` | `ask` 模式同名冲突 / `overwrite` 目标是目录 | `{"error":"...","code":"conflict|target_is_dir","existing":[...]}` |
| `413` | 单文件或请求体超过上限 | `{"error":"单个文件超过大小上限"}` |
| `500` | 临时文件创建/写入/落位失败（磁盘满、权限等） | `{"error":"..."}` |

### 安全边界与副作用

- **路径**：沿用 `safePath`（拒绝 `..`、绝对路径、符号链接逃逸）+ `pathWithin`
  二次校验；目标目录解析后必须仍在服务根目录内。文件名不允许路径分隔符，
  因此不能借文件名逃逸。
- **禁止写入**：隐藏文件/目录（点开头，无论 `--hidden`）、保留目录 `.FileServer`
  及其大小写变体、Windows 保留设备名（`CON`/`NUL` 等）、UTF-8 超过 255 字节的名称。
- **原子写**：每个文件先写入目标目录内的 `.upload-*` 临时文件，全部校验通过后再
  `rename` 到最终名；任一步失败都会删除尚未落位的临时文件，不留半截文件。
  POSIX 覆盖为原子 `rename`；Windows 的 `rename` 不能覆盖已存在文件，`overwrite`
  模式会先删除目标再改名（该模式下存在极小的非原子窗口）。
- **并发**：`ask` 与 `rename` 在落位前检查目标是否存在；若本地其他进程在检查与
  `rename` 之间创建同名文件，仍可能被替换（与既有文本写入相同的残余 TOCTOU 面）。
- **不覆盖目录**：任何模式下目标为文件夹都拒绝（`409`），不会删除目录。
- **副作用**：成功后失效目录列表缓存；不修改任何其他文件。

## 文件管理

重命名、移动、复制、删除（回收站）与回收站操作。**仅登录会话可用**：即使认证未启用
（`--auth-mode none`），这些端点也一律返回 `401`，不存在匿名文件管理。前端在
`/api/info` 的 `manage=false` 时不渲染选择/操作入口。

- 所有路径均为服务根目录下的相对路径，服务端拒绝 `..`、绝对路径、盘符（Windows）、
  `.FileServer` 与隐藏项、以及指向根目录外的符号链接；`../` 等越界路径返回 `400`。
- 单批最多 **200** 项；超限返回 `400`，需分批操作。
- 批量操作逐项执行并返回 `{moved|copied|restored, failed}`；部分失败仍是 `200`，
  `failed` 里逐项给出路径与中文原因，不会整体静默失败。
- 同名冲突策略：`ask`（默认，整批 `409` 等前端询问）、`overwrite`（覆盖同名文件；
  同名目录整体替换，失败自动回滚备份）、`rename`（生成 `name (1).ext`）。
- 副作用：成功后失效目录列表缓存；服务端日志只记录操作类型、数量、结果、会话短 id
  与来源 IP，不记录文件名/内容。
- 回收站条目 id 为字符串（`YYYYMMDD-HHMMSS-<8位十六进制>`），客户端必须按字符串处理。

### 公共请求头

```bash
# 先登录拿 token（认证启用时）
TOKEN=$(curl -s -X POST http://127.0.0.1:8080/api/auth/login \
  -H 'Content-Type: application/json' \
  -d '{"password":"secret123","remember":true}' | sed -n 's/.*"token":"\([^"]*\)".*/\1/p')
```

### POST /api/rename

重命名当前目录内的文件或文件夹；只改名，不移动位置。

- 请求体：`{path, name}`。
  - `path`：源相对路径（必填）。
  - `name`：新名称（必填），沿用跨平台命名校验：非空、不含 `/ \` 与 NUL、
    不以 `.` 开头、不含控制字符与 Windows 非法字符、非保留设备名、UTF-8 ≤ 255 字节。
- 成功：`200 {ok:true, path, name}`，`path` 为新相对路径。
- 冲突：同目录已有同名项 `409 {"error":"同名文件或文件夹已存在"}`。
- 错误：非法名称/越界路径 `400`；源目录只读 `403`；源不存在 `404`。

```bash
curl -s -X POST http://127.0.0.1:8080/api/rename \
  -H "Authorization: Bearer $TOKEN" -H 'Content-Type: application/json' \
  -d '{"path":"/docs/old.md","name":"new.md"}'
# {"name":"new.md","ok":true,"path":"/docs/new.md"}
```

### POST /api/move

把一个或多个条目移动到目标目录。目标不能是条目自身或其子目录。

- 请求体：`{paths, dir, mode?}`；`mode` 也可用查询串 `?mode=ask|overwrite|rename`，查询串优先。
  - `paths`：源相对路径数组（1..200）。
  - `dir`：目标目录相对路径，必须存在且可写。
  - `mode`：默认 `ask`。
- 成功：`200 {ok, moved:[{from,to}], failed:[{path,error}]}`。
- 冲突（`ask`）：`409 {"error":"目标目录已有同名项","code":"conflict","existing":[...]}`，
  本次不做任何移动；前端选择后用 `overwrite` 或 `rename` 重试。
- 错误：越界路径/目标是自身子目录/批量超限/同一批次重名目标 `400`；目标只读 `403`。

```bash
curl -s -X POST 'http://127.0.0.1:8080/api/move?mode=rename' \
  -H "Authorization: Bearer $TOKEN" -H 'Content-Type: application/json' \
  -d '{"paths":["/a.txt","/docs"],"dir":"/backup"}'
# {"ok":true,"moved":[{"from":"/a.txt","to":"/backup/a.txt"}],"failed":[]}
```

### POST /api/copy

复制文件或目录到目标目录；目录递归复制，保留目录结构、权限位与修改时间。

- 请求体/响应/冲突规则与 `POST /api/move` 相同，成功字段为 `copied`。
- 复制符号链接不跟随（逐项失败 `不支持复制符号链接`）；目标位于源目录内返回 `400`。
- 单文件同样保留 mtime。

```bash
curl -s -X POST http://127.0.0.1:8080/api/copy \
  -H "Authorization: Bearer $TOKEN" -H 'Content-Type: application/json' \
  -d '{"paths":["/photos"],"dir":"/backup"}'
# {"ok":true,"copied":[{"from":"/photos","to":"/backup/photos"}],"failed":[]}
```

### POST /api/delete

把条目移入回收站（**不是物理删除**），可恢复。

- 请求体：`{paths}`（1..200）。
- 成功：`200 {ok, moved:[{path,id}], failed:[{path,error}]}`；`id` 为回收站条目 id（字符串）。
- 每条目拥有独立 id 目录，原相对路径完整保留在 `.FileServer/trash/<id>/` 下。
- 错误：任一路径越界/指向 `.FileServer`/隐藏项 → 整批 `400`；不存在的条目进入 `failed`。
- 副作用：文件从原位置消失并出现在 `GET /api/trash`；普通浏览/搜索/缩略图不索引回收站。

```bash
curl -s -X POST http://127.0.0.1:8080/api/delete \
  -H "Authorization: Bearer $TOKEN" -H 'Content-Type: application/json' \
  -d '{"paths":["/docs/a.txt","/docs/b.txt"]}'
# {"ok":true,"moved":[{"id":"20261030-153045-a1b2c3d4","path":"/docs/a.txt"}],"failed":[]}
```

### GET /api/trash

列出回收站条目及总占用。

- 成功：`200 {items:[{id,path,name,kind,is_dir,size,deleted_at}], count, total_size}`。
  - `path` 为原相对路径；`deleted_at` 为 Unix 秒；`size` 对目录递归统计。
  - 按删除时间倒序。
- 回收站为空时 `items:[]`、`count:0`、`total_size:0`。

```bash
curl -s http://127.0.0.1:8080/api/trash -H "Authorization: Bearer $TOKEN"
# {"count":1,"items":[{"id":"...","path":"/docs/a.txt","name":"a.txt","kind":"text","is_dir":false,"size":123,"deleted_at":1761782400}],"total_size":123}
```

### POST /api/trash/restore

把回收站条目恢复到原位置（父目录不存在时自动创建）。

- 请求体：`{ids, mode?}`；`mode` 也可用查询串，取值 `ask`（默认）| `rename`。
- 成功：`200 {ok, restored:[{id,path,from}], failed:[{path,error}]}`。
- 原位置已有同名项且 `mode=ask`：`409 {code:"conflict", existing:[原路径...]}`，本次不恢复；
  前端询问后用 `mode=rename`（自动改名）重试或取消。
- 错误：id 非法/越界/元信息路径非法 `400`；条目不存在进入 `failed`。

```bash
curl -s -X POST http://127.0.0.1:8080/api/trash/restore \
  -H "Authorization: Bearer $TOKEN" -H 'Content-Type: application/json' \
  -d '{"ids":["20261030-153045-a1b2c3d4"],"mode":"ask"}'
# {"ok":true,"restored":[{"from":"/docs/a.txt","id":"...","path":"/docs/a.txt"}],"failed":[]}
```

### POST /api/trash/empty

彻底删除回收站条目（不可恢复）。

- 请求体：`{ids?, all?}`。
  - `ids` 非空：只删除指定条目（1..200）。
  - `ids` 为空、缺省或 `all=true`：清空整个回收站。
- 成功：`200 {ok, deleted:[id...], failed:[{path,error}]}`。
- 错误：id 格式非法 `400`；条目已不存在进入 `failed`。

```bash
curl -s -X POST http://127.0.0.1:8080/api/trash/empty \
  -H "Authorization: Bearer $TOKEN" -H 'Content-Type: application/json' \
  -d '{"all":true}'
# {"deleted":["20261030-153045-a1b2c3d4"],"ok":true,"failed":[]}
```

### 文件管理错误码

| 状态码 | 场景 | 响应 |
|---|---|---|
| `400` | 路径为 `..`/绝对路径/越界链接、`.FileServer` 或隐藏项、非法名称、目标不是目录、移动到自身子目录、单批超过 200、`mode` 非法 | `{"error":"..."}` |
| `401` | 未登录、会话无效，或认证未启用（不存在登录会话） | `{"error":"需要登录后管理文件"}` |
| `403` | 目标/源目录只读；回收站条目路径越界 | `{"error":"..."}` |
| `404` | 重命名的源不存在 | `{"error":"源路径不存在"}` |
| `409` | 同名冲突（`ask`）；重命名目标已存在 | `{"error":"...","code":"conflict","existing":[...]}` |
| `413` | 请求体超过 1 MiB（管理接口只接受短 JSON） | `{"error":"请求体过大"}` |
| `500` | 磁盘/权限等底层错误 | `{"error":"操作失败"}` |

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
