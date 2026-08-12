---
name: new-field
description: 给已有业务模块加一个字段、改一个字段、或删一个字段。当用户说「XX 再加个字段」「供应商要能存邮箱」「把状态字段改成可筛选」「订单加个金额」时使用，即使没提到「字段」二字。
---

# 给模块加 / 改字段

## 铁律

**一条字段动一处，全栈要动九步。** 后端改了前端不跟、迁移改了没重生成，都会表现成「看着做完了、实际半截」，而且大多不报错。按顺序走完，一步不落。

**改数据库只走 goose 迁移，绝不手改库、不改已提交的迁移**（红线 #4）。加字段 = 写一个新迁移。

**托管文件不手改**：`internal/repo/internal/sqlcgen/`、`frontend/src/api/schema.d.ts`、`docs/SCHEMA.md`、`docs/ERROR_CODES.md` 都是生成物，改 YAML/SQL/迁移后重跑生成器，别手动编辑。

## ⚡ 生成模块（`generated: true`）走捷径

如果模块是生成器产的（`modules/<key>.yaml` 里 `generated: true`，如 supplier），**加字段大半自动**：
在 YAML 的 `fields:` 里加一行 → `make gen-module name=<key>`。生成器检测表已建，**自动产 ALTER 增量迁移**
（新字段可选就可空、必填就要求给 default，否则拒——防给有数据的表加 NOT NULL 列崩）+ **重写 queries**（托管）。
你只需**手工把新字段补进种子文件**：service 的 Input/View、handler 的 Body/toInput、前端 types/schema/页面
（照同模块已有字段抄）。然后 `make gen-sqlc gen-tenant-queries gen-api schemadoc` + `make check`。

下面的手动九步是给**手写模块**（`generated: false`，如 department 树形）用的，或你想完全手工时的完整清单。

---

## 第 0 步：想清楚这个字段的四个属性 ⬅ 唯一要跟用户确认的

一个字段落地前必须定死：

1. **类型 + 可空**：文本 / 数字 / 金额 / 布尔 / 时间 / 枚举；必填还是可空。
2. **唯一吗**：要唯一的话 ——⚠️ **唯一索引必须 `tenant_id` 打头**（`(tenant_id, 字段)`），
   否则跨租户串味（A 租户建不了 B 租户已有的值）。漏了 `make lint-sql` 现在会当场拦
   （`checkTenantTableSchema`）。且软删除表要用**部分索引** `WHERE deleted_at IS NULL`。
3. **能筛选 / 能搜索吗**：决定 handler 的 query 参数和前端筛选控件要不要加。
4. **谁能改**：一般跟模块现有权限点走；如果「改这个字段」比「改别的」敏感（比如改额度 vs 改备注），
   考虑单独一个权限点（参考 `user` 模块把 `reset_password` 单拎出来）。

猜不准的用大白话跟用户念一遍确认，一轮问完，别一个个问。

---

## 第 1 步：迁移（加列）

在 `backend/db/migrations/` 新建 `000NN_<模块>_add_<字段>.sql`，照 goose 上下段写。

- 加列：`ALTER TABLE <表> ADD COLUMN <字段> <类型>`。**给已有行留活路**：加 `NOT NULL`
  必须带 `DEFAULT`，或者分两步（先可空回填、再 `SET NOT NULL`），否则 `squawk` 拦你。
- 要唯一：`CREATE UNIQUE INDEX ... ON <表> (tenant_id, <字段>) WHERE deleted_at IS NULL`。
- Down 段写对应的 `DROP COLUMN` / `DROP INDEX`。

**漏了会怎样**：直接 `ALTER` 生产库 = 红线 #4 违规；`NOT NULL` 不带默认值 = 迁移在有数据的库上崩。

## 第 2 步：queries（读写这个字段）

改 `backend/db/queries/<模块>.sql` 里相关的 `:one` / `:many` / `:exec`：
SELECT 列表加上新字段、INSERT/UPDATE 带上它。

⚠️ **每条查询都要带 `tenant_id`**，「按 id 查一行」也不例外（BOLA，MULTI-TENANCY.md §11.1）。
改完这步别急着往下，`make lint-sql` 会查（也会顺带查你上一步的索引列顺序）。

**漏了会怎样**：SELECT 漏列 = 字段读不出来，service 里那个结构体字段永远是零值。

## 第 3 步：重生成后端类型

```bash
make gen-sqlc            # 由 SQL 生成类型安全的 Go（sqlcgen/，别手改）
make gen-tenant-queries  # 由 sqlc 产物生成 ForTenant 句柄
```

**漏了会怎样**：service 里用不到新字段（sqlc 结构体还是旧的），或者编译不过。

## 第 4 步：service

`internal/service/<模块>/`：Create/Update 的入参结构体加字段、映射到 repo 参数；
需要校验就在这里加（长度、范围、枚举白名单）。**service 不 import echo/huma**（红线 #6）。
校验失败返回**注册过的 `*errs.Code`**，不用 `fmt.Errorf`（红线 #7）——
新错误码在 `errs.Define` 声明，然后 `make errdoc` + `make errcodes-ts` 重生成。

## 第 5 步：handler

`internal/handler/<模块>.go`：请求/响应 struct 加字段（huma 的 tag 决定校验和 OpenAPI）。
若第 0 步定了「能筛选」，给 List 的 query 参数加上，并透传给 service。
**handler 不写业务逻辑**：只做绑定 → 调 service → 封套返回（红线 #6）。

## 第 6 步：重生成前端类型

```bash
make gen-api   # 由后端 OpenAPI 生成 frontend/src/api/schema.d.ts（真相唯一在 Go 侧）
```

实体/响应类型都从 `schema.d.ts` 取，**不手写**。请求体的 `XxxInput` 若是手写的，同步改一下。

## 第 7 步：前端页面

`frontend/src/features/<模块>/`：

- 详情/新增/编辑表单加这个字段的控件（走 shadcn + 页面骨架，别造轮子，红线 #8）。
- zod `schema.ts` 加对应校验（和后端校验对齐）。
- 列表要展示就加列；第 0 步定了能筛选就加筛选控件（走 `list-params`）。
- 时间字段用 `<DateTime>`，别裸调 `toLocaleString`；颜色/间距走 Tailwind token（红线 #9）。

## 第 8 步：文档 + 测试

```bash
make schemadoc   # 由迁移重生成 docs/SCHEMA.md（别手改）
```

测试（`make check` 之外机器保证不了的，见 `precheck` skill）：
- 改了唯一约束/查询的，补/改**跨租户测试**，且**两个租户都要有数据**（只给一个租户造数据、
  断言另一个查不到，是假绿 —— 把租户条件删了照样过）。
- 写操作测「影响行数是 0」（跨租户写应该动 0 行、表现成 NotFound）。
- 新加的守门测试做**变异验证**：改回坏版本，确认测试会红。不会红的测试等于没写。

---

## 收尾自检

跑 **`make check`**（唯一的「做完了」标准），然后走一遍 `precheck` skill 里那几条机器查不了的。
`make check` 红着不算做完 —— 自己修，实在修不动再问。

**登记**：`docs/MODULES.md` 里该模块那节补一句字段说明；踩到的坑追加进 `docs/MEMORY.md`。
