-- 角色管理（DECISIONS.md §3）。
--
-- Casbin 用的那几个策略查询在 auth.sql 里，别混在一起：
-- 那些是**认证链路**用的（加载进内存），这里是**管理页面**用的。
--
-- 角色的 key 现在是**租户内**唯一：每家公司各有一个自己的内置 admin（§3.2 ①）。
-- 子查询里的 count 也要带租户 —— 少一处就会把别家公司的人数算进来。

-- name: ListRoles :many
SELECT r.*,
       (SELECT count(*) FROM user_roles ur
        WHERE ur.tenant_id = r.tenant_id AND ur.role_id = r.id)       AS user_count,
       (SELECT count(*) FROM role_permissions rp
        WHERE rp.tenant_id = r.tenant_id AND rp.role_id = r.id)       AS permission_count
FROM roles r
WHERE r.tenant_id = sqlc.arg('tenant_id')
  AND r.deleted_at IS NULL
  AND (sqlc.narg('keyword')::varchar IS NULL
       OR r.name ILIKE sqlc.narg('keyword') OR r.key ILIKE sqlc.narg('keyword'))
  AND (sqlc.narg('status')::varchar IS NULL OR r.status = sqlc.narg('status'))
ORDER BY r.builtin DESC, r.key
LIMIT sqlc.arg('limit') OFFSET sqlc.arg('offset');

-- name: CountRoles :one
SELECT count(*) FROM roles r
WHERE r.tenant_id = sqlc.arg('tenant_id')
  AND r.deleted_at IS NULL
  AND (sqlc.narg('keyword')::varchar IS NULL
       OR r.name ILIKE sqlc.narg('keyword') OR r.key ILIKE sqlc.narg('keyword'))
  AND (sqlc.narg('status')::varchar IS NULL OR r.status = sqlc.narg('status'));

-- name: GetRole :one
-- 计数和 ListRoles 保持一致 —— 详情页要显示「有多少人在用这个角色」，
-- 少一个 user_count 就会显示成「还没有人用」，而列表上明明写着 2 个人。
-- 同一个字段两个接口说法不一样，比不显示还糟。
SELECT r.*,
       (SELECT count(*) FROM user_roles ur
        WHERE ur.tenant_id = r.tenant_id AND ur.role_id = r.id)       AS user_count,
       (SELECT count(*) FROM role_permissions rp
        WHERE rp.tenant_id = r.tenant_id AND rp.role_id = r.id)       AS permission_count
FROM roles r
WHERE r.tenant_id = sqlc.arg('tenant_id') AND r.id = sqlc.arg('id') AND r.deleted_at IS NULL;

-- name: ListRolePermissions :many
SELECT resource, action FROM role_permissions
WHERE tenant_id = sqlc.arg('tenant_id') AND role_id = sqlc.arg('role_id')
ORDER BY resource, action;

-- name: CreateRole :one
-- builtin 不给传：内置角色只由「开租户」那条链路建（第 ⑤ 步），
-- 管理页面新建出来的一律是普通角色。
INSERT INTO roles (tenant_id, id, key, name, description, data_scope, status, created_by)
VALUES (sqlc.arg('tenant_id'), sqlc.arg('id'), sqlc.arg('key'), sqlc.arg('name'),
        sqlc.arg('description'), sqlc.arg('data_scope'), sqlc.arg('status'),
        sqlc.narg('created_by'))
RETURNING *;

-- name: CreateBuiltinAdminRole :one
-- 开组织时给新组织建它自己的内置 admin 角色（MULTI-TENANCY.md §3.2 ①）。
--
-- 和 CreateRole 分开是因为**只有这条能设 builtin = true**：
-- 管理页面新建出来的一律是普通角色，内置角色只由「开组织」这条链路建。
-- 内置角色不可改不可删，是权限体系的兜底 —— 它被谁随手建出来都不行。
INSERT INTO roles (tenant_id, id, key, name, description, data_scope, status, builtin)
VALUES (sqlc.arg('tenant_id'), sqlc.arg('id'),
        'admin', '超级管理员', '拥有全部权限，内置角色不可删除', 'all', 'active', true)
RETURNING *;

-- name: UpdateRole :one
-- key 不给改：它是 Casbin 策略里的标识，改了等于换了一个角色，
-- 而已经发出去的会话还带着老 key。要换就新建一个。
UPDATE roles
SET name        = sqlc.arg('name'),
    description = sqlc.arg('description'),
    data_scope  = sqlc.arg('data_scope'),
    status      = sqlc.arg('status'),
    version     = version + 1
WHERE tenant_id = sqlc.arg('tenant_id')
  AND id = sqlc.arg('id')
  AND version = sqlc.arg('version')
  AND deleted_at IS NULL
RETURNING *;

-- name: SoftDeleteRole :one
UPDATE roles
SET deleted_at = now(), version = version + 1
WHERE tenant_id = sqlc.arg('tenant_id')
  AND id = sqlc.arg('id')
  AND version = sqlc.arg('version')
  AND deleted_at IS NULL
RETURNING id;

-- name: ClearRolePermissions :exec
DELETE FROM role_permissions
WHERE tenant_id = sqlc.arg('tenant_id') AND role_id = sqlc.arg('role_id');

-- name: AddRolePermission :exec
INSERT INTO role_permissions (tenant_id, role_id, resource, action)
VALUES (sqlc.arg('tenant_id'), sqlc.arg('role_id'), sqlc.arg('resource'), sqlc.arg('action'))
ON CONFLICT DO NOTHING;

-- name: CountRoleUsers :one
SELECT count(*) FROM user_roles
WHERE tenant_id = sqlc.arg('tenant_id') AND role_id = sqlc.arg('role_id');

-- name: CountRoleServiceAccounts :one
SELECT count(*) FROM service_accounts
WHERE tenant_id = sqlc.arg('tenant_id') AND role_id = sqlc.arg('role_id') AND deleted_at IS NULL;
