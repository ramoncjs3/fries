-- 自助注册的待验证记录（MULTI-TENANCY.md §5）。表没有 tenant_id（还没建租户），
-- 跨租户 infra 表，全走 Unscoped 句柄。豁免注释写在 `-- name:` 之后。

-- CreatePendingRegistration 存一条待验证注册。
-- name: CreatePendingRegistration :exec
-- tenant-exempt: pending_registrations 是「建租户之前」的 infra 表，没有 tenant_id
INSERT INTO pending_registrations
    (id, email, company_name, desired_code, admin_password_hash, token_hash, expires_at)
VALUES (sqlc.arg('id'), sqlc.arg('email'), sqlc.arg('company_name'), sqlc.arg('desired_code'),
        sqlc.arg('admin_password_hash'), sqlc.arg('token_hash'), sqlc.arg('expires_at'));

-- InvalidatePendingRegistrationsByEmail 删掉同一邮箱之前没验证的记录 —— 重发验证信时先清旧的。
-- name: InvalidatePendingRegistrationsByEmail :exec
-- tenant-exempt: 同上，跨租户 infra 表
DELETE FROM pending_registrations WHERE lower(email) = lower(sqlc.arg('email'));

-- ClaimPendingRegistrationByToken 按 token_hash **原子认领**一条还没过期的待验证记录：
-- 一条 DELETE ... RETURNING 同时删掉并取回资料。并发里两次验证只有一个能拿到行，
-- 另一个 0 行 → 「链接无效或已过期」，天然防重复建租户（TOCTOU）。
-- name: ClaimPendingRegistrationByToken :one
-- tenant-exempt: 验证时还没有租户，按 token 定位（token_hash 全平台唯一）
DELETE FROM pending_registrations
WHERE token_hash = sqlc.arg('token_hash') AND expires_at > now()
RETURNING email, company_name, desired_code, admin_password_hash;

-- DeleteExpiredPendingRegistrations 清过期记录，后台任务定时调。
-- name: DeleteExpiredPendingRegistrations :execrows
-- tenant-exempt: 清理任务跨租户一次扫描
DELETE FROM pending_registrations WHERE expires_at < now();
