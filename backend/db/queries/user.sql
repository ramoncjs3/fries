-- 用户管理（DECISIONS.md §3、§6）。
--
-- 登录链路用的那几个查询在 auth.sql 里，别混：那些是**认证**用的，
-- 这里是**管理页面**用的。
--
-- ⚠️ 这个文件里**每一条**都必须带 tenant_id，包括「按 id 改一行」的那些（§11.1）：
-- 按 id 更新时最容易觉得「id 是唯一的，够了」—— 不够，那是 BOLA。
-- 跨租户测试会拿 A 的上下文去 update/delete B 的 id，影响行数必须是 0（§10.8）。

-- name: ListUsers :many
SELECT sqlc.embed(u),
       d.name AS department_name,
       -- 角色名拼成一列返回，省掉 N+1。ORDER BY 保证多次查询顺序一致，
       -- 否则同一条记录每次刷新角色顺序都在变，看着像数据在跳。
       -- 显式 ::text —— 不加的话 sqlc 推不出 string_agg 的类型，生成 interface{}。
       COALESCE((SELECT string_agg(r.name, ',' ORDER BY r.key)
                 FROM user_roles ur
                 JOIN roles r ON r.id = ur.role_id AND r.tenant_id = ur.tenant_id
                 WHERE ur.tenant_id = u.tenant_id AND ur.user_id = u.id
                   AND r.deleted_at IS NULL), '')::text AS role_names
FROM users u
LEFT JOIN departments d ON d.id = u.department_id AND d.tenant_id = u.tenant_id
                       AND d.deleted_at IS NULL
WHERE u.tenant_id = sqlc.arg('tenant_id')
  AND u.deleted_at IS NULL
  AND (sqlc.narg('keyword')::varchar IS NULL
       OR u.username ILIKE sqlc.narg('keyword')
       OR u.display_name ILIKE sqlc.narg('keyword')
       OR u.email ILIKE sqlc.narg('keyword')
       OR u.phone ILIKE sqlc.narg('keyword'))
  AND (sqlc.narg('status')::varchar IS NULL OR u.status = sqlc.narg('status'))
  -- 部门筛选支持**多选 + 「未分配」**：
  --   department_ids 为空数组且 include_unassigned=false → 不按部门筛
  --   否则命中「在这些部门里」或「压根没部门」
  AND (
    (cardinality(sqlc.arg('department_ids')::uuid[]) = 0 AND NOT sqlc.arg('include_unassigned')::bool)
    OR u.department_id = ANY(sqlc.arg('department_ids')::uuid[])
    OR (sqlc.arg('include_unassigned')::bool AND u.department_id IS NULL)
  )
ORDER BY u.created_at DESC, u.id DESC
LIMIT sqlc.arg('limit') OFFSET sqlc.arg('offset');

-- name: CountUsersFiltered :one
SELECT count(*) FROM users u
WHERE u.tenant_id = sqlc.arg('tenant_id')
  AND u.deleted_at IS NULL
  AND (sqlc.narg('keyword')::varchar IS NULL
       OR u.username ILIKE sqlc.narg('keyword')
       OR u.display_name ILIKE sqlc.narg('keyword')
       OR u.email ILIKE sqlc.narg('keyword')
       OR u.phone ILIKE sqlc.narg('keyword'))
  AND (sqlc.narg('status')::varchar IS NULL OR u.status = sqlc.narg('status'))
  AND (
    (cardinality(sqlc.arg('department_ids')::uuid[]) = 0 AND NOT sqlc.arg('include_unassigned')::bool)
    OR u.department_id = ANY(sqlc.arg('department_ids')::uuid[])
    OR (sqlc.arg('include_unassigned')::bool AND u.department_id IS NULL)
  );

-- name: CreateManagedUser :one
INSERT INTO users (tenant_id, id, username, display_name, email, phone, password_hash,
                   must_change_password, status, department_id, created_by)
VALUES (sqlc.arg('tenant_id'), sqlc.arg('id'), sqlc.arg('username'), sqlc.arg('display_name'),
        sqlc.narg('email'), sqlc.narg('phone'), sqlc.arg('password_hash'),
        true, sqlc.arg('status'), sqlc.narg('department_id'), sqlc.narg('created_by'))
RETURNING *;

-- name: UpdateManagedUser :one
-- username 不给改：它是登录标识，也是审计里认人的依据。
-- 改了之后老审计记录指向的那个人就对不上了。
UPDATE users
SET display_name  = sqlc.arg('display_name'),
    email         = sqlc.narg('email'),
    phone         = sqlc.narg('phone'),
    status        = sqlc.arg('status'),
    department_id = sqlc.narg('department_id'),
    version       = version + 1
WHERE tenant_id = sqlc.arg('tenant_id')
  AND id = sqlc.arg('id')
  AND version = sqlc.arg('version')
  AND deleted_at IS NULL
RETURNING *;

-- name: SoftDeleteUser :one
UPDATE users
SET deleted_at = now(), version = version + 1
WHERE tenant_id = sqlc.arg('tenant_id')
  AND id = sqlc.arg('id')
  AND version = sqlc.arg('version')
  AND deleted_at IS NULL
RETURNING id;

-- name: ResetUserPassword :execrows
-- 管理员重置密码：**一定要 must_change_password = true** ——
-- 临时密码经过了管理员的手，本人必须马上换掉。
UPDATE users
SET password_hash        = sqlc.arg('password_hash'),
    password_changed_at  = now(),
    must_change_password = true,
    failed_attempts      = 0,
    locked_until         = NULL,
    version              = version + 1
WHERE tenant_id = sqlc.arg('tenant_id') AND id = sqlc.arg('id') AND deleted_at IS NULL;

-- name: ClearUserRoles :exec
DELETE FROM user_roles
WHERE tenant_id = sqlc.arg('tenant_id') AND user_id = sqlc.arg('user_id');

-- name: ListRoleIDsOfUser :many
SELECT role_id FROM user_roles
WHERE tenant_id = sqlc.arg('tenant_id') AND user_id = sqlc.arg('user_id');

-- name: GetUserWithDepartment :one
SELECT sqlc.embed(u), d.name AS department_name
FROM users u
LEFT JOIN departments d ON d.id = u.department_id AND d.tenant_id = u.tenant_id
                       AND d.deleted_at IS NULL
WHERE u.tenant_id = sqlc.arg('tenant_id') AND u.id = sqlc.arg('id') AND u.deleted_at IS NULL;

-- name: ListRolesByIDs :many
SELECT * FROM roles
WHERE tenant_id = sqlc.arg('tenant_id')
  AND id = ANY(sqlc.arg('ids')::uuid[])
  AND deleted_at IS NULL;

-- name: CountActiveAdmins :one
-- 还有几个「启用状态 + 拥有通配权限」的人。
-- 停用/删除最后一个管理员会把所有人锁在门外，必须拦住。
--
-- ⚠️ 这条**必须**按租户数（§3.2 ①）。原来是一条不带租户条件的全表 count，
-- 多租户下等于：A 公司还有 admin，B 公司删掉自己最后一个 admin 时检查照样通过 ——
-- **B 公司被锁在门外，只能上数据库救。**
SELECT count(DISTINCT u.id)
FROM users u
JOIN user_roles ur ON ur.user_id = u.id AND ur.tenant_id = u.tenant_id
JOIN roles r ON r.id = ur.role_id AND r.tenant_id = ur.tenant_id
JOIN role_permissions rp ON rp.role_id = r.id AND rp.tenant_id = r.tenant_id
WHERE u.tenant_id = sqlc.arg('tenant_id')
  AND u.deleted_at IS NULL AND u.status = 'active'
  AND r.deleted_at IS NULL AND r.status = 'active'
  AND rp.resource = '*' AND rp.action = '*'
  AND u.id <> sqlc.arg('exclude_user_id')::uuid;

-- name: SetUsersDepartment :execrows
-- 批量调整部门归属。department_id 传 NULL 就是移出部门。
-- **不动 version**：调部门是管理动作，不该让正在编辑这个人的另一个页面撞版本冲突。
--
-- ⚠️ 批量按 id 改是 BOLA 的重灾区：不带 tenant_id 的话，传一串别家公司的 user_id
-- 就能把人从他们的部门里摘出来。影响行数会如实返回，也就成了一个存在性探针。
UPDATE users
SET department_id = sqlc.narg('department_id')
WHERE tenant_id = sqlc.arg('tenant_id')
  AND id = ANY(sqlc.arg('user_ids')::uuid[])
  AND deleted_at IS NULL;

-- name: ListUsersNotInDepartment :many
-- 「添加成员」候选：不在这个部门里的人。department_id 传 NULL 表示「不在任何部门的人」。
SELECT sqlc.embed(u), d.name AS department_name
FROM users u
LEFT JOIN departments d ON d.id = u.department_id AND d.tenant_id = u.tenant_id
                       AND d.deleted_at IS NULL
WHERE u.tenant_id = sqlc.arg('tenant_id')
  AND u.deleted_at IS NULL
  AND u.status = 'active'
  AND (u.department_id IS DISTINCT FROM sqlc.narg('department_id')::uuid)
  AND (sqlc.narg('keyword')::varchar IS NULL
       OR u.username ILIKE sqlc.narg('keyword')
       OR u.display_name ILIKE sqlc.narg('keyword'))
ORDER BY u.display_name
LIMIT sqlc.arg('limit');
