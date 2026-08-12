-- 平台管理端（MULTI-TENANCY.md §6、第 ⑤ 步）。
--
-- 平台管理员**不属于任何租户** —— 他开租户、停租户，但碰不到客户的业务数据。
-- 所以它是独立的一张表，不是 users 上加个 is_platform_admin 标记（§2.3）：
-- 塞进 users 就得让 users.tenant_id 可空，而那会毁掉整套强制机制
-- （ForTenant 注入的条件对 NULL 行永远不匹配，于是每处都要开特例，漏一处就是
-- 把平台管理员暴露给客户）。

-- +goose Up

-- 超时设置，理由见 00001_init.sql。
SET LOCAL lock_timeout = '5s';
SET LOCAL statement_timeout = '60s';

-- ---------------------------------------------------------------- 平台管理员

CREATE TABLE platform_admins (
    id                   uuid         PRIMARY KEY,
    username             varchar(64)  NOT NULL,
    display_name         varchar(64)  NOT NULL,
    password_hash        text         NOT NULL,
    password_changed_at  timestamptz  NOT NULL DEFAULT now(),
    must_change_password boolean      NOT NULL DEFAULT false,
    status               varchar(16)  NOT NULL DEFAULT 'active'
                                      CHECK (status IN ('active', 'disabled')),
    -- 登录失败锁定，和租户用户同一套字段（IP 维度在内存里）
    failed_attempts      integer      NOT NULL DEFAULT 0,
    locked_until         timestamptz,
    last_login_at        timestamptz,

    created_at           timestamptz  NOT NULL DEFAULT now(),
    updated_at           timestamptz  NOT NULL DEFAULT now(),
    deleted_at           timestamptz,
    created_by           uuid,
    version              integer      NOT NULL DEFAULT 0
);

-- 没有 tenant_id，所以是全平台唯一（部分唯一索引，和 users 一个道理）
CREATE UNIQUE INDEX uk_platform_admins_username ON platform_admins (username)
    WHERE deleted_at IS NULL;

CREATE TRIGGER trg_platform_admins_updated_at
    BEFORE UPDATE ON platform_admins
    FOR EACH ROW EXECUTE FUNCTION set_updated_at();

-- ---------------------------------------------------------------- 平台会话

-- ⚠️ **必须是单独一张表**（§10.1）。sessions.user_id 是
-- `NOT NULL REFERENCES users (id)`，而平台管理员在另一张表里 ——
-- 它们的会话**根本插不进 sessions**。这件事方案里一个字没提，
-- 实施到这一步会当场卡住。
--
-- 另外两个选项都更糟：
--   · sessions.user_id 改可空 + 加 platform_admin_id → 多态外键，
--     两列互斥要靠 CHECK，每个查询都要判，容易漏
--   · 把平台管理员塞回 users → 直接推翻 §2.3
CREATE TABLE platform_sessions (
    id           uuid        PRIMARY KEY,
    -- 只存 token 的 sha256，和租户会话一样
    token_hash   bytea       NOT NULL,
    admin_id     uuid        NOT NULL REFERENCES platform_admins (id) ON DELETE CASCADE,
    ip           inet,
    user_agent   text        NOT NULL DEFAULT '',
    expires_at   timestamptz NOT NULL,
    last_seen_at timestamptz NOT NULL DEFAULT now(),
    revoked_at   timestamptz,
    created_at   timestamptz NOT NULL DEFAULT now()
);

-- 和 uk_sessions_token 一样：认证时拿 token 查这是谁，那一刻还不知道是谁。
-- 两张表的 token 空间是分开的，但 token 本身是 32 字节随机数，撞不上。
CREATE UNIQUE INDEX uk_platform_sessions_token ON platform_sessions (token_hash);
CREATE INDEX idx_platform_sessions_admin ON platform_sessions (admin_id);
CREATE INDEX idx_platform_sessions_expires ON platform_sessions (expires_at);

-- ---------------------------------------------------------------- 审计里的平台主体

-- 平台管理员的每一个动作都要进审计（§9.2）。actor_type 得认识这个新身份。
--
-- ⚠️ 改 CHECK 约束要先删再加。actor_id 没有外键（本来就没有），
-- 所以 platform_admins 的 id 可以直接放进去。
-- NOT VALID 一样要用：这次是**放宽**约束（多认一个取值），存量行不可能违反 ——
-- 但加约束时那次全表扫描照样会发生，audit_logs 是最大的一张表。校验放 00010。
ALTER TABLE audit_logs DROP CONSTRAINT audit_logs_actor_type_check;
ALTER TABLE audit_logs ADD CONSTRAINT audit_logs_actor_type_check
    CHECK (actor_type IN ('user', 'service', 'anonymous', 'system', 'platform')) NOT VALID;

-- ---------------------------------------------------------------- 租户人数冗余

-- 租户列表要显示「这家公司有多少人」。
--
-- ⚠️ **不为它开一条跨租户查业务表的口子**（§6）。那需要
-- `count(users) group by tenant_id`，而一旦平台端能查 users，
-- 「平台端结构上碰不到客户业务数据」这个性质就没了 ——
-- 那句话是将来跟客户解释隔离时最有力的一句（§10.11）。
--
-- 冗余一个数字便宜得多。用触发器维护而不是应用层记得改：
-- 应用层会漏（软删除、批量操作、将来的导入），触发器不会。
ALTER TABLE tenants ADD COLUMN user_count integer NOT NULL DEFAULT 0;

-- +goose StatementBegin
CREATE OR REPLACE FUNCTION sync_tenant_user_count() RETURNS trigger AS $$
BEGIN
    IF TG_OP = 'INSERT' THEN
        IF NEW.deleted_at IS NULL THEN
            UPDATE tenants SET user_count = user_count + 1 WHERE id = NEW.tenant_id;
        END IF;
    ELSIF TG_OP = 'DELETE' THEN
        IF OLD.deleted_at IS NULL THEN
            UPDATE tenants SET user_count = user_count - 1 WHERE id = OLD.tenant_id;
        END IF;
    ELSE
        -- 软删除和恢复：只有 deleted_at 的有无变了才算数
        IF OLD.deleted_at IS NULL AND NEW.deleted_at IS NOT NULL THEN
            UPDATE tenants SET user_count = user_count - 1 WHERE id = NEW.tenant_id;
        ELSIF OLD.deleted_at IS NOT NULL AND NEW.deleted_at IS NULL THEN
            UPDATE tenants SET user_count = user_count + 1 WHERE id = NEW.tenant_id;
        END IF;
    END IF;
    RETURN NULL;
END;
$$ LANGUAGE plpgsql;
-- +goose StatementEnd

CREATE TRIGGER trg_users_tenant_user_count
    AFTER INSERT OR DELETE OR UPDATE OF deleted_at ON users
    FOR EACH ROW EXECUTE FUNCTION sync_tenant_user_count();

-- 已有数据对一次账（本地库这时候可能已经有人了）
UPDATE tenants t SET user_count = (
    SELECT count(*) FROM users u WHERE u.tenant_id = t.id AND u.deleted_at IS NULL
);

-- ---------------------------------------------------------------- 受限角色授权

-- +goose StatementBegin
DO $$
BEGIN
    IF NOT EXISTS (SELECT FROM pg_roles WHERE rolname = 'fries_app') THEN
        RAISE NOTICE '没有 fries_app 角色，跳过平台表权限授予（本地开发正常，生产必须建）';
        RETURN;
    END IF;
    EXECUTE 'GRANT SELECT, INSERT, UPDATE, DELETE ON platform_admins TO fries_app';
    EXECUTE 'GRANT SELECT, INSERT, UPDATE, DELETE ON platform_sessions TO fries_app';
END $$;
-- +goose StatementEnd

-- +goose Down

SET LOCAL lock_timeout = '5s';
SET LOCAL statement_timeout = '60s';

DROP TRIGGER IF EXISTS trg_users_tenant_user_count ON users;
DROP FUNCTION IF EXISTS sync_tenant_user_count();
ALTER TABLE tenants DROP COLUMN user_count;

ALTER TABLE audit_logs DROP CONSTRAINT audit_logs_actor_type_check;
ALTER TABLE audit_logs ADD CONSTRAINT audit_logs_actor_type_check
    CHECK (actor_type IN ('user', 'service', 'anonymous', 'system'));

DROP TABLE platform_sessions;
DROP TABLE platform_admins;
