-- 审计日志（DECISIONS.md §6）。只有写入和查询 —— 改和删在 DB 层就被禁了。
--
-- ⚠️ audit_logs 的 tenant_id **可空**（MULTI-TENANCY.md §7.1）：未认证请求
-- （登录失败、健康检查、直接打接口）也要写审计，那些记录没有租户。
-- NULL = 平台级 / 无租户事件，只有平台端看得到。
-- 细节：公司代码有效、密码错误的登录失败要记在**那个租户名下**，客户才能看到
-- 「有人在爆破我们的账号」；只有公司代码本身无效时才记 NULL。

-- name: InsertAuditLog :exec
-- tenant-exempt: 写审计的中间件不是租户绑定的 —— 未认证请求也要写，那时 tenant_id 是 NULL。
--   列里带着 tenant_id，租户归属由调用方显式传，不靠 ForTenant 注入。
-- prev_hash / hash 由触发器算，应用不传：应用能算就意味着应用能伪造。
INSERT INTO audit_logs (
    id, tenant_id, occurred_at, request_id,
    actor_type, actor_id, actor_name,
    resource, action, resource_id,
    method, path, ip, user_agent, http_status, duration_ms, detail
) VALUES (
    sqlc.arg('id'), sqlc.narg('tenant_id'), sqlc.arg('occurred_at'), sqlc.arg('request_id'),
    sqlc.arg('actor_type'), sqlc.narg('actor_id'), sqlc.arg('actor_name'),
    sqlc.arg('resource'), sqlc.arg('action'), sqlc.narg('resource_id'),
    sqlc.arg('method'), sqlc.arg('path'), sqlc.narg('ip'), sqlc.arg('user_agent'),
    sqlc.arg('http_status'), sqlc.arg('duration_ms'), sqlc.arg('detail')
);

-- name: ListAuditLogs :many
-- resource / action 传的是 ILIKE 模式（`%li%`），由 repo.LikePattern 拼 ——
-- **别在这里拼 '%' || ... || '%'**，那样用户输入的 % 和 _ 就成了通配符。
--
-- tenant_id = ? 是等值条件，NULL 的那批（平台级事件）自然落不进来 —— 这是想要的。
SELECT * FROM audit_logs
WHERE tenant_id = sqlc.arg('tenant_id')::uuid
  AND (sqlc.narg('actor_id')::uuid IS NULL OR actor_id = sqlc.narg('actor_id'))
  AND (sqlc.narg('resource')::varchar IS NULL OR resource ILIKE sqlc.narg('resource'))
  AND (sqlc.narg('action')::varchar IS NULL OR action ILIKE sqlc.narg('action'))
  AND (sqlc.narg('from')::timestamptz IS NULL OR occurred_at >= sqlc.narg('from'))
  AND (sqlc.narg('to')::timestamptz IS NULL OR occurred_at < sqlc.narg('to'))
ORDER BY occurred_at DESC, id DESC
LIMIT sqlc.arg('limit') OFFSET sqlc.arg('offset');

-- name: CountAuditLogs :one
SELECT count(*) FROM audit_logs
WHERE tenant_id = sqlc.arg('tenant_id')::uuid
  AND (sqlc.narg('actor_id')::uuid IS NULL OR actor_id = sqlc.narg('actor_id'))
  AND (sqlc.narg('resource')::varchar IS NULL OR resource ILIKE sqlc.narg('resource'))
  AND (sqlc.narg('action')::varchar IS NULL OR action ILIKE sqlc.narg('action'))
  AND (sqlc.narg('from')::timestamptz IS NULL OR occurred_at >= sqlc.narg('from'))
  AND (sqlc.narg('to')::timestamptz IS NULL OR occurred_at < sqlc.narg('to'));

-- name: ListAuditChain :many
-- 按写入顺序取出某个租户的哈希链，用来验完整性。链现在**每租户各一条**（§2.4）。
SELECT id, occurred_at, tenant_id, actor_type, actor_id, resource, action, resource_id,
       http_status, detail, prev_hash, hash
FROM audit_logs
WHERE tenant_id = sqlc.arg('tenant_id')::uuid
ORDER BY occurred_at, id;

-- name: ListPlatformAuditChain :many
-- tenant-exempt: 平台级事件（未认证请求）的那条链，tenant_id 就是 NULL ——
--   它不属于任何租户，按定义就带不了租户条件。只有平台管理端查得到。
--
-- 单独一条查询而不是在 ListAuditChain 里 coalesce：
--   1. 那样 ListAuditChain 用不上 (tenant_id, occurred_at) 索引
--   2. 更要紧的是，coalesce 版本要传全零哨兵 UUID 才能取到平台链，
--      而 ForTenant 现在拒绝零值租户（零值只该来自 bug）。两件事分开各走各的路。
SELECT id, occurred_at, tenant_id, actor_type, actor_id, resource, action, resource_id,
       http_status, detail, prev_hash, hash
FROM audit_logs
WHERE tenant_id IS NULL
ORDER BY occurred_at, id;

-- name: EnsureAuditPartition :one
-- tenant-exempt: 建分区是跨租户的后台任务 —— 分区按月切，不按租户切
SELECT ensure_audit_partition(sqlc.arg('target'))::text AS partition_name;

-- name: DropOldAuditPartitions :one
-- tenant-exempt: 同上。保留期到了整分区 DROP，保留天数也是平台级配置
SELECT drop_audit_partitions_before(sqlc.arg('cutoff'))::int AS dropped;
