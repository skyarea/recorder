# 打包 Windows 单文件 exe（Task 14 / issue #15）。
#
#   make            构建 dist/recorder.exe（带图标与版本信息）
#   make version    打印本次构建使用的版本号
#   make test       全量回归（Go + 前端）
#   make clean      清掉构建产物
#
# 版本号来自 git tag（v1.2.3 -> 1.2.3）；没有 tag 时用 0.0.0-dev。
# 换图标：替换 build/windows/icon.ico，重新打 tag 跑 make 即可 ——
# rsrc_windows_amd64.syso 每次现场生成，不入库。
#
# 依赖：go-winres（生成资源）——
#   go install github.com/tc-hib/go-winres@latest

VERSION ?= $(shell git describe --tags --abbrev=0 2>/dev/null | sed 's/^v//')
ifeq ($(VERSION),)
VERSION := 0.0.0-dev
endif

GOEXE   := go
WINRES  := go-winres

.PHONY: all build exe version test clean syso

all: exe

# 资源文件：图标 + 版本信息 + GUI manifest（无控制台窗口）。
# --arch amd64：产物只有 64 位；386 不在支持范围。
syso:
	$(WINRES) simply \
		--icon build/windows/icon.ico \
		--arch amd64 \
		--out rsrc \
		--manifest gui \
		--product-version $(VERSION) \
		--file-version $(VERSION) \
		--product-name "recorder" \
		--file-description "PDF 阅读进度 · 笔记" \
		--copyright "skyarea"

# 约定的打包命令（计划 Task 14）：-s -w 去符号与 DWARF，-X main.version 注版本。
# CGO 必须为 0：SQLite 用纯 Go 驱动，整个产物无 C 工具链依赖。
# go build 不会自建输出目录，所以先 mkdir -p dist。
exe: syso
	mkdir -p dist
	CGO_ENABLED=0 $(GOEXE) build -trimpath \
		-ldflags "-s -w -X main.version=$(VERSION)" \
		-o dist/recorder.exe .

build: exe

version:
	@echo $(VERSION)

test:
	CGO_ENABLED=0 $(GOEXE) test ./... -count=1
	node --test tests/frontend/*.test.mjs

clean:
	rm -rf dist
	rm -f rsrc_windows_*.syso
