# Changelog

本项目所有值得记录的变更都写在这里。
格式遵循 [Keep a Changelog](https://keepachangelog.com/zh-CN/1.1.0/)，
版本号遵循 [语义化版本](https://semver.org/lang/zh-CN/)。

## [Unreleased]

### Added

- **新建空文件与新建文件夹**：新增 `POST /api/create`（`kind: file|dir`）——空文件不再限制扩展名
  （`.txt` 之外也能建，如 `.conf`、无扩展名文件），文件夹为单层创建（父目录须已存在）；
  前端顶部按钮改为通用「新建」弹窗，文件 / 文件夹二选一，仅文本白名单扩展名才显示「初始内容」。
- 构建期版本注入：`internal/version`（Version / Commit / BuildDate），CLI `--version`，
  健康检查接口 `GET /api/health` 返回 `{ok, version, commit, go, platform}`。
- 一条命令的校验入口 `Makefile`（`fmt` / `fmt-check` / `vet` / `build` / `dist` / `test` / `cover` / `check`）。
- CI：`.github/workflows/ci.yml`（test / cross / lint 三个 job）与 `release.yml`（打 `v*` tag 产 lite 包 + SHA256SUMS）。
- `golangci-lint` 配置 `.golangci.yml`（只开正确性规则）与 `.editorconfig`、`.gitattributes`、dependabot。
- `web/third-party.md` 登记内嵌前端第三方库来源与校验和。

### Changed

- 移动端响应式：窄屏/触屏指针（`pointer: coarse`）下工具栏与面包屑按钮 ≥44px、视图切换等
  图标按钮 ≥32×32、搜索框与表单控件 ≥44px 高且字号 ≥16px（防 iOS 聚焦放大整页）、相邻操作
  间距 ≥8px；登录/新建弹窗窄屏按钮单列；可见小字号抬到 ≥12px。断点媒体查询不覆盖 ≥992px
  细指针桌面，桌面观感逐像素不变。新增 `scripts/responsive_e2e_test.py` 机检。
- 写入范围放开：由「仅新建/编辑文本文件」扩为「新建空文件 + 新建文件夹 + 编辑文本文件」；
  删除 / 重命名 / 移动 / 上传仍不做。`POST /api/text/new` 作为兼容入口保留白名单限制，
  但与新端点共用同一套命名校验与写入内核。
- 新建名称校验收紧为跨平台交集：拒绝首尾空白、`.`/`..`、路径分隔符、控制字符、
  Windows 非法字符 `< > : " | ? *`、点开头（隐藏）、点或空格结尾、Windows 保留设备名
  （`CON`/`CON.txt` 等）、以及 UTF-8 超过 255 字节的名称。
- 模块路径正名为 `github.com/YLing2024/FileServer`（原 `fileserver`）。
- `build.bat` 去掉构建期的 `go mod tidy` 副作用，改为 `go vet` + `go build`。
- `release.ps1 -Version` 现在真的把版本注入 exe，并输出 `dist/SHA256SUMS.txt`。
- Windows 专属 syscall 隔离进 `internal/platform/`，`internal/server` 在非 Windows 上也
  可编译与测试。

### Fixed

- **提示条（toast）此前从不显示**：`#toast` 初始带 `.hidden`，而全局规则 `.hidden { display: none !important }` 会压过 `.toast.show` 的透明度动画 —— `toast()` 只加 `.show`、从不摘 `.hidden`，于是所有「已保存 / 已创建 / 失败」提示对用户完全不可见（功能本身正常）。修复：显示时移除 `.hidden`，隐藏时补回。该缺陷从早期版本即在，由新增的 `scripts/create_e2e_test.py` 首次跑出。
- gofmt 全仓库对齐；修正 errcheck / ineffassign / staticcheck / unused 报告的正确性告警。

## [1.0.0] - 2026-08-15

### Added

- Windows 局域网文件服务器：双击即用，控制台打印访问地址与二维码。
- 只读浏览 / 预览 / 下载服务目录（或 `--dir` 指定目录），支持隐藏文件、搜索、zip 打包。
- 视频在线播放：原生格式直链秒开；可选 ffmpeg 实时转 H.264（GPU 优先）播放冷门格式；
  怪封装 MP4 走 HLS copy 重封装提速。
- 访问口令（`--auth user:pass`，默认关闭）、自研纯 Go 二维码、内嵌前端。

[Unreleased]: https://github.com/YLing2024/FileServer/compare/v1.0...HEAD
[1.0.0]: https://github.com/YLing2024/FileServer/releases/tag/v1.0
