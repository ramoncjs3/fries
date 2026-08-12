# 已实现模块清单

> **动手前先读这里**，找结构最像的模块照抄。第一个模块定下的样子就是所有模块的模板。
>
> 每个模块由 `make gen-module` 在**首次生成时**追加一节；**已存在的条目生成器不会覆盖**，
> 业务说明请人工/AI 补在「说明」栏里，不会丢。

| 模块 key | 中文名 | 表 | scoped | 权限点 | 页面 | 错误码前缀 |
|---|---|---|---|---|---|---|
| _(还没有业务模块)_ | | | | | | |

## 系统内置模块

第 ④ 步实现，它们同时是后续所有模块的样板：

| 模块 key | 中文名 | 表 | 权限点 | 页面 | 它是什么的样板 |
|---|---|---|---|---|---|
| `user` | 用户管理 | `users` `user_roles` | list / create / update / delete / reset_password | `/users` `/users/new` `/users/:id` | **最标准的 CRUD**：详情页内编辑、唯一冲突翻译、一次性凭据 |
| `role` | 角色管理 | `roles` `role_permissions` | list / create / update / delete | `/roles` `/roles/new` `/roles/:id` | **配置型**：勾选树来自权限注册表、内置对象保护、多步写包事务 |
| `department` | 部门管理 | `departments` | list / create / update / delete | `/departments` | **树形**：递归 CTE、成环检查、前端拼树 |
| `audit` | 审计日志 | `audit_logs`（按月分区） | list | `/audit` | **只读 + 行内展开** |
| `service_account` | 机器账号 | `service_accounts` | list / create / update / delete / rotate_key | `/service-accounts` `/service-accounts/new` `/service-accounts/:id` | **凭据型**：一次性密钥、密钥轮换、停用/删除/过期立刻断开 |
| `settings.security` | 安全设置 | `settings` | list / update | `/settings/security` | **配置型（注册表驱动）**：表单完全由服务端返回的配置项注册表渲染，前端不硬编码 key 和取值范围 |

## 平台级的表（不属于任何业务模块）

这些表**没有 `tenant_id`**，也不归任何业务模块管。它们归平台管理端（`/platform`），
那是另一套身份、另一套会话、另一棵前端子树（MULTI-TENANCY.md §6、§10.1）。

| 表 | 说明 |
|---|---|
| `tenants` | 租户（对用户叫「组织」）。只停用不删除（MULTI-TENANCY.md §9.3）；`user_count` 是冗余列，由 `users` 上的触发器维护 —— **平台端不查客户的业务表**（§6） |
| `platform_settings` | 平台级配置：`audit.retention_days`、`ui.system_name`，以及**租户设置的上下界**（§10.5）。平台端页面在 `/platform/settings`，权限模块 `platform_setting` |
| `platform_admins` | 平台管理员。和 `users` 是两张表、两套登录，密码策略更严（最少 12 位，写死在代码里不受租户配置影响） |
| `platform_sessions` | 平台会话。cookie 名和租户端不同 —— **同一个浏览器可以同时登着两边**，这是有意支持的 |

⚠️ **`sessions` / `service_accounts` / `tenants` 这三张表只能靠代码自觉**（§3.2 ③）：
它们的查询没法机械校验租户条件（前两张要先按 token/prefix 定位才知道租户是谁）。
清单**就这三张**，想加第四张要单独讨论。

### 加新表时的租户规则

新表一律带 `tenant_id NOT NULL`，唯一索引 **`tenant_id` 打头**（§8.4），
外键写成复合的 `(tenant_id, x)`（MULTI-TENANCY.md §2.2.1）。查询每条都要带 `sqlc.arg('tenant_id')` ——
**「按 id 查一行」也不例外**，那是 BOLA，OWASP API Top 10 第一名（MULTI-TENANCY.md §11.1）。

这几条现在都被 `make lint-sql` 机械拦下，**漏写就红灯**：
- 查询漏带租户条件 → checkGoRawSQL / tenantsql.Analyze
- **新表有 `tenant_id` 列却没登记进 `tenantsql.tenantTables`** → checkTenantTableSchema
- **租户表上的唯一索引没以 `tenant_id` 打头** → checkTenantTableSchema（这条以前只在文档里承诺、并没真查，现在补上了）

真有正当理由破例的：查询上写 `-- tenant-exempt: <理由>`；表或索引的例外加进 `cmd/gen/lintsql.go`
的 `schemaExemptTenantTables` / `schemaGlobalUniqueIndexes` 并写明理由。那行理由会被人读到。

**Service Account 的管理页面已经有了**（`service_account` 模块）。
⚠️ 那张表的 `authz_changed` 触发器是后补的（迁移 00012）——
00003 只给 roles / role_permissions / user_roles 挂了，漏了它，
后果是**给机器账号换角色之后降权不生效**。触发器按列限定（只认 role_id / status /
deleted_at），因为 `last_used_at` 每个请求都写，挂全表会变成策略重载风暴。

## 说明

### audit —— 审计日志

第 ② 步随认证与权限一起落地，因为它同时是「权限链路通没通」的验收手段。

- **权限点**：`audit:list`
- **表**：`audit_logs`（按月分区）、`audit_chain_head`
- **接口**：`GET /api/v1/audit-logs`，支持按操作人、资源、动作、时间范围筛选 + 分页
- **页面**：`/audit`（`frontend/src/features/audit/`）—— 用 `<ListPage>` 骨架，时间走 `<DateTime>`
- **数据权限**：不参与（`Scoped: false`）—— 审计要给管理员看全。
  ⚠️ 多租户下**「看全」只能是「看全本租户」**，别照着这句写出不带租户条件的查询
- **只增不改不删**：哈希链由 DB 触发器维护，应用账号在 DB 层被撤销 UPDATE/DELETE

写入是中间件自动做的，业务代码不用管；只有「新增」类操作要在 handler 里调
`audit.SetResourceID(ctx, id)` 补上是哪条记录 —— 那个 ID 在响应里，中间件看不到。

### department —— 部门管理

树形模块的样板。以后再有分类、菜单这类树，照着它抄。

- **权限点**：`department:{list,create,update,delete}`
- **表**：`departments`（自引用外键 `parent_id`）
- **不分页**：树切成一页页就拼不起来了，`GET /departments` 一次返回全部节点，前端拼树
- **成环检查**：改上级时用递归 CTE 取自己的整棵子树，新父节点在里面就拒绝
  （`department.cycle`）。不拦的话那一支会从根断开，页面上直接消失，而数据库层面完全合法
- **详情和新增是右侧面板里就地做的，不是独立路由**（`DetailPage.tsx` / `NewPage.tsx`
  导出的是组件而不是页面）—— 左边那棵树是它的上下文，跳走整页反而把树丢了。
  面板处于什么状态存 URL（`?pane=edit` / `?pane=new&under=<id>`）
- **删除前置条件**：下面还有子部门（`has_children`）或成员（`has_users`）就不给删
- **同级不许重名**：根节点的 `parent_id` 是 NULL，而 NULL 在唯一索引里互不相等，
  所以根节点那条唯一索引得**单独建一个**

### role —— 角色管理

- **权限点**：`role:{list,create,update,delete}`
- **详情、新增、编辑全是页面**（`/roles/:id`、`?edit=1`、`/roles/new`），和用户模块同一套骨架
- **勾选树来自后端**：`GET /roles/permission-catalog` 直接吐 perm 注册表，
  前端不许自己维护一份 —— 那样每加一个模块都要记得回来补，必然会漏
- **勾的权限点会校验**：不在注册表里就拒（`unknown_permission`）。不校验的话库里会留下
  一条永远匹配不上的死策略，页面显示「勾了」实际没用，排查极其难受
- **`key` 建后不可改**：它是 Casbin 策略里的身份，改了等于换了个角色，
  而已经签发的会话还带着老 key
- **改权限包事务**：先 `DELETE` 再逐条 `INSERT`，中间失败会让角色变成零权限

### user —— 用户管理

- **权限点**：`user:{list,create,update,delete,reset_password}`。
  重置密码单独一个点 —— 能改显示名和能换任何人的密码是两个量级的事
- **详情、新增、编辑全是页面，没有弹窗** —— `/users/:id` 看，`?edit=1` 改，
  `/users/new` 建（`DetailPage.tsx` / `NewPage.tsx`）。这是所有模块的样板，
  返回列表不丢筛选、两态不跳版、离开拦截的机制都在 DECISIONS.md §7.6
- **初始密码后端随机生成、只返回一次**，且强制首次登录改密。不让管理员自己设
  （大概率是 123456），也不写进任何日志
- **停用 / 删除 / 重置密码都会立刻吊销该用户的全部会话**，否则他能一直用到 cookie 过期
- **兜底护栏**见 DECISIONS.md §3.5.1

_(其它模块的业务说明按同样格式往下加，一节一个模块)_

## 还没做的

按当前排定的顺序。**动手前先读 [AGENTS.md](../AGENTS.md) 和这份清单里对应那条的注意事项。**

| 做什么 | 状态 | 注意 |
|---|---|---|
| **⑥ AI 助手 + MCP** | 未做（用户定：排到最后） | tool 注册机制 + 权限联动 + 确认流 + 会话审计 + 对话 UI + MCP Server。权限点上的 `AITool` 标志位已占位，真正的接线没做 |
| **文件存储子系统** | 未做 | `file` 字段类型的前置：对象存储（S3/本地）+ 上传接口 + presigned URL。要传文件的业务才需要，是独立功能不是「生成器欠账」 |
| 生成器 `json` 类型 | 未做（niche） | jsonb + json.RawMessage，有 []byte/base64 陷阱 + 前端 textarea 存 JSON 字符串 vs jsonb 存 JSON 值的类型错配。场景窄（结构化数据本该建正经字段/表），当初标「暂不做」。`ref` 已**全链路做完**（复合外键 + 编辑态远程搜索选择器 + 读态 JOIN 出目标名字，列表/详情统一） |
| 审计哈希链运维入口 | 未做 | `audit.VerifyChain` 至今只在集成测试里调，缺一个运维命令 |
| 数据范围维度 | 只有 all/self | 部门还不是范围维度（§3.3）；用户定：先这样 |

**已实现**（曾在本清单里、现已完成）：模块生成器 `make gen-module`（一键写盘 + 自动登记 + 9 种字段类型含外键 + `make test-gen` 自测 + 增量改字段 ALTER）、发信（Mailer 接口 + LogMailer + SES + **异步化** + **按收件人限流**）、忘记密码、自助注册、幂等/限流的 PG 实现。
| ~~`precheck` / `troubleshoot` / `new-field` 三个 skill~~ | ✅ 已写 | 见 `.claude/skills/*/SKILL.md`。三个都不依赖生成器，是手工链路 / 检索表 / 自检单 |
| ~~限流器 / 幂等键换 PG 实现~~ | ✅ 已实现 | 两者都做了 PG 版（`rate_limits` / `idempotency_keys` 表），`server.shared_state_store: postgres` 一键切（默认 memory）。⚠️ 限流器 PG 版是固定窗口、每请求打库有代价，高并发建议 Redis；幂等键仍只记键不记响应（[SCALING.md](SCALING.md) §1） |
| 每租户配额 | 明确推后 | MULTI-TENANCY.md §12.3 |

**明确不做的**见 MULTI-TENANCY.md §16 那张表，以及 [SCALING.md](SCALING.md) §2（为什么不拆微服务）、§6（现在不动的三件事）。
