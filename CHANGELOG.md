# Changelog

本项目所有值得记录的变更都写在这里。
格式遵循 [Keep a Changelog](https://keepachangelog.com/zh-CN/1.1.0/)，
版本号遵循 [语义化版本](https://semver.org/lang/zh-CN/)。

## [Unreleased]

### Added

- **分享链接（公开访问）**：新增 `POST /api/share`、`GET /api/share`、
  `POST /api/share/revoke`、`DELETE /api/share/record`，以及公开接口
  `GET /api/public/share/{token}/{info,list,download,zip,preview}` 与
  `POST /api/public/share/{token}/verify`、独立页面 `GET /s/{token}`。支持文件/目录分享、
  1/7/30 天或永久、4 位提取码（30 分钟 cookie）、下载次数上限、预览开关；记录存
  `.FileServer/shares.json`（0600 原子写，提取码只存哈希），撤销立即失效；公开接口以
  分享根为边界拒绝 `..`/越界符号链接，下载强制附件并带 `noindex`。前端新增创建/结果/
  管理弹窗与独立公开页（原生 JS，不依赖管理页脚本）。
- **文件夹上传、分片断点续传与秒传（仅登录会话）**：新增
  `POST /api/upload/chunk`、`GET /api/upload/status`、`POST /api/upload/complete`、
  `POST /api/upload/instant`、`POST /api/upload/mkdir`。前端支持拖入/选择文件夹
  （`webkitGetAsEntry` + `webkitdirectory`，保留结构与空目录，单次上限 2000 文件，
  顶层目录冲突按目录整体三选一）；>8 MiB 文件按 8 MiB 分片，`upload_id` 由前 1 MiB +
  大小 + 名称派生，中断后重新选择同一文件只补缺片；浏览器流式 SHA-256 先查秒传，
  命中用 `.FileServer/blobs/<前缀>/<sha256>` 硬链接落位，删除只减少链接、无引用后回收。
  服务端 24 小时清理残缺分片，启动与每日清理同时回收孤儿 blob；单文件仍可走原
  `/api/upload`（可选 `sha256` 登记索引）。文档新增 `doc/api.md` 端点说明、前端/
  安全边界与组件登记。
- **文件管理（仅登录会话）**：新增 `POST /api/rename`、`POST /api/move`、
  `POST /api/copy`、`POST /api/delete`、`GET /api/trash`、
  `POST /api/trash/restore`、`POST /api/trash/empty`。删除只移入
  `.FileServer/trash/<id>/`（保留原相对路径，可恢复/彻底删除/清空）；移动与复制支持
  `ask|overwrite|rename` 同名策略，目录复制递归保留结构与 mtime；批量逐项返回
  `moved|copied|restored` 与 `failed`，部分失败不静默。全部路径做根目录/越界链接/
  `.FileServer` 校验，单批上限 200；未登录（包括认证关闭）一律 `401`。前端新增多选、
  右键菜单、`Delete`/`F2`/`Ctrl+A`/`Esc` 快捷键、移动端长按选择、目录树选择器和回收站页面。
- **文件上传（可选，默认关闭）**：新增 `POST /api/upload`（multipart，可多文件，
  `mode=ask|overwrite|rename`），仅当 `--allow-upload` / `FILESERVER_ALLOW_UPLOAD=1`
  时开放；单文件上限 `--max-upload-mb`（默认 1024 MiB），超限 413。前端工具栏新增
  「上传」按钮与整页拖拽投放层，逐文件进度/取消/重试、同名冲突弹窗（覆盖/自动改名/
  取消，整批生效）、完成后刷新并高亮、上传中离开确认。服务端先写 `.upload-*`
  临时文件再原子 rename，失败清理；路径沿用穿越/符号链接/隐藏目录防护，文件名保留
  中文与空格但禁止分隔符、隐藏名与 `.FileServer`。
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
- 写入范围放开：由「仅新建/编辑文本文件」扩为「新建空文件 + 新建文件夹 + 编辑文本文件」，
  并新增默认关闭的可选上传；删除 / 重命名 / 移动改为仅登录会话可用的文件管理（删除进回收站）。
  `POST /api/text/new` 作为兼容入口保留白名单限制，但与新端点共用同一套命名校验与写入内核。
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
