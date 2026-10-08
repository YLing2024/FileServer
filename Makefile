# FileServer — 一条命令的校验 / 构建入口（Linux / macOS / WSL）。
#
# Windows 侧：双击 build.bat 走等价命令；发布走 release.ps1（内部同样注入版本）。
# 交叉编译目标恒为 windows/amd64 —— 本项目的运行目标是 Windows 原生 exe。
#
#   make fmt         就地格式化
#   make fmt-check   断言无未格式化文件
#   make vet         交叉 go vet
#   make build       交叉构建 dist/FileServer.exe（无版本注入）
#   make dist VERSION=x.y.z [COMMIT=..]   发布构建（注入版本/commit/构建时间）
#   make test         go test ./...
#   make cover        覆盖率
#   make check        fmt-check + vet + build + test（一条命令，必须 exit 0）

GO         ?= go
GOOS_TGT   ?= windows
GOARCH_TGT ?= amd64
MODULE     := github.com/YLing2024/FileServer
PKG        := ./cmd/fileserver
BIN        := dist/FileServer.exe
LDFLAGS    := -s -w

VERSION    ?= dev
COMMIT     ?= $(shell git rev-parse --short HEAD 2>/dev/null)
BUILD_DATE ?= $(shell date -u +%Y-%m-%dT%H:%M:%SZ)
XFLAGS     := -X $(MODULE)/internal/version.Version=$(VERSION) \
              -X $(MODULE)/internal/version.Commit=$(COMMIT) \
              -X $(MODULE)/internal/version.BuildDate=$(BUILD_DATE)

.PHONY: all fmt fmt-check vet build dist test cover check clean

all: check

fmt:
	gofmt -w .

fmt-check:
	@out="$$(gofmt -l .)"; \
	if [ -n "$$out" ]; then echo "以下文件未格式化（gofmt -l）:"; echo "$$out"; exit 1; fi

vet:
	GOOS=$(GOOS_TGT) GOARCH=$(GOARCH_TGT) $(GO) vet ./...

build:
	mkdir -p dist
	GOOS=$(GOOS_TGT) GOARCH=$(GOARCH_TGT) $(GO) build -trimpath -ldflags "$(LDFLAGS)" -o $(BIN) $(PKG)

dist:
	mkdir -p dist
	GOOS=$(GOOS_TGT) GOARCH=$(GOARCH_TGT) $(GO) build -trimpath \
		-ldflags "$(LDFLAGS) $(XFLAGS)" -o $(BIN) $(PKG)
	@echo "built $(BIN) version=$(VERSION) commit=$(COMMIT)"

test:
	$(GO) test ./...

cover:
	mkdir -p dist
	@pkgs="$$($(GO) list -f '{{if or .TestGoFiles .XTestGoFiles}}{{.ImportPath}}{{end}}' ./...)"; \
	$(GO) test -coverprofile=dist/cover.out -covermode=atomic $$pkgs && \
	$(GO) tool cover -func=dist/cover.out | tail -1

check: fmt-check vet build test
	@echo "check: all green"

clean:
	rm -rf dist
