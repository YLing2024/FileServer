# 构建与测试

## 1. 环境

- Go（首次构建自动下载工具链到 `.tools\`，免安装；也可用系统 Go）。
- make（GNU Make，Linux/macOS/WSL 的统一入口；Windows 用 `build.bat` 等价命令）。
- golangci-lint v2.14.0（可选，本地复现 CI 的 lint；CI 会自动安装同版本）。
- ffmpeg：可选组件，测试视频生成与 GPU 转码需要。
- Python 3.12 + Playwright（浏览器端到端测试）。

## 2. 构建与校验

统一入口是根目录 `Makefile`（Linux / macOS / WSL）；Windows 侧用 `build.bat` 走等价命令。
交叉编译目标固定 `windows/amd64`（本项目运行目标是 Windows exe）。

```bash
make check                 # fmt-check + 交叉 vet + 交叉 build + go test ./...（提交前必跑）
make fmt                   # gofmt -w .
make fmt-check             # 断言无未格式化文件
make vet                   # GOOS=windows GOARCH=amd64 go vet ./...
make build                 # 交叉构建 dist/FileServer.exe
make test                  # go test ./...
make cover                 # 覆盖率
make dist VERSION=1.2.3    # 发布构建：注入版本 / commit / 构建时间
```

Windows 侧：

```bat
build.bat                         rem 首次下载工具链（如需）+ go vet + go build（不再跑 go mod tidy）
release.ps1 -Version 1.2.3        rem 构建注入版本的 exe + lite/full zip + dist\SHA256SUMS.txt
```

产物：`dist\FileServer.exe`（单文件，~8MB），内含注入的版本/commit/构建时间
（`FileServer.exe --version` 可查；`/api/health` 亦返回同一版本信息）。

### CI

`.github/workflows/ci.yml`（push / PR 触发）三个 job：

- **test**（`windows-latest`，主力）：`gofmt -l .` 断言为空 → `go vet ./...` →
  `go test -race ./...` → `go build ./...`。Windows 是原生环境，所有包都能编译、测试才全量。
- **cross**（`ubuntu-latest`）：`GOOS=windows GOARCH=amd64 go vet/build` + `gofmt` 断言。
- **lint**（`ubuntu-latest`，`GOOS=windows`）：`golangci-lint` v2.14.0（配置见 `.golangci.yml`）。

`.github/workflows/release.yml`：打 `v*` tag 时在 `windows-latest` 上构建注入版本的 exe，
产出 `FileServer-lite-<v>.zip` 与 `SHA256SUMS.txt`，创建 GitHub Release。
**ffmpeg 二进制不进仓库 / CI**（体积与许可）：CI 只产 lite 包，full 包由本机
`release.ps1` 生成。

> 本仓库已把 Windows 专属 syscall（进程优先级等）隔离进 `internal/platform/`，
> 因此 Linux 侧现在也能 `go vet ./...` / `go test ./...` 全部包；CI 的 test job
> 仍选 Windows 原生 runner，以确保 Windows 行为被真实覆盖。


## 3. 发布部署（目标机）

```powershell
# 1. 停旧服务
Get-Process FileServer -ErrorAction SilentlyContinue | Stop-Process -Force
Get-Process ffmpeg -ErrorAction SilentlyContinue | Stop-Process -Force

# 2.（可选）清缓存：目标目录\.FileServer

# 3. 拷贝 exe 与 ffmpeg 组件
Copy-Item dist\FileServer.exe <目标目录>\      # 例：D:\共享\FileServer.exe
# ffmpeg 放 exe 同目录 ffmpeg\ 子文件夹（full build 含 GPU 编码器）

# 4. 后台启动（无窗口）
Start-Process <目标目录>\FileServer.exe -WorkingDirectory <目标目录> -WindowStyle Hidden

# 5. 验证
(Invoke-WebRequest http://127.0.0.1:8080/api/list?path=/ -UseBasicParsing).StatusCode  # 200
```

部署后务必在**真实目录 + 有头浏览器**上验收（见下）。

## 4. 测试体系

### Go 测试

`internal/server/*_test.go`：
- 播放决策单测（`TestVideoInfoDecision`）：小文件→direct、大文件 moov 尾部→hls 等；
- 缩略图缓存/失败记忆、路径安全等单元测试。

```bash
make test          # go test ./...（提交前必跑；Windows 全量，其他平台亦全绿）
make cover         # 覆盖率
```

手工验收清单里的第一条由此升级为：**`make check` 全绿**（见 §2 与 AGENTS.md 铁律）。

### Playwright 端到端（scripts/）

所有脚本运行前需启动被测服务（默认 `http://127.0.0.1:8099`，目录指向仓库 testdata）：

```powershell
Start-Process dist\FileServer.exe -ArgumentList '--dir','H:\proj\FileServer\testdata','--port','8099','--no-qr' -WindowStyle Hidden
$env:PYTHONIOENCODING='utf-8'   # 控制台中文输出
```

| 脚本 | 验证内容 |
|---|---|
| `smoke_test.py` | 冒烟：列表/导航/下载/搜索 |
| `regression_test.py` | 回归：三层面包屑、12 视频缩略图（浏览器抽帧 + 失败降级）、播放器释放 |
| `nav_test.py` | URL 驱动导航（前进/后退/刷新恢复） |
| `mobile_test.py` | 移动端视口 + zip 打包下载 |
| `mobile_ui_check.py` | 移动端 UI 检查（布局/触控） |
| `special_e2e_test.py` | 特殊字符文件名端到端 |
| `frontthumb_test.py` | 前端抽帧模式（需**无 ffmpeg** 服务，见下） |
| `hls_e2e_test.py` | HLS 起播/seek（生成 HEVC/MKV 测试片） |
| `thumbspeed_test.py` | **缩略图速度验收**：大目录冷浏览出图速度 + 抽帧中播放不受影响 |
| `starvation_test.py` | **播放饥饿验收**：大目录浏览抽帧正忙时点开视频，起播快、播放不卡、缩略图恢复 |

`starvation_test.py`（真实目录验收，有头浏览器）：

```powershell
python scripts\starvation_test.py --base http://127.0.0.1:8080 --dir /大目录 --video 目标视频关键词
# --dir/--video 换成被测机器上的真实大目录与目标视频
```

`thumbspeed_test.py`（缩略图速度验收，有头浏览器）：

```powershell
python scripts\thumbspeed_test.py --base http://127.0.0.1:8080 --dir /大目录 --video 目标视频关键词
# 断言：首张缩略图 ≤8s、可见 ≥6 张、起播成功、播放推进 ≥9s/12s
```

断言：起播 ≤15s（实测 ~1s）、播放 15s 推进 ≥10s、播放期间 0 个新缩略图请求、
返回列表后缩略图请求恢复。

`frontthumb_test.py` 需要无 ffmpeg 服务（排除 PATH/LOCALAPPDATA 的 ffmpeg）：

```powershell
# 把 exe 拷到临时目录，用 cmd 包装清除 PATH/LOCALAPPDATA 后启动 8098
$env:BASE='http://127.0.0.1:8098'; python scripts\frontthumb_test.py
```

### 手工验收清单（每次改动播放/缩略图相关代码后）

1. `go vet` + `go build` + `go test ./internal/...` 全绿；
2. 全部 Playwright 套件（上表）通过；
3. 真实目录 + 有头浏览器：大目录浏览 → 抽帧进行中点开视频 → 起播秒级、播放无卡顿；
4. 部署后核对 exe 哈希一致（`Get-FileHash`）。

## 5. 常见问题

- **测试视频生成失败**：`hls_e2e_test.py` 用 PATH 里的 `ffmpeg` 生成测试片，
  确保 `ffmpeg` 在 PATH 或脚本同目录。
- **hevc/mkv 视频没有缩略图**：Chromium 无 HEVC 解码器，抽帧源加载失败保持图标
  ——浏览器能力限制，与服务端无关（在线播放不受影响，走 HLS 转码）。
- **改前端不生效**：静态资源 `no-store` + exe 内嵌，需重新编译部署。
