-- 认证与权限的表（DECISIONS.md §3、§6、§8.1）。
--
-- 权限点的**目录**在 Go 代码里（perm.Module 声明），不落库 —— 落库就会和代码漂。
-- 这里存的是「哪个角色勾了哪些权限点」这类运行时可改的数据。

-- +goose Up

-- 超时设置，理由见 00001_init.sql。
SET LOCAL lock_timeout = '5s';
SET LOCAL statement_timeout = '60s';

-- ---------------------------------------------------------------- 用户

CREATE TABLE users (
    id                   uuid         PRIMARY KEY,
    username             varchar(64)  NOT NULL,
    display_name         varchar(64)  NOT NULL,
    email                varchar(255),
    password_hash        text         NOT NULL,
    password_changed_at  timestamptz  NOT NULL DEFAULT now(),
    must_change_password boolean      NOT NULL DEFAULT false,
    status               varchar(16)  NOT NULL DEFAULT 'active'
                                      CHECK (status IN ('active', 'disabled')),
    -- 登录失败锁定的账号维度（IP 维度在内存里，见 internal/auth）
    failed_attempts      integer      NOT NULL DEFAULT 0,
    locked_until         timestamptz,
    last_login_at        timestamptz,

    created_at           timestamptz  NOT NULL DEFAULT now(),
    updated_at           timestamptz  NOT NULL DEFAULT now(),
    deleted_at           timestamptz,
    created_by           uuid,
    version              integer      NOT NULL DEFAULT 0
);

-- 软删除会让删掉的用户名继续占位，所以用部分唯一索引（DECISIONS.md §2.3）
CREATE UNIQUE INDEX uk_users_username ON users (username) WHERE deleted_at IS NULL;
CREATE INDEX idx_users_status ON users (status) WHERE deleted_at IS NULL;

CREATE TRIGGER trg_users_updated_at
    BEFORE UPDATE ON users
    FOR EACH ROW EXECUTE FUNCTION set_updated_at();

-- ---------------------------------------------------------------- 角色

CREATE TABLE roles (
    id          uuid         PRIMARY KEY,
    key         varchar(64)  NOT NULL,
    name        varchar(64)  NOT NULL,
    description text         NOT NULL DEFAULT '',
    -- 数据范围只有两档，多角色取最宽（DECISIONS.md §3.3）
    data_scope  varchar(8)   NOT NULL DEFAULT 'self'
                             CHECK (data_scope IN ('all', 'self')),
    -- 内置角色不允许删除或改 key
    builtin     boolean      NOT NULL DEFAULT false,
    status      varchar(16)  NOT NULL DEFAULT 'active'
                             CHECK (status IN ('active', 'disabled')),

    created_at  timestamptz  NOT NULL DEFAULT now(),
    updated_at  timestamptz  NOT NULL DEFAULT now(),
    deleted_at  timestamptz,
    created_by  uuid,
    version     integer      NOT NULL DEFAULT 0
);

CREATE UNIQUE INDEX uk_roles_key ON roles (key) WHERE deleted_at IS NULL;

CREATE TRIGGER trg_roles_updated_at
    BEFORE UPDATE ON roles
    FOR EACH ROW EXECUTE FUNCTION set_updated_at();

-- 角色勾了哪些权限点。permission 形如 "user:list"，`*` 表示全部。
CREATE TABLE role_permissions (
    role_id    uuid         NOT NULL REFERENCES roles (id) ON DELETE CASCADE,
    resource   varchar(64)  NOT NULL,
    action     varchar(64)  NOT NULL,
    created_at timestamptz  NOT NULL DEFAULT now(),
    PRIMARY KEY (role_id, resource, action)
);

-- ---------------------------------------------------------------- 主体与角色的关系

CREATE TABLE user_roles (
    user_id    uuid        NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    role_id    uuid        NOT NULL REFERENCES roles (id) ON DELETE CASCADE,
    created_at timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (user_id, role_id)
);

CREATE INDEX idx_user_roles_role ON user_roles (role_id);

-- ---------------------------------------------------------------- 会话

-- 会话存 PG，不用 localStorage JWT：httpOnly cookie 挡 XSS 偷 token，
-- 且能服务端即时踢人（DECISIONS.md §1）。
CREATE TABLE sessions (
    id           uuid        PRIMARY KEY,
    -- 只存 token 的 sha256：库被读走也拿不到能用的 cookie
    token_hash   bytea       NOT NULL,
    user_id      uuid        NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    ip           inet,
    user_agent   text        NOT NULL DEFAULT '',
    expires_at   timestamptz NOT NULL,
    last_seen_at timestamptz NOT NULL DEFAULT now(),
    revoked_at   timestamptz,
    created_at   timestamptz NOT NULL DEFAULT now()
);

CREATE UNIQUE INDEX uk_sessions_token ON sessions (token_hash);
CREATE INDEX idx_sessions_user ON sessions (user_id);
CREATE INDEX idx_sessions_expires ON sessions (expires_at);

-- ---------------------------------------------------------------- Service Account

-- 机器身份，绝不让外部系统用人的账号（DECISIONS.md §8.1）。
-- 走同一套 Casbin 权限，审计里能区分「张三点的」和「某系统调的」。
CREATE TABLE service_accounts (
    id           uuid         PRIMARY KEY,
    name         varchar(64)  NOT NULL,
    description  text         NOT NULL DEFAULT '',
    -- API Key 形如 fsa_<prefix>_<secret>：prefix 用来定位记录，secret 只存哈希
    key_prefix   varchar(16)  NOT NULL,
    key_hash     bytea        NOT NULL,
    role_id      uuid         NOT NULL REFERENCES roles (id),
    status       varchar(16)  NOT NULL DEFAULT 'active'
                              CHECK (status IN ('active', 'disabled')),
    expires_at   timestamptz,
    last_used_at timestamptz,

    created_at   timestamptz  NOT NULL DEFAULT now(),
    updated_at   timestamptz  NOT NULL DEFAULT now(),
    deleted_at   timestamptz,
    created_by   uuid,
    version      integer      NOT NULL DEFAULT 0
);

CREATE UNIQUE INDEX uk_service_accounts_prefix ON service_accounts (key_prefix) WHERE deleted_at IS NULL;
CREATE UNIQUE INDEX uk_service_accounts_name ON service_accounts (name) WHERE deleted_at IS NULL;

CREATE TRIGGER trg_service_accounts_updated_at
    BEFORE UPDATE ON service_accounts
    FOR EACH ROW EXECUTE FUNCTION set_updated_at();

-- ---------------------------------------------------------------- 内置角色

-- admin 拿通配权限：以后新增模块，超级管理员自动就有权限，不用记得回来补。
INSERT INTO roles (id, key, name, description, data_scope, builtin, created_at)
VALUES (
    '01920000-0000-7000-8000-000000000001',
    'admin', '超级管理员', '拥有全部权限，内置角色不可删除', 'all', true, now()
);

INSERT INTO role_permissions (role_id, resource, action)
VALUES ('01920000-0000-7000-8000-000000000001', '*', '*');

-- 权限、角色变更后通知所有实例重新加载策略（同 settings 的机制）。
-- +goose StatementBegin
CREATE OR REPLACE FUNCTION notify_authz_changed() RETURNS trigger AS $$
BEGIN
    PERFORM pg_notify('authz_changed', '');
    RETURN NULL;
END;
$$ LANGUAGE plpgsql;
-- +goose StatementEnd

CREATE TRIGGER trg_role_permissions_notify
    AFTER INSERT OR UPDATE OR DELETE ON role_permissions
    FOR EACH STATEMENT EXECUTE FUNCTION notify_authz_changed();

CREATE TRIGGER trg_user_roles_notify
    AFTER INSERT OR UPDATE OR DELETE ON user_roles
    FOR EACH STATEMENT EXECUTE FUNCTION notify_authz_changed();

CREATE TRIGGER trg_roles_notify
    AFTER INSERT OR UPDATE OR DELETE ON roles
    FOR EACH STATEMENT EXECUTE FUNCTION notify_authz_changed();

-- +goose Down
DROP TABLE service_accounts;
DROP TABLE sessions;
DROP TABLE user_roles;
DROP TABLE role_permissions;
DROP TABLE roles;
DROP TABLE users;
DROP FUNCTION IF EXISTS notify_authz_changed();
