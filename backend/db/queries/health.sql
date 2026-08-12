-- 探活用的最小查询：/readyz 靠它确认「连得上库，且库真的在响应」，
-- 而不是只看连接池状态。

-- name: DatabaseNow :one
-- tenant-exempt: 探活查询，一张表都不碰
SELECT now()::timestamptz AS now;
