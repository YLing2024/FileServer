# FileServer — 一条命令的校验 / 构建入口（Linux / macOS / WSL）。
#
# Windows 侧：双击 build.bat 走等价命令；发布走 release.ps1（内部同样注入版本）。
# 本项目为跨平台项目：Windows / Linux / macOS 均为一等目标，故 check 覆盖三平台
# 交叉 vet/build；`make build` 仍保持（windows/amd64）不变量。
#
#   make fmt            就地格式化
#   make fmt-check      断言无未格式化文件
#   make vet            交叉 go vet（windows/amd64）
#   make vet-all        三平台交叉 go vet
#   make build          构建 dist/FileServer.exe（windows/amd64，无版本注入）
#   make build-all      构建全部平台产物到 dist/
#   make dist VERSION=x.y.z [COMMIT=..]   发布构建（windows/amd64，注入版本）
#   make test           go test ./...
#   make test-race      go race 测试
#   make cover          覆盖率
#   make check          fmt-check + vet-all + 前端检查（有 pnpm 时）+ build-all + test（必须 exit 0）
#   make ci             与 GitHub CI 等价的全量门禁（fmt/lint 双 GOOS/vet/race/前端/产物一致性）；
#                       push 前必须绿，pre-push 钩子会自动调用（make hooks 安装）
#   make web-install    安装前端依赖（需要 pnpm）
#   make web-build      构建前端到 internal/server/web-dist
#   make web-check      前端四段：typecheck + lint + test + build
#   make web-check-optional  有 pnpm 与依赖时跑 web-check，否则提示后跳过

GO         ?= go
PNPM       ?= pnpm
GOOS_TGT   ?= windows
GOARCH_TGT ?= amd64
MODULE     := github.com/YLing2024/FileServer
PKG        := ./cmd/fileserver
BIN        := dist/FileServer.exe
LDFLAGS    := -s -w

# 三平台产物目标：Linux / macOS 的 amd64+arm64，以及 Windows amd64。
PLATFORMS  := linux/amd64 linux/arm64 darwin/amd64 darwin/arm64 windows/amd64

VERSION    ?= dev
COMMIT     ?= $(shell git rev-parse --short HEAD 2>/dev/null)
BUILD_DATE ?= $(shell date -u +%Y-%m-%dT%H:%M:%SZ)
XFLAGS     := -X $(MODULE)/internal/version.Version=$(VERSION) \
              -X $(MODULE)/internal/version.Commit=$(COMMIT) \
              -X $(MODULE)/internal/version.BuildDate=$(BUILD_DATE)

.PHONY: all fmt fmt-check vet vet-all build build-all dist test test-race cover check clean web-install web-build web-check web-check-optional ci lint-all ci-web-dist hooks

all: check

fmt:
	gofmt -w .

fmt-check:
	@out="$$(gofmt -l .)"; \
	if [ -n "$$out" ]; then echo "以下文件未格式化（gofmt -l）:"; echo "$$out"; exit 1; fi

vet:
	GOOS=$(GOOS_TGT) GOARCH=$(GOARCH_TGT) $(GO) vet ./...

vet-all:
	@set -e; for p in $(PLATFORMS); do \
		os=$${p%/*}; arch=$${p#*/}; \
		echo "vet $$os/$$arch"; \
		GOOS=$$os GOARCH=$$arch CGO_ENABLED=0 $(GO) vet ./...; \
	done

build:
	mkdir -p dist
	GOOS=$(GOOS_TGT) GOARCH=$(GOARCH_TGT) $(GO) build -trimpath -ldflags "$(LDFLAGS)" -o $(BIN) $(PKG)

build-all:
	mkdir -p dist
	@set -e; for p in $(PLATFORMS); do \
		os=$${p%/*}; arch=$${p#*/}; \
		out=dist/FileServer-$$os-$$arch; \
		if [ "$$os" = windows ]; then out=$$out.exe; fi; \
		echo "build $$os/$$arch -> $$out"; \
		GOOS=$$os GOARCH=$$arch CGO_ENABLED=0 $(GO) build -trimpath -ldflags "$(LDFLAGS)" -o $$out $(PKG); \
	done

dist:
	mkdir -p dist
	GOOS=$(GOOS_TGT) GOARCH=$(GOARCH_TGT) $(GO) build -trimpath \
		-ldflags "$(LDFLAGS) $(XFLAGS)" -o $(BIN) $(PKG)
	@echo "built $(BIN) version=$(VERSION) commit=$(COMMIT)"

test:
	$(GO) test ./...

test-race:
	$(GO) test -race ./...

cover:
	mkdir -p dist
	@pkgs="$$($(GO) list -f '{{if or .TestGoFiles .XTestGoFiles}}{{.ImportPath}}{{end}}' ./...)"; \
	$(GO) test -coverprofile=dist/cover.out -covermode=atomic $$pkgs && \
	$(GO) tool cover -func=dist/cover.out | tail -1

check: fmt-check vet-all web-check-optional build-all test
	@echo "check: all green"

# 前端检查独立于 Go 门禁：没有 pnpm 或未安装依赖时给出提示并跳过，
# 不让前端工具链缺失阻断 Go 构建；CI 的 frontend job 仍强制跑完整四段。
web-check-optional:
	@if ! command -v $(PNPM) >/dev/null 2>&1; then \
		echo "web-check: 未找到 pnpm，跳过前端检查（不阻断 Go 构建）"; \
	elif [ ! -d web/node_modules ]; then \
		echo "web-check: 未安装前端依赖，跳过（先运行 make web-install）"; \
	else \
		$(MAKE) web-check; \
	fi

# 前端独立目标：安装依赖后跑 typecheck + lint + test + build。
web-install:
	cd web && $(PNPM) install --frozen-lockfile

web-build:
	cd web && $(PNPM) build

web-check:
	cd web && $(PNPM) typecheck && $(PNPM) lint && $(PNPM) test && $(PNPM) build

# ---- 与 .github/workflows/ci.yml 等价的门禁 ----
# CI 比 make check 多三样东西：golangci-lint（钉版本、双 GOOS）、-race、前端产物一致性。
# 本地缺哪一样，就会出现「本地绿、push 红」。make ci 把它们一次跑全。

CI_LINT_VERSION ?= v2.14.0

ci: fmt-check lint-all vet-all build-all test-race web-check ci-web-dist
	@echo "ci: all green"

lint-all:
	@command -v golangci-lint >/dev/null 2>&1 || { \
		echo "缺少 golangci-lint $(CI_LINT_VERSION)："; \
		echo "  curl -sSL https://github.com/golangci/golangci-lint/releases/download/$(CI_LINT_VERSION)/golangci-lint-$(patsubst v%,%,$(CI_LINT_VERSION))-linux-amd64.tar.gz | tar xz -C /tmp && sudo install -m755 /tmp/golangci-lint-$(patsubst v%,%,$(CI_LINT_VERSION))-linux-amd64/golangci-lint /usr/local/bin/"; \
		exit 1; }
	@v=$$(golangci-lint version 2>/dev/null | grep -oE '[0-9]+\.[0-9]+\.[0-9]+' | head -1); \
	if [ "v$$v" != "$(CI_LINT_VERSION)" ]; then \
		echo "golangci-lint 版本不一致：本地 v$$v，CI $(CI_LINT_VERSION)（请安装 CI 版本，否则结论不可比）"; exit 1; \
	fi
	GOOS=linux GOARCH=amd64 golangci-lint run
	GOOS=windows GOARCH=amd64 golangci-lint run

# CI 会校验提交进仓库的 web-dist 与源码构建结果一致；本地构建后如产生 diff 必须一并提交。
ci-web-dist:
	@if ! git diff --quiet -- internal/server/web-dist; then \
		echo "internal/server/web-dist 与源码构建结果不一致，请提交构建产物："; \
		git diff --stat -- internal/server/web-dist; \
		exit 1; \
	fi

# 安装 pre-push 钩子：push 前自动跑 make ci（可用 SKIP_CI=1 git push 临时跳过）。
hooks:
	@mkdir -p .git/hooks
	@printf '%s\n' \
		'#!/bin/sh' \
		'# 由 make hooks 安装：push 前跑与 CI 等价的门禁，避免远端红。' \
		'if [ "$$SKIP_CI" = "1" ]; then echo "pre-push: SKIP_CI=1，跳过门禁"; exit 0; fi' \
		'make ci || { echo "pre-push: make ci 未通过，已阻止 push（临时跳过：SKIP_CI=1 git push）"; exit 1; }' \
		> .git/hooks/pre-push
	@chmod +x .git/hooks/pre-push
	@echo "已安装 .git/hooks/pre-push"

clean:
	rm -rf dist
