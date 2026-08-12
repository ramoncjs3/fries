-- 忘记密码的一次性 token（MULTI-TENANCY.md §5）。库里只存哈希。
-- 租户已知的（申请、改密）走 ForTenant；按 token_hash 校验那一条租户未知，走 Unscoped + 豁免。
-- 豁免注释写在 `-- name:` 之后（SplitQueries 按此归属）。

-- CreatePasswordResetToken 建一条 token。申请时按公司代码定位了租户，走 ForTenant。
-- name: CreatePasswordResetToken :exec
INSERT INTO password_reset_tokens (tenant_id, id, user_id, token_hash, expires_at)
VALUES (sqlc.arg('tenant_id'), sqlc.arg('id'), sqlc.arg('user_id'),
        sqlc.arg('token_hash'), sqlc.arg('expires_at'));

-- InvalidateUserResetTokens 把某人还没用的 token 全标记成已用 —— 申请新的时先作废旧的，
-- 保证一个用户同时只有一条有效 token。
-- name: InvalidateUserResetTokens :exec
UPDATE password_reset_tokens
SET used_at = now()
WHERE tenant_id = sqlc.arg('tenant_id')
  AND user_id = sqlc.arg('user_id')
  AND used_at IS NULL;

-- GetLivePasswordResetToken 按 token_hash 取一条还有效的 token（未用、未过期）。
-- name: GetLivePasswordResetToken :one
-- tenant-exempt: 校验 token 的那一刻用户还没登录、租户未知 —— 查出来的这行才告诉我们
--   tenant_id + user_id（和 GetLiveSession 一个道理，token_hash 全平台唯一）。
SELECT id, tenant_id, user_id FROM password_reset_tokens
WHERE token_hash = sqlc.arg('token_hash')
  AND used_at IS NULL
  AND expires_at > now();

-- MarkPasswordResetTokenUsed **原子认领**一条 token：只有还没被用的才标记成功、返回 1 行。
-- 改密前先认领，认领到 0 行说明并发里别人已经用掉了 —— 保证一次性、防 TOCTOU 重用。
-- name: MarkPasswordResetTokenUsed :execrows
UPDATE password_reset_tokens
SET used_at = now()
WHERE tenant_id = sqlc.arg('tenant_id') AND id = sqlc.arg('id') AND used_at IS NULL;

-- DeleteExpiredPasswordResetTokens 清死掉的 token（过期或已用），后台任务定时调。
-- name: DeleteExpiredPasswordResetTokens :execrows
-- tenant-exempt: 清理任务跨租户一次扫描，只按状态删死掉的行。
DELETE FROM password_reset_tokens WHERE expires_at < now() OR used_at IS NOT NULL;
