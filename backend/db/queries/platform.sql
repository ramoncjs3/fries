-- 平台管理端（MULTI-TENANCY.md §6、第 ⑤ 步）+ 平台级配置（§7.2）。
--
-- ⚠️ **这个文件里的查询只许碰四张平台级表**：
-- `tenants` / `platform_admins` / `platform_sessions` / `platform_settings`。
--
-- 这不是口头约定：生成器按「引用的表全在平台级清单里」把它们分流到
-- `Store.Platform()` 那个句柄上，平台服务**结构上就够不到业务表**。
-- 哪天有人在这里写一条 `SELECT ... FROM users`，这条查询会掉到别的句柄上，
-- 平台服务编译不过。
--
-- 为什么这么在意：一旦平台端能查 users，「平台端结构上碰不到客户业务数据」
-- 这个性质就没了 —— 而那句话是将来跟客户解释隔离时最有力的一句（§10.11）。
-- 「租户列表要显示人数」这个需求就是靠 tenants.user_count 冗余绕开的，不是靠 join。

-- ---------------------------------------------------------------- 平台级配置

-- name: ListPlatformSettings :many
-- tenant-exempt: platform_settings 是平台级表，没有 tenant_id 这一列
SELECT key, value FROM platform_settings ORDER BY key;

-- name: UpsertPlatformSetting :exec
-- tenant-exempt: 同上
INSERT INTO platform_settings (key, value, updated_by)
VALUES (sqlc.arg('key'), sqlc.arg('value'), sqlc.narg('updated_by'))
ON CONFLICT (key) DO UPDATE
SET value = excluded.value, updated_by = excluded.updated_by;

-- ---------------------------------------------------------------- 平台管理员

-- name: CountPlatformAdmins :one
-- tenant-exempt: platform_admins 是平台级表，平台管理员不属于任何租户（§2.3）
SELECT count(*) FROM platform_admins WHERE deleted_at IS NULL;

-- name: CreatePlatformAdmin :one
-- tenant-exempt: 同上
INSERT INTO platform_admins (id, username, display_name, password_hash, must_change_password)
VALUES (sqlc.arg('id'), sqlc.arg('username'), sqlc.arg('display_name'),
        sqlc.arg('password_hash'), sqlc.arg('must_change_password'))
RETURNING *;

-- name: GetPlatformAdminByUsername :one
-- tenant-exempt: 同上。平台登录只按用户名 —— 没有公司代码这一层
SELECT * FROM platform_admins WHERE username = sqlc.arg('username') AND deleted_at IS NULL;

-- name: GetPlatformAdminByID :one
-- tenant-exempt: 同上
SELECT * FROM platform_admins WHERE id = sqlc.arg('id') AND deleted_at IS NULL;

-- name: MarkPlatformLoginSuccess :exec
-- tenant-exempt: 同上
UPDATE platform_admins
SET failed_attempts = 0, locked_until = NULL, last_login_at = now()
WHERE id = sqlc.arg('id');

-- name: MarkPlatformLoginFailure :one
-- tenant-exempt: 同上
UPDATE platform_admins
SET failed_attempts = failed_attempts + 1,
    locked_until    = CASE WHEN failed_attempts + 1 >= sqlc.arg('max_failures')::int
                           THEN now() + make_interval(mins => sqlc.arg('lock_minutes')::int)
                           ELSE locked_until END
WHERE id = sqlc.arg('id')
RETURNING failed_attempts, locked_until;

-- name: SetPlatformAdminPassword :exec
-- tenant-exempt: 同上
UPDATE platform_admins
SET password_hash        = sqlc.arg('password_hash'),
    password_changed_at  = now(),
    must_change_password = false,
    failed_attempts      = 0,
    locked_until         = NULL,
    version              = version + 1
WHERE id = sqlc.arg('id') AND deleted_at IS NULL;

-- ---------------------------------------------------------------- 平台会话

-- name: CreatePlatformSession :one
-- tenant-exempt: 平台会话不属于任何租户（§10.1）
INSERT INTO platform_sessions (id, token_hash, admin_id, ip, user_agent, expires_at)
VALUES (sqlc.arg('id'), sqlc.arg('token_hash'), sqlc.arg('admin_id'),
        sqlc.narg('ip'), sqlc.arg('user_agent'), sqlc.arg('expires_at'))
RETURNING *;

-- name: GetLivePlatformSession :one
-- tenant-exempt: 和租户那边的 GetLiveSession 同理 —— 拿 token 查这是谁
SELECT sqlc.embed(s), sqlc.embed(a)
FROM platform_sessions s
JOIN platform_admins a ON a.id = s.admin_id
WHERE s.token_hash = sqlc.arg('token_hash')
  AND s.revoked_at IS NULL
  AND s.expires_at > now()
  AND a.deleted_at IS NULL;

-- name: TouchPlatformSession :exec
-- tenant-exempt: 同上
UPDATE platform_sessions SET last_seen_at = now() WHERE id = sqlc.arg('id');

-- name: RevokePlatformSession :exec
-- tenant-exempt: 同上
UPDATE platform_sessions SET revoked_at = now()
WHERE id = sqlc.arg('id') AND revoked_at IS NULL;

-- name: RevokeOtherPlatformSessions :exec
-- tenant-exempt: 同上。改密码之后把其它会话全踢掉，只留当前这条
UPDATE platform_sessions SET revoked_at = now()
WHERE admin_id = sqlc.arg('admin_id')
  AND id <> sqlc.arg('keep_session_id')
  AND revoked_at IS NULL;

-- name: DeleteDeadPlatformSessions :execrows
-- tenant-exempt: 清理任务，只按时间删已经死掉的行
DELETE FROM platform_sessions
WHERE expires_at < now() - interval '7 days'
   OR (revoked_at IS NOT NULL AND revoked_at < now() - interval '7 days');

-- ---------------------------------------------------------------- 租户

-- name: ListTenantsForPlatform :many
-- tenant-exempt: tenants 是平台级表。这是平台端的租户列表 ——
--   人数走 tenants.user_count 冗余列，**不 join users**（§6）
SELECT * FROM tenants
WHERE (sqlc.narg('keyword')::varchar IS NULL
       OR name ILIKE sqlc.narg('keyword') OR code ILIKE sqlc.narg('keyword'))
  AND (sqlc.narg('status')::varchar IS NULL OR status = sqlc.narg('status'))
ORDER BY created_at DESC
LIMIT sqlc.arg('limit') OFFSET sqlc.arg('offset');

-- name: CountTenantsForPlatform :one
-- tenant-exempt: 同上
SELECT count(*) FROM tenants
WHERE (sqlc.narg('keyword')::varchar IS NULL
       OR name ILIKE sqlc.narg('keyword') OR code ILIKE sqlc.narg('keyword'))
  AND (sqlc.narg('status')::varchar IS NULL OR status = sqlc.narg('status'));

-- name: SetTenantStatus :one
-- tenant-exempt: 同上。租户只停用不删除（§9.3）—— 没有删除语句是有意的
UPDATE tenants
SET status = sqlc.arg('status'), version = version + 1
WHERE id = sqlc.arg('id') AND version = sqlc.arg('version')
RETURNING *;
