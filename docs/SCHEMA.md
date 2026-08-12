# 数据库表结构总览

> **本文件由 `make schemadoc` 自动生成，请勿手改。**
> 数据源是 `backend/db/migrations/`，改表一律走 goose 迁移（红线 #4）。
> `make check` 会校验本文件是最新的。

迁移文件 18 个，表 19 张。

## settings

建表于 `00002_settings.sql`。

| 列 | 定义 |
|---|---|
| `key` | `varchar(100) PRIMARY KEY` |
| `value` | `jsonb NOT NULL` |
| `description` | `text NOT NULL DEFAULT ''` |
| `updated_at` | `timestamptz NOT NULL DEFAULT now()` |
| `updated_by` | `uuid` |

**索引**

- `CREATE UNIQUE INDEX settings_pkey ON settings (tenant_id, key)`

**后续变更**

- `ADD COLUMN tenant_id uuid（00007_multi_tenancy.sql）`
- `ALTER COLUMN tenant_id SET NOT NULL（00007_multi_tenancy.sql）`
- `ADD CONSTRAINT fk_settings_tenant FOREIGN KEY (tenant_id) REFERENCES tenants (id) NOT VALID（00007_multi_tenancy.sql）`
- `DROP CONSTRAINT settings_pkey（00007_multi_tenancy.sql）`
- `ADD PRIMARY KEY USING INDEX settings_pkey（00007_multi_tenancy.sql）`
- `VALIDATE CONSTRAINT fk_settings_tenant（00008_validate_tenant_constraints.sql）`

## users

建表于 `00003_auth.sql`。

| 列 | 定义 |
|---|---|
| `id` | `uuid PRIMARY KEY` |
| `username` | `varchar(64) NOT NULL` |
| `display_name` | `varchar(64) NOT NULL` |
| `email` | `varchar(255)` |
| `password_hash` | `text NOT NULL` |
| `password_changed_at` | `timestamptz NOT NULL DEFAULT now()` |
| `must_change_password` | `boolean NOT NULL DEFAULT false` |
| `status` | `varchar(16) NOT NULL DEFAULT 'active' CHECK (status IN ('active', 'disabled'))` |
| `failed_attempts` | `integer NOT NULL DEFAULT 0` |
| `locked_until` | `timestamptz` |
| `last_login_at` | `timestamptz` |
| `created_at` | `timestamptz NOT NULL DEFAULT now()` |
| `updated_at` | `timestamptz NOT NULL DEFAULT now()` |
| `deleted_at` | `timestamptz` |
| `created_by` | `uuid` |
| `version` | `integer NOT NULL DEFAULT 0` |

**索引**

- `CREATE UNIQUE INDEX uk_users_username ON users (username) WHERE deleted_at IS NULL`
- `CREATE INDEX idx_users_status ON users (status) WHERE deleted_at IS NULL`
- `CREATE UNIQUE INDEX uk_users_email ON users (lower(email)) WHERE deleted_at IS NULL AND email IS NOT NULL`
- `CREATE UNIQUE INDEX uk_users_phone ON users (phone) WHERE deleted_at IS NULL AND phone IS NOT NULL`
- `CREATE INDEX idx_users_department ON users (department_id) WHERE deleted_at IS NULL`
- `CREATE UNIQUE INDEX uk_users_username ON users (tenant_id, username) WHERE deleted_at IS NULL`
- `CREATE UNIQUE INDEX uk_users_email ON users (tenant_id, lower(email)) WHERE deleted_at IS NULL AND email IS NOT NULL`
- `CREATE UNIQUE INDEX uk_users_phone ON users (tenant_id, phone) WHERE deleted_at IS NULL AND phone IS NOT NULL`
- `CREATE UNIQUE INDEX uk_users_tenant_id ON users (tenant_id, id)`
- `CREATE INDEX idx_users_status ON users (tenant_id, status) WHERE deleted_at IS NULL`
- `CREATE INDEX idx_users_department ON users (tenant_id, department_id) WHERE deleted_at IS NULL`

**后续变更**

- `ADD COLUMN phone varchar(32)（00005_user_identifiers.sql）`
- `ADD COLUMN department_id uuid（00006_departments.sql）`
- `ADD CONSTRAINT fk_users_department FOREIGN KEY (department_id) REFERENCES departments (id) NOT VALID（00006_departments.sql）`
- `ADD COLUMN tenant_id uuid（00007_multi_tenancy.sql）`
- `ALTER COLUMN tenant_id SET NOT NULL（00007_multi_tenancy.sql）`
- `ADD CONSTRAINT fk_users_tenant FOREIGN KEY (tenant_id) REFERENCES tenants (id) NOT VALID（00007_multi_tenancy.sql）`
- `DROP CONSTRAINT fk_users_department（00007_multi_tenancy.sql）`
- `ADD CONSTRAINT fk_users_department FOREIGN KEY (tenant_id, department_id) REFERENCES departments (tenant_id, id) NOT VALID（00007_multi_tenancy.sql）`
- `VALIDATE CONSTRAINT fk_users_tenant（00008_validate_tenant_constraints.sql）`
- `VALIDATE CONSTRAINT fk_users_department（00008_validate_tenant_constraints.sql）`

## roles

建表于 `00003_auth.sql`。

| 列 | 定义 |
|---|---|
| `id` | `uuid PRIMARY KEY` |
| `key` | `varchar(64) NOT NULL` |
| `name` | `varchar(64) NOT NULL` |
| `description` | `text NOT NULL DEFAULT ''` |
| `data_scope` | `varchar(8) NOT NULL DEFAULT 'self' CHECK (data_scope IN ('all', 'self'))` |
| `builtin` | `boolean NOT NULL DEFAULT false` |
| `status` | `varchar(16) NOT NULL DEFAULT 'active' CHECK (status IN ('active', 'disabled'))` |
| `created_at` | `timestamptz NOT NULL DEFAULT now()` |
| `updated_at` | `timestamptz NOT NULL DEFAULT now()` |
| `deleted_at` | `timestamptz` |
| `created_by` | `uuid` |
| `version` | `integer NOT NULL DEFAULT 0` |

**索引**

- `CREATE UNIQUE INDEX uk_roles_key ON roles (key) WHERE deleted_at IS NULL`
- `CREATE UNIQUE INDEX uk_roles_key ON roles (tenant_id, key) WHERE deleted_at IS NULL`
- `CREATE UNIQUE INDEX uk_roles_tenant_id ON roles (tenant_id, id)`

**后续变更**

- `ADD COLUMN tenant_id uuid（00007_multi_tenancy.sql）`
- `ALTER COLUMN tenant_id SET NOT NULL（00007_multi_tenancy.sql）`
- `ADD CONSTRAINT fk_roles_tenant FOREIGN KEY (tenant_id) REFERENCES tenants (id) NOT VALID（00007_multi_tenancy.sql）`
- `VALIDATE CONSTRAINT fk_roles_tenant（00008_validate_tenant_constraints.sql）`

## role_permissions

建表于 `00003_auth.sql`。

| 列 | 定义 |
|---|---|
| `role_id` | `uuid NOT NULL REFERENCES roles (id) ON DELETE CASCADE` |
| `resource` | `varchar(64) NOT NULL` |
| `action` | `varchar(64) NOT NULL` |
| `created_at` | `timestamptz NOT NULL DEFAULT now()` |
| `PRIMARY` | `KEY (role_id, resource, action)` |

**索引**

- `CREATE UNIQUE INDEX role_permissions_pkey ON role_permissions (tenant_id, role_id, resource, action)`

**后续变更**

- `ADD COLUMN tenant_id uuid（00007_multi_tenancy.sql）`
- `ALTER COLUMN tenant_id SET NOT NULL（00007_multi_tenancy.sql）`
- `ADD CONSTRAINT fk_role_permissions_tenant FOREIGN KEY (tenant_id) REFERENCES tenants (id) NOT VALID（00007_multi_tenancy.sql）`
- `DROP CONSTRAINT role_permissions_role_id_fkey（00007_multi_tenancy.sql）`
- `ADD CONSTRAINT fk_role_permissions_role FOREIGN KEY (tenant_id, role_id) REFERENCES roles (tenant_id, id) ON DELETE CASCADE NOT VALID（00007_multi_tenancy.sql）`
- `DROP CONSTRAINT role_permissions_pkey（00007_multi_tenancy.sql）`
- `ADD PRIMARY KEY USING INDEX role_permissions_pkey（00007_multi_tenancy.sql）`
- `VALIDATE CONSTRAINT fk_role_permissions_tenant（00008_validate_tenant_constraints.sql）`
- `VALIDATE CONSTRAINT fk_role_permissions_role（00008_validate_tenant_constraints.sql）`

## user_roles

建表于 `00003_auth.sql`。

| 列 | 定义 |
|---|---|
| `user_id` | `uuid NOT NULL REFERENCES users (id) ON DELETE CASCADE` |
| `role_id` | `uuid NOT NULL REFERENCES roles (id) ON DELETE CASCADE` |
| `created_at` | `timestamptz NOT NULL DEFAULT now()` |
| `PRIMARY` | `KEY (user_id, role_id)` |

**索引**

- `CREATE INDEX idx_user_roles_role ON user_roles (role_id)`
- `CREATE UNIQUE INDEX user_roles_pkey ON user_roles (tenant_id, user_id, role_id)`
- `CREATE INDEX idx_user_roles_role ON user_roles (tenant_id, role_id)`

**后续变更**

- `ADD COLUMN tenant_id uuid（00007_multi_tenancy.sql）`
- `ALTER COLUMN tenant_id SET NOT NULL（00007_multi_tenancy.sql）`
- `ADD CONSTRAINT fk_user_roles_tenant FOREIGN KEY (tenant_id) REFERENCES tenants (id) NOT VALID（00007_multi_tenancy.sql）`
- `DROP CONSTRAINT user_roles_user_id_fkey（00007_multi_tenancy.sql）`
- `ADD CONSTRAINT fk_user_roles_user FOREIGN KEY (tenant_id, user_id) REFERENCES users (tenant_id, id) ON DELETE CASCADE NOT VALID（00007_multi_tenancy.sql）`
- `DROP CONSTRAINT user_roles_role_id_fkey（00007_multi_tenancy.sql）`
- `ADD CONSTRAINT fk_user_roles_role FOREIGN KEY (tenant_id, role_id) REFERENCES roles (tenant_id, id) ON DELETE CASCADE NOT VALID（00007_multi_tenancy.sql）`
- `DROP CONSTRAINT user_roles_pkey（00007_multi_tenancy.sql）`
- `ADD PRIMARY KEY USING INDEX user_roles_pkey（00007_multi_tenancy.sql）`
- `VALIDATE CONSTRAINT fk_user_roles_tenant（00008_validate_tenant_constraints.sql）`
- `VALIDATE CONSTRAINT fk_user_roles_user（00008_validate_tenant_constraints.sql）`
- `VALIDATE CONSTRAINT fk_user_roles_role（00008_validate_tenant_constraints.sql）`

## sessions

建表于 `00003_auth.sql`。

| 列 | 定义 |
|---|---|
| `id` | `uuid PRIMARY KEY` |
| `token_hash` | `bytea NOT NULL` |
| `user_id` | `uuid NOT NULL REFERENCES users (id) ON DELETE CASCADE` |
| `ip` | `inet` |
| `user_agent` | `text NOT NULL DEFAULT ''` |
| `expires_at` | `timestamptz NOT NULL` |
| `last_seen_at` | `timestamptz NOT NULL DEFAULT now()` |
| `revoked_at` | `timestamptz` |
| `created_at` | `timestamptz NOT NULL DEFAULT now()` |

**索引**

- `CREATE UNIQUE INDEX uk_sessions_token ON sessions (token_hash)`
- `CREATE INDEX idx_sessions_user ON sessions (user_id)`
- `CREATE INDEX idx_sessions_expires ON sessions (expires_at)`
- `CREATE INDEX idx_sessions_user ON sessions (tenant_id, user_id)`

**后续变更**

- `ADD COLUMN tenant_id uuid（00007_multi_tenancy.sql）`
- `ALTER COLUMN tenant_id SET NOT NULL（00007_multi_tenancy.sql）`
- `ADD CONSTRAINT fk_sessions_tenant FOREIGN KEY (tenant_id) REFERENCES tenants (id) NOT VALID（00007_multi_tenancy.sql）`
- `DROP CONSTRAINT sessions_user_id_fkey（00007_multi_tenancy.sql）`
- `ADD CONSTRAINT fk_sessions_user FOREIGN KEY (tenant_id, user_id) REFERENCES users (tenant_id, id) ON DELETE CASCADE NOT VALID（00007_multi_tenancy.sql）`
- `VALIDATE CONSTRAINT fk_sessions_tenant（00008_validate_tenant_constraints.sql）`
- `VALIDATE CONSTRAINT fk_sessions_user（00008_validate_tenant_constraints.sql）`

## service_accounts

建表于 `00003_auth.sql`。

| 列 | 定义 |
|---|---|
| `id` | `uuid PRIMARY KEY` |
| `name` | `varchar(64) NOT NULL` |
| `description` | `text NOT NULL DEFAULT ''` |
| `key_prefix` | `varchar(16) NOT NULL` |
| `key_hash` | `bytea NOT NULL` |
| `role_id` | `uuid NOT NULL REFERENCES roles (id)` |
| `status` | `varchar(16) NOT NULL DEFAULT 'active' CHECK (status IN ('active', 'disabled'))` |
| `expires_at` | `timestamptz` |
| `last_used_at` | `timestamptz` |
| `created_at` | `timestamptz NOT NULL DEFAULT now()` |
| `updated_at` | `timestamptz NOT NULL DEFAULT now()` |
| `deleted_at` | `timestamptz` |
| `created_by` | `uuid` |
| `version` | `integer NOT NULL DEFAULT 0` |

**索引**

- `CREATE UNIQUE INDEX uk_service_accounts_prefix ON service_accounts (key_prefix) WHERE deleted_at IS NULL`
- `CREATE UNIQUE INDEX uk_service_accounts_name ON service_accounts (name) WHERE deleted_at IS NULL`
- `CREATE UNIQUE INDEX uk_service_accounts_name ON service_accounts (tenant_id, name) WHERE deleted_at IS NULL`

**后续变更**

- `ADD COLUMN tenant_id uuid（00007_multi_tenancy.sql）`
- `ALTER COLUMN tenant_id SET NOT NULL（00007_multi_tenancy.sql）`
- `ADD CONSTRAINT fk_service_accounts_tenant FOREIGN KEY (tenant_id) REFERENCES tenants (id) NOT VALID（00007_multi_tenancy.sql）`
- `DROP CONSTRAINT service_accounts_role_id_fkey（00007_multi_tenancy.sql）`
- `ADD CONSTRAINT fk_service_accounts_role FOREIGN KEY (tenant_id, role_id) REFERENCES roles (tenant_id, id) NOT VALID（00007_multi_tenancy.sql）`
- `VALIDATE CONSTRAINT fk_service_accounts_tenant（00008_validate_tenant_constraints.sql）`
- `VALIDATE CONSTRAINT fk_service_accounts_role（00008_validate_tenant_constraints.sql）`

## audit_logs

建表于 `00004_audit.sql`。

| 列 | 定义 |
|---|---|
| `id` | `uuid NOT NULL` |
| `occurred_at` | `timestamptz NOT NULL DEFAULT now()` |
| `request_id` | `varchar(64) NOT NULL DEFAULT ''` |
| `actor_type` | `varchar(16) NOT NULL CHECK (actor_type IN ('user', 'service', 'anonymous', 'system'))` |
| `actor_id` | `uuid` |
| `actor_name` | `varchar(64) NOT NULL DEFAULT ''` |
| `resource` | `varchar(64) NOT NULL` |
| `action` | `varchar(64) NOT NULL` |
| `resource_id` | `uuid` |
| `method` | `varchar(8) NOT NULL DEFAULT ''` |
| `path` | `text NOT NULL DEFAULT ''` |
| `ip` | `inet` |
| `user_agent` | `text NOT NULL DEFAULT ''` |
| `http_status` | `integer NOT NULL DEFAULT 0` |
| `duration_ms` | `integer NOT NULL DEFAULT 0` |
| `detail` | `jsonb NOT NULL DEFAULT '{}'::jsonb` |
| `prev_hash` | `bytea` |
| `hash` | `bytea NOT NULL` |
| `PRIMARY` | `KEY (id, occurred_at)` |

**索引**

- `CREATE INDEX idx_audit_logs_occurred ON audit_logs (occurred_at DESC)`
- `CREATE INDEX idx_audit_logs_actor ON audit_logs (actor_id, occurred_at DESC)`
- `CREATE INDEX idx_audit_logs_resource ON audit_logs (resource, resource_id, occurred_at DESC)`
- `CREATE INDEX idx_audit_logs_request ON audit_logs (request_id)`
- `CREATE INDEX idx_audit_logs_occurred ON audit_logs (tenant_id, occurred_at DESC)`
- `CREATE INDEX idx_audit_logs_actor ON audit_logs (tenant_id, actor_id, occurred_at DESC)`
- `CREATE INDEX idx_audit_logs_resource ON audit_logs (tenant_id, resource, resource_id, occurred_at DESC)`

**后续变更**

- `ADD COLUMN tenant_id uuid（00007_multi_tenancy.sql）`
- `DROP CONSTRAINT audit_logs_actor_type_check（00009_platform_admin.sql）`
- `ADD CONSTRAINT audit_logs_actor_type_check CHECK (actor_type IN ('user', 'service', 'anonymous', 'system', 'platform')) NOT VALID（00009_platform_admin.sql）`
- `VALIDATE CONSTRAINT audit_logs_actor_type_check（00010_validate_platform_constraints.sql）`

## audit_chain_head

建表于 `00004_audit.sql`。

| 列 | 定义 |
|---|---|
| `only_row` | `boolean PRIMARY KEY DEFAULT true CHECK (only_row)` |
| `hash` | `bytea` |

**索引**

- `CREATE UNIQUE INDEX audit_chain_head_pkey ON audit_chain_head (tenant_id)`

**后续变更**

- `DROP CONSTRAINT audit_chain_head_pkey（00007_multi_tenancy.sql）`
- `ADD COLUMN tenant_id uuid NOT NULL DEFAULT '00000000-0000-0000-0000-000000000000'（00007_multi_tenancy.sql）`
- `ALTER COLUMN tenant_id DROP DEFAULT（00007_multi_tenancy.sql）`
- `DROP COLUMN only_row（00007_multi_tenancy.sql）`
- `ADD PRIMARY KEY USING INDEX audit_chain_head_pkey（00007_multi_tenancy.sql）`

## departments

建表于 `00006_departments.sql`。

| 列 | 定义 |
|---|---|
| `id` | `uuid PRIMARY KEY` |
| `parent_id` | `uuid REFERENCES departments (id)` |
| `name` | `varchar(64) NOT NULL` |
| `code` | `varchar(64) NOT NULL` |
| `sort_order` | `integer NOT NULL DEFAULT 0` |
| `remark` | `text NOT NULL DEFAULT ''` |
| `status` | `varchar(16) NOT NULL DEFAULT 'active' CHECK (status IN ('active', 'disabled'))` |
| `created_at` | `timestamptz NOT NULL DEFAULT now()` |
| `updated_at` | `timestamptz NOT NULL DEFAULT now()` |
| `deleted_at` | `timestamptz` |
| `created_by` | `uuid` |
| `version` | `integer NOT NULL DEFAULT 0` |

**索引**

- `CREATE UNIQUE INDEX uk_departments_code ON departments (code) WHERE deleted_at IS NULL`
- `CREATE UNIQUE INDEX uk_departments_sibling_name ON departments (parent_id, name) WHERE deleted_at IS NULL AND parent_id IS NOT NULL`
- `CREATE UNIQUE INDEX uk_departments_root_name ON departments (name) WHERE deleted_at IS NULL AND parent_id IS NULL`
- `CREATE INDEX idx_departments_parent ON departments (parent_id) WHERE deleted_at IS NULL`
- `CREATE UNIQUE INDEX uk_departments_code ON departments (tenant_id, code) WHERE deleted_at IS NULL`
- `CREATE UNIQUE INDEX uk_departments_sibling_name ON departments (tenant_id, parent_id, name) WHERE deleted_at IS NULL AND parent_id IS NOT NULL`
- `CREATE UNIQUE INDEX uk_departments_root_name ON departments (tenant_id, name) WHERE deleted_at IS NULL AND parent_id IS NULL`
- `CREATE UNIQUE INDEX uk_departments_tenant_id ON departments (tenant_id, id)`
- `CREATE INDEX idx_departments_parent ON departments (tenant_id, parent_id) WHERE deleted_at IS NULL`

**后续变更**

- `ADD COLUMN tenant_id uuid（00007_multi_tenancy.sql）`
- `ALTER COLUMN tenant_id SET NOT NULL（00007_multi_tenancy.sql）`
- `ADD CONSTRAINT fk_departments_tenant FOREIGN KEY (tenant_id) REFERENCES tenants (id) NOT VALID（00007_multi_tenancy.sql）`
- `DROP CONSTRAINT departments_parent_id_fkey（00007_multi_tenancy.sql）`
- `ADD CONSTRAINT fk_departments_parent FOREIGN KEY (tenant_id, parent_id) REFERENCES departments (tenant_id, id) NOT VALID（00007_multi_tenancy.sql）`
- `VALIDATE CONSTRAINT fk_departments_tenant（00008_validate_tenant_constraints.sql）`
- `VALIDATE CONSTRAINT fk_departments_parent（00008_validate_tenant_constraints.sql）`

## tenants

建表于 `00007_multi_tenancy.sql`。

| 列 | 定义 |
|---|---|
| `id` | `uuid PRIMARY KEY` |
| `code` | `varchar(32) NOT NULL CHECK (code ~ '^[a-z0-9][a-z0-9-]{0,30}[a-z0-9]$')` |
| `CONSTRAINT` | `ck_tenants_code_reserved CHECK (code NOT IN ( 'platform', 'admin', 'api', 'www', 'app', 'static', 'assets', 'auth', 'login', 'logout', 'docs', 'health', 'healthz', 'status', 'system', 'root', 'internal', 'public', 'support', 'help', 'mail', 'test', 'dev', 'staging', 'fries' ))` |
| `name` | `varchar(64) NOT NULL` |
| `status` | `varchar(16) NOT NULL DEFAULT 'active' CHECK (status IN ('active', 'suspended'))` |
| `created_at` | `timestamptz NOT NULL DEFAULT now()` |
| `updated_at` | `timestamptz NOT NULL DEFAULT now()` |
| `created_by` | `uuid` |
| `version` | `integer NOT NULL DEFAULT 0` |

**索引**

- `CREATE UNIQUE INDEX uk_tenants_code ON tenants (lower(code))`
- `CREATE INDEX idx_tenants_status ON tenants (status)`

**后续变更**

- `ADD COLUMN user_count integer NOT NULL DEFAULT 0（00009_platform_admin.sql）`

## platform_settings

建表于 `00007_multi_tenancy.sql`。

| 列 | 定义 |
|---|---|
| `key` | `varchar(100) PRIMARY KEY` |
| `value` | `jsonb NOT NULL` |
| `description` | `text NOT NULL DEFAULT ''` |
| `updated_at` | `timestamptz NOT NULL DEFAULT now()` |
| `updated_by` | `uuid` |

## platform_admins

建表于 `00009_platform_admin.sql`。

| 列 | 定义 |
|---|---|
| `id` | `uuid PRIMARY KEY` |
| `username` | `varchar(64) NOT NULL` |
| `display_name` | `varchar(64) NOT NULL` |
| `password_hash` | `text NOT NULL` |
| `password_changed_at` | `timestamptz NOT NULL DEFAULT now()` |
| `must_change_password` | `boolean NOT NULL DEFAULT false` |
| `status` | `varchar(16) NOT NULL DEFAULT 'active' CHECK (status IN ('active', 'disabled'))` |
| `failed_attempts` | `integer NOT NULL DEFAULT 0` |
| `locked_until` | `timestamptz` |
| `last_login_at` | `timestamptz` |
| `created_at` | `timestamptz NOT NULL DEFAULT now()` |
| `updated_at` | `timestamptz NOT NULL DEFAULT now()` |
| `deleted_at` | `timestamptz` |
| `created_by` | `uuid` |
| `version` | `integer NOT NULL DEFAULT 0` |

**索引**

- `CREATE UNIQUE INDEX uk_platform_admins_username ON platform_admins (username) WHERE deleted_at IS NULL`

## platform_sessions

建表于 `00009_platform_admin.sql`。

| 列 | 定义 |
|---|---|
| `id` | `uuid PRIMARY KEY` |
| `token_hash` | `bytea NOT NULL` |
| `admin_id` | `uuid NOT NULL REFERENCES platform_admins (id) ON DELETE CASCADE` |
| `ip` | `inet` |
| `user_agent` | `text NOT NULL DEFAULT ''` |
| `expires_at` | `timestamptz NOT NULL` |
| `last_seen_at` | `timestamptz NOT NULL DEFAULT now()` |
| `revoked_at` | `timestamptz` |
| `created_at` | `timestamptz NOT NULL DEFAULT now()` |

**索引**

- `CREATE UNIQUE INDEX uk_platform_sessions_token ON platform_sessions (token_hash)`
- `CREATE INDEX idx_platform_sessions_admin ON platform_sessions (admin_id)`
- `CREATE INDEX idx_platform_sessions_expires ON platform_sessions (expires_at)`

## idempotency_keys

建表于 `00014_idempotency_keys.sql`。

| 列 | 定义 |
|---|---|
| `key` | `text PRIMARY KEY` |
| `expires_at` | `timestamptz NOT NULL` |

**索引**

- `CREATE INDEX idx_idempotency_keys_expires ON idempotency_keys (expires_at)`

## rate_limits

建表于 `00015_rate_limits.sql`。

| 列 | 定义 |
|---|---|
| `key` | `text NOT NULL` |
| `window_start` | `timestamptz NOT NULL` |
| `count` | `integer NOT NULL DEFAULT 0` |
| `PRIMARY` | `KEY (key, window_start)` |

**索引**

- `CREATE INDEX idx_rate_limits_window ON rate_limits (window_start)`

## password_reset_tokens

建表于 `00016_password_reset_tokens.sql`。

| 列 | 定义 |
|---|---|
| `id` | `uuid PRIMARY KEY` |
| `tenant_id` | `uuid NOT NULL` |
| `user_id` | `uuid NOT NULL` |
| `token_hash` | `bytea NOT NULL` |
| `expires_at` | `timestamptz NOT NULL` |
| `used_at` | `timestamptz` |
| `created_at` | `timestamptz NOT NULL DEFAULT now()` |
| `FOREIGN` | `KEY (tenant_id, user_id) REFERENCES users (tenant_id, id) ON DELETE CASCADE` |

**索引**

- `CREATE UNIQUE INDEX uk_password_reset_tokens_hash ON password_reset_tokens (token_hash)`
- `CREATE INDEX idx_password_reset_tokens_expires ON password_reset_tokens (expires_at)`
- `CREATE INDEX idx_password_reset_tokens_user ON password_reset_tokens (tenant_id, user_id)`

## pending_registrations

建表于 `00017_pending_registrations.sql`。

| 列 | 定义 |
|---|---|
| `id` | `uuid PRIMARY KEY` |
| `email` | `varchar(255) NOT NULL` |
| `company_name` | `varchar(100) NOT NULL` |
| `desired_code` | `varchar(32) NOT NULL` |
| `admin_password_hash` | `text NOT NULL` |
| `token_hash` | `bytea NOT NULL` |
| `expires_at` | `timestamptz NOT NULL` |
| `created_at` | `timestamptz NOT NULL DEFAULT now()` |

**索引**

- `CREATE UNIQUE INDEX uk_pending_registrations_token ON pending_registrations (token_hash)`
- `CREATE INDEX idx_pending_registrations_expires ON pending_registrations (expires_at)`
- `CREATE INDEX idx_pending_registrations_email ON pending_registrations (lower(email))`

## suppliers

建表于 `00018_create_suppliers.sql`。

| 列 | 定义 |
|---|---|
| `id` | `uuid PRIMARY KEY` |
| `tenant_id` | `uuid NOT NULL REFERENCES tenants (id)` |
| `name` | `varchar(100) NOT NULL` |
| `status` | `varchar(32) DEFAULT 'active' CHECK (status IN ('active', 'terminated'))` |
| `credit` | `numeric(18, 2)` |
| `started_at` | `date` |
| `remark` | `text` |
| `created_at` | `timestamptz NOT NULL DEFAULT now()` |
| `updated_at` | `timestamptz NOT NULL DEFAULT now()` |
| `deleted_at` | `timestamptz` |
| `created_by` | `uuid` |
| `version` | `integer NOT NULL DEFAULT 0` |

**索引**

- `CREATE UNIQUE INDEX uk_suppliers_tenant_id ON suppliers (tenant_id, id)`
- `CREATE UNIQUE INDEX uk_suppliers_name ON suppliers (tenant_id, name) WHERE deleted_at IS NULL`
- `CREATE INDEX idx_suppliers_status ON suppliers (tenant_id, status)`
- `CREATE INDEX idx_suppliers_started_at ON suppliers (tenant_id, started_at)`
- `CREATE INDEX idx_suppliers_name_trgm ON suppliers USING gin (name gin_trgm_ops)`
