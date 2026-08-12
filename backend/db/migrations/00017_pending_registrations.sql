-- 自助注册的待验证记录（MULTI-TENANCY.md §5、§9.2）。
--
-- 陌生人提交注册后，先把资料连同一次性验证 token 存在这里，**邮箱验证通过才真的建租户**。
-- 没验证就建租户会让垃圾邮箱涌进来。验证后原子建租户 + 首个 admin，然后这条记录删掉。
--
-- 不是业务表：还没有租户呢（这就是「建租户之前」那一刻），没有 tenant_id，
-- 是跨租户 infra 表 —— 不在 tenantsql 的租户表清单里，按 token_hash 查走 Unscoped + 豁免。
-- admin_password_hash 是 argon2 哈希（不是明文）；token 只存哈希（和忘记密码一致）。

-- +goose Up

SET LOCAL lock_timeout = '5s';
SET LOCAL statement_timeout = '60s';

CREATE TABLE pending_registrations (
    id             uuid         PRIMARY KEY,
    email          varchar(255) NOT NULL,
    company_name   varchar(100) NOT NULL,
    desired_code   varchar(32)  NOT NULL,
    admin_password_hash text    NOT NULL,
    token_hash     bytea        NOT NULL,
    expires_at     timestamptz  NOT NULL,
    created_at     timestamptz  NOT NULL DEFAULT now()
);

-- token_hash 全平台唯一：验证时按它定位（还没有租户）。
CREATE UNIQUE INDEX uk_pending_registrations_token ON pending_registrations (token_hash);
-- 清理过期记录、以及同一邮箱重发时作废旧的，按这两个扫。
CREATE INDEX idx_pending_registrations_expires ON pending_registrations (expires_at);
CREATE INDEX idx_pending_registrations_email ON pending_registrations (lower(email));

-- 权限走 00006 的 DEFAULT PRIVILEGES。

-- +goose Down
DROP TABLE pending_registrations;
