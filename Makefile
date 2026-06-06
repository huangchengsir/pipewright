SHELL := /bin/bash
BIN := pipewright
# 显式包范围:根 + cmd + internal。不用 ./... 以免扫进 web/node_modules 里的 Go 包。
GO_PKGS := . ./cmd/... ./internal/...
GO_FMT_DIRS := cmd internal embed.go

# 版本元数据:tag 优先(git describe),源码态回退 dev。发版由 .goreleaser.yaml 注入同名变量。
VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
COMMIT  ?= $(shell git rev-parse --short HEAD 2>/dev/null || echo none)
DATE    ?= $(shell date -u +%Y-%m-%dT%H:%M:%SZ)
VPKG    := github.com/huangchengsir/pipewright/internal/version
LDFLAGS := -s -w -X $(VPKG).Version=$(VERSION) -X $(VPKG).Commit=$(COMMIT) -X $(VPKG).Date=$(DATE)

.PHONY: all build embed-frontend go-build test vet fmt fmt-check mem-check dev run version clean

all: build

## embed-frontend: 构建前端静态资源到 web/dist(供 go:embed)
embed-frontend:
	cd web && npm ci && npm run build

go-build:
	CGO_ENABLED=0 go build -ldflags "$(LDFLAGS)" -o $(BIN) ./cmd/pipewright

## build: 前端构建 → go:embed → 静态单二进制(双运行模式之原生形态)
build: embed-frontend go-build

test:
	go test $(GO_PKGS)

vet:
	go vet $(GO_PKGS)

## fmt: 用内置 gofmt 格式化(无需配置文件)
fmt:
	gofmt -w $(GO_FMT_DIRS)

## fmt-check: CI 用;有未格式化文件则失败
fmt-check:
	@out="$$(gofmt -l $(GO_FMT_DIRS))"; if [ -n "$$out" ]; then echo "需要 gofmt 的文件:"; echo "$$out"; exit 1; fi

## mem-check: 断言平台常驻内存 ≤100MB(NFR-4)
mem-check:
	bash scripts/mem-check.sh

## dev: 本地开发(两个终端:Go API + Vite 热更代理)
dev:
	@echo "终端1: go run ./cmd/pipewright"
	@echo "终端2: cd web && npm run dev   # 代理 /api /healthz 到 :8080"

## version: 打印将注入二进制的版本元数据(调试发版用)
version:
	@echo "VERSION=$(VERSION)"; echo "COMMIT=$(COMMIT)"; echo "DATE=$(DATE)"

run: go-build
	./$(BIN)

clean:
	rm -f $(BIN) *.db
	rm -rf web/dist web/node_modules
