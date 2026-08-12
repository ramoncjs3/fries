---
name: new-module
description: 在 fries 脚手架上新增一个业务管理模块。当用户说「我要能管理 XX」「加一个 XX 管理」「做个 XX 列表页」时使用，即使没提到「模块」二字。
---

# 新增业务模块

## 铁律

**按顺序走完六步，一步不落。** 缺任何一步都不算做完。

**生成器负责样板，你负责业务逻辑。** `make gen-module` 一次产出 13 个文件：其中 2 个是**托管文件**
（`db/migrations/*_create_*.sql` 和 `db/queries/<key>.sql`，头写 `DO NOT EDIT`，改 YAML 重跑）；
其余 11 个是**种子文件**（头写 `Safe to edit`，重跑不覆盖），你按需改。日常你主要动 service/errors/perm
和三个页面，types/schema/api/queries 这几个前端数据层种子一般不用碰。

---

## 第 0 步：找可以照抄的

读 `docs/MODULES.md`，找结构最像的已有模块。打开它的 `modules/<key>.yaml` 和 `internal/service/<key>/service.go` 当参考。

**`modules/supplier.yaml` 是生成器的活样板**（`generated: true`，覆盖 string/enum/decimal/date/text + 全 CRUD），
照它的 YAML 写最省事；对应的生成产物（service/handler/前端页面）也可当「生成出来长什么样」的参考。

⚠️ **supplier 只覆盖 5 种类型**。要写 `int` / `bool` / `timestamp` / `ref` 时别照它猜 ——
`scripts/test-gen.sh` 里的 fixture YAML 覆盖**全 9 种**（含 `ref: supplier` 和 `bool` 的
`default: "true"` 这个易错的引号写法），那份是每次 `make test-gen` 都会跑通的，抄它准没错。

**新模块是「填内容」，不是「重设计」。**

## 第 1 步：起草定义，用大白话确认 ⬅ 唯一需要用户参与的关卡

写 `modules/<key>.yaml`（格式见下），然后**不要把 YAML 甩给用户**，翻译成人话念一遍：

> 供应商要存这些信息：
> - **供应商名称**（必填、不能重复、能搜索）
> - 联系人、联系电话
> - **状态**：合作中 / 已终止（可筛选）
> - 授信额度（金额）
> - 合作起始日（可筛选）
> - 备注
>
> 另外确认一下：供应商数据要不要按人隔离？
> （要的话，具体哪个角色能看全部、哪个只能看自己录入的，之后在「角色管理」里配，不写死在这里）
>
> 要改的地方直接说，没问题我就开始做了。

**猜不准的字段也先列出来让用户删，不要一个个问。** 一轮问完。

## 第 2 步：跑生成器 + 登记

```bash
make gen-module name=<key>
```

它写出 13 个文件，**并自动登记**进 app.go（装配）、tenantsql（租户表）、routes（前端路由）——
幂等，重跑不会插重复；登记不上会报出来让你手动加。然后打印剩下要**手动跑的命令**：

1. 有 `decimal` 字段：`cd backend && go get github.com/shopspring/decimal`
2. 把 SQL/类型/文档同步出来：`make gen-sqlc gen-tenant-queries gen-api schemadoc`
3. `docs/MODULES.md` 手动加一行业务说明（生成器不动它）

**icon 要用 frontend `MenuIcon.tsx` 里注册过的**（truck / users / settings / scroll-text …）——
用没注册的 `make check` 的 lint-structure 会报（菜单会显示成一个圆点）。要新图标先去那注册。

## 第 3 步：填业务逻辑

**只动种子文件**（头写 `Safe to edit`），托管文件（头写 `DO NOT EDIT`：迁移 + `queries.sql`）改了 CI 会红。常动的：

| 文件 | 干什么 |
|---|---|
| `backend/internal/service/<key>/service.go` | 业务规则、跨表校验、事务边界（冒烟测试没生成，自己补 `service_test.go`） |
| `backend/internal/service/<key>/errors.go` | 业务错误码（`errs.Define`，前缀必须是 `<key>.`；唯一字段的冲突码已生成） |
| `backend/internal/perm/modules/<key>.go` | 调 `AITool` / `AIDesc` 开关（生成器默认不暴露给 AI，要暴露自己开） |
| `frontend/src/features/<key>/ListPage.tsx` | 列宽、格式化、Badge 颜色（默认按类型渲染，够用就不用改） |
| `frontend/src/features/<key>/NewPage.tsx` | 新增表单的字段布局、联动（页面式，不是弹窗，§7.6） |
| `frontend/src/features/<key>/DetailPage.tsx` | 详情排版、看/改双态 |

**AI 工具开关怎么判断**：

| 动作类型 | 设置 |
|---|---|
| 只读（list / read / 统计） | `AITool: true` |
| 一般写操作（create / update / export） | `AITool: true, Confirm: true` |
| 危险操作（delete / 改权限 / 改安全设置） | `AITool: false` —— **根本不暴露给 AI** |

## 第 4 步：验收

```bash
make check
```

红了自己修，不要来问。常见报错：

- **`--selfcheck` 失败** → 路由和权限点没对齐：要么接口没声明权限点，要么声明的权限点不在模块注册表里，要么模块勾了权限点却没有对应接口
- **scope 测试失败**（`不带 scope 调 List 必须报错`）→ repo 层漏了 `authz.MustScope`。selfcheck 查不出这个，只有测试能抓
- **`sqlc diff` 非空** → 改了 `.sql` 忘了 `sqlc generate`
- **`git diff` 非空** → 手改了托管文件，改回去，改 YAML 重跑

## 第 5 步：更新文档

- `docs/MODULES.md` —— **手动加一行**（生成器不动它），一句话说清这个模块管什么
- 新增了表 → `make schemadoc` 重生成 `docs/SCHEMA.md`
- 踩到坑或定了新约定 → 追加 `docs/MEMORY.md`

## 第 6 步：一句话交代

用户视角，不要列文件清单：

> 供应商管理做好了。左侧菜单能看到「供应商」，可以新增、编辑、导出，支持按名称搜索和按状态筛选。
> 现在只有管理员看得到，去「角色管理」里给需要的角色勾上权限就行。

---

## 模块定义格式

```yaml
key: supplier                         # 小写单数，同时是权限点前缀和错误码前缀
name: 供应商                           # 中文名，菜单和权限配置页显示
generated: true                       # 必须，生成器只处理 generated: true 的模块（手写模块填 false 会被跳过）
scoped: true                          # 是否参与数据权限（见下方判断）
menu: { path: /suppliers, icon: truck }   # icon 用 lucide-react 的名字

fields:
  - { name: name,       type: string,  label: 供应商名称, required: true, unique: true, searchable: true, max: 100 }
  - { name: contact,    type: string,  label: 联系人, max: 50 }
  - { name: status,     type: enum,    label: 状态, filterable: true, default: active,
      values: { active: 合作中, terminated: 已终止 } }
  - { name: credit,     type: decimal, label: 授信额度, precision: [18, 2] }
  - { name: started_at, type: date,    label: 合作起始日, filterable: true }
  - { name: remark,     type: text,    label: 备注 }

sortable: [created_at, name, started_at]      # 排序白名单，不在里面的排序参数直接拒绝
actions:  [list, read, create, update, delete, export]    # 省略则为默认全套
```

**字段属性**

| 属性 | 效果 |
|---|---|
| `required` | `NOT NULL` + zod 必填 + 表单星号 |
| `unique` | partial unique index（`WHERE deleted_at IS NULL`）+ 唯一冲突错误码 |
| `searchable` | 进关键字搜索 + pg_trgm GIN 索引（**只对 `string`/`text`**） |
| `filterable` | 列表页筛选控件 + 索引 + `sqlc.narg()` 参数（**只支持 `string`/`text`/`enum`/`date`/`timestamp`** —— 数字/金额/bool 的精确等值筛选没意义，被校验拒） |
| `max` | `varchar(n)` + zod `.max(n)`（`string` 必填这项） |
| `default` | PG DEFAULT + 表单默认值（**`decimal`/`date`/`timestamp` 暂不支持 default，会被校验拒** —— 要默认值设成 required 或在 seed service 里补） |

**可用类型**：`string` `text` `int` `decimal` `bool` `enum` `date` `timestamp` `ref`（这 9 种已支持）。
- `ref` 是**外键**：写 `type: ref, ref: <目标模块 key>`，字段名照惯例带 `_id`（如 `supplier_id`）。产出复合外键
  `(tenant_id, <字段>) → <目标复数表>(tenant_id, id)`（防跨租户引用）+ uuid 类型。**前端全自动、不用手改**：
  编辑态是远程搜索选择器（搜目标名字点一下存 uuid），读态（列表 + 详情）显示目标名字（查询 JOIN 出来的，无 N+1）。
  目标模块得先存在（它的 `(tenant_id, id)` 锚点才有），且要有可显示的文本字段（建议 `name` 且 `searchable`），否则生成期报错。
- `file` / `json` **还没做**（会被校验拒，别用）。`bigint` / `uuid` 同样不认。完整映射表见 `docs/DECISIONS.md` §10.2。

**标准列不用写** —— `id` / `created_at` / `updated_at` / `deleted_at` / `created_by` / `version` 生成器自动加。

---

## 常见判断

| 问题 | 判断 |
|---|---|
| **要不要 `scoped: true`？** | 有「归属」概念的（订单、客户、工单、我录入的供应商）→ `true`；全局共享的（字典、分类、系统配置）→ `false` |
| **图表 / 报表模块怎么办？** | **不需要自己的权限和 scope**。聚合查询走底层资源的同一个 scope 注入点，用户看到的自然是自己那份数据 |
| **金额字段** | 一律 `decimal` + `precision`。**绝不用 `float`**，JSON number 是双精度浮点会丢精度 |
| **状态类字段** | 一律 `enum`，不要 `string` + 魔法值 |
| **关联其它表** | 用 `ref`：`type: ref, ref: <目标 key>`，字段名带 `_id`。产复合外键（防跨租户引用）+ uuid。前端全自动：编辑态远程搜索选择器、读态显示目标名字（JOIN 出来的）。目标要有可显示文本字段（建议 name+searchable） |
| **一个页面里要按权限显示部分区块** | 把资源拆细成多个 key（如 `settings.security` / `settings.storage`），见 `docs/DECISIONS.md` §3.4 |

## 反模式（别做）

- ❌ 手写迁移 SQL —— 改 YAML 重跑
- ❌ 在 `handler` 里写业务逻辑 —— 放 `service`
- ❌ 在 `ListPage.tsx` 里裸 `fetch` —— 走 `queries.ts` 的 hooks
- ❌ 自己搭页面结构 —— 用 `ListPage` / `DetailPage` / `DetailItem` 骨架（生成的模块新增/编辑都是**页面**不是弹窗，§7.6）
- ❌ 忘了 `invalidateQueries` —— mutation 成功后必须失效相关查询（生成器已带，别删）
- ❌ 给 `delete` 开 `AITool: true`
