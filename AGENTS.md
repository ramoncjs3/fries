# AGENTS.md — fries 开发契约

> **每次动手前必读的最小契约。** 详细规范在 `docs/`，按下面的路由表按需读，不要一次全加载。
> Claude Code / Codex / 其它 agent 一律以此为准。

## 这是什么

fries 是通用后台管理系统脚手架：Go + Echo + huma + PostgreSQL + React + shadcn/ui。
**多租户**：一次部署，多家公司各自使用；单个组织 10 人规模，权限要求严格。

**设计优先级：规范一致 > 功能丰富。** 代码由 AI 逐次生成，一致的结构才能让每次生成可预测、可复查。

## 动手前必做三件事

1. 读 **`docs/MODULES.md`** —— 找结构最像的已有模块照抄。第一个模块定下的样子就是所有模块的模板。
2. 读 **`docs/MEMORY.md`** —— 这个项目踩过什么坑、定过什么临时约定。
3. **判断任务类型**，按路由表读对应 skill。不要凭记忆动手。

## 红线（违反即 review 不过）

**后端**

1. **不硬编码密钥** —— 从 config 读；`config/config.yaml` 不进版本库
2. **不拼接 SQL** —— sqlc / 参数化；动态排序必须走白名单。
   **也不许绕过租户隔离**：查数据只能从 `repo.Store.ForTenant` 拿句柄，每条 SQL 都要带
   `tenant_id`（`make lint-sql` 查）。确实做不到的写 `-- tenant-exempt: <理由>`，
   且**必须**在 `docs/MULTI-TENANCY.md` §3.2 ③ 那份清单上有据
3. **不跳过认证、授权、审计** —— 任何写接口都要过三关；注册路由时权限点是必填参数
4. **不手改数据库** —— 一律 goose 迁移
5. **不把内部错误返回前端** —— 5xx 只给通用文案 + request_id，堆栈只进日志
6. **handler 不写业务逻辑；service 不 import echo/huma** —— 分层不许穿透
7. **不用 `errors.New` / `fmt.Errorf` 直接返回给前端** —— 必须是注册过的 `*errs.Code`

**前端**

8. **不造轮子、不新引 UI 库** —— 只用 shadcn/ui + 页面骨架（`ListPage` / `DetailPage`；`FormDialog` 只给批量动作用，见 §7.6）
9. **不裸 `fetch`、不硬编码颜色和 px、不裸调 `toLocaleString`** —— 走 `client.ts`、Tailwind token、`<DateTime>`
10. **token / 敏感数据不进 localStorage** —— httpOnly cookie

**通用**

11. **不引入未审查的依赖** —— 新增/升级前过 `govulncheck` / `npm audit`；开源库至少 1k star 且在维护
12. **不改托管文件** —— 文件头写着 `DO NOT EDIT` 的归生成器所有，要改就改 YAML 重跑

## 任务路由

| 要做的事 | 读这个 |
|---|---|
| 新增业务模块（「我要能管理 XX」） | `.claude/skills/new-module/SKILL.md` |
| 给已有模块加 / 改字段 | `.claude/skills/new-field/SKILL.md` |
| **写非 CRUD 功能**<br>登录 / 导出 / Webhook / AI 工具 / 纯业务逻辑 / 跨模块统计 | **没有生成器兜底，全靠红线**：#3 权限点必须手动注册、#6 分层不许穿透、#7 错误必须是 `*errs.Code`。分层看 §1.1，接口契约看 §4，权限注册看 §3.7 |
| 提交前自检 | `.claude/skills/precheck/SKILL.md` |
| 跑不起来 / 报错排查 | `.claude/skills/troubleshoot/SKILL.md` |
| 目录结构 / module path | `docs/DECISIONS.md` §1.1 |
| 权限、角色、数据范围 | `docs/DECISIONS.md` §3 |
| API 响应格式、错误码 | `docs/DECISIONS.md` §4 + `docs/ERROR_CODES.md` |
| 配置该放 yaml 还是 DB | `docs/DECISIONS.md` §5 |
| 安全相关 | `docs/DECISIONS.md` §6 |
| **多租户 / 租户隔离 / 平台管理端** | `docs/MULTI-TENANCY.md` ⚠️ 碰数据表和登录之前必读 |
| 加了新表 / 新查询，怎么让它进租户隔离 | `docs/MULTI-TENANCY.md` §1.2；改完跑 `make lint-sql`（会告诉你漏了哪一处） |
| 前端页面 | `docs/DECISIONS.md` §7 |
| 对外集成 / Connector / Webhook | `docs/DECISIONS.md` §8 |
| 加 AI 工具 / MCP | `docs/DECISIONS.md` §8.4 |
| 生成器怎么用 | `docs/DECISIONS.md` §10 |
| 「为什么这么选」 | `docs/DECISIONS.md` |
| **都不匹配** | 读 `docs/DECISIONS.md` 目录挑相关章节；仍拿不准就**问用户**，不要自己发明约定 |

## 「完成」的定义

两个档位，**别用错**：

```bash
make dev-check    # 秒级，写代码过程中随时跑
make check        # 全量，「做完了」的唯一标准
```

| | `dev-check` | `check` |
|---|---|---|
| 后端 | `go build` · `go vet` · `golangci-lint` · **`squawk`（迁移）** · **`lint-sql`（每条查询带租户条件）** · `go test -short` · `server --selfcheck` | 上述 + `sqlc diff` + 完整 `go test`（testcontainers 起真 PG） |
| 前端 | `pnpm typecheck` · `pnpm lint` · `pnpm test` | 上述 + `pnpm build` |
| 生成物 | — | `make lint-generated` + `make sqlc-diff`（生成物是最新的，也没被手改） |
| 耗时 | 几秒 | 半分钟以上 |

**`make check` 红着的时候不许说做完了。** 自己修，实在修不动再来问。

`make check` 之外还要人工确认两件它查不了的事：

- [ ] `docs/MODULES.md` 登记了模块（表 / 权限点 / 页面 / 错误码）
- [ ] 踩到的坑、定下的约定追加进 `docs/MEMORY.md`

## 沟通方式

- **中文，少术语。** 做完用「你现在可以……」的用户视角讲结果，不要复述改了哪些文件。
- **让用户选择时给推荐 + 一句话理由**，不要甩一堆选项。
- **全程静默执行，做完给一个总结。** 中间不要汇报进度。
- **打断用户要克制** —— 只在「不问就可能白做」时才问，且一轮问完。各 skill 里标了 ⬅ 的步骤是必须的关卡（如 new-module 的表结构确认），其余自己拿主意。
