-- 认证相关查询（DECISIONS.md §3、§6、§8.1）。
--
-- ⚠️ 这个文件是整套租户隔离里**最需要小心**的地方（MULTI-TENANCY.md §3.2 ③）：
-- 认证发生在租户上下文建立**之前**。验证会话是「拿 cookie 里的 token 去查这是谁」，
-- 那一刻还不知道租户是谁；API Key 同理。所以查 sessions / service_accounts 的那几条
-- 只能靠代码自觉，标了 `-- tenant-exempt:`。**其余每一条都必须带 tenant_id。**

-- ---------------------------------------------------------------- 用户

-- name: GetUserByIdentifier :one
-- 登录标识符：用户名 / 邮箱 / 手机号都行。**租户内**唯一 ——
-- 登录时先用公司代码查到租户，再拿租户 + 标识符查人。
-- 万一有人的用户名恰好等于另一个人的邮箱，优先按用户名匹配，结果才是确定的。
SELECT * FROM users
WHERE tenant_id = sqlc.arg('tenant_id')
  AND deleted_at IS NULL
  AND (username = sqlc.arg('identifier')
       OR lower(email) = lower(sqlc.arg('identifier'))
       OR phone = sqlc.arg('identifier'))
ORDER BY (username = sqlc.arg('identifier')) DESC
LIMIT 1;

-- name: GetUserByID :one
-- ⚠️ 「按 id 查一行」也必须带 tenant_id（§11.1）—— 这是 OWASP API Top 10 第一名的 BOLA。
-- id 唯一不代表这一行是你的。
SELECT * FROM users
WHERE tenant_id = sqlc.arg('tenant_id') AND id = sqlc.arg('id') AND deleted_at IS NULL;

-- name: CountUsers :one
SELECT count(*) FROM users
WHERE tenant_id = sqlc.arg('tenant_id') AND deleted_at IS NULL;

-- name: CreateUser :one
INSERT INTO users (tenant_id, id, username, display_name, email, phone,
                   password_hash, must_change_password, created_by)
VALUES (sqlc.arg('tenant_id'), sqlc.arg('id'), sqlc.arg('username'), sqlc.arg('display_name'),
        sqlc.narg('email'), sqlc.narg('phone'), sqlc.arg('password_hash'),
        sqlc.arg('must_change_password'), sqlc.narg('created_by'))
RETURNING *;

-- name: MarkLoginSuccess :exec
UPDATE users
SET failed_attempts = 0,
    locked_until    = NULL,
    last_login_at   = now()
WHERE tenant_id = sqlc.arg('tenant_id') AND id = sqlc.arg('id');

-- name: MarkLoginFailure :one
UPDATE users
SET failed_attempts = failed_attempts + 1,
    -- 达到阈值就锁一段时间；阈值和时长来自 settings 表（现在是**租户级**的）
    locked_until    = CASE WHEN failed_attempts + 1 >= sqlc.arg('max_failures')::int
                           THEN now() + make_interval(mins => sqlc.arg('lock_minutes')::int)
                           ELSE locked_until END
WHERE tenant_id = sqlc.arg('tenant_id') AND id = sqlc.arg('id')
RETURNING failed_attempts, locked_until;

-- name: SetUserPassword :exec
UPDATE users
SET password_hash        = sqlc.arg('password_hash'),
    password_changed_at  = now(),
    must_change_password = false,
    failed_attempts      = 0,
    locked_until         = NULL,
    version              = version + 1
WHERE tenant_id = sqlc.arg('tenant_id') AND id = sqlc.arg('id') AND deleted_at IS NULL;

-- ---------------------------------------------------------------- 角色

-- name: GetRoleByKey :one
-- 角色 key 现在是**租户内**唯一：每家公司各有一个自己的内置 admin（§3.2 ①）。
SELECT * FROM roles
WHERE tenant_id = sqlc.arg('tenant_id') AND key = sqlc.arg('key') AND deleted_at IS NULL;

-- name: AssignUserRole :exec
INSERT INTO user_roles (tenant_id, user_id, role_id)
VALUES (sqlc.arg('tenant_id'), sqlc.arg('user_id'), sqlc.arg('role_id'))
ON CONFLICT DO NOTHING;

-- name: ListRolesOfUser :many
SELECT r.* FROM roles r
JOIN user_roles ur ON ur.role_id = r.id AND ur.tenant_id = r.tenant_id
WHERE r.tenant_id = sqlc.arg('tenant_id')
  AND ur.user_id = sqlc.arg('user_id')
  AND r.deleted_at IS NULL
  AND r.status = 'active'
ORDER BY r.key;

-- 下面三个查询喂给 Casbin：策略加载进内存，变更靠 LISTEN/NOTIFY 触发重载。
--
-- 都带上了 tenant_id —— 加载方按租户逐个加载。⚠️ Casbin 的 **domain 模型**
-- 是第 ③ 步的事（§3.1），在那之前多个租户的同名角色 key 在 enforcer 里还是会撞。
-- 查询这一层先摆正，③ 只用改 enforcer，不用回来改 SQL。

-- name: ListRolePolicies :many
SELECT r.key AS role_key, rp.resource, rp.action
FROM role_permissions rp
JOIN roles r ON r.id = rp.role_id AND r.tenant_id = rp.tenant_id
WHERE rp.tenant_id = sqlc.arg('tenant_id')
  AND r.deleted_at IS NULL AND r.status = 'active'
ORDER BY r.key, rp.resource, rp.action;

-- name: ListUserRoleBindings :many
SELECT ur.user_id, r.key AS role_key, r.data_scope
FROM user_roles ur
JOIN roles r ON r.id = ur.role_id AND r.tenant_id = ur.tenant_id
JOIN users u ON u.id = ur.user_id AND u.tenant_id = ur.tenant_id
WHERE ur.tenant_id = sqlc.arg('tenant_id')
  AND r.deleted_at IS NULL AND r.status = 'active'
  AND u.deleted_at IS NULL AND u.status = 'active'
ORDER BY ur.user_id;

-- name: ListServiceAccountRoleBindings :many
SELECT sa.id AS service_account_id, r.key AS role_key, r.data_scope
FROM service_accounts sa
JOIN roles r ON r.id = sa.role_id AND r.tenant_id = sa.tenant_id
WHERE sa.tenant_id = sqlc.arg('tenant_id')
  AND sa.deleted_at IS NULL AND sa.status = 'active'
  AND r.deleted_at IS NULL AND r.status = 'active'
ORDER BY sa.id;

-- ---------------------------------------------------------------- 会话

-- name: CreateSession :one
-- sessions.tenant_id 看着像冗余（能从 users 推出来），但整套隔离都以「会话里的租户」
-- 为唯一来源，所以它必须存在这里。两份真相会不会漂？不会 ——
-- 复合外键 (tenant_id, user_id) → users (tenant_id, id) 让数据库保证它们永远一致（§10.2）。
INSERT INTO sessions (tenant_id, id, token_hash, user_id, ip, user_agent, expires_at)
VALUES (sqlc.arg('tenant_id'), sqlc.arg('id'), sqlc.arg('token_hash'), sqlc.arg('user_id'),
        sqlc.narg('ip'), sqlc.arg('user_agent'), sqlc.arg('expires_at'))
RETURNING *;

-- name: GetLiveSession :one
-- tenant-exempt: 这就是「认证发生在租户上下文之前」的那一刻 —— 拿 cookie 里的 token
--   查这是谁，查出来的这一行才告诉我们 tenant_id。所以 uk_sessions_token 也保持
--   全平台唯一，没加 tenant_id（§2.2）。
--   ⚠️ 返回的 session.tenant_id 是后续所有隔离的唯一来源，谁都别再从别处推。
--
-- 顺带把租户状态一起取出来（§8.2）。**停用一个租户必须立刻生效** ——
-- 只挡新登录的话，客户的人拿着已有 cookie 能一直用到过期，而界面上显示「已停用」。
-- 在这里查而不是「停用时逐条吊销会话」：这样连绕过应用直接改库的停用也照样生效。
SELECT sqlc.embed(s), sqlc.embed(u), t.status AS tenant_status
FROM sessions s
JOIN users u ON u.id = s.user_id AND u.tenant_id = s.tenant_id
JOIN tenants t ON t.id = s.tenant_id
WHERE s.token_hash = sqlc.arg('token_hash')
  AND s.revoked_at IS NULL
  AND s.expires_at > now()
  AND u.deleted_at IS NULL;

-- name: TouchSession :exec
UPDATE sessions SET last_seen_at = now()
WHERE tenant_id = sqlc.arg('tenant_id') AND id = sqlc.arg('id');

-- name: RevokeSession :exec
UPDATE sessions SET revoked_at = now()
WHERE tenant_id = sqlc.arg('tenant_id') AND id = sqlc.arg('id') AND revoked_at IS NULL;

-- name: RevokeUserSessions :exec
UPDATE sessions SET revoked_at = now()
WHERE tenant_id = sqlc.arg('tenant_id') AND user_id = sqlc.arg('user_id') AND revoked_at IS NULL;

-- name: RevokeOtherUserSessions :exec
-- 改密码之后用：把这个人的其它会话全踢掉，只留当前这条。
UPDATE sessions SET revoked_at = now()
WHERE tenant_id = sqlc.arg('tenant_id')
  AND user_id = sqlc.arg('user_id')
  AND id <> sqlc.arg('keep_session_id')
  AND revoked_at IS NULL;

-- name: DeleteDeadSessions :execrows
-- tenant-exempt: 清理过期会话的后台任务是跨租户的一次扫描，不属于任何租户。
--   它只按时间删已经死掉的行，读不到任何租户数据。
DELETE FROM sessions
WHERE expires_at < now() - interval '7 days'
   OR (revoked_at IS NOT NULL AND revoked_at < now() - interval '7 days');

-- ---------------------------------------------------------------- Service Account

-- name: GetServiceAccountByPrefix :one
-- tenant-exempt: 和 GetLiveSession 同理 —— 拿 API Key 的 prefix 定位到一行，
--   那一行才告诉我们它属于哪个租户。uk_service_accounts_prefix 也保持全平台唯一。
--   租户状态同样要带上：机器身份也不能在停用的租户里继续跑（§8.2）。
SELECT sqlc.embed(sa), t.status AS tenant_status
FROM service_accounts sa
JOIN tenants t ON t.id = sa.tenant_id
WHERE sa.key_prefix = sqlc.arg('key_prefix')
  AND sa.deleted_at IS NULL
  AND sa.status = 'active';

-- name: TouchServiceAccount :exec
UPDATE service_accounts SET last_used_at = now()
WHERE tenant_id = sqlc.arg('tenant_id') AND id = sqlc.arg('id');
