---
name: troubleshoot
description: 跑不起来 / 报错 / 行为不对时的排查路径。当用户说「跑不起来」「报错了」「登录后一直跳登录页」「菜单空的」「测试单独跑绿一起跑挂」「make check 挂了」时使用。按症状检索根因，别从零瞎试。
---

# 排查

## 铁律

**先按症状查这张表，再动手。** 这个项目踩过的坑都记在 `docs/MEMORY.md`，下面是按症状整理的检索版。
表里没有的，去 `docs/MEMORY.md` 全文搜关键词，再没有才从头查。

**先复现、再改。** 改一处、验一次，别一次动一堆。

---

## 按症状检索

### 起不来 / 编译不过 / 依赖问题

| 症状 | 大概率根因 | 怎么办 |
|---|---|---|
| `go build` 拉不到依赖 / 代理报错 | 全局 GOPROXY 指向连不上的私服 | 换公共代理 `go env -w GOPROXY='https://proxy.golang.org,direct'`；用企业私服则把凭据写 `~/.netrc`，**不能**写进 proxy URL（MEMORY） |
| 集成测试起容器 `TLS handshake timeout`（registry-1.docker.io） | docker registry 瞬时网络问题，**不是代码** | 镜像有本地缓存，直接重跑；别改代码 |
| `make check` 挂在 `gofmt` / File is not properly formatted | 结构体字段带**中文注释**，宽度把对齐算歪了 | `gofmt -w <文件>`，别手动对空格 |
| 迁移被 `squawk` 拦 | 加 `NOT NULL` 没给 `DEFAULT`、加唯一索引没 `CONCURRENTLY` 之类的锁表/危险操作 | 按提示改；`NOT NULL` 分两步（先可空回填再 `SET NOT NULL`） |
| huma 或 Echo 升级后编译炸一片 | huma 和 Echo **版本绑死** | 两个一起升，别单独动（MEMORY、DECISIONS §1） |
| 测试里 jsonb 参数报 `invalid input syntax for type json` | pgx 简单协议把 jsonb 当 bytea 发 | 简单协议只在跑迁移时用，查询走默认协议（MEMORY） |

### 租户隔离 / lint-sql

| 症状 | 根因 | 怎么办 |
|---|---|---|
| `make lint-sql` 报「没登记进 tenantsql」 | 新表有 `tenant_id` 列但没加进 `tenantTables` | 加进 `internal/tenantsql` 的 `tenantTables`；纯触发器表加进 `schemaExemptTenantTables` 并写理由 |
| `make lint-sql` 报「唯一索引不是 tenant_id 打头」/「内联 UNIQUE」 | 租户表唯一性没 `tenant_id` 打头，或用了内联 `UNIQUE` 约束 | 改成 `CREATE UNIQUE INDEX ... (tenant_id, ...)`；认证类全局唯一的加进 `schemaGlobalUniqueIndexes` |
| `make lint-sql` 报「逗号 JOIN」 | 用了 `FROM a, b` | 改成显式 `JOIN ... ON ...`（检查器对逗号后的表是盲的） |
| 运行期 panic：查回来的行 tenant_id 对不上 | `TenantAssertions` 开着，某条查询真漏了租户条件 | 别关断言，去修那条查询 —— 它抓到的是真漏洞（staging 开、生产关） |

### 登录 / 权限 / 菜单

| 症状 | 根因 | 怎么办 |
|---|---|---|
| 登录后反复跳登录页 | 缓存里的旧 401 / 平台和租户两套 cookie 认错了身份 | 查 `client.ts` 的 CSRF 按目标端点选 cookie；`TenantSessionLayout` 的 `/me` 作用域 |
| curl 写接口 403 / CSRF 校验失败 | 没带 `X-CSRF-Token` 头，或拿租户 token 去打平台接口 | 头值在登录响应的 `csrf_token`；两套 cookie 别混（MEMORY） |
| 菜单一项都没有 | 权限模块 `Realm` 空了 / Casbin 匹配器丢了通配 | 检查 `perm.Register` 的 `Realm` 必填；Casbin model 的匹配器 |
| 菜单项显示成一个圆点 | 后端菜单 `Icon` 名没在前端 `MenuIcon.tsx` 注册 | 注册它；`make lint-structure` 现在会当场拦这个 |
| 改了机器账号的角色但权限没变 | 降权靠 `authz_changed` 触发器，`service_accounts` 上是后补的（迁移 00012） | 确认触发器挂着且按列限定（role_id/status/deleted_at） |

### 测试

| 症状 | 根因 | 怎么办 |
|---|---|---|
| 测试单独跑绿、一起跑挂（撞唯一约束） | `testdb` 清理清单漏了新表，上个用例的数据留到下个用例 | 把新表加进 `testdb.go` 的 `truncatedTables` |
| 测试单独跑绿、一起跑挂（「不能小于 20」这类） | 有种子数据的表被改了没还原（`platform_settings` 是快照+还原，不能 TRUNCATE） | 照 `platformSeed` 的快照/还原模式 |
| 跨租户测试「怎么改都绿」 | 只给一个租户造了数据，隔离根本没测到 | 两个租户都造数据（`TwoTenants`）；写操作断言「影响行数 0」 |
| 「DB 层收权」的测试测不出漏洞 | 用了库 owner 连（superuser 绕过权限检查） | 用受限角色 `testdb.StartWithAppRole` |

---

## 表里没有？

1. 全文搜 `docs/MEMORY.md`：症状关键词、报错原文片段。
2. 看 `make check` / `make dev-check` 具体是哪一步、哪个检查器报的（输出里有中文说明和怎么办）。
3. 分层定位：编译期（build/vet/lint）→ 契约（sqlc-diff/gen-api）→ 运行期（selfcheck/集成测试）。
4. **查清楚了、且不显然，就把「症状 → 根因 → 怎么办」追加进 `docs/MEMORY.md`**，下次就进这张表了。
