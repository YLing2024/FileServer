# 贡献指南

本仓库是 Windows 局域网文件服务器（Go，单文件 exe）。开发机通常也是 Windows，
但校验流程在 Linux / macOS / WSL 上等价可跑。

## 提交前必做

```bash
make check
```

一条命令跑完 `gofmt` 断言 + `GOOS=windows` 交叉 `vet`/`build` + `go test ./...`，
**必须 exit 0**。CI（`.github/workflows/ci.yml`）会重复同样的检查并拦下不合规提交。

常用子命令：

```bash
make fmt          # gofmt -w .
make vet          # GOOS=windows GOARCH=amd64 go vet ./...
make build        # 交叉构建 dist/FileServer.exe
make test         # go test ./...
make dist VERSION=1.2.3   # 发布构建（注入版本）
```

Windows 侧等价入口：`build.bat`（vet + build）、`release.ps1 -Version x.y.z`（打包发布）。

## 提交信息

**统一英文 conventional commits**：`feat:` / `fix:` / `chore:` / `ci:` / `docs:` /
`refactor:` / `test:` / `perf:` / `style:`，一句话说清这一步做了什么。
小步多次提交，一个逻辑单元一次提交。

## 代码约定

- Go 用 `gofmt`；导入路径统一 `github.com/YLing2024/FileServer/internal/...`。
- 平台差异隔离在 `internal/platform/`：`*_windows.go` / `*_other.go` 成对出现，改一个必须同步另一个。
- 用户可见行为（flag 名称与语义、默认端口与自增、`--auth`/`--ffmpeg` 默认关、
  控制台输出与二维码、`.FileServer/` 缓存位置）不得擅自变更。
- 浏览即读，不写用户文件；密钥/域名/IP 一律不硬编码（示例用 `example.com` / `127.0.0.1`）。

## 端到端测试（可选）

需要 Python 3.12 + Playwright，并先启动被测服务（默认 `http://127.0.0.1:8099`）：

```bash
python scripts/smoke_test.py
python scripts/regression_test.py
python scripts/hls_e2e_test.py
```

详见 `doc/build-and-test.md`。
