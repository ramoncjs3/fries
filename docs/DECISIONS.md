# fries — 选型与约定

> 这是脚手架的**决策记录**：为什么这样选、约定是什么。
> 讨论出的每条硬约束都在这里，实现和 review 都以此为准。
> 改变其中任何一条，先改这份文档，再改代码。

最后更新：2026-08-07

**目录**：[0 定位](#0-定位与边界) · [1 技术栈](#1-技术栈) · [1.1 目录结构](#11-目录结构) · [2 数据库约定](#2-数据库约定) · [3 权限模型](#3-权限模型一处声明三处生效-️-核心设计) · [4 API 契约](#4-api-契约) · [5 配置分层](#5-配置分层) · [6 安全基线](#6-安全基线) · [7 前端约定](#7-前端约定) · [8 开放性与集成](#8-开放性与集成) · [9 明确不做](#9-明确不做划边界) · [10 代码生成器](#10-代码生成器fries-gen) · [11 实施计划](#11-实施计划) · [12 环境备注](#12-环境备注)

---

## 0. 定位与边界

**fries 是一个通用后台管理系统脚手架**，不面向特定业务领域。目标是：新系统开工时不用重头搭地基，说需求就能让 AI 按既定规范把活干完。

- 典型规模：**单个组织 10 人以内**，但**权限要求严格**
- 默认部署：单机 docker compose（nginx / backend / postgres 三容器）
- **做多租户**：一次部署，多家公司各自注册使用。方案见 `docs/MULTI-TENANCY.md`
- 不做 i18n（中文单语）

⚠️ 多租户是**后来推翻的一条边界**（原文是「不做多租户、不做 SaaS」）。
它横切数据、认证、授权、审计四层，动手前先读 `docs/MULTI-TENANCY.md`，
尤其是「本地开发默认 RLS 不生效」那一节。

**设计优先级**：规范一致 > 功能丰富。因为代码由 AI 逐次生成，一致的结构才能让每次生成可预测、可复查。

---

## 1. 技术栈

| 层 | 选型 | 关键理由 |
|---|---|---|
| 语言 | Go 1.25 | |
| Web 框架 | Echo v5 + **huma v2** | huma 声明式 handler：input/output struct 即契约，自动 OpenAPI 3.1 + 自动入参校验。我们的代码只用 Echo v5。⚠️ 注意 `humaecho` 适配器包同时含 v4/v5 两个适配文件，一 import 会把 Echo **v4 也链进二进制**（`go list -deps` 可见）—— 剔不掉（上游同一个 package），只是多点体积，v4 仍在维护无 CVE。别据此以为「只有 v5」 |
| 数据访问 | **sqlc + pgx v5** | 写 SQL 生成类型安全代码；列名写错编译期报错；无反射开销；参数化天然防注入；复杂 SQL（CTE/窗口/JOIN）是它的主场 |
| 动态过滤 | sqlc 的 `sqlc.narg()` 可空参数 | 一条查询覆盖所有过滤组合，**不引 squirrel**,后期有需要可以考虑 |
| 动态排序 | 白名单 map，由生成器产出 | 防注入 + 防全表扫描 |
| 数据库 | PostgreSQL 17 | |
| 迁移 | goose | 禁止手改库 |
| 认证 | 服务端 session（存 PG）+ httpOnly/SameSite cookie + argon2id + CSRF double-submit | **不用 localStorage JWT**：httpOnly 挡 XSS 偷 token，且能服务端即时踢人 |
| 授权 | Casbin RBAC（功能权限）+ 自研 data scope（数据权限） | 包在 `authz.Checker` 接口后，可替换 |
| 配置 | config.yaml（koanf）+ DB settings 表 + PG LISTEN/NOTIFY 热加载 | **禁止 .env** |
| 日志 | slog 结构化 + requestID 贯穿 | |
| 指标 | Prometheus `/metrics`  | 不上 OpenTelemetry、不上 Sentry |
| 前端构建 | **pnpm** + Vite 6 + TypeScript strict（`--max-warnings=0`）| pnpm 依赖隔离严格，防 AI 用到未声明的包 |
| 前端 | React 19 + shadcn/ui + Tailwind v4 | 不引任何其它 UI 库 |
| 前端数据 | TanStack Query v5 | 表格先用原生 table（`<ListPage>` 封好了）；要列排序 / 列宽拖拽再引 TanStack Table |
| 前端路由 | React Router v8（data mode）+ 懒加载 | 不选 TanStack Router：语料多，AI 生成出错率明显更低 |
| 前端表单 | react-hook-form + zod | |
| 前端提示 | sonner（toast） | |
| 图表 | Recharts（shadcn charts 底座） | 大数据量/复杂交互再考虑 ECharts |
| API 类型契约 | huma 输出 openapi.json → `openapi-typescript` 生成 TS 类型，CI 校验不漂移 | 真相唯一在 Go 侧 |
| 加密 | `crypto.Cipher` 接口：local AES-256-GCM，**预留 AWS KMS** | 密文带版本前缀，可平滑迁移 |
| 文件存储 | `storage.Storage` 接口：本地磁盘，**预留 S3**（不用 MinIO） | |
| 导入导出 | excelize v2；PDF 用 maroto v2（中文字体实现时验证） | |
| 通知（人） | **nikoksr/notify** —— Slack / 飞书 / 钉钉 / 企微 / 邮件 | 包在自己的 `Notifier` 接口后（该库自己声明不保证外部服务一致性）。⚠️ 这是**未来租户通知渠道子系统**的选型，尚未落地。**当前的平台事务邮件（认证流）不走它** —— `notify.sesMailer` 直连 `aws-sdk-go-v2/ses` 按 **Body.Text** 发信：nikoksr 的 amazonses 封装只填 Body.Html，而正文当纯文本、还拼了用户可控字段（company_name），按 HTML 发会成注入面。故平台 mailer 直连 SDK，nikoksr 依赖待该子系统开工时再引 |
| 通知（系统） | **自研通用 Webhook** —— HMAC-SHA256 签名 + 退避重试 + 死信 + 投递记录 | 关注可靠性与可审计，无现成库可用 |
| 定时任务 | goroutine + ticker + PG advisory lock | 不引 cron 框架、不引消息队列 |
| AI 模型 | **OpenAI 兼容协议**（`sashabaranov/go-openai`），`base_url` 从 DB settings 热改 | DeepSeek / GLM 都提供兼容端点，**不引网关**（将来要加 one-api 是零代码改动） |
| AI 编排 | 自写 tool-calling 循环 | 不引 eino / langchaingo：循环本身百来行，而我们要在循环里插权限检查和人工确认，框架反而碍事 |
| AI 接入口 | **官方 MCP Go SDK**（`modelcontextprotocol/go-sdk`） | openclaw / hermes / Claude Code 全部原生支持，不用为每个 agent 写适配 |
| 测试 | testcontainers-go 起真 PG 跑集成测试 | 不 mock DB |
| 质量 | golangci-lint + govulncheck + npm audit + ESLint 硬规则 | |

### 1.1 目录结构

Go module path：**`github.com/ramoncjs3/fries`**（go.mod 在 `backend/`，故 `backend/internal/<pkg>` 的 import 路径是 `github.com/ramoncjs3/fries/internal/<pkg>`）。

```
fries/
├── AGENTS.md                 # AI 契约，每次必读
├── CLAUDE.md                 # 指向 AGENTS.md
├── README.md                 # 给人看的入口
├── Makefile                  # 唯一入口：dev / check / gen-module / migrate ...
├── .golangci.yml             # Go lint 规则
├── .squawk.toml              # PostgreSQL 迁移 linter 规则（MULTI-TENANCY.md §13）
├── .dockerignore  .gitignore
│
├── modules/                  # ★ 模块定义 YAML —— 同时驱动前后端生成，故不放 backend 内
│   └── <key>.yaml
│
├── backend/
│   ├── go.mod  sqlc.yaml
│   ├── cmd/server/           # 主程序
│   ├── cmd/gen/              # ★ 生成器（写 backend/ 与 ../frontend/ 两侧）
│   │                         #   同时承载 errdoc / schemadoc / tree / lint-* 等子命令
│   ├── internal/
│   │   ├── errs/             # ★ 纯错误码定义，不 import 任何 HTTP 框架
│   │   ├── httpx/            # huma 胶水：响应封套 / NewError 覆盖 / Transformer
│   │   ├── config/  middleware/  task/
│   │   ├── auth/             # 登录、会话、密码
│   │   ├── authz/            # Casbin 包装 + data scope
│   │   ├── perm/             # 权限点注册表 + 模块声明
│   │   ├── tenantsql/        # ★ 「一条 SQL 有没有绑到租户上」的唯一判据（叶子包，只依赖标准库）
│   │   │                     #   被两处调用：构建期 gen lint-sql、运行期 repo 的 pgx tracer。
│   │   │                     #   两边同一份实现 —— 分成两份，中间那道缝就是下一个漏洞
│   │   ├── audit/
│   │   └── repo/  service/  handler/
│   │       # ★ repo/internal/sqlcgen/ —— sqlc 的裸产物再套一层 internal。
│   │       #   Go 规定 internal 下的包只能被它父目录那棵树 import，所以
│   │       #   service / handler **在编译期**就拿不到不带租户的查询句柄
│   │       #   （MULTI-TENANCY.md §1.2 ①）。业务代码只能走 repo.Store.ForTenant。
│   │       # crypto/ storage/ notify/ llm/ mcp/ 是后面几步才建的包，
│   │       # 现在**不留空目录** —— git 不跟踪空目录，留了在别人机器上也不存在
│   └── db/{migrations,queries}/
│
├── frontend/src/
│   ├── api/                  # client.ts（唯一请求出口）+ OpenAPI 生成的类型
│   ├── components/           # ★ 项目级组件：ListPage / FormDialog / DetailPage / DateTime / RequirePerm
│   ├── components/ui/        # shadcn 原始组件
│   ├── features/<key>/       # 每个模块一个目录
│   ├── routes/  lib/
│
├── config/config.example.yaml   # config.yaml 不进版本库
│
├── deploy/                   # ★ 部署相关全部收拢
│   └── docker-compose.yml  backend.Dockerfile
│                           # nginx.conf / frontend.Dockerfile / deploy.sh 等真要部署时再加
│
├── docs/                     # DECISIONS / MODULES / MEMORY / ERROR_CODES / SCHEMA
│   │                         # + MULTI-TENANCY（横切四层，单独成文）/ SCALING（伸缩评估）
│   └── superpowers/specs/    # 单个功能的设计稿，做完就是历史记录，不是长期契约
├── scripts/                  # ★ 开发脚本：test-gen.sh（生成器自测，§10.5）等
└── .claude/skills/           # new-module / new-field / precheck / troubleshoot
```

**`internal/errs` 与 `internal/httpx` 必须分开** —— service 层只 import `errs`（纯 Go，无框架依赖），
`httpx` 才碰 huma。否则「service 不 import echo/huma」这条红线在错误处理这里就破了。

**防目录漂移靠 lint，不靠这份文档。** 上面这棵树只说「应该长什么样」，实际约束由 `make lint-structure` 强制：

- 顶层目录必须在白名单内（新增顶层目录 → 失败，先改本节再改代码）
- `backend/internal/*` 的包名必须在白名单内
- `backend/internal/*/internal/*`（再套一层 internal）只允许白名单里那几处 ——
  目前只有 `repo/internal/sqlcgen`，理由见上面那棵树
- `backend/internal/service/<key>/` 下只允许 `service.go` / `errors.go` / `service_test.go`
- `frontend/src/features/<key>/` 下只允许约定的 8 个文件名
- `modules/*.yaml` 的 `key` 必须与 `service/<key>`、`features/<key>` 一一对应（三处对不上 → 失败）。
  **例外：`service/platform`** —— 平台管理端不是业务模块（没有业务表、没有 YAML、
  前端是另一套外壳走 `routes/platform`），白名单在 `lintstructure.go` 的 `notBusinessModules`

需要看**当前真实**目录时跑 `make tree`，不要手工往文档里抄 —— 抄了就会漂。

---

## 2. 数据库约定

### 2.1 主键：UUIDv7 ⚠️ 硬约束

所有表主键统一 **UUIDv7**（Go 侧 `google/uuid` 的 `NewV7()` 生成，PG 存原生 `uuid` 类型）。

- 自增 bigint **ID 可枚举** —— `/api/orders/1`、`/2` 可遍历，`#1024` 号订单泄露业务量
- UUIDv4 纯随机 → B-tree 索引碎片化、不可按时间排序
- UUIDv7 = 时间有序前缀 + 随机后缀：索引友好、天然按创建时间排序、不可枚举

代价是索引 16 字节（vs bigint 8 字节），本项目数据量下无感。

### 2.2 每张表的标准列

```sql
id          uuid        primary key,          -- UUIDv7，应用侧生成
created_at  timestamptz not null default now(),
updated_at  timestamptz not null default now(),   -- DB 触发器自动维护
deleted_at  timestamptz,                          -- 软删除
created_by  uuid,                                 -- 创建人，审计 + data scope 都要用
version     integer     not null default 0        -- 乐观锁
```

**业务表按需再加 `dept_id`** —— 当前 data scope 只做 `all`/`self` 两档，不建部门表；将来要加是加列 + 回填，本项目数据量下可控。

### 2.3 软删除 + 唯一约束 ⚠️ 经典坑

软删除会让删掉的值继续占用唯一约束。**一律用部分唯一索引**：

```sql
CREATE UNIQUE INDEX uk_users_username ON users(username) WHERE deleted_at IS NULL;
```

生成器自动产出，AI 不需要记住这条。

### 2.4 乐观锁

更新一律 `WHERE id = ? AND version = ?`，`RETURNING` 新 version。影响行数为 0 → 返回 `common.version_conflict`。

### 2.5 时间 ⚠️ 存 UTC，显示北京时间

**一条线贯穿到底：库里全是 UTC，接口传 UTC，只有页面上显示成北京时间。**

| 层 | 约定 |
|---|---|
| DB | 所有时间列 `timestamptz`，PG 容器 `TZ=UTC` |
| Go | 进程入口设 `time.Local = time.UTC`（`cmd/server/main.go`）。**不靠每处记得写 `.UTC()`** —— pgx 解码出来的 `time.Time` 默认带本机时区，少写一处就漂一处 |
| API | RFC3339 带 `Z`，如 `2026-08-07T08:15:02Z`。`TestAPITimesAreUTC` 守着这条 |
| 前端 | **固定按 `Asia/Shanghai`（UTC+8）渲染，不跟浏览器时区** |

前端为什么不跟浏览器时区：内部系统，所有人说的都是北京时间。跟浏览器时区的话，
有人电脑时区设错、或者出差改了时区，同一条记录两个人看到的时间不一样，对账时会吵架。
时区常量写在 `frontend/src/lib/datetime.ts` 一处，将来真要做多时区，改那一处 + 加用户偏好即可。

渲染一律走 `<DateTime value={...} />`，**ESLint 禁止裸调 `toLocaleString`**（§7.3）。

### 2.6 其它

- 常用过滤/排序字段自动建索引
- 文本模糊搜索字段建 `pg_trgm` GIN 索引（`ILIKE '%x%'` 用不到普通索引，这是最常见的性能坑）
- 分页用 offset（不做深翻页，keyset 的复杂度不值得）
- 审计表按月分区，保留期到了整分区 `DROP`

---

## 3. 权限模型：一处声明，三处生效 ⚠️ 核心设计

菜单权限、API 权限、数据权限**不是三套东西**，是同一个权限点的三个投影面。**配置只有一处。**

### 3.1 模块声明

```go
var OrdersModule = perm.Module{
    Key:    "orders",
    Name:   "订单管理",
    Menu:   perm.Menu{Path: "/orders", Icon: "package", ShowIf: "list"},
    Scoped: true,                      // 参与数据权限；共享资源填 false
    Actions: []perm.Action{
        {Key: "list",   Name: "查看列表", AITool: true,  AIDesc: "查询订单，支持按时间范围、状态、关键字筛选"},
        {Key: "read",   Name: "查看详情", AITool: true},
        {Key: "create", Name: "新增",     AITool: false},
        {Key: "update", Name: "编辑",     AITool: true,  Confirm: true},
        {Key: "delete", Name: "删除",     AITool: false},   // 危险操作不给 AI
        {Key: "export", Name: "导出",     AITool: true,  Confirm: true},
    },
}
```

一份声明自动产出四样东西：Casbin 资源清单、菜单树、角色配置页的勾选树、MCP/AI 的工具列表。

### 3.2 资源分三类

| 类型 | 例子 | 数据权限 |
|---|---|---|
| **共享资源** | 系统设置、字典、角色、部门 | 不参与，只有功能权限（`Scoped: false`） |
| **归属资源** | 订单、客户、工单 | 走 scope 过滤 |
| **派生资源** | 图表、仪表盘、报表 | **不需要自己的权限** —— 聚合查询走同一个 scope 注入点，看到的自然是自己那份 |

### 3.3 数据范围

角色上一个字段 `data_scope`，只有 `all` / `self`。多角色取最宽（有一个 `all` 就是 `all`）。

⚠️ **多租户下 `all` 的意思是「本租户内的全部数据」，不是真·全部。**
租户是**硬边界，不是一档 scope**：`data_scope` 可配置，`tenant_id` 不可配置，
任何角色任何 scope 都跨不过去（`docs/MULTI-TENANCY.md` §3.2）。界面文案用「本组织全部数据」。

repo 层**默认拒绝**：

```go
scope := authz.MustScope(ctx, "orders")   // ctx 无 scope → 报错，不是放行
```

SQL 侧一条查询覆盖两种范围，不需要 builder：

```sql
AND (sqlc.narg('owner_id')::uuid IS NULL OR created_by = sqlc.narg('owner_id'))
```

`all` 传 NULL，`self` 传当前用户 ID。

### 3.4 页面内细粒度 = 把资源拆细

「只能管理部分系统设置」这类需求，做法是按分组拆成多个资源：`settings.security` / `settings.storage` / `settings.audit` / `settings.appearance`。

于是自动成立：一个都没有 → 看不到菜单、URL 直输被守卫挡回、API 403；只有一个 → 看得到菜单，页面里**只渲染那一组**（不是灰掉，是不存在），改别的组 403。

菜单 `ShowIf: any` = 拥有 `settings.*` 任意一个即显示。

### 3.5 用户界面上只做两件事

勾权限点复选框、选数据范围（全部 / 仅自己，只在 `Scoped: true` 的模块上出现）。给人赋权只有一步：**这个人是什么角色**。

### 3.5.1 权限体系的三条兜底 ⚠️ 都是「防止把自己锁在门外」

这三条不是功能，是**不加就会出人命的护栏**，第 ④ 步的用户/角色模块里都有实现和测试。

⚠️ **多租户下三条全部是「每租户一份」** —— 判断查询必须带 `tenant_id`。
不带的话，A 公司还有 admin 就会让 B 公司删掉自己最后一个 admin，**B 公司被锁死**
（`docs/MULTI-TENANCY.md` §3.2 ①）。

1. **内置 admin 角色不可改不可删**（`role.builtin_immutable`）。它是权限体系的地板 ——
   被改成没权限之后，没人能再进后台把它改回来，只能上数据库救。
2. **通配权限 `*:*` 只保留给内置 admin**（`role.wildcard_reserved`）。
   普通角色勾不到，所以「唯一的通配角色不可被停用」这条自动成立。
3. **最后一个可用的超级管理员既不能停用、不能删除，也不能被摘掉角色**（`user.last_admin`）。
   ⚠️ 第三种最隐蔽：状态还是「启用」，页面上那个人看着好好的，其实已经什么都干不了了。

另外**不能对自己动手**（`user.self_target`）：自己删自己、自己重置自己都是纯误操作。
改自己的密码走 `/me/password`，那条路要验原密码。

**已知的权限边界**：拿到 `user:update` 就等于能给任何人（包括自己）分配任何角色，
也就是能自我提权到管理员。这是「谁能改用户的角色」这件事本身的性质，不是漏洞 ——
⚠️ 但在多租户下**这条自提权的上限必须钉死在「本租户 admin」，永远够不到平台管理员**
（`docs/MULTI-TENANCY.md` §3.2 ⑤）——
缓解手段是别乱发 `user:update`，以及所有变更都进审计。真要拆，就把「分配角色」
做成独立权限点（§3.4 的拆资源手法）。

### 3.6 四层拦截

```
① 菜单层：后端 GET /api/me 返回「过滤后的菜单树」，前端只渲染 → 前后端不可能不一致
② 路由层：RequirePerm 守卫，防直输 URL 绕过菜单
③ 接口层：Casbin 中间件 ← 真正的拦截线
④ 数据层：scope 注入 ← 决定看到哪些行
```

### 3.7 防漏配（不靠自觉）

- **注册路由时权限点是必填参数**，写不出没有权限的接口
- **启动自检**（`server --selfcheck`，纯内存、不连库、秒级）：每个路由都有权限点声明 / 声明的权限点存在于模块注册表 / 每个模块声明的权限点都有路由实现（反向检查，防止勾了权限点却没接口）—— 任一不满足**服务直接启动失败**并打印是哪个路由
- **scope 漏注入靠运行时 + 测试**，不靠启动自检 —— 静态查不出来。`authz.MustScope(ctx, key)` 在 ctx 无 scope 时返回错误（默认拒绝）；生成器为每个 `Scoped: true` 模块产出一条测试：不带 scope 调 List 必须报错
- 权限点变更走 goose 迁移，部署时自动同步到 Casbin
- ⚠️ **多租户下还要查「新表忘了开 RLS」** —— 那是最致命的漏配，一张裸奔的表就是全租户泄露。
  可以机械检查（查 `pg_class.relrowsecurity`），见 `docs/MULTI-TENANCY.md` §3.2 ⑧

---

## 4. API 契约

### 4.1 不用「永远 200 + 封套」

HTTP 状态码表达结果，body 结构统一。全 200 的代价：监控错误率永远 0%、逆着 huma/OpenAPI 走、污染生成的 TS 类型、对外提供 API 不专业。

**统一的是「前端组件看到的接口」，不是「HTTP body 的字面结构」** —— 转换在 `client.ts` 里做一次。

### 4.2 成功响应

```go
type Data[T any] struct {
    Data      T      `json:"data"`
    RequestID string `json:"request_id"`
}

type Page[T any] struct {
    Data       []T        `json:"data"`
    Pagination Pagination `json:"pagination"`   // page / page_size / total
    RequestID  string     `json:"request_id"`
}
```

分页信息独立，**禁止塞进 data 或响应头**。

### 4.3 错误响应：RFC 9457 + 扩展

huma 原生输出 RFC 9457 Problem Details，顺着它走并扩展（RFC 明确允许扩展成员）：

```json
{
  "type": "about:blank",
  "title": "Conflict",
  "status": 409,
  "detail": "用户名已存在",
  "code": "user.duplicate_username",
  "request_id": "req_01HX...",
  "errors": [{ "location": "body.username", "message": "该用户名已被占用" }]
}
```

前端只读 `code`（机器判断）和 `detail`（中文文案）。

实现：覆盖 `huma.NewError`。

### 4.4 request_id 用 Transformer 统一注入

`huma.NewError` 签名里没有 ctx，注入不了。正确钩子是 **`huma.Transformer`**（`func(ctx huma.Context, status string, v any) (any, error)`）—— handler 之后、序列化之前，能拿到 ctx。注册一次，**所有响应自动带 request_id，handler 一行不用写**。

**日志那一侧同理，用 `slog.Handler` 包一层**（`httpx.LogHandler`）：从 ctx 里
自动补 `request_id` 和 `tenant_id`（MULTI-TENANCY.md §12.1），
每个调用点一行不用写。靠自觉写的话，漏掉的恰好会是出事那条 ——
平时没人会想起来给一条 warn 加租户，而客户报障时给你的是「我们公司昨天下午打不开」。

⚠️ 包 handler 时 **`WithAttrs` / `WithGroup` 必须重新包一层再返回**，
少写就等于 `logger.With(...)` 之后全丢 —— 而 `With` 正是长期运行的组件最爱用的写法。

Transformer 里**用接口断言，不用反射**（反射跑在每个响应上，既慢又脆）：

```go
type requestIDSetter interface{ SetRequestID(string) }
// Data[T] / Page[T] / 自定义错误模型都实现它
if s, ok := v.(requestIDSetter); ok { s.SetRequestID(reqIDFrom(ctx)) }
```

中间件同时在所有响应打 `X-Request-Id` 头，覆盖 204 这类无 body 场景。

### 4.5 错误码注册表

```go
func Define(code string, status int, message string) *Code   // 重复 code → 启动 panic
```

每模块一个 `errors.go`（生成器自动创建）：

```go
var (
    ErrNotFound  = errs.Define("user.not_found",          404, "用户不存在")
    ErrDuplicate = errs.Define("user.duplicate_username", 409, "用户名已存在")
)
```

命名 `<domain>.<reason>`，全小写下划线分词。domain 四段：`common.*` / `auth.*` / `perm.*` / `<module>.*`（**模块 key 和权限点里的 key 保持一致**）。

### 4.6 内置通用错误码

| code | HTTP | 文案 | 前端全局处理 |
|---|---|---|---|
| `common.validation_failed` | 400 | 请求参数校验失败 | errors 自动映射到 RHF 字段 |
| `common.not_found` | 404 | 资源不存在 | |
| `common.version_conflict` | 409 | 数据已被他人修改，请刷新后重试 | 弹确认框 + 「刷新」按钮 |
| `common.idempotency_conflict` | 409 | 重复请求 | 静默忽略 |
| `common.rate_limited` | 429 | 操作太频繁，请稍后再试 | toast |
| `common.internal_error` | 500 | 服务器内部错误，请稍后重试 | toast + 显示 request_id |
| `common.service_unavailable` | 503 | 服务暂时不可用 | toast |
| `auth.unauthenticated` | 401 | 请先登录 | 跳登录页 |
| `auth.session_expired` | 401 | 登录已过期，请重新登录 | 跳登录页 |
| `auth.invalid_credentials` | 401 | 用户名或密码错误 | 表单内提示，不跳转 |
| `auth.account_locked` | 403 | 账号已锁定 | 表单内提示 |
| `auth.must_change_password` | 403 | 首次登录请修改密码 | 跳改密页 |
| `auth.password_expired` | 403 | 密码已过期，请修改 | 跳改密页 |
| `auth.csrf_invalid` | 403 | 请求校验失败，请刷新页面 | 提示刷新 |
| `perm.denied` | 403 | 无权限执行此操作 | toast |
| `perm.scope_denied` | 403 | 无权访问该数据 | toast |

`common.version_conflict` 体现了统一错误码的价值：乐观锁冲突全站写一次处理逻辑，后面所有模块自动继承。

### 4.7 自动化保障

1. 重复 code → 启动 panic
2. `make errdoc` 导出 `docs/ERROR_CODES.md`，CI 检查是否最新（生成后 `git diff` 必须为空）
3. `make errcodes-ts` 由错误码注册表生成 `frontend/src/api/errorCodes.ts` 的联合类型 `ErrorCode`，
   `ApiError.code` 收成这个类型，前端 `err.code === 'xxx'` 拼错**编译期报错**（`make check` 校验它最新）。
   ⚠️ 不是靠 `openapi-typescript`：huma 出的 OpenAPI 里 `code` 只是 `string`，
   单靠它前端拿到的还是裸 string，拼错不报 —— 这条曾经是空头承诺，现在由专门的生成器兑现
4. 生成器给新模块产出 `errors.go` 骨架
5. lint：handler 包里返回的 error 必须来自 `*errs.Code`

### 4.8 API 硬规则

1. handler 里**禁止** `errors.New` / `fmt.Errorf` 直接返回给前端
2. **5xx 永远不带内部细节** —— 只有通用文案 + request_id；堆栈和 SQL 只进日志
3. 每个响应带 `X-Request-Id` 头
4. 新模块必须有 `errors.go`，错误码前缀 = 模块 key
5. API 版本化 `/api/v1/`
6. 所有写接口支持 `Idempotency-Key` 请求头
7. **可选的 body 字段必须加 `,omitempty`** —— huma 默认把所有字段当必填，
   漏了的话调用方不传 `remark` 就是 400，而 OpenAPI 上还写着它有默认值，自相矛盾
8. **默认值在 service 兜，不在 handler 兜**。huma 的 `default:` tag 只写进文档，
   反序列化时不填值；放 handler 的话每个入口都要记得兜一次，漏一个就往库里写空串，
   撞 CHECK 约束变成 500。service 的 `applyDefaults()` 是唯一一处
9. **更新/删除都要带 `version`**（乐观锁，§2.4）。DELETE 也带 body ——
   塞 query string 里不如放 body 干净，RFC 9110 允许

---

## 5. 配置分层

| 层 | 内容 | 特性 |
|---|---|---|
| `config/config.yaml` | **仅启动必需**：DB DSN、监听端口、session 密钥、日志级别、KMS/S3 provider 与凭据 | 不进版本库，仓库只留 `config.example.yaml`；用 koanf 加载（不用 viper，依赖太重）；**禁止 .env** |

容器部署时允许用环境变量覆盖**单项**：前缀 `FRIES_`，双下划线表示层级
（`FRIES_DATABASE__HOST=postgres` 覆盖 `database.host`）。只为解决「compose 里 DB 主机名
和本地不同」这一类问题，**配置的真相仍在 config.yaml**，不是把配置搬进环境变量。
| DB `settings` 表 | 密码策略、登录锁定阈值、审计保留天数、上传大小限制、分页默认值、系统名称/Logo、导出行数上限、AI 模型与 base_url、AI prompt、token 配额 | 后台页面可改，**改完立即生效不重启** |

**热加载机制**：内存缓存 + PG `LISTEN/NOTIFY`。改配置 → 写库 → `NOTIFY settings_changed` → 所有实例刷新缓存。多实例自动同步，不引 Redis。

**AI 的 prompt 必须放 DB**，不能硬编码 —— 否则调一次 prompt 就要重新编译部署。

---

## 6. 安全基线

- 登录失败锁定（账号 + IP）、密码复杂度与有效期、首登强制改密
- **审计防篡改**：审计表 DB 层撤销应用账号的 UPDATE/DELETE 权限（只留 INSERT）+ 每行 `prev_hash`/`hash` 哈希链 + 按月分区 + 保留期可配
  - 哈希链由 **DB 触发器**计算，不由应用算 —— 应用能算就意味着应用能伪造
  - DB 层那道防线要求应用用**受限角色 `fries_app`** 连库；本地开发用超级用户，
    这条防线不生效，服务启动时会打 WARN 提醒（迁移见 `backend/db/migrations`）
- **审计分两层**（缺一不可）：
  - **中间件层**（自动，全部接口）：谁 / 何时 / 什么资源 / 什么动作 / IP / UA / 结果码 / 耗时
  - **handler 层**（生成器产出）：**哪条记录**、参数摘要（脱敏截断）。因为「新增」操作的记录 ID 在响应里而不在请求里，中间件只看得到字节流，拿不到 —— 必须在 handler 里补
- **审计范围**：读写全记 + 前端路由守卫上报 `page_view`（路由切换命中缓存时后端看不见）
- 密码 argon2id；敏感字段入审计前脱敏；请求体大小限制 + 超时中间件
- **CSRF 只对 cookie 认证生效** —— Service Account 用 API Key 认证时必须跳过 CSRF 校验（没有 cookie 就没有 CSRF 风险）。漏了这条，外部系统对接会莫名其妙 403，且很难排查
- **限流三个维度**，都是 `x/time/rate` 的内存版（不引 Redis）：
  - **登录**：IP + 公司代码（`internal/auth/guard.go`）—— 后者挡「换一堆 IP 打同一家公司」
  - **全局**：按 IP，跑在认证之前（`internal/middleware/ratelimit.go`）
  - **按租户**：跑在认证之后，同一个文件里的另一个实例 —— 少了它，
    一个租户跑批量导入会让所有客户变慢（MULTI-TENANCY.md §3.2 ⑦ 和 MULTI-TENANCY.md §12.3）
- 文件上传：扩展名白名单 + 真实 MIME 嗅探（不信任前端 Content-Type）+ 大小限制 + 磁盘随机名 + **下载走后端鉴权接口，不暴露静态目录**
- 幂等键中间件（所有写接口）
- 数据库**每日自动备份** + 保留期 + 失败告警（`pg_dump` 定时任务）

**不做**（用户明确选择）：TOTP 两步验证、敏感操作二次输密码。

⚠️ **平台管理员那一侧是明确的欠账，不是「不做」**（MULTI-TENANCY.md §9.2）：
拿到一个平台管理员账号 = **所有租户**的数据，和普通用户不是一个风险层级。
现在给它的补偿是：登录单独限流（比租户端更严）、强制强密码（最少 12 位，
写死在代码里不吃租户级配置）、每个动作都进平台审计链。
这些拦得住撞库，拦不住凭据被钓走 —— 真接了正经客户，TOTP 要补上。

---

## 7. 前端约定

### 7.1 一致性靠三层强制，不靠自觉

**第一层：没有第二个选择。** 服务端数据只能走 TanStack Query（`src/api/client.ts` 是唯一出口）。不引 zustand/Redux —— 服务端状态归 Query，UI 状态用 `useState` 且只在单组件内。全局共享状态只有两个：当前用户+权限（Query 缓存 + Context）、明暗主题。

**第二层：布局组件封死。** 新模块只能用 `<ListPage>` / `<FormDialog>` / `<DetailPage>` 三个骨架填内容，**不允许自己搭页面结构**。

| 踩过的坑 | 封装层解法 |
|---|---|
| 表单无法下滑 | `<FormDialog>` 内置 `max-h-[85vh]` + 内容区 `overflow-y-auto` + **sticky footer**（保存/取消永远可见） |
| 找不到关闭按钮 | 强制右上角 X + ESC + 点遮罩，三条路都通 |
| 字体大小 UI 不一 | 只允许 Tailwind theme token，页面不写死任何 hex / px |

**第三层：CI 拦截。** ESLint 规则禁止：tsx 里出现 `#[0-9a-fA-F]{6}`、`style={{`、裸 `fetch(`、裸 `new Date().toLocaleString()`、直接 import 原始 `Dialog`（只能用封装的 `FormDialog`）。CI 跑 lint + typecheck，不过不许合。

### 7.2 数据刷新：全局配置，不靠每页自觉

```ts
new QueryClient({
  defaultOptions: {
    queries: {
      staleTime: 0,
      refetchOnMount: 'always',    // ← 每次进页面必重取
      refetchOnWindowFocus: true,
      retry: 1,
    },
  },
})
```

先渲染缓存的旧数据 + 后台重取 + 无缝替换，秒开且是最新的。再加 mutation `onSuccess` 里 `invalidateQueries` 精确失效相关查询，双保险。

### 7.3 其它

- 列表页：分页 + 搜索（300ms 防抖）+ 表头排序（服务端），**参数同步到 URL query**（刷新/分享保持状态）
- 加载/空/错误三种状态都要有；提交中禁用按钮 + loading；成功给 toast
- **表单脏检查**：改了没保存想离开 → 弹确认
- 时间统一用 `<DateTime value={...} />` 组件（`Intl.DateTimeFormat` **固定 Asia/Shanghai**，见 §2.5），**ESLint 禁止裸调 toLocaleString**
- 暗色模式（shadcn + next-themes）
- 路由懒加载（`React.lazy` + Suspense）
- 生成页面前先想清楚这一页的主操作是什么，再铺组件；间距按 §7.5 的节奏来，别自己发明

### 7.4 每个模块的前端结构

`features/<模块>/` 只允许这几个文件（`make lint-structure` 强制）：

`api.ts` / `queries.ts` / `schema.ts` / `types.ts` /
`ListPage.tsx`（`/xxx`）/ `NewPage.tsx`（`/xxx/new`）/ `DetailPage.tsx`（`/xxx/:id`，自带编辑态）/
`FormDialog.tsx`（只放批量操作这类「一次动作」，不是单条记录的编辑器，§7.6）

外加它们各自的 `*.test.tsx`（`Foo.tsx` 的测试就叫 `Foo.test.tsx`，`make lint-structure` 认这条）。

放不下就说明这个模块该拆了，不要往里加文件。

### 7.5 设计语言 ⚠️ 改之前先看完，每个数都有理由

方向是**中性扛主视觉、彩色只做点缀、松**。

**为什么主色是近黑而不是某个彩色**：这是通用后台框架，主题色一浓就带上了行业和人群的
暗示（比如淡紫在 HR 系统里很合适，放到通用框架里就不合适了）。近黑 + 一点低饱和蓝
最不挑场景，也最难做丑。

**颜色**（全部在 `src/index.css` 的 `:root` / `.dark` 里，页面不许写死 hex）

| token | 浅色 | 说明 |
|---|---|---|
| `--background` | `#fbfbfa` | 内容区。略偏暖的灰白，纯冷灰会硬 |
| `--sidebar` / `--card` | `#ffffff` | **侧边栏、顶栏、卡片是纯白**，比内容区亮一档 |
| `--foreground` | `#232227` | 正文。中性石墨，不带色偏 |
| `--muted-foreground` | `#75737b` | 次要文字、表头 |
| `--primary` | `#1f1d24` | **近黑**。主按钮、品牌标记 |
| `--accent` | `#3b6ea5` | 低饱和蓝。**只用于选中态、链接、焦点环** |
| `--accent-subtle` | `#eef2f7` | 选中态底色 |
| `--border` | `#e7e5e4` | 分隔线 |
| `--border-subtle` | `#e7e5e4` @70% | **卡片边框**。比分隔线再淡一档 |

暗色里 `--primary` 翻成近白 `#ebebee` + 深色字 —— 近黑按钮在深色底上等于隐身。

**蓝按钮（`variant="accent"`）一个页面最多一个**，多了蓝就不再是点缀。

**层级靠底色差，不靠投影。** `.surface` 没有 `box-shadow` —— 一给投影每块面板都「浮」起来，
整页立刻变廉价。

**尺寸**

| 元素 | 值 |
|---|---|
| 根字号 / 行高 | 14px / 1.6（中文在紧行高下会挤成一团） |
| 圆角 | 卡片、输入、导航项 12px；按钮 10px |
| 按钮 | 高 32px、字号 12px、字重 500 |
| 输入 / 下拉 | 高 40px、字号 14px、底色 `--background`（**不是白**） |
| 表头 `th` | 高 44px、内边距 `12px 20px`、字号 12px/500、底色 `bg-muted/40` |
| 单元格 `td` | 高 **56px**、内边距 `0 20px`、字号 14px |

⚠️ **根字号是 14px，所以 `h-11` 是 38.5px 不是 44px** —— Tailwind 的 rem 工具类
全都被缩了 0.875 倍。设计稿给的是 px 的地方（表头、行高）一律写 `h-[44px]` 这种
显式值，别用 `h-11` 凑，凑出来永远差一截。
| 侧边栏 | 宽 224px，菜单项 40px、圆角 12px，分组标题 11px/字距 `0.05em` |
| 顶部条 | 高 **56px**，和侧栏品牌块**必须等高** —— 差 1px 那条通栏分隔线就错位 |
| 页面标题 `h1` | 24px/600，**字距 `-0.025em`** —— 中文大标题不收字距会显得松垮 |

**每页条数按视口高度自适应**（`useListParams`，下限 10、上限 50）。固定条数两头都不对：
小屏上撑破一屏要滚，大屏上表格底下空一大片。表格卡片**高度跟着内容走、不撑满**。
每页条数变了会回到第 1 页 —— 否则原来的页码可能已经越界。

表格列一律 `whitespace-nowrap`：折一行整行就从 56px 变 72px，十行下来全乱。

**文本筛选一律模糊匹配**，走 `repo.LikePattern`（`li` 能筛出 `list`）。等值匹配是错的，
用户不会先背下完整值再来筛。`%` `_` `\` 必须转义 —— 不转义的话用户敲个 `%` 就等于
「匹配全部」。

**筛选项是数据不是 JSX**：模块传 `FilterSpec[]`（`{ key, label, placeholder }`），
排版由 `<ListFilters>` 统一决定。形态是「搜索框 + 筛选按钮 + 清空 + 已生效条件 chip」，
**顺序固定**：按钮在前、chip 在后 —— chip 数量随筛选变多，放前面会把按钮挤到右边找不着。
`label` 必填：只靠 placeholder 的话一输入内容提示就没了。

筛选输入框**必须受控**（`useFilterInput`）。非受控只在挂载时读一次 `defaultValue`，
外面清空了框里的字还在，看着像没清掉。但也不能直接绑 URL —— URL 是 300ms 防抖之后
才更新的，直接绑会打一个字卡一下；所以本地存即时值，只在外部值不是自己刚写进去的那个
时才回灌。

⚠️ **一次 tick 里只能改一次 URL 参数。** `setSearchParams` 的函数式写法拿到的是当前
这次渲染的参数，连调几次每次都从同一份旧值算起，后一次盖掉前一次 —— 表现出来就是
「点了清空一点反应都没有」。要一起改多个参数就用 `params.reset()` 那样在一个 `update`
里删完，并且**顺手清掉防抖的待提交值**，否则排队中的 `setTimeout` 会在 300ms 后把刚
清掉的条件再写回去。

**浮层的底色必须不透明**（`bg-popover`）。下拉菜单、弹窗压在表格和表单上，
半透明的话下面的字会透上来，两层字叠在一起完全没法读 —— 这个坑踩过一次。

**留白节奏**：内容区 `px-8 py-8` → 标题块 `mb-6` → 筛选行 `mb-4` → 表格卡片。
筛选控件独立一行、40px 高，表格上方必须留出这块位置。

**外壳结构**：左侧栏（只放菜单）+ 顶部条（面包屑、全局搜索、账号）+ 内容区。
导航之外的东西一律进顶部条 —— 混进侧栏，侧栏就变成杂物抽屉了。

#### 字号刻度 ⚠️ 全站只有 6 档

没定刻度之前，一个抽屉里同时出现过 9.6 / 10 / 11 / 11.4 / 12 / 13px —— 那不是层次，那就是乱。
现在定死 6 档，ESLint 禁 `text-[...]` 和 `text-3xl` 以上：

| class | px | 用在哪 |
|---|---|---|
| `text-xs` | 11 | 角标、`kbd`、侧栏菜单分组名（`.nav-group`） |
| `text-sm` | 12 | 表头、字段说明/提示、错误文案、**所有等宽文本** |
| `text-base` | 14 | 正文、**详情页的字段标签**（靠 `text-muted-foreground` 弱化，不靠压字号）。默认值，基本不用显式写 |
| `text-lg` | 16 | 区块标题（`.section-label`，详情页字段分组）、品牌名 |
| `text-xl` | 18 | 抽屉 / 弹窗标题 |
| `text-2xl` | 24 | 页面标题 |

值直接写 px 不写 rem：根字号是 14px，Tailwind 默认的 rem 刻度全被缩了 0.875 倍
（`text-2xl` 会变成 21px 而不是 24px）。**不够用就先改刻度，不许就地发明第 7 档。**

#### 中英文混排

- **字体栈中文排第一**：`'PingFang SC', 'HarmonyOS Sans SC', 'MiSans', 'Noto Sans SC',
  'Microsoft YaHei', system-ui, …`。顺序反了中文会被拉丁字体的回退字形接管，字形和字重都不对
- **等宽只用于「机器标识」**：ID、key、路径、权限点、密钥。`<code>` / `<kbd>` / `.mono`
  由 CSS 统一给字体和字号，**页面里不许再给 code 配字号** ——
  之前 `<code className="text-xs">` 是「又换字体又换字号」，两个变化叠一起最乱
- 等宽**固定小一档（12px）**：等宽字形本身更宽，和 14px 中文并排时看着会比中文还大
- 表格和任何要对齐的数字加 `.tabular`（`font-variant-numeric: tabular-nums`），
  否则每行数字宽度不同，列就是歪的
- **中文标题收字距 `-0.025em`**（`h1/h2/h3` 里统一给了）。中文字面本来就满，
  按默认字距排 24px 标题会显得松垮；纯拉丁不需要，所以不往正文上加

#### 表单弹窗的初始值 ⚠️ 必须走 defaultValues

编辑要先拉详情，很容易写成「弹窗常驻 + 数据到了 `reset()` 一下」。**那是错的**：
受控控件（`<Controller>` 包着的下拉）在 `reset()` **之后**才挂载，
首次渲染读到 `undefined`，Radix 就把它当非受控组件接管内部状态，之后 prop 再变也不认。
症状是「明明有值，下拉框却是空的」，而且表单一打开就是「脏」的（`isDirty` 为真）。

正确写法是拆两层：

```tsx
export function XxxFormDialog({ open, editing }) {
  const detail = useXxx(open && editing ? editing.id : undefined)
  if (!open) return null
  const ready = !editing || Boolean(detail.data)
  return <XxxForm key={ready ? (editing?.id ?? 'new') : 'loading'} initial={...} ready={ready} />
}
function XxxForm({ initial }) {
  const form = useForm({ defaultValues: initial })   // ← 初始值只从这里进
}
```

`key` 一换就是全新的表单实例，顺带解决「第二次打开还留着上次的数据」。

**枚举下拉一律用 `<SelectField options={...}>`**，不要自己拼 `Select` + `SelectItem`：
Radix 的 `SelectValue` 只在挂载时解析一次「当前值对应哪个选项的文字」，
后面把值换掉，触发器上还是空的。`SelectField` 自己渲染标签，绕开这个坑。

#### 交互硬规则

- **图标按钮的热区不能等于图标本身**。14px 的 ✕ 只有 14px 热区，鼠标差几像素就点不中，
  用户的感受是「这个按钮是坏的」。一律套一层 `size-8` 的容器
- **枚举字段的筛选必须是下拉**（`FilterSpec.options`），绝不能留成文本框：
  后端认的是 `active`，用户看到的是「启用」，让人手敲只会敲出 400
- **树上点行只选中，不折叠**。试过「点行顺带展开/收起」，结果选中父节点就会把子节点收起来，
  想看「这个部门下面有哪些组」反而要再点一次 —— 越用越别扭。折叠交给箭头，把它的热区放大到 20px
- **不许用 `window.confirm` / `alert` / `prompt`**（ESLint 拦着）。内嵌浏览器、WebView、
  开了「阻止弹窗」的标签页会**静默屏蔽并返回 false** —— 表现就是弹窗永远关不掉，
  ESC 和 ✕ 都没反应。用 `useConfirm` + `<ConfirmDialog>`
- **树形结构的计数要给两个数**：直属和含下级。只给直属，父节点上的数和下面几个子节点对不上，
  人会以为算错了；只给合计，又答不出「这个部门自己有几个人」
- **批量勾选只选当前页**。跨页全选是「我以为只选了这一页」的经典事故来源

#### 详情字段的排版

`<DetailItem>` 是**标签在左、值在右的一行一条**，不是两列网格：抽屉只有 448px 宽，
两列会把邮箱这种长值从中间劈断。标签列固定宽 + `whitespace-nowrap` ——
一换行这一条就变两行高，整列的节奏立刻乱掉。

#### 「没有分类」必须有入口 ⚠️ 容易漏

树形分类（部门、目录、标签）一定会有「还没被归类」的数据。它们**不属于树上任何节点，
所以在树里永远看不到** —— 不给入口的话，这些数据就是隐形的，谁也不知道还有没有人漏了。

fries 的做法是在部门树底部挂一个「未分配」入口（`department_id IS NULL`），
用户管理的部门筛选里也有同名选项。

**不要用「建一个『公司』根部门把所有人塞进去」来绕开**：那样每个人都挂在根节点上，
「谁还没被安排过」反而更查不出来了。真正要回答的问题是「有没有人漏了」，
那就直接按「没有归类」查。

⚠️ `:root` 必须写在 `.dark` 前面。next-themes 把 `dark` 类加在 `<html>` 上，两者命中同一元素、
权重相同，谁在后面谁赢。反了的话暗色整个失效，而且症状很怪（只有部分颜色不对）。

---

### 7.6 详情怎么展开 ⚠️ 只有两种，别自己发明第三种

**居中 Modal 放详情是错的** —— 内容一多就得滚，还把列表整个遮住。
`FormDialog` 只管新增/编辑，不许拿来当详情。

| 内容量 | 用什么 | 组件 |
|---|---|---|
| 同构的补充信息（一条日志的原始 detail、一单的商品明细） | **行内展开** | `<ListPage expandable={...}>` |
| 其余一切 | **独立页面** `/xxx/:id` | `<DetailPage>` + `<ListPage rowLink={...}>` |

选行内展开的判据是「**要不要上下对比**」：查审计最常干的事就是看同一个请求前后两次
改了什么，展开两条直接比最快。字段一多就该换详情页，别把列表撑成两层表格。

**没有侧拉抽屉这个选项。** 抽屉试过一版，废掉了：448px 宽逼着人把字段拆成页签，
每个页签又装不满；链接发不出去、开不了新标签、页面里再想加东西没地方放。
独立页面这些问题一个都没有 —— 而且详情页**允许分 tab**，那是它相对抽屉的空间优势。

**详情页路由和列表页平级**（`/users` 和 `/users/:id`），不要嵌套。嵌套会让列表
一直挂在那儿，那就退回抽屉了。列表状态不靠「留在内存里」保住，靠下面这条。

#### 从列表点进详情，返回时不能丢状态

三样东西各有各的保住方式，**都不需要把条件抄一份带在身上**：

| 要保住的 | 靠什么 |
|---|---|
| 筛选、搜索、页码 | 本来就在列表页 URL 上，且是 `replace` 写的（`useListParams`）—— 列表在浏览器历史里始终是**一条带着完整状态的记录**，退回去就是原样 |
| 列表数据不闪 | React Query 缓存先画出来，再后台刷新 |
| 滚动位置 | `<ListPage>` 内置（`useScrollRestore`），按 `location.key` 记 |

所以「返回」要做的只有一件事：**判断身后有没有那条记录**。从列表点进来的有，
退回去（`navigate(-1)`）；别人发的链接、⌘+点击开的新标签、直接输 URL 进来的没有，
跳到列表（`replace: true`，别再多堆一条历史）。这套判断在 `lib/detail-nav.ts` 里，
`<DetailPage backTo="/users">` 传个列表路径就够，模块不用自己写。

**行要用 `rowLink` 而不是在 `onRowClick` 里 navigate**：`rowLink` 会自动打上
「我是从列表来的」记号，还顺带把首列渲染成真链接 —— ⌘+点击开新标签、键盘 Tab 得进去，
这些是「详情做成页面」本来就该白拿的好处，用 `onRowClick` 就全丢了。

`<ScrollRestoration>` 用不了：react-router 自带的那个**只认 `window.scrollY`**，
而我们的外壳是整屏不滚、只有内容区内部滚（见 `AppShell`）。所以 `useScrollRestore`
盯的是容器，做的事和它一样。位置存内存不存 sessionStorage —— 真刷新时
`location.key` 会重新生成，存了也对不上号。

字段一律用 `<DetailSection>` / `<DetailItem>` 排，留白和字号和 `<ListPage>` 同一套
—— 从列表点进详情时标题不该跳一下。

#### 编辑在详情页里做，不弹窗 ⚠️ 全站没有编辑弹窗

`FormDialog` 只剩下**批量操作**这类「一次动作」还在用（比如批量调部门）。
**单条记录的新增和编辑一律是页面**：

| 干什么 | 去哪 |
|---|---|
| 新增 | `/xxx/new` —— 独立页面 |
| 看 | `/xxx/:id` |
| 改 | `/xxx/:id?edit=1` —— 同一页切编辑态 |

理由是字段一多弹窗就撑爆：内容区自己滚，人一边填一边找不到「保存」在哪；
而字段少的时候弹窗看着还行，等模块长大了再回来改，每个模块都要返一次工。

三条实现约定，**都是踩出来的**：

1. **只读态和编辑态必须是同一棵 JSX 树**，每行自己决定渲染文字还是控件
   （`<DetailItem>` 保证两态行高和标签位置一致）。拆成两个组件写，加字段时必然漏一边。
2. **数据没到之前不许挂载表单组件**。`useForm` 只在挂载那一刻读一次 `defaultValues`，
   早挂一步表单就是空的，之后数据到了也不会自己填上。直接开
   `/xxx/:id?edit=1` 这种链接最容易撞上。
3. **进编辑态要换 key 重挂表单**，让它带着当时最新的数据初始化。
   ⚠️ 但 key **不能用 `version`** —— 编辑到一半后台刷新一次（切窗口回来就会刷），
   版本一变就重挂，人正在敲的东西直接没了。用一个「第几次进编辑」的计数器。

离开页面前拦未保存的修改用 `useUnsavedGuard`（包的是 react-router 的 `useBlocker`）。
判断分两层，**两层都是踩出来的**：

- **换 pathname** → 拦。
- **同一个 pathname 下，只有换记录才拦，换模式不拦。** 退出编辑态是摘掉 `?edit=1`，
  那是同一页换模式 —— 都拦的话「保存」成功之后会被自己弹一道确认，而东西已经存进去了。
  但左右分栏页换一个树节点改的是 `?detail=`，那是换记录，**不拦就会一声不吭丢掉改动**。
  所以分栏页要把记录参数报给守卫：`useUnsavedGuard(dirty, ['detail'])`。

#### 表单排版有两套，别混用

| 场合 | 排法 | 组件 |
|---|---|---|
| 详情页、详情面板 | **标签在左、值在右** | `<DetailItem>` |
| 登录 / 改密 / 一次性动作弹窗 | **标签在控件上方** | `<FormField>` |

详情要能一屏装下几十个字段，左标签省掉近一半高度；而登录那种居中窄卡片里，
左标签会把控件挤得没地方。两种都对，各有各的场合。

#### 提交失败必须看得见 ⚠️ 「以为成功其实失败」是最贵的 bug

两条硬规矩：

1. **`catch` 里非 `ApiError` 的分支也要落地。** 只写 `if (err instanceof ApiError)`
   的话，别的异常会被静默吞掉 —— 按钮转一圈回来什么都没变，人以为存上了。
2. **错误横幅放在卡片最顶上**（`<DetailPage alert>` + `<FormAlert>`），紧挨着标题栏里的
   「保存」。挂在页面最底下等于没有：按钮在最上面，人根本看不见，只觉得点了没反应。

**版本冲突要给出路，不能只报错。** `common.version_conflict` 说明有人在你之前改了
同一条记录。光说「数据已被他人修改」没用，得配一个「重新加载」：
把最新数据取回来、换 key 重挂表单。⚠️ 必须 **`await refetch()` 之后再换 key**，
先换 key 的话重挂时拿到的还是旧数据，看着像按钮没反应。

## 8. 开放性与集成

### 8.1 别人调我（inbound）

**Service Account** —— 机器身份，绝不让外部系统用人的账号：

- 有自己的身份和角色，走**同一套 Casbin 权限**（不是另开后门）
- 认证用 API Key（当前）/ OAuth2 `client_credentials`（预留）。
  Key 形如 `fsa_<prefix>_<secret>`：prefix 明文存用来定位记录，secret 只存 argon2id 哈希；
  请求头用 `X-API-Key` 或 `Authorization: Bearer`
- 审计能区分「张三点的」和「某系统调的」
- 独立限流配额

**幂等性**：要求 `Idempotency-Key` 请求头，服务端记录去重。外部系统会重试，同一个操作不能执行两次。

### 8.2 我调别人（outbound）

```go
type Connector interface {
    Name() string
    Actions() []ActionSpec          // 声明能做什么，自动生成配置界面
    Execute(ctx, action string, params map[string]any) (Result, error)
}
```

三条纪律：凭据走 `Cipher` 加密存储（KMS 接进来自动生效）；每次外呼都审计；超时 + 重试 + 熔断。

### 8.3 事件推送

- **通知人** → nikoksr/notify（Slack / 飞书 / 钉钉 / 企微 / 邮件），包在自己的 `Notifier` 接口后
- **通知系统** → 自研通用 Webhook：目标 URL + 方法 + 自定义 Header + Body 模板（后台可配）+ HMAC-SHA256 签名 + 指数退避重试 + 死信队列 + 投递记录可查

### 8.4 AI 接入

**核心原则：Agent 不是新的权限主体，它是用户的代理。**

Agent 调的每个工具都走和人点按钮完全一样的链路：Casbin → data scope → 审计。tool 不直接碰 repo，调**同一个 service 层方法**并带上用户 ctx。

- **Tool 从权限点自动派生**：对话开始时按当前用户权限动态生成 tool 列表；没权限的工具**根本不出现**，AI 连尝试都不会尝试
- **绝不做 text-to-SQL**：LLM 生成的 SQL 不带 scope 条件 → 数据权限彻底失效。用受限语义层（预定义指标 × 维度 × 时间范围）
- **写操作三档**：只读直接执行 / 写操作返回「准备执行 X，确认吗」等用户点确认 / 危险操作（删除、改权限、改安全设置）**根本不暴露 tool**
- **提示注入防御**：工具返回结果标记为数据边界；写操作永远需人确认；tool list 在对话开始时按权限固定，中途改不了
- **全链路审计**：`ai_conversations` + `ai_tool_calls`，记原始提问、每步 tool call 参数、结果摘要、最终回复、token 消耗
- **配额**：每人每日 token 上限、单次对话轮数上限、超时熔断
- **MCP Server 和 REST API 是同一个 service 层的两个门面** —— 权限、scope、审计只实现一次
- **MCP 不做全量自动转换**：200 个接口全转会塞 4-8 万 token 进上下文，反而让模型选不对工具。只有 `AITool: true` 的进 MCP，工具描述单独写

---

## 9. 明确不做（划边界）

| 不做 | 理由 |
|---|---|
| OpenTelemetry / Jaeger | requestID 贯穿日志已够定位 |
| Sentry | panic 推飞书 + 结构化日志够了 |
| i18n | 中文单语，引入后每个字符串都要过 key |
| ~~多租户~~ | **已推翻，改为要做** —— 见 `docs/MULTI-TENANCY.md` |
| Redis | session 存 PG、限流用内存、配置同步用 LISTEN/NOTIFY |
| 消息队列 | |
| WebSocket | AI 流式用 SSE，通知中心用 Query 轮询 |
| pgvector | **预留不启用** —— 将来要文档问答加一行 `CREATE EXTENSION` |
| 表格虚拟滚动 | 分页已解决 |
| 上传断点续传 | 内网上传用不上 |
| feature flag | 10 人团队直接发布 |
| Playbook / SOAR 编排 | 不属于通用脚手架 |
| LLM 网关（one-api 等） | 现在直连；将来要加是改一行 base_url，零代码改动 |
| squirrel / ORM | sqlc + narg + 白名单排序已覆盖。⚠️ 做多租户时重新评估过 ent（它的 Privacy 层能自动过滤租户），结论仍是不换 —— 理由见 `docs/MULTI-TENANCY.md` §1.3 |

---

## 10. 代码生成器（fries-gen）

**这是「AI 自动做需求」能保持一致的机械保证，不是可选的便利工具。**

### 10.1 输入：模块定义 YAML

`modules/<key>.yaml`，**由 AI 起草，用户只过目**（AI 把它翻译成大白话念一遍，用户不碰 YAML）。

```yaml
key: supplier
name: 供应商
scoped: true                          # 参与数据权限
menu: { path: /suppliers, icon: truck }

fields:
  - { name: name,       type: string,  label: 供应商名称, required: true, unique: true, searchable: true, max: 100 }
  - { name: status,     type: enum,    label: 状态, filterable: true, default: active,
      values: { active: 合作中, terminated: 已终止 } }
  - { name: credit,     type: decimal, label: 授信额度, precision: [18, 2] }
  - { name: started_at, type: date,    label: 合作起始日, filterable: true }
  - { name: remark,     type: text,    label: 备注 }

sortable: [created_at, name, started_at]     # 排序白名单
actions:  [list, read, create, update, delete, export]
```

字段属性 → 产出：

| 属性 | 生成什么 |
|---|---|
| `required` | PG `NOT NULL` + zod `.min(1)` + 表单必填标记 |
| `unique` | **partial unique index**（`WHERE deleted_at IS NULL`）+ 唯一冲突错误码 |
| `searchable` | 关键字 `ILIKE` 条件 + **pg_trgm GIN 索引** |
| `filterable` | 列表页筛选控件 + 普通索引 + `sqlc.narg()` 参数 |
| `max` | `varchar(n)` + zod `.max(n)` + 表单 maxLength |
| `enum` | PG `CHECK` + Go 常量 + TS 联合类型 + Select + 列表页 Badge |

### 10.2 类型映射

| YAML | PG | Go | TS | zod | 控件 |
|---|---|---|---|---|---|
| `string` | `varchar(n)` | `string` | `string` | `z.string().max(n)` | Input |
| `text` | `text` | `string` | `string` | `z.string()` | Textarea |
| `int` | `integer` | `int32` | `number` | `z.number().int()` | Input |
| `decimal` | `numeric(p,s)` | `decimal.Decimal` | **`string`** | `z.string().regex(...)` | Input |
| `bool` | `boolean` | `bool` | `boolean` | `z.boolean()` | Switch |
| `enum` | `varchar` + CHECK | 常量组 | 联合类型 | `z.enum([...])` | Select + Badge |
| `date` / `timestamp` | `date` / `timestamptz` | `time.Time` | `string` | `z.string().datetime()` | DatePicker |
| `ref` | `uuid` + FK | `uuid.UUID` | `string` | `z.string().uuid()` | Select（远程搜索） |
| `file` | `uuid` + FK→files | | | | Upload |
| `json` | `jsonb` | `json.RawMessage` | `unknown` | | — |

**金额一律 `decimal`，JSON 传 string** —— JSON number 是双精度浮点，会丢精度。

**实现现状**（上表是设计意图，落地进度）：`string`/`text`/`int`/`decimal`/`bool`/`enum`/`date`/`timestamp`/`ref`
**已实现**。`ref` 的后端是复合外键 `(tenant_id, <字段>) → <目标>(tenant_id, id)`（防跨租户引用）+ uuid；
**编辑态是远程搜索选择器**（`RefSelect`）：搜目标模块的名字点一下、存 uuid；生成期解析目标模块的显示字段
（优先「name 且可搜」→ 任意可搜文本 → name → 第一个文本字段；一个文本字段都没有就报错）。**读态（列表 cell
+ 详情 view）显示名字**：List/Get 查询用 `sqlc.embed(t)` + `LEFT JOIN` 目标表（租户锚在 JOIN 条件
`r.tenant_id = t.tenant_id` 上，对齐 user 模块，租户检查器认）把目标显示字段作为 `<字段>_label` 一并查出，
View 加只读 `<字段>Label` 字段，前端读态直接显示 —— 无 N+1、无前端反查、名字随目标改动实时刷新。
`file`/`json` **还没做**（用会被校验拒）。控件列里 `bool` 实际用 Checkbox（不是 Switch）、`date` 用原生 date input。

### 10.3 托管文件 vs 种子文件 ⚠️ 重生成的前提

文件头打不同标记：

```go
// Code generated by fries-gen. DO NOT EDIT.          ← 托管：重跑无脑覆盖
// Generated by fries-gen as a starting point.        ← 种子：只在不存在时生成
// Safe to edit — regeneration will not overwrite.
```

文件分**三类**：

| 类别 | 重生成行为 | 文件（共 17 个） |
|---|---|---|
| **托管** ×9<br>AI 不许碰 | 无脑覆盖 | 迁移 SQL、`db/queries/<key>.sql`、`repo/<key>.go`、`handler/<key>.go`、`features/<key>/{api,queries,schema,types}.ts`、`routes/index.tsx` |
| **追加** ×1<br>只增不改 | 模块不存在才追加一节，**已存在的条目一个字不动** | `docs/MODULES.md` |
| **种子** ×7<br>归 AI/人所有 | 只在文件不存在时生成，之后只打印 diff 提示 | `service/<key>/{service,errors,service_test}.go`、`perm/modules/<key>.go`、`features/<key>/{ListPage,FormDialog,DetailPage}.tsx` |

**`docs/MODULES.md` 必须是「追加」而不是「托管」** —— 否则改一次字段重跑生成器，人写的业务说明就全没了。

**lint 规则：托管文件被手改 → CI 失败**（`make gen-all && git diff --exit-code` 必须干净）。

一个模块 17 个文件，**10 个 AI 完全不用碰**。AI 的工作面收缩到「写业务逻辑」。

### 10.4 增量重生成（改字段）

后台系统改字段远比新建模块频繁。流程：改 YAML 一行 → `make gen-module name=<key>` → 生成器检测表已存在 → 产出 `ALTER TABLE` 增量迁移 → 覆盖托管文件（查询/repo/handler/api/schema 自动带上新字段）→ 种子文件不覆盖，只打印 diff 提示。

没有生成器的话，加一个字段要人工改 8 个地方，漏一个就是 bug。

### 10.5 生成器自身的正确性

`make test-gen`（已实现，`scripts/test-gen.sh`）：生成一个**覆盖全字段类型**（含 ref 外键）的 fixture 模块 →
`make gen-module`（含自动登记）→ 跑生成流水线 → 验证 → 无论成败都回滚。**改产出器模板后跑它**，
确认「生成一个真模块」这条路没被悄悄改坏。

验证跑的是 `dev-check` + `lint-structure`/`lint-generated`/`sqlc-diff`（build/vet/全 lint/短测/selfcheck/
前端 tsc+eslint），**不跑完整 `make check` 里的 `go test ./...` 全量集成测试** —— 那些是 testcontainers
（连接偶发闪断、慢、且测的是已有流程不测生成的模块），验生成器正确性用不上。

### 10.6 加载期输入校验（宁可拒，不产坏码）

模块 YAML 在 `LoadModuleDef` 就一次性校验到底（`Validate` 不遇错即停，一把报全），把「配得进、产出却编译不过 / 产坏 SQL」的输入挡在生成之前 —— 生成器产物**从不真编译**（`test-gen` 也只到 tsc/vet，不到运行期），所以校验是唯一防线。除类型专属规则（enum 要 values、decimal 要 precision、string 要 max 等）外，还挡这几类**报错点离根因很远**的坑：

- **enum code 字符集**：code 会原样拼进迁移的 `CHECK (...)` 和前端标签映射，`enumCheck` 不转义 —— 带引号/特殊字符会产坏 SQL/坏 TS。强制和标识符同套（`^[a-z0-9_]+$`）。
- **字段名撞 sqlc rename**：`sqlc.yaml` 的 rename（`ip→IP`、`http_status→HTTPStatus`）会让 sqlc 产的 Go 字段名和生成器 `pascal()` 的不一致，service/handler 引用不上、4e 才炸。校验期直接拒；**加 sqlc rename 时 `sqlcRenamedNames` 要同步加**。
- **int/bool 的 default**：非整数 / 非 `true|false` 的 default 会静默产出坏迁移，用 `strconv` 判死。
- **可选 decimal/date 配 default、字段名撞 `page`/`page_size`/`keyword`、缺 `list` 动作**：都是「看着配了其实没生效」或会撞内建字段，一律拒。

变异测试在 `moduledef_test.go`（每种坏法一例）+ `gen_alter_test.go`（增量扫列不被 default 值里的 `);` 截断）。

---

## 11. 实施计划

| 步骤 | 内容 | 完成时你能做什么 |
|---|---|---|
| ① 地基 | compose 起 PG、goose、sqlc、huma+Echo 骨架、slog、统一响应封套 + 错误码注册表、requestID Transformer、幂等键中间件、优雅关闭、健康检查、Prometheus | `curl` 通一个接口 |
| ② 认证与权限 | 用户/角色/权限点/Service Account 表 + session 登录 + Casbin + data scope + 审计中间件 + 首个管理员引导 | 登录进去、看到审计记录 |
| ③ 前端骨架 | Vite + shadcn + 布局 + 登录页 + `client.ts` + 三个页面骨架 + 页面-权限映射 + 类型生成 + ESLint 规则 | 浏览器里登录进去 |
| ④ 系统管理四模块 | 用户管理 / 角色管理（含数据范围配置）/ 部门管理 / 审计日志查询 | 既是功能，也是**后续所有模块的样板** |
| ⑤ 脚手架化 | `make gen-module` 生成器 + AGENTS.md 开发规范 + 项目 skill + README | 说「我要管理商品」，AI 按规范十步走完 |
| ⑥ AI 助手 + MCP | tool 注册机制 + 权限联动 + 确认流 + 会话审计 + 对话 UI + MCP Server | 对着系统说话干活；openclaw / hermes 能接进来 |

第 ④ 步做完脚手架就「活」了；第 ⑤ 步把它固化成 AI 能自动遵循的规范。

**第 ④ 步产出的三个样板**，第 ⑤ 步的生成器照着它们抽模板：

| 模块 | 它示范的是什么 |
|---|---|
| 部门 | **树形**：自引用外键 + 递归 CTE 查子树 + 成环检查 + 前端拼树/缩进/展开收起 |
| 角色 | **配置型**：勾选树来自权限点注册表、内置对象保护、多步写包事务 |
| 用户 | **最标准的 CRUD**：抽屉详情、唯一标识冲突翻译、只出现一次的凭据、级联踢会话 |

以后再有树就抄部门，再有普通 CRUD 就抄用户。

**第 ④ 步没做的**（留给后面，不是忘了）：

- Service Account 的管理界面（表和认证链路第 ② 步就有了，缺的是 CRUD 页面）
- 审计哈希链没有生产入口 —— `audit.VerifyChain` 只在测试里调，得加个运维命令
- 数据范围仍然只有 `all` / `self`；部门只是组织结构，还不是范围维度（§3.3）

---

## 12. 环境备注

- 本机 Go 1.25.5 / Node 25.8.2 / Docker 29.4 / PostgreSQL 17.10（brew）
- **本地开发不用 docker**：`make dev` 直接跑后端（air 热重载），`make fe-dev` 跑 vite，
  数据库连本机 brew 装的 PG（5432，`make db-setup` 建角色和库）。
  docker compose 只用于部署；`make up` 起的那套 PG 映射在宿主机 5433，不跟本机 PG 抢端口
- 后端默认监听 `:8080`，vite 代理默认打到那里。**本机 8080 被别的服务占了**就两边一起挪：
  `FRIES_SERVER__ADDR=:8081 make dev` + `VITE_API_TARGET=http://localhost:8081 pnpm dev`
- **本地管理员固定 `admin` / `admin`**：`make dev-admin` 重置（重建库之后重跑一次）。
  它会顺手把库里的密码策略放宽到 1 位、不要求混合 —— 否则 `admin` 过不了自己的策略。
  命令内置主机白名单，数据库不是 localhost 直接拒绝跑；**这个检查不许去掉**。
  部署环境不受影响，仍是 bootstrap 随机密码 + 首次登录强制改密（§6）
- **`GOPROXY` 用公共代理**：`go env -w GOPROXY='https://proxy.golang.org,direct'`（国内可换
  `https://goproxy.cn,direct`）。若换成企业私服，注意 Go 拒绝在 HTTP proxy URL 里传凭据，
  凭据要写进 `~/.netrc`
