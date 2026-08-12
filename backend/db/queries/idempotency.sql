-- 幂等键的持久化存储（SCALING.md §1）。表没有 tenant_id（租户已拼进 key），
-- 是跨租户的 infra 表 —— 不在 tenantsql 的租户表清单里，查询天然走 Unscoped 句柄。
-- 豁免注释一律写在 `-- name:` 之后（SplitQueries 按此归属，写在前面会错位到上一条）。

-- ClaimIdempotencyKey 原子地「认领」一个键：能认领（新键、或旧键已过期）返回 1 行，
-- 键还有效（重放）返回 0 行。靠 ON CONFLICT ... WHERE 一条语句做完，不用先查后写。
-- name: ClaimIdempotencyKey :execrows
-- tenant-exempt: idempotency_keys 是跨租户 infra 表，没有 tenant_id 列（租户已拼进 key 字符串）
INSERT INTO idempotency_keys (key, expires_at)
VALUES (sqlc.arg('key'), sqlc.arg('expires_at'))
ON CONFLICT (key) DO UPDATE
    SET expires_at = EXCLUDED.expires_at
    WHERE idempotency_keys.expires_at <= now();

-- ForgetIdempotencyKey 释放一个键，让客户端能安全重试。请求失败时调。
-- name: ForgetIdempotencyKey :exec
-- tenant-exempt: 同上，跨租户 infra 表
DELETE FROM idempotency_keys WHERE key = sqlc.arg('key');

-- DeleteExpiredIdempotencyKeys 清过期键，后台任务定时调。
-- name: DeleteExpiredIdempotencyKeys :execrows
-- tenant-exempt: 清过期键的后台任务，跨租户一次扫描
DELETE FROM idempotency_keys WHERE expires_at <= now();
