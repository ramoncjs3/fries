-- 配置的第二层：可在后台改、改完立即生效（DECISIONS.md §5）。
--
-- 多租户之后 settings 是**租户级**的（主键 `(tenant_id, key)`，MULTI-TENANCY.md §7.2）：
-- 密码策略、会话超时每家公司自己定。平台级的那些（是否允许自助注册、租户设置的上下界）
-- 在 platform_settings 里，不走这里。

-- name: ListSettings :many
SELECT key, value FROM settings
WHERE tenant_id = sqlc.arg('tenant_id')
ORDER BY key;

-- name: UpsertSetting :exec
INSERT INTO settings (tenant_id, key, value, updated_by)
VALUES (sqlc.arg('tenant_id'), sqlc.arg('key'), sqlc.arg('value'), sqlc.narg('updated_by'))
ON CONFLICT (tenant_id, key) DO UPDATE
SET value = excluded.value, updated_by = excluded.updated_by;
