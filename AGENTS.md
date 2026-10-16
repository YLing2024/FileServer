# AGENTS.md — FileServer（跨平台文件服务器）

> 维护本仓库前先读本文件。README.md 是面向用户的介绍；本文件是面向维护者的入口。
> **本仓库已有深度文档：改任何子系统前先读 `doc/` 下对应文档**（见下表），不要凭直觉改。

## 这个项目是什么

跨平台（Windows / Linux / macOS）文件服务器，编译成**单文件可执行**（~8MB）：Windows 双击即用，Linux/macOS 命令行或 systemd 运行；控制台打印访问地址 + 二维码，同一网络下任意设备只读浏览/预览/下载服务目录（或 `--dir` 指定目录）。可选开启密码 + TOTP 双因子认证；支持新建/编辑白名单文本文件。

核心卖点：**视频在线播放**——原生格式直链秒开；MKV/HEVC/AVI/RMVB 等冷门格式开启后由 ffmpeg 实时转 H.264 边转边播；怪封装 MP4 走 HLS copy 重封装实现秒开。永久规整（mdat 合并 / moov 前置）已拆分到独立项目 mp4norm。

## 技术栈

- **Go 1.25**（`go.mod` 仅依赖 `golang.org/x/image`，其余全部标准库/自研）
- 前端是**内嵌**在 exe 里的静态资源（`//go:embed web`，见 `internal/server/server.go`），无独立构建
- QR 码是自研纯 Go 实现（`internal/qrcode/`，不引第三方库）
- ffmpeg 为**可选外部组件**（放 exe 同目录 `ffmpeg/` 子文件夹，用于 GPU 转码/冷门格式）

## 目录结构

```
cmd/fileserver/main.go        # 入口：flag 解析、监听地址、控制台地址/二维码、优雅退出、认证重置
internal/server/              # HTTP 服务主体
├── server.go                 # 路由装配（单表 routes）+ go:embed web
├── list.go / search.go / zip.go
├── path.go                   # 路径解析与安全（穿越防护，有 path_test / path_posix_test）
├── text.go                   # 文本新建/预览/编辑（白名单 + 原子写 + 版本校验）
├── auth_handlers.go          # 认证 HTTP 层（引导/登录/会话/门禁）
├── procs.go                  # POSIX 子进程组跟踪与启动孤儿清理
├── security_test.go
├── hls.go / ffmpeg.go / prewarm.go                  # 视频管线：HLS 会话、转码、预热
├── thumb.go                  # 缩略图
└── lanip.go                  # 局域网地址探测
internal/auth/                # 自研 Argon2id/BLAKE2b、TOTP、会话与限速
internal/platform/            # OS 差异（platform_windows.go / platform_other.go）
internal/qrcode/              # 纯 Go QR 编码 + 终端渲染（+ TOTP 绑定二维码 SVG）
web/                          # 内嵌前端（播放器、灯箱、缩略图抽帧、认证、文本编辑）
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
| 路径穿越、符号链接、写入范围（文本新建/编辑有限放开） | `doc/security.md` |
| 构建、发布打包、测试体系 | `doc/build-and-test.md` |

## 命令

统一校验入口是根目录 `Makefile`（Linux / macOS / WSL）；Windows 开发机用 `build.bat`
（等价命令，首次运行会把 Go 工具链下载到 `.tools\`，免安装）。

```bash
make check        # 提交前必跑：fmt-check + 三平台 vet + 三平台 build + go test ./...（exit 0）
make fmt          # gofmt -w .
make vet          # GOOS=windows GOARCH=amd64 go vet ./...
make vet-all      # 三平台交叉 go vet（linux/darwin amd64+arm64、windows amd64）
make build        # 交叉构建 dist/FileServer.exe（windows/amd64）
make build-all    # 构建五平台产物到 dist/FileServer-<os>-<arch>[.exe]
make test         # go test ./...
make test-race    # go test -race ./...
make dist VERSION=1.2.3   # 发布构建（注入版本/commit/构建时间）
```

```powershell
build.bat                                        # 下载工具链（如需）+ go vet + go build
release.ps1 -Version 1.2.3                       # 注入版本，产 lite/full zip + SHA256SUMS.txt
```

E2E（Python 3.12 + Playwright，需先跑起服务）：

```bash
python scripts/smoke_test.py        # 另有 regression_test.py / hls_e2e_test.py / special_e2e_test.py …
```

CI（`.github/workflows/ci.yml`）在 push / PR 上跑：`test` 三平台矩阵（ubuntu / macos / windows，均真跑 `go test`，ubuntu/windows 带 `-race`）、`cross`（ubuntu，五目标交叉 vet/build）、`lint`（golangci-lint v2.14.0，linux+windows 两个 build tag）。`.github/workflows/release.yml` 在 `v*` tag 上产 lite 包。
平台专属 syscall 已隔离进 `internal/platform/`，Linux/macOS 侧也能 `go vet/test ./...` 全部包。

## 设计约定

- **浏览即读，写入范围有限**：浏览 / 预览 / 下载路径不得写用户目录；缓存与临时文件统一放目标目录下的 `.FileServer/`。唯一的用户文件写入是「新建文本文件」与「编辑文本文件」两条路径（白名单扩展名、2 MiB 上限、原子写、版本校验防并发覆盖），删除/重命名/移动/上传/建目录一律不做。内置规整化已移除，服务不再改写视频原文件；永久规整（mdat 合并 / moov 前置）改用独立项目 mp4norm。
- **访问口令默认不启用**（用户 2026-09-29 明确决定）：本项目自用、局域网风险可控，保持「不传任何认证参数即敞开」。认证形态为 `--auth-mode none|totp|password|password+totp`；`--auth user:pass` 是 `password` 模式的兼容用法。**不要**擅自改成默认开启或首启随机口令。
- **认证硬性约定**：口令派生的 Argon2id 与 TOTP 全部**自研、只用标准库**（不引 x/crypto）；口令/TOTP 恒定时间比较；TOTP 时间步重放防护；失败按 IP 阶梯限速；凭据只存 `.FileServer/auth.json`（0600，token 只存 sha256），日志绝不打印口令/token/验证码；认证相关响应统一文案，不区分失败原因。改认证务必跑 `internal/auth` 的 RFC 向量测试。
- **零安装**：不引入需要额外安装的运行时依赖；能自研就自研（qrcode 就是先例）。
- **平台差异隔离在 `internal/platform/`**：`*_windows.go` / `*_other.go` 成对出现，改一个必须同步另一个（打开浏览器、控制台能力、进程终止三类差异逐平台明确）。
- **进程安全**：Windows 用 Job Object 防孤儿；POSIX 用独立进程组 + 启动时清理遗留 ffmpeg（见 `procs.go`）——这块逻辑改动务必回归。
- **前端内嵌**：改 `web/` 后必须重新编译 exe 才生效，没有独立热更新。

## 已知坑

- **提交前必须 `make check` 全绿**（fmt-check + 三平台交叉 vet/build + `go test ./...`），CI 会拦——不绿不许提交。`internal/` 下的 `_test.go` 覆盖播放决策、路径安全、缩略图、认证等，是主要防线；提交信息一律英文 conventional commits。
- 永久规整**已拆分到独立项目 mp4norm**：FileServer 不再改动用户原文件，`.FileServer\backup\` 仅为历史遗留。
- 冷门格式 / GPU 转码相关的行为**强依赖 ffmpeg 是否同目录**，没有 ffmpeg 时相关分支应优雅降级（保持文件图标），不要报错。
- **跨平台均为一等目标**：Windows / Linux / macOS 都要真实可用。改动涉及子进程、打开浏览器、控制台/二维码时，务必核对 `internal/platform/` 两个文件；macOS 上用 `open`（非 `xdg-open`）。真实验收建议在目标平台有头浏览器 + 真实大文件目录上做。
- **认证默认关闭**：`--auth-mode` 不传即敞开；改动认证不得改变该默认语义。
- `testdata/*.mp4`、`dist/`、`.tools/`、`*.exe`、`.FileServer/` 均被 `.gitignore` 忽略，不要提交。
- 二维码实现是自研的，改动 `internal/qrcode/` 后要跑 `compare_test.go`（与参考实现比对）。

