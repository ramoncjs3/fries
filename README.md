# fries

通用后台管理系统脚手架。开新项目时不用重头搭地基 —— 说需求，AI 按既定规范把表、后端、前端一路做完。

面向 10 人以内内部使用，权限要求严格。

**栈**：Go + Echo + huma + sqlc + PostgreSQL ｜ React + shadcn/ui + TanStack Query ｜ docker compose

## 快速开始

本地开发**不用 docker**：直接跑，数据库用本机装的 PostgreSQL。

```bash
brew install postgresql@17 && brew services start postgresql@17   # 只需一次
make db-setup   # 建 fries 角色和库
make dev        # 终端 1：后端热重载（第一次会自动生成 config/config.yaml）
make fe-dev     # 终端 2：前端 http://localhost:5173
make check      # 全量检查，「做完了」的唯一标准
```

容器只在部署时用：`make up` 会用 `deploy/docker-compose.yml` 起一套完整环境（PG + 后端）。

打开 http://localhost:5173 ，用后端第一次启动时打印的初始管理员账号登录
（**密码只打印一次**，首次登录强制改密）。

接口文档在 http://localhost:8080/api/v1/docs （由 `server.expose_docs` 控制，本地默认开、
**生产默认关**——它不鉴权，会摊出含平台端的整份接口清单）。用 curl 调接口时注意：会话在 httpOnly
cookie 里，写请求还要带 `X-CSRF-Token` 头（值在登录响应的 `csrf_token`）。

> 系统管理模块、多租户、平台管理端、配置管理、机器账号、**模块生成器**都已落地；
> 内置 AI 助手 + MCP（第 ⑥ 步）还没做（见 [docs/MODULES.md](docs/MODULES.md) 尾部「还没做的」）。

## 用 AI 开发（这个脚手架的正经用法）

拉下来直接跟 Claude Code 说人话就行，**不用先读文档**——`CLAUDE.md` 会把它引到
[AGENTS.md](AGENTS.md) 的任务路由表，它自己会挑对应 skill 走既定流程：

| 你说 | 它走的流程 | 产出 |
|---|---|---|
| 「我要能管理供应商」 | `.claude/skills/new-module` | 跟你确认表结构 ⬅ → 写模块定义 YAML（照 [modules/supplier.yaml](modules/supplier.yaml)）→ `make gen-module` 一键生成迁移 + 查询 + service + handler + 前端列表/详情/新增页，并自动注册权限点和路由 → 再跑一遍生成器同步 SQL/TS 类型 |
| 「供应商再加个邮箱字段」 | `.claude/skills/new-field` | 改 YAML → 生成 ALTER 迁移 + 全链路改字段 |
| 「导出成 Excel」等非 CRUD 需求 | 没有生成器兜底，走 AGENTS.md 红线 | 手写，但分层、权限点、错误码都有强制规范 |
| 「跑不起来 / 报错了」 | `.claude/skills/troubleshoot` | 按症状检索根因，不从零瞎试 |
| 「可以提交了吗」 | `.claude/skills/precheck` | `make check` 查不了但漏了会出事的那几件 |

生成器支持 9 种字段类型（含外键 `ref`——自动生成复合外键、编辑态远程搜索选择器、
读态 JOIN 出目标名字）。`make test-gen` 是生成器自己的测试。

## 目录

```
modules/     模块定义 YAML —— 一处定义，生成前后端全套
backend/     Go 服务（cmd / internal / db）
frontend/    React 前端
config/      config.yaml 不进版本库，只留 example
deploy/      compose / nginx / Dockerfile
docs/        约定、模块清单、项目记忆
.claude/     给 AI 的工作流 skill
```

完整目录约定见 [docs/DECISIONS.md §1.1](docs/DECISIONS.md#11-目录结构)，`make lint-structure` 会强制校验。

## 常用命令

```bash
make                        # 列出所有命令
make dev-check              # 秒级自检，写代码时随时跑
make check                  # 全量检查，「做完了」的唯一标准
make gen-module name=xxx    # 按 modules/xxx.yaml 生成全套代码（第 ⑤ 步）
make gen-api                # 由后端 OpenAPI 重新生成前端 TS 类型
make migrate                # 跑数据库迁移
make tree                   # 打印当前真实目录树
```

## 文档

| 文件 | 看什么 |
|---|---|
| [AGENTS.md](AGENTS.md) | **AI 开发契约** —— 红线、任务路由、完成标准 |
| [docs/DECISIONS.md](docs/DECISIONS.md) | 选型与约定，以及为什么这么选 |
| [docs/MODULES.md](docs/MODULES.md) | 已实现模块清单 |
| [docs/MEMORY.md](docs/MEMORY.md) | 踩过的坑、临时约定 |

> 用 AI 改这个项目？让它先读 [AGENTS.md](AGENTS.md)。
