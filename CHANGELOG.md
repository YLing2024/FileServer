# Changelog

本项目所有值得记录的变更都写在这里。
格式遵循 [Keep a Changelog](https://keepachangelog.com/zh-CN/1.1.0/)，
版本号遵循 [语义化版本](https://semver.org/lang/zh-CN/)。

## [Unreleased]

### Added

- 构建期版本注入：`internal/version`（Version / Commit / BuildDate），CLI `--version`，
  健康检查接口 `GET /api/health` 返回 `{ok, version, commit, go, platform}`。
- 一条命令的校验入口 `Makefile`（`fmt` / `fmt-check` / `vet` / `build` / `dist` / `test` / `cover` / `check`）。
- CI：`.github/workflows/ci.yml`（test / cross / lint 三个 job）与 `release.yml`（打 `v*` tag 产 lite 包 + SHA256SUMS）。
- `golangci-lint` 配置 `.golangci.yml`（只开正确性规则）与 `.editorconfig`、`.gitattributes`、dependabot。
- `web/third-party.md` 登记内嵌前端第三方库来源与校验和。

### Changed

- 模块路径正名为 `github.com/YLing2024/FileServer`（原 `fileserver`）。
- `build.bat` 去掉构建期的 `go mod tidy` 副作用，改为 `go vet` + `go build`。
- `release.ps1 -Version` 现在真的把版本注入 exe，并输出 `dist/SHA256SUMS.txt`。
- Windows 专属 syscall 隔离进 `internal/platform/`，`internal/server` 在非 Windows 上也
  可编译与测试。

### Fixed

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
