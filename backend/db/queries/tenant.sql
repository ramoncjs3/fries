-- 租户本身（MULTI-TENANCY.md §2.1）。
--
-- ⚠️ `tenants` 是**平台级**的表，它自己没有 tenant_id ——
-- 和 sessions / service_accounts 一样属于「只能靠代码自觉」的那三张（§3.2 ③）。
-- 原因是认证发生在租户上下文建立**之前**：登录时先拿公司代码来这张表找租户，
-- 那一刻还不知道租户是谁。**清单就这三张，加第四张要单独讨论。**
--
-- 所以这里每一条都要写 `-- tenant-exempt:`，让 SQL 静态检查放行并留下理由。
--
-- 平台管理端自己的租户查询在 platform.sql 里。这里放的是**认证链路和启动加载**
-- 要用的那几条 —— 它们不属于平台端，但同样只碰 tenants 这一张平台级表。

-- name: GetTenantByCode :one
-- tenant-exempt: tenants 是平台级表，登录按公司代码找租户时还没有租户上下文
-- code 存的一律是小写（DB 有 CHECK 保证），查询侧 lower() 是为了用上 uk_tenants_code
-- 这个函数索引 —— 用户在登录框里敲 ACME 也要认得出来。
SELECT * FROM tenants WHERE lower(code) = lower(sqlc.arg('code')::text);

-- name: GetTenantByID :one
-- tenant-exempt: 同上
SELECT * FROM tenants WHERE id = sqlc.arg('id');

-- name: ListTenants :many
-- tenant-exempt: 同上。加载权限策略、加载各租户配置都要遍历租户
--
-- ⚠️ **不要按 status 过滤。** 这里曾经写的是 `WHERE status = 'active'`，
-- 结果是：停用一个租户 → 下次刷新策略时它的权限从 enforcer 里消失 →
-- 重新启用之后**没有任何东西会触发重载**（authz_changed 只在角色相关的表上有触发器），
-- 那家公司的人能登录但**每个请求都 403**，只能重启服务或者随便改个角色才恢复。
--
-- 停用的租户根本认证不过去（认证时会核 tenants.status），
-- 所以把它们的策略留在内存里没有任何风险，反而省掉一整类「状态没跟上」的问题。
SELECT * FROM tenants ORDER BY code;

-- name: CreateTenant :one
-- tenant-exempt: 同上
INSERT INTO tenants (id, code, name, status, created_by)
VALUES (sqlc.arg('id'), sqlc.arg('code'), sqlc.arg('name'), sqlc.arg('status'), sqlc.narg('created_by'))
RETURNING *;
