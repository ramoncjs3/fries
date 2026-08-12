-- Service Account（机器身份）管理（DECISIONS.md §8.1）。
--
-- 🔴 **这个文件里的查询一律不返回 `key_hash`。**
-- 所以每条都写明列名，不用 `SELECT *` —— 加一列的时候 `*` 会把新列自动带出去，
-- 而这张表上「不该出去的列」正是它的核心资产。密钥只在新建和轮换时以明文返回一次，
-- 之后库里只有哈希，谁都取不回来。
--
-- ⚠️ 认证链路用的那两条（按 prefix 定位、写 last_used_at）在 auth.sql 里，别混：
-- 那条按 prefix 查的是**不带租户条件**的（认证发生在租户上下文之前，§3.2 ③），
-- 而这里每一条都必须带租户。

-- name: ListServiceAccounts :many
SELECT sa.id, sa.name, sa.description, sa.key_prefix, sa.role_id,
       r.name AS role_name, sa.status, sa.expires_at, sa.last_used_at,
       sa.created_at, sa.version
FROM service_accounts sa
JOIN roles r ON r.id = sa.role_id AND r.tenant_id = sa.tenant_id
WHERE sa.tenant_id = sqlc.arg('tenant_id')
  AND sa.deleted_at IS NULL
  AND (sqlc.narg('keyword')::varchar IS NULL
       OR sa.name ILIKE sqlc.narg('keyword')
       OR sa.description ILIKE sqlc.narg('keyword'))
  AND (sqlc.narg('status')::varchar IS NULL OR sa.status = sqlc.narg('status'))
ORDER BY sa.created_at DESC, sa.id DESC
LIMIT sqlc.arg('limit') OFFSET sqlc.arg('offset');

-- name: CountServiceAccounts :one
SELECT count(*) FROM service_accounts sa
WHERE sa.tenant_id = sqlc.arg('tenant_id')
  AND sa.deleted_at IS NULL
  AND (sqlc.narg('keyword')::varchar IS NULL
       OR sa.name ILIKE sqlc.narg('keyword')
       OR sa.description ILIKE sqlc.narg('keyword'))
  AND (sqlc.narg('status')::varchar IS NULL OR sa.status = sqlc.narg('status'));

-- name: GetServiceAccount :one
-- ⚠️ 「按 id 查一行」也带 tenant_id（MULTI-TENANCY.md §11.1）—— id 唯一不代表这一行是你的。
SELECT sa.id, sa.name, sa.description, sa.key_prefix, sa.role_id,
       r.name AS role_name, sa.status, sa.expires_at, sa.last_used_at,
       sa.created_at, sa.version
FROM service_accounts sa
JOIN roles r ON r.id = sa.role_id AND r.tenant_id = sa.tenant_id
WHERE sa.tenant_id = sqlc.arg('tenant_id') AND sa.id = sqlc.arg('id')
  AND sa.deleted_at IS NULL;

-- name: CreateServiceAccount :one
-- 返回的行同样不含 key_hash：明文密钥在应用侧，调用方手里已经有了，
-- 不需要也不该从这里再拿一次。
INSERT INTO service_accounts (tenant_id, id, name, description, key_prefix, key_hash,
                              role_id, status, expires_at, created_by)
VALUES (sqlc.arg('tenant_id'), sqlc.arg('id'), sqlc.arg('name'), sqlc.arg('description'),
        sqlc.arg('key_prefix'), sqlc.arg('key_hash'), sqlc.arg('role_id'),
        sqlc.arg('status'), sqlc.narg('expires_at'), sqlc.narg('created_by'))
RETURNING id, name, description, key_prefix, role_id, status, expires_at, created_at, version;

-- name: UpdateServiceAccount :one
-- 乐观锁：影响 0 行说明版本对不上，service 层翻成 common.version_conflict。
--
-- ⚠️ **key_prefix / key_hash 不在这里改** —— 换密钥是另一个动作、另一个权限点，
-- 走 RotateServiceAccountKey。混在一起的话，「改个描述」和「作废对接方手里的凭据」
-- 就成了同一个按钮。
UPDATE service_accounts
SET name        = sqlc.arg('name'),
    description = sqlc.arg('description'),
    role_id     = sqlc.arg('role_id'),
    status      = sqlc.arg('status'),
    expires_at  = sqlc.narg('expires_at'),
    version     = version + 1
WHERE tenant_id = sqlc.arg('tenant_id')
  AND id = sqlc.arg('id')
  AND version = sqlc.arg('version')
  AND deleted_at IS NULL
RETURNING id, name, description, key_prefix, role_id, status, expires_at, created_at, version;

-- name: RotateServiceAccountKey :execrows
-- 换一副新密钥，记录本身不动。
--
-- 泄露之后要的就是这个：对接方换一串新的继续跑，不用重建记录、不用重配权限。
-- 旧密钥当场失效 —— 认证是按 key_prefix 定位的，prefix 一换，旧的那串谁也对不上。
UPDATE service_accounts
SET key_prefix = sqlc.arg('key_prefix'),
    key_hash   = sqlc.arg('key_hash'),
    version    = version + 1
WHERE tenant_id = sqlc.arg('tenant_id') AND id = sqlc.arg('id') AND deleted_at IS NULL;

-- name: SoftDeleteServiceAccount :one
UPDATE service_accounts
SET deleted_at = now(), version = version + 1
WHERE tenant_id = sqlc.arg('tenant_id')
  AND id = sqlc.arg('id')
  AND version = sqlc.arg('version')
  AND deleted_at IS NULL
RETURNING id;
