-- 限流器的持久化存储（SCALING.md §1）。表没有 tenant_id（维度已拼进 key），
-- 跨租户 infra 表，走 Unscoped 句柄。豁免注释写在 `-- name:` 之后（SplitQueries 按此归属）。

-- BumpRateLimit 原子地把 (key, 当前窗口) 的计数 +1，返回加完之后的值。
-- 靠一条 INSERT ... ON CONFLICT DO UPDATE 完成，多副本并发自增不会丢。
-- name: BumpRateLimit :one
-- tenant-exempt: rate_limits 是跨租户 infra 表，没有 tenant_id（维度已拼进 key）
INSERT INTO rate_limits (key, window_start, count)
VALUES (sqlc.arg('key'), sqlc.arg('window_start'), 1)
ON CONFLICT (key, window_start) DO UPDATE
    SET count = rate_limits.count + 1
RETURNING count;

-- DeleteOldRateLimits 清掉早于 cutoff 的窗口，后台任务定时调。
-- name: DeleteOldRateLimits :execrows
-- tenant-exempt: 清旧窗口的后台任务，跨租户一次扫描
DELETE FROM rate_limits WHERE window_start < sqlc.arg('cutoff');
