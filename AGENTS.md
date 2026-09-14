# AGENTS.md — FileServer（局域网文件服务器）

> AI 编码代理进入本仓库先读本文件。README.md 是面向用户的介绍；本文件是面向代理的入口。
> **本仓库已有深度文档：改任何子系统前先读 `doc/` 下对应文档**（见下表），不要凭直觉改。

## 这个项目是什么

Windows 局域网文件服务器，编译成**单文件 exe**（~8MB）：双击即用，控制台打印局域网访问地址 + 二维码，同 WiFi 下任意设备只读浏览/预览/下载 exe 所在目录（或 `--dir` 指定目录）。

核心卖点：**视频在线播放**——原生格式直链秒开；MKV/HEVC/AVI/RMVB 等冷门格式开启后由 ffmpeg 实时转 H.264 边转边播；怪封装 MP4 走 HLS copy 重封装或手动"规整化"实现秒开。

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
├── hls.go / ffmpeg.go / prewarm.go / normalize.go   # 视频管线：HLS 会话、转码、预热、怪封装规整化
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
| 路径穿越、符号链接、只读保证 | `doc/security.md` |
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

- **只读保证**：任何写操作都不得发生在用户目录里；项目自身的缓存/临时文件统一放目标目录下的 `.FileServer/`。
- **零安装**：不引入需要额外安装的运行时依赖；能自研就自研（qrcode 就是先例）。
- **平台差异隔离在 `internal/platform/`**：`*_windows.go` / `*_other.go` 成对出现，改一个必须同步另一个。
- **进程安全**：ffmpeg 子进程用 Job Object 防孤儿，abandon 即终止（taskkill 兜底），空闲 10 分钟回收——这块逻辑改动务必回归。
- **前端内嵌**：改 `web/` 后必须重新编译 exe 才生效，没有独立热更新。

## 已知坑

- **`go vet` / `go test` 必须全绿再提交**：`internal/` 下有 10 个 `_test.go`，覆盖播放决策、路径安全、规整化、缩略图，是主要防线。
- 规整化会**改动用户原文件**：先自动备份（可恢复/删除），改动这段逻辑前先读 `doc/video-pipeline.md` 与 `doc/cache.md`。
- 冷门格式 / GPU 转码相关的行为**强依赖 ffmpeg 是否同目录**，没有 ffmpeg 时相关分支应优雅降级（保持文件图标），不要报错。
- 服务器（本仓库所在的 Linux VPS）**不是运行目标**：`internal/platform/platform_other.go` 让代码在 Linux 上能编译/测试，但真实验收要在 Windows + 有头浏览器 + 真实大文件目录上做。
- `testdata/*.mp4`、`dist/`、`.tools/`、`*.exe`、`.FileServer/` 均被 `.gitignore` 忽略，不要提交。
- 二维码实现是自研的，改动 `internal/qrcode/` 后要跑 `compare_test.go`（与参考实现比对）。

## 项目记忆（PROJECT_MEMORY.md）

`PROJECT_MEMORY.md` 用于保存可演进的项目记忆；`AGENTS.md` 保持为稳定的硬规则。处理非简单任务，或任务涉及既有业务判断、播放/转码行为回归、API 参数、历史 bug、产品/UI 习惯时，先按关键词查阅 `PROJECT_MEMORY.md`。

- Agent 可以**自迭代** `PROJECT_MEMORY.md`：当前任务中确认了可复用、长期有效的项目经验后，应追加或更新对应条目。
- 每条记忆必须写明日期、适用范围和可追溯证据（源码路径/行号、有头浏览器实测结果对照、提交或验证结果）；可能过期的结论须标明复核条件。
- 不记录临时猜测、单次偶发现象、未经验证的产品判断、敏感信息或与项目无关的个人偏好。
- `PROJECT_MEMORY.md` 与 `AGENTS.md` 冲突时，以 `AGENTS.md` 为准；只有经明确确认的、长期稳定且必须遵守的规则，才能由用户决定升级到 `AGENTS.md`。
- 本文件已在 `.gitignore` 中忽略：**只存本机，不提交、不推送**。
