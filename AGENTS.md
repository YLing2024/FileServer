# AGENTS.md — FileServer（局域网文件服务器）

> 维护本仓库前先读本文件。README.md 是面向用户的介绍；本文件是面向维护者的入口。
> **本仓库已有深度文档：改任何子系统前先读 `doc/` 下对应文档**（见下表），不要凭直觉改。

## 这个项目是什么

Windows 局域网文件服务器，编译成**单文件 exe**（~8MB）：双击即用，控制台打印局域网访问地址 + 二维码，同 WiFi 下任意设备只读浏览/预览/下载 exe 所在目录（或 `--dir` 指定目录）。

核心卖点：**视频在线播放**——原生格式直链秒开；MKV/HEVC/AVI/RMVB 等冷门格式开启后由 ffmpeg 实时转 H.264 边转边播；怪封装 MP4 走 HLS copy 重封装实现秒开。永久规整（mdat 合并 / moov 前置）已拆分到独立项目 mp4norm。

## 技术栈

- **Go 1.25**（`go.mod` 仅依赖 `golang.org/x/image`，其余全部标准库/自研）
- 前端是**内嵌**在 exe 里的静态资源（`//go:embed web`，见 `internal/server/server.go`），无独立构建
- QR 码是自研纯 Go 实现（`internal/qrcode/`，不引第三方库）
- ffmpeg 为**可选外部组件**（放 exe 同目录 `ffmpeg/` 子文件夹，用于 GPU 转码/冷门格式）

## 目录结构

```
cmd/fileserver/main.go        # 入口：flag 解析、局域网 IP 探测、控制台地址/二维码、优雅退出
internal/server/              # HTTP 服务主体
├── server.go                 # 路由装配 + go:embed web
├── list.go / search.go / zip.go
├── path.go                   # 路径解析与安全（穿越防护，有 path_test / path_posix_test）
├── security_test.go
├── hls.go / ffmpeg.go / prewarm.go                  # 视频管线：HLS 会话、转码、预热
├── thumb.go                  # 缩略图
└── lanip.go                  # 局域网地址探测
internal/platform/            # OS 差异（platform_windows.go / platform_other.go）
internal/qrcode/              # 纯 Go QR 编码 + 终端渲染
web/                          # 内嵌前端（播放器、灯箱、缩略图抽帧）
scripts/*.py                  # Python + Playwright 端到端/冒烟/回归测试
testdata/                     # 测试样本（videos / photos / docs / special / manyvideos / deep）
doc/                          # 项目深度文档（见下）
```

## 文档索引（改代码前必读对应的那篇）

| 改动范围 | 先读 |
|---|---|
| 总体架构、请求数据流、设计决策 | `doc/architecture.md` |
| 任何 HTTP 接口的增删改 | `doc/api.md` |
| 视频播放链路 / HLS / seek / 起播 | `doc/video-pipeline.md` |
| ffmpeg 集成、GPU 编码器探测、转码规格 | `doc/ffmpeg.md` |
| `.FileServer` 缓存生命周期 | `doc/cache.md` |
| 前端播放器、抽帧缩略图、移动端适配 | `doc/frontend.md` |
| 路径穿越、符号链接、写入范围（浏览即读，不写用户文件） | `doc/security.md` |
| 构建、发布打包、测试体系 | `doc/build-and-test.md` |

## 命令

开发机是 Windows（本项目产物是 Windows exe）。build.bat 会在首次运行时把 Go 工具链下载到 `.tools\`（免安装）：

```powershell
build.bat                                        # 下载工具链（如需）+ vet + build + test
.tools\go\bin\go.exe vet ./...
.tools\go\bin\go.exe build ./...
.tools\go\bin\go.exe test ./internal/...
.tools\go\bin\go.exe build -trimpath -ldflags "-s -w" -o dist\FileServer.exe .\cmd\fileserver
release.ps1 -Version 1.2.3                       # 打包 dist\FileServer-lite.zip / -full.zip
```

在有系统 Go 的机器上直接 `go vet ./... && go test ./internal/... && go build ./cmd/fileserver` 亦可。

E2E（Python 3.12 + Playwright，需先跑起服务）：

```bash
python scripts/smoke_test.py        # 另有 regression_test.py / hls_e2e_test.py / special_e2e_test.py …
```

## 设计约定

- **浏览即读，不写用户文件**：浏览 / 预览 / 下载路径不得写用户目录；缓存与临时文件统一放目标目录下的 `.FileServer/`。内置规整化已移除，服务不再改写用户原文件；永久规整（mdat 合并 / moov 前置）改用独立项目 mp4norm。
- **访问口令默认不启用**（用户 2026-09-29 明确决定）：本项目自用、只跑局域网、风险可控，所以保持「不传 `--auth` 即敞开」。**不要**擅自改成默认开启或首启随机口令；自带的就是 `--auth user:pass`（Basic Auth）这一档。
- **零安装**：不引入需要额外安装的运行时依赖；能自研就自研（qrcode 就是先例）。
- **平台差异隔离在 `internal/platform/`**：`*_windows.go` / `*_other.go` 成对出现，改一个必须同步另一个。
- **进程安全**：ffmpeg 子进程用 Job Object 防孤儿，abandon 即终止（taskkill 兜底），空闲 10 分钟回收——这块逻辑改动务必回归。
- **前端内嵌**：改 `web/` 后必须重新编译 exe 才生效，没有独立热更新。

## 已知坑

- **`go vet` / `go test` 必须全绿再提交**：`internal/` 下的 `_test.go` 覆盖播放决策、路径安全、缩略图等，是主要防线。
- 永久规整**已拆分到独立项目 mp4norm**：FileServer 不再改动用户原文件，`.FileServer\backup\` 仅为历史遗留。
- 冷门格式 / GPU 转码相关的行为**强依赖 ffmpeg 是否同目录**，没有 ffmpeg 时相关分支应优雅降级（保持文件图标），不要报错。
- 服务器（本仓库所在的 Linux VPS）**不是运行目标**：`internal/platform/platform_other.go` 让代码在 Linux 上能编译/测试，但真实验收要在 Windows + 有头浏览器 + 真实大文件目录上做。
- `testdata/*.mp4`、`dist/`、`.tools/`、`*.exe`、`.FileServer/` 均被 `.gitignore` 忽略，不要提交。
- 二维码实现是自研的，改动 `internal/qrcode/` 后要跑 `compare_test.go`（与参考实现比对）。

## 项目记忆（PROJECT_MEMORY.md）

**分工**：`AGENTS.md` 记**规则**（稳定、必须遵守）；`PROJECT_MEMORY.md` 记**记忆**（可演进、随事实更新）。
两者冲突时以 `AGENTS.md` 为准；只有经用户明确确认、且长期稳定的规则，才由用户决定升级进 `AGENTS.md`。
`PROJECT_MEMORY.md` 已被 `.gitignore` 拦截：**只存本机，不提交、不推送**。

### 什么时候写

- 读完代码 / 查完日志后，**确认了可复用、长期有效**的结论：API 契约与参数语义、数据模型与单位、踩坑的根因、
  产品与 UI 习惯、历史 bug 的判据（"见到 X 现象就查 Y"）。
- **任务收尾时必须回写**：本次确认了什么、推翻了什么、遗留了什么（写清复核条件）。
- **不要写**：临时猜测、单次偶发现象、未经验证的产品判断、敏感信息（密钥 / token / 口令 / 私有地址）、
  与项目无关的个人偏好、以及从代码一眼可见的常识。

### 每条记忆的字段（缺一不可）

```md
### YYYY-MM-DD · 主题（一句话）
- **结论**：一句话说清（可执行、可判断真假）。
- **适用范围**：哪个模块 / 接口 / 页面；**不适用**的情况也要写。
- **证据**：`路径:行号` / commit / 实测输出摘要（附可复现命令）。
- **复核条件**：什么情况下这条会失效（如"升级 Flutter 大版本后重测"）。
- **最后复核**：YYYY-MM-DD
```

### 迭代规则

1. **先查后写**：任务开始时按关键词（模块名 / 接口名 / 报错文本 / 表名）检索本文件；命中就按结论行事，
   并**把该条的「最后复核」更新为今天**（同一次任务只更新一次，不要刷日期）。
2. **更新优先于新增**：主题已有条目 → 就地改写（结论变了要写"曾认为 X，实测为 Y"），**不要追加重复条目**。
3. **失效即删**：结论被推翻、或复核条件已命中（代码已改 / 版本已升）→ 直接删掉或改写，不留"已废弃"堆积。
4. **合并同类**：同一模块超过 3 条相关记忆 → 合并成一节，只保留最新结论 + 关键证据。

### 容量与清理（硬约束）

- 文件上限 **200 行 / 12 KB**（以 `wc -c` 为准）。超限时按以下优先级淘汰：
  ① 已被代码或配置取代的（先删）→ ② 「最后复核」最久远的 → ③ 证据最弱的（只有结论、没有出处）。
- 单条记忆 **≤ 15 行**；细节过长就把细节留在代码注释 / `references/` 里，本文件只留结论与指针。
- **每次写入后顺手清理一次**（行数、体积、重复项、失效项），保证文件始终处于上限内。
- 清理若删掉仍有价值的内容，必须在提交说明或对话里说明，**不要静默丢弃**。

### 写法

- 写给下一个接手的维护者，不是写给用户：用最短的句子、最强的证据，先写结论再写理由。
- 结论要能被证伪：写"接口 X 的 `:id` 是数据库数字 id（`WHERE id = ?`）"，不要写"注意 id 类型"。
- 需要跨文件的长篇背景（架构选型、迁移过程）放 `references/` 或项目文档，这里只留一行指针。
