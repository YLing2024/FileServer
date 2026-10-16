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
#   make check          fmt-check + vet-all + build-all + test（一条命令，必须 exit 0）

GO         ?= go
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

.PHONY: all fmt fmt-check vet vet-all build build-all dist test test-race cover check clean

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

check: fmt-check vet-all build-all test
	@echo "check: all green"

clean:
	rm -rf dist
