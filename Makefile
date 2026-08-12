# fries —— 唯一的命令入口。
#
# 两个档位，别用错（AGENTS.md「完成」的定义）：
#   make dev-check   秒级，写代码过程中随时跑
#   make check       全量，「做完了」的唯一标准
#
# make 不带参数会列出所有目标。

SHELL := /bin/bash
.DEFAULT_GOAL := help

ROOT       := $(abspath $(dir $(lastword $(MAKEFILE_LIST))))
BACKEND    := $(ROOT)/backend
FRONTEND   := $(ROOT)/frontend
CONFIG     := $(ROOT)/config/config.yaml
COMPOSE    := docker compose -f $(ROOT)/deploy/docker-compose.yml
# 容器里的 PG 映射到宿主机的这个端口 —— 本地开发用的 PG 占着 5432，不能撞。
# 改这里要同时改 deploy/docker-compose.yml 里的默认值。
COMPOSE_PG_PORT := 5433
BIN        := $(BACKEND)/bin
GEN        := cd $(BACKEND) && go run ./cmd/gen

# 工具版本锁死，换机器结果一致。装到 backend/bin（已在 .gitignore 里）。
SQLC_VERSION          := v1.31.1
GOOSE_VERSION         := v3.27.3
GOLANGCI_LINT_VERSION := v2.12.2
AIR_VERSION           := v1.67.4
# squawk 是 Rust 写的，装不了 go install，直接抓 GitHub release 的二进制。
SQUAWK_VERSION        := 2.62.0

SQLC      := $(BIN)/sqlc
GOOSE     := $(BIN)/goose
GOLANGCI  := $(BIN)/golangci-lint
AIR       := $(BIN)/air
SQUAWK    := $(BIN)/squawk

# squawk 的产物名用的是 darwin/linux + arm64/x64，和 uname 的叫法对不上，要翻译一下。
SQUAWK_OS   := $(shell uname -s | tr '[:upper:]' '[:lower:]')
SQUAWK_ARCH := $(shell uname -m | sed -e 's/^x86_64$$/x64/' -e 's/^aarch64$$/arm64/')

# OpenAPI 中间产物。只有生成出来的 schema.d.ts 进版本库，这份 json 不进。
OPENAPI_SPEC := $(shell echo $${TMPDIR:-/tmp})/fries-openapi.json

VERSION := $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)

.PHONY: help
help: ## 列出所有命令
	@echo "fries —— 可用命令："
	@grep -hE '^[a-zA-Z0-9_-]+:.*?## ' $(MAKEFILE_LIST) \
		| awk 'BEGIN {FS = ":.*?## "}; {printf "  \033[36m%-18s\033[0m %s\n", $$1, $$2}'

# ---------------------------------------------------------------- 本地开发（不用 docker）

.PHONY: dev
dev: $(CONFIG) $(AIR) ## 本地开发：后端热重载，直连本机 PostgreSQL
	@$(MAKE) --no-print-directory migrate
	cd $(BACKEND) && $(AIR)

.PHONY: db-setup
db-setup: ## 在本机 PostgreSQL 上建 fries 角色和库（只做缺的那部分）
	@createuser -s fries 2>/dev/null && echo "已建角色 fries" || echo "角色 fries 已存在"
	@createdb -O fries fries 2>/dev/null && echo "已建库 fries" || echo "库 fries 已存在"
	@echo "✓ 本机数据库就绪（brew 装的 PG 本地连接是 trust 认证，密码填什么都行）"

# 本地开发不想记 bootstrap 随机出来的那串密码。**只对本机库生效**，
# 命令自己会检查数据库主机，不是 localhost 就拒绝跑。重建库之后重跑一次。
.PHONY: dev-admin
dev-admin: $(CONFIG) ## 把本地 admin 密码重置为 admin（仅本机）
	@$(GEN) dev-admin -config $(CONFIG)

# config.yaml 不进版本库，第一次跑自动从样例复制一份。含密码，权限收到 600。
# 顺手把占位的会话密钥换成随机值 —— 共用同一个密钥等于没有密钥。
$(CONFIG):
	@cp $(ROOT)/config/config.example.yaml $(CONFIG)
	@chmod 600 $(CONFIG)
	@SECRET="$$(openssl rand -base64 32)" && \
		sed -i.bak "s|PLACEHOLDER-CHANGE-ME-openssl-rand-base64-32|$$SECRET|" $(CONFIG) && \
		rm -f $(CONFIG).bak
	@echo "已生成 $(CONFIG)（会话密钥已随机生成；按需改数据库密码等）"

# ---------------------------------------------------------------- 容器（部署用）

.PHONY: up
up: $(CONFIG) ## 用 docker compose 起完整环境（部署同款，本地开发不需要）
	$(COMPOSE) up -d --build
	@echo "等数据库就绪…"
	@FRIES_DATABASE__PORT=$(COMPOSE_PG_PORT) $(MAKE) --no-print-directory migrate
	@echo
	@echo "后端已就绪：http://localhost:8080/healthz"
	@echo "接口文档：   http://localhost:8080/api/v1/docs"

.PHONY: down
down: ## 停容器（数据卷保留）
	$(COMPOSE) down

.PHONY: logs
logs: ## 跟踪容器日志
	$(COMPOSE) logs -f

# ---------------------------------------------------------------- 检查

.PHONY: dev-check
dev-check: ## 秒级自检：编译 + vet + lint + 短测试 + 启动自检 + 前端类型检查
	cd $(BACKEND) && go build ./...
	cd $(BACKEND) && go vet ./...
	@$(MAKE) --no-print-directory lint-go lint-migrations lint-sql
	cd $(BACKEND) && go test -short ./...
	cd $(BACKEND) && go run ./cmd/server --selfcheck -config $(ROOT)/config/config.example.yaml
	@$(MAKE) --no-print-directory fe-typecheck fe-lint fe-test

.PHONY: check
check: dev-check lint-docs lint-structure lint-generated sqlc-diff ## 全量检查：「做完了」的唯一标准
	cd $(BACKEND) && go test ./...
	@$(MAKE) --no-print-directory fe-build
	@echo
	@echo "✓ make check 全绿"

.PHONY: lint-go
lint-go: $(GOLANGCI) ## 只跑 Go lint
	cd $(BACKEND) && $(GOLANGCI) run --config $(ROOT)/.golangci.yml ./...

.PHONY: lint-migrations
lint-migrations: $(SQUAWK) ## 检查迁移文件会不会锁表 / 写坏数据（配置在 .squawk.toml）
	@# 只查 Up 段：squawk 不认识 goose 的 `-- +goose Down`，会把回滚语句里的
	@# DROP TABLE / DROP COLUMN 当成要上线的删除操作报一堆。Down 段的职责本来就是删东西，
	@# 而且它只在开发期跑 —— 真正要按生产标准审的是 Up 段。
	@cd $(BACKEND) && for f in db/migrations/*.sql; do \
		out=$$(sed '/-- +goose Down/,$$d' "$$f" \
			| $(SQUAWK) -c $(ROOT)/.squawk.toml --stdin-filepath "$$f") \
			|| { echo "$$out"; exit 1; }; \
	done
	@echo "✓ 迁移文件通过 squawk"

.PHONY: lint-sql
lint-sql: ## 租户隔离静态检查：查询带租户条件、谁用不带租户的句柄、唯一冲突索引名
	@cd $(BACKEND) && go run -tags genonly ./cmd/gen lint-sql

.PHONY: lint-docs
lint-docs: ## 检查文档引用的路径 / 标识符 / 章节号 / make 目标是否有效
	@$(GEN) lint-docs

.PHONY: lint-structure
lint-structure: ## 检查目录结构是否符合 DECISIONS.md §1.1
	@$(GEN) lint-structure

.PHONY: lint-generated
lint-generated: lint-api-types ## 检查生成物是最新的（没被手改，也没忘了重跑）
	@$(GEN) errdoc -check
	@$(GEN) errcodes-ts -check
	@$(GEN) schemadoc -check
	@cd $(BACKEND) && go run -tags genonly ./cmd/gen tenant-queries -check
	@echo "✓ 生成的文档是最新的"

.PHONY: lint-api-types
lint-api-types: fe-install ## 检查前端 TS 类型和后端接口没有漂移
	@cd $(BACKEND) && go run ./cmd/server -openapi -config $(ROOT)/config/config.example.yaml > $(OPENAPI_SPEC)
	@cd $(FRONTEND) && pnpm exec openapi-typescript $(OPENAPI_SPEC) -o $(OPENAPI_SPEC).d.ts
	@cmp -s $(OPENAPI_SPEC).d.ts $(FRONTEND)/src/api/schema.d.ts || { \
		echo "前端 TS 类型和后端接口对不上了。跑一下 make gen-api 再提交"; exit 1; }
	@echo "✓ 前端类型和后端接口一致"

.PHONY: sqlc-diff
sqlc-diff: $(SQLC) ## 检查 sqlc 生成物和 SQL 是同步的
	cd $(BACKEND) && $(SQLC) diff
	@echo "✓ sqlc 生成物是最新的"

.PHONY: fmt
fmt: ## 格式化 Go 代码
	cd $(BACKEND) && go fmt ./...

# ---------------------------------------------------------------- 生成

.PHONY: gen-all
gen-all: gen-sqlc gen-tenant-queries gen-api errdoc errcodes-ts schemadoc ## 重跑所有生成器

.PHONY: gen-api
gen-api: fe-install ## 由后端 OpenAPI 生成前端 TS 类型（真相唯一在 Go 侧）
	@cd $(BACKEND) && go run ./cmd/server -openapi -config $(ROOT)/config/config.example.yaml > $(OPENAPI_SPEC)
	@cd $(FRONTEND) && pnpm exec openapi-typescript $(OPENAPI_SPEC) -o src/api/schema.d.ts
	@echo "✓ 已更新 frontend/src/api/schema.d.ts"

.PHONY: gen-sqlc
gen-sqlc: $(SQLC) ## 由 SQL 生成类型安全的 Go 代码
	cd $(BACKEND) && $(SQLC) generate

.PHONY: gen-tenant-queries
gen-tenant-queries: ## 由 sqlc 产物生成租户绑定的查询句柄（ForTenant）
	@# -tags genonly 只编译生成器自己，不拉 internal/config → internal/repo 那条链。
	@# 生成器要能在 internal/repo 还编译不过的时候跑起来 —— 那正是它要去修的状态。
	cd $(BACKEND) && go run -tags genonly ./cmd/gen tenant-queries

.PHONY: gen-module
gen-module: ## 按 modules/<name>.yaml 生成模块全套代码（用法：make gen-module name=xxx）
	@test -n "$(name)" || { echo "用法：make gen-module name=<模块 key>"; exit 2; }
	@$(GEN) module -name $(name)

.PHONY: test-gen
test-gen: ## 生成器自测：生成覆盖全类型的 fixture 模块 → 跑完整 check → 回滚（改产出器后跑）
	@bash $(ROOT)/scripts/test-gen.sh

.PHONY: errdoc
errdoc: ## 由错误码注册表生成 docs/ERROR_CODES.md
	@$(GEN) errdoc

.PHONY: errcodes-ts
errcodes-ts: ## 由错误码注册表生成 frontend/src/api/errorCodes.ts
	@$(GEN) errcodes-ts

.PHONY: schemadoc
schemadoc: ## 由迁移文件生成 docs/SCHEMA.md
	@$(GEN) schemadoc

.PHONY: tree
tree: ## 打印当前真实目录树（文档里别手抄目录）
	@$(GEN) tree

# ---------------------------------------------------------------- 数据库

.PHONY: migrate
migrate: $(GOOSE) $(CONFIG) ## 跑数据库迁移到最新
	@GOOSE_DRIVER=postgres GOOSE_DBSTRING="$$($(GEN) dsn -config $(CONFIG))" \
		$(GOOSE) -dir $(BACKEND)/db/migrations up

.PHONY: migrate-status
migrate-status: $(GOOSE) $(CONFIG) ## 看迁移状态
	@GOOSE_DRIVER=postgres GOOSE_DBSTRING="$$($(GEN) dsn -config $(CONFIG))" \
		$(GOOSE) -dir $(BACKEND)/db/migrations status

.PHONY: migrate-down
migrate-down: $(GOOSE) $(CONFIG) ## 回滚最后一个迁移
	@GOOSE_DRIVER=postgres GOOSE_DBSTRING="$$($(GEN) dsn -config $(CONFIG))" \
		$(GOOSE) -dir $(BACKEND)/db/migrations down

.PHONY: migrate-create
migrate-create: $(GOOSE) ## 新建一个迁移（用法：make migrate-create name=add_xxx）
	@test -n "$(name)" || { echo "用法：make migrate-create name=<描述>"; exit 2; }
	$(GOOSE) -dir $(BACKEND)/db/migrations -s create $(name) sql

# ---------------------------------------------------------------- 前端

.PHONY: fe-install
fe-install: $(FRONTEND)/node_modules ## 装前端依赖

$(FRONTEND)/node_modules: $(FRONTEND)/package.json
	cd $(FRONTEND) && pnpm install
	@touch $(FRONTEND)/node_modules

.PHONY: fe-typecheck
fe-typecheck: fe-install ## 前端类型检查
	cd $(FRONTEND) && pnpm typecheck

.PHONY: fe-lint
fe-lint: fe-install ## 前端 lint
	cd $(FRONTEND) && pnpm lint

.PHONY: fe-test
fe-test: fe-install ## 前端测试（vitest + jsdom）
	cd $(FRONTEND) && pnpm test

.PHONY: fe-build
fe-build: fe-install ## 前端构建
	cd $(FRONTEND) && pnpm build

.PHONY: fe-dev
fe-dev: fe-install ## 前端开发服务器
	cd $(FRONTEND) && pnpm dev

# ---------------------------------------------------------------- 工具

.PHONY: tools
tools: $(SQLC) $(GOOSE) $(GOLANGCI) $(AIR) $(SQUAWK) ## 装齐所有固定版本的工具
	@echo "工具就绪：$(BIN)"

$(SQLC):
	GOBIN=$(BIN) go install github.com/sqlc-dev/sqlc/cmd/sqlc@$(SQLC_VERSION)

$(GOOSE):
	GOBIN=$(BIN) go install github.com/pressly/goose/v3/cmd/goose@$(GOOSE_VERSION)

$(GOLANGCI):
	GOBIN=$(BIN) go install github.com/golangci/golangci-lint/v2/cmd/golangci-lint@$(GOLANGCI_LINT_VERSION)

$(AIR):
	GOBIN=$(BIN) go install github.com/air-verse/air@$(AIR_VERSION)

$(SQUAWK):
	@mkdir -p $(BIN)
	curl -fsSL -o $(SQUAWK) \
		https://github.com/sbdchd/squawk/releases/download/v$(SQUAWK_VERSION)/squawk-$(SQUAWK_OS)-$(SQUAWK_ARCH)
	@chmod +x $(SQUAWK)

.PHONY: clean
clean: ## 清掉本地产物（不动数据库）
	rm -rf $(BIN) $(BACKEND)/tmp $(FRONTEND)/dist
