# 堡垒机项目 Makefile（Linux / macOS）
#
# 与 Windows 下的 build.ps1 等价，提供一致的构建入口。
#
# 用法:
#   make              构建全部（manager + server + migrate）
#   make test         运行全部测试
#   make vet          静态检查
#   make cross        交叉编译全部平台
#   make dist         构建发布包到 dist/
#   make db-init      初始化数据库
#   make db-status    查看迁移状态
#   make db-reset     清空并重建数据库
#   make clean        清理构建产物

SHELL := /bin/bash
GO    ?= go
BIN   := bin
DIST  := dist

# 三个独立模块，通过 go.mod 的 replace 指令引用本地 shared
MODULES := shared manager server

# 应用清单：名称:模块:包路径
APPS := manager:manager:./cmd/manager \
        server:server:./cmd/server \
        migrate:manager:./cmd/migrate

# 交叉编译目标
PLATFORMS := windows/amd64 linux/amd64 linux/arm64 darwin/amd64 darwin/arm64

.PHONY: all build test vet cross dist clean help \
        db-init db-status db-reset db-up

all: build

## 构建全部应用（当前平台）
build:
	@mkdir -p $(BIN)
	@$(foreach app,$(APPS), \
		name=$$(echo $(app) | cut -d: -f1); \
		mod=$$(echo $(app) | cut -d: -f2); \
		pkg=$$(echo $(app) | cut -d: -f3); \
		ext=""; \
		if [ "$$(go env GOOS)" = "windows" ]; then ext=".exe"; fi; \
		echo "构建 $$name ..."; \
		$(GO) build -C $$mod -o "$(CURDIR)/$(BIN)/$$name$$ext" $$pkg || exit 1; \
	)
	@echo "构建完成，产物位于 $(BIN)/"

## 运行全部测试
test:
	@for m in $(MODULES); do \
		echo "--- $$m ---"; \
		$(GO) test -C $$m ./... || exit 1; \
	done

## 静态检查
vet:
	@for m in $(MODULES); do \
		echo "--- $$m ---"; \
		$(GO) vet -C $$m ./... || exit 1; \
	done

## 交叉编译全部平台（纯静态，无 CGO）
cross:
	@CGO_ENABLED=0; export CGO_ENABLED; \
	for p in $(PLATFORMS); do \
		os=$${p%/*}; arch=$${p#*/}; \
		dir="$(DIST)/$$os-$$arch"; \
		mkdir -p $$dir; \
		echo "交叉编译 $$os/$$arch ..."; \
		ext=""; if [ "$$os" = "windows" ]; then ext=".exe"; fi; \
		for app in $(APPS); do \
			name=$$(echo $$app | cut -d: -f1); \
			mod=$$(echo $$app | cut -d: -f2); \
			pkg=$$(echo $$app | cut -d: -f3); \
			GOOS=$$os GOARCH=$$arch $(GO) build -C $$mod -o "$(CURDIR)/$$dir/$$name$$ext" $$pkg || exit 1; \
		done; \
		echo "  -> $$dir"; \
	done

## 构建发布包
dist: cross
	@echo "发布产物位于 $(DIST)/"

## 初始化数据库
db-init:
	@$(MAKE) --no-print-directory build
	@./$(BIN)/migrate init

## 应用未执行的迁移
db-up:
	@$(MAKE) --no-print-directory build
	@./$(BIN)/migrate up

## 查看迁移状态
db-status:
	@$(MAKE) --no-print-directory build
	@./$(BIN)/migrate status

## 清空并重建数据库（恢复出厂 / 测试重建）
db-reset:
	@$(MAKE) --no-print-directory build
	@./$(BIN)/migrate reset --yes

## 清理构建产物
clean:
	@rm -rf $(BIN) $(DIST)
	@echo "已清理 $(BIN)/ 与 $(DIST)/"

## 显示帮助
help:
	@grep -E '^## ' $(MAKEFILE_LIST) | sed 's/^## //' | awk -F': ' '{printf "  %-12s %s\n", $$1, $$2}' 2>/dev/null || \
	grep -B1 -E '^[a-z-]+:' $(MAKEFILE_LIST) | grep -E '^##' | sed 's/## //'
