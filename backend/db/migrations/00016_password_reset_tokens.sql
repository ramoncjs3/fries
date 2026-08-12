-- 忘记密码用的一次性 token（MULTI-TENANCY.md §5）。
--
-- 和 sessions 一样：库里只存 token 的哈希，明文只在邮件里给用户一次。按 token_hash 查，
-- 那一刻还不知道租户（用户还没登录）—— 查出来的行才告诉我们 tenant_id + user_id。
-- 所以 token_hash 全平台唯一，那条查询走 tenant-exempt（§3.2 ③）。

-- +goose Up

SET LOCAL lock_timeout = '5s';
SET LOCAL statement_timeout = '60s';

CREATE TABLE password_reset_tokens (
    id         uuid        PRIMARY KEY,
    tenant_id  uuid        NOT NULL,
    user_id    uuid        NOT NULL,
    token_hash bytea       NOT NULL,
    expires_at timestamptz NOT NULL,
    used_at    timestamptz,
    created_at timestamptz NOT NULL DEFAULT now(),
    -- 复合外键：和 sessions 一样，(tenant_id, user_id) 一起指过去，防两份租户真相漂移。
    FOREIGN KEY (tenant_id, user_id) REFERENCES users (tenant_id, id) ON DELETE CASCADE
);

-- token_hash 全平台唯一：认证前按它定位，还不知道租户（和 uk_sessions_token 一个道理）。
CREATE UNIQUE INDEX uk_password_reset_tokens_hash ON password_reset_tokens (token_hash);
-- 清理过期 token 按 expires_at 扫。
CREATE INDEX idx_password_reset_tokens_expires ON password_reset_tokens (expires_at);
-- 按 (租户, 用户) 找某人的 token（作废旧的），tenant_id 打头。
CREATE INDEX idx_password_reset_tokens_user ON password_reset_tokens (tenant_id, user_id);

-- 权限走 00006 的 DEFAULT PRIVILEGES，fries_app 自动拿到读写。

-- +goose Down
DROP TABLE password_reset_tokens;
