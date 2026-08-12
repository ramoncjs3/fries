-- 多租户地基（MULTI-TENANCY.md 第 ① 步）。
--
-- 隔离方式是「共享表 + tenant_id，应用层强制」，**不用 RLS**（§1.1）。
-- 也就是说：数据库这一层只负责**约束**（NOT NULL、复合唯一、复合外键），
-- 「查询有没有带租户条件」由第 ② 步的 ForTenant 包装和 SQL 静态检查负责。
-- 这里每加一条约束，都是在给那套应用层强制兜底 —— 数据库拦不住漏写条件的 SELECT，
-- 但拦得住写进去的脏数据。
--
-- 迁移按 §7.5 的三步走，即使本地库是空的也照写 ——
-- 存量库上「先 NOT NULL 再回填」是直接失败的，这个顺序要留在文件里给将来看。
--
-- ⚠️ 新增的约束一律 `NOT VALID`，校验存量行放在 00008 里单独一个事务
-- （NOT VALID 和 VALIDATE 挤在同一个事务里等于没拆，squawk 会报）。

-- +goose Up

-- 超时设置，理由见 00001_init.sql。
SET LOCAL lock_timeout = '5s';
SET LOCAL statement_timeout = '60s';

-- ================================================================ 第一步：新表

-- ---------------------------------------------------------------- tenants

-- 租户（对用户一律叫「组织」，代码里一律叫 tenant，§10.11）。
-- 它自己**没有** tenant_id —— 它是平台级的表，和 sessions / service_accounts 一样
-- 属于「只能靠代码自觉」的那三张（§3.2 ③）。清单就这三张，加第四张要单独讨论。
CREATE TABLE tenants (
    id         uuid         PRIMARY KEY,

    -- code 是登录时要输入的「公司代码」。规则按 §9.1 定死，全部由 CHECK 强制：
    --   · 只允许小写字母、数字、中划线（不许下划线和点 —— 将来做子域名时它们是麻烦）
    --   · 首尾不能是中划线
    --   · 长度 2–32
    --   · 存小写。大小写不敏感是靠「只准存小写」+ 查询前 lower() 做到的，
    --     比只在查询侧 lower() 更稳（写入侧也堵死了）
    code       varchar(32)  NOT NULL
                            CHECK (code ~ '^[a-z0-9][a-z0-9-]{0,30}[a-z0-9]$'),

    -- 保留字。不拦的话，客户可以注册成 `platform`，将来做子域名就直接撞上平台端；
    -- `api` / `www` / `static` 同理。
    -- ⚠️ 这份清单在 Go 侧还有一份（给友好报错用），改这里要同步改那边。
    --    DB 这份是兜底：种子脚本、手工 INSERT 也绕不过去。
                            CONSTRAINT ck_tenants_code_reserved CHECK (code NOT IN (
                                'platform', 'admin', 'api', 'www', 'app', 'static',
                                'assets', 'auth', 'login', 'logout', 'docs', 'health',
                                'healthz', 'status', 'system', 'root', 'internal',
                                'public', 'support', 'help', 'mail', 'test', 'dev',
                                'staging', 'fries'
                            )),

    name       varchar(64)  NOT NULL,
    -- 只停用、不删除（§9.3）：审计链要完整，误删没法恢复，客户过两个月又回来也很常见。
    -- 所以这张表**没有** deleted_at。
    status     varchar(16)  NOT NULL DEFAULT 'active'
                            CHECK (status IN ('active', 'suspended')),

    created_at timestamptz  NOT NULL DEFAULT now(),
    updated_at timestamptz  NOT NULL DEFAULT now(),
    created_by uuid,
    version    integer      NOT NULL DEFAULT 0
);

-- 全平台唯一。索引建在 lower(code) 上，即使哪天 CHECK 放宽了也仍然大小写不敏感。
CREATE UNIQUE INDEX uk_tenants_code ON tenants (lower(code));
CREATE INDEX idx_tenants_status ON tenants (status);

CREATE TRIGGER trg_tenants_updated_at
    BEFORE UPDATE ON tenants
    FOR EACH ROW EXECUTE FUNCTION set_updated_at();

-- ---------------------------------------------------------------- platform_settings

-- 配置分两类，混在一起会乱（§7.2）：
--   · 平台级 —— 是否允许自助注册、全局限流阈值、**租户级设置的上下界**（§10.5）
--     → 这张表，只有平台管理员能改
--   · 租户级 —— 密码策略、会话超时 → settings，主键改成 (tenant_id, key)
--
-- 00002 塞进 settings 的七个默认值里，有两个其实是平台级的，下面会搬过来：
--   · audit.retention_days —— 审计按月分区，过期整分区 DROP，分区本来就是跨租户的，
--     没法「A 公司留 180 天、B 公司留 30 天」
--   · ui.system_name —— 产品名。客户自己的名字在 tenants.name 里，不是这个
--
-- 还会有但现在不塞的：租户密码最短长度的**下限**、会话时长的**上限**（§10.5）、
-- 是否允许自助注册（§5）—— 那些要等第 ④、⑤ 步做到那里才定得准，
-- 现在塞进来大概率要改名。
CREATE TABLE platform_settings (
    key         varchar(100) PRIMARY KEY,
    value       jsonb        NOT NULL,
    description text         NOT NULL DEFAULT '',
    updated_at  timestamptz  NOT NULL DEFAULT now(),
    updated_by  uuid
);

CREATE TRIGGER trg_platform_settings_updated_at
    BEFORE UPDATE ON platform_settings
    FOR EACH ROW EXECUTE FUNCTION set_updated_at();

-- +goose StatementBegin
CREATE OR REPLACE FUNCTION notify_platform_settings_changed() RETURNS trigger AS $$
BEGIN
    PERFORM pg_notify('platform_settings_changed', '');
    RETURN NULL;
END;
$$ LANGUAGE plpgsql;
-- +goose StatementEnd

CREATE TRIGGER trg_platform_settings_notify
    AFTER INSERT OR UPDATE OR DELETE ON platform_settings
    FOR EACH STATEMENT EXECUTE FUNCTION notify_platform_settings_changed();

-- 把 00002 里那两个平台级的默认值搬过来。
-- 搬而不是复制：留在 settings 里的话，每个租户都会看到一份改不动的配置项，
-- 改了也不生效 —— 那比不给改更糟。
INSERT INTO platform_settings (key, value, description)
SELECT key, value, description FROM settings
WHERE key IN ('audit.retention_days', 'ui.system_name');

DELETE FROM settings WHERE key IN ('audit.retention_days', 'ui.system_name');

-- ================================================================ 第二步：加列（可空）+ 回填

-- 先可空，回填完再收 NOT NULL。反过来做在有存量行的库上必然失败（§7.5）。
ALTER TABLE users            ADD COLUMN tenant_id uuid;
ALTER TABLE roles            ADD COLUMN tenant_id uuid;
ALTER TABLE role_permissions ADD COLUMN tenant_id uuid;
ALTER TABLE user_roles       ADD COLUMN tenant_id uuid;
ALTER TABLE sessions         ADD COLUMN tenant_id uuid;
ALTER TABLE service_accounts ADD COLUMN tenant_id uuid;
ALTER TABLE settings         ADD COLUMN tenant_id uuid;
ALTER TABLE departments      ADD COLUMN tenant_id uuid;

-- ⚠️ audit_logs 的 tenant_id **必须可空**（§7.1）：未认证请求（登录失败、健康检查、
-- 直接打接口）也要写审计，那些记录没有租户。NULL = 平台级 / 无租户事件。
-- 租户查审计时显式带 `tenant_id = ?`，NULL 的那些自然看不到；只有平台端查得到。
ALTER TABLE audit_logs       ADD COLUMN tenant_id uuid;

-- 迁移生成的默认租户，用来接住存量行。
--
-- ⚠️ code 是**随机**的（§10.9）：叫 `default` 的话，部署到公网就是一个
-- 任何人都能猜到公司代码的真实租户。开发期的产物不能变成生产上的入口。
-- id 是固定的哨兵值 —— make dev-admin 要靠它找到本地这个租户。
INSERT INTO tenants (id, code, name)
VALUES (
    '01920000-0000-7000-8000-0000000000ff',
    -- gen_random_uuid() 是 PG13+ 内置的，不用装 pgcrypto
    't' || substr(replace(gen_random_uuid()::text, '-', ''), 1, 10),
    '默认组织'
);

UPDATE users            SET tenant_id = '01920000-0000-7000-8000-0000000000ff' WHERE tenant_id IS NULL;
UPDATE roles            SET tenant_id = '01920000-0000-7000-8000-0000000000ff' WHERE tenant_id IS NULL;
UPDATE role_permissions SET tenant_id = '01920000-0000-7000-8000-0000000000ff' WHERE tenant_id IS NULL;
UPDATE user_roles       SET tenant_id = '01920000-0000-7000-8000-0000000000ff' WHERE tenant_id IS NULL;
UPDATE sessions         SET tenant_id = '01920000-0000-7000-8000-0000000000ff' WHERE tenant_id IS NULL;
UPDATE service_accounts SET tenant_id = '01920000-0000-7000-8000-0000000000ff' WHERE tenant_id IS NULL;
UPDATE settings         SET tenant_id = '01920000-0000-7000-8000-0000000000ff' WHERE tenant_id IS NULL;
UPDATE departments      SET tenant_id = '01920000-0000-7000-8000-0000000000ff' WHERE tenant_id IS NULL;
-- audit_logs 不回填：迁移之前的审计确实不属于任何租户，NULL 是对的。

-- ================================================================ 第三步：收紧约束

-- squawk 会提醒 SET NOT NULL 要拿 ACCESS EXCLUSIVE 锁扫全表，读写全挡 —— 它是对的。
-- 存量库上的标准做法是先 `CHECK (tenant_id IS NOT NULL) NOT VALID` → 单独事务 VALIDATE
-- → 再 SET NOT NULL（这时 PG 能用已校验的 CHECK 跳过扫描），拆成三个迁移。
--
-- 这里**明知故犯**：现在只有本地库，八张表全是空的，扫描是 0 行，拆三段只会让这个
-- 已经很长的迁移更难读。⚠️ 真到了有存量数据的库上，必须按上面那三段拆开重写。
-- squawk-ignore adding-not-nullable-field
ALTER TABLE users            ALTER COLUMN tenant_id SET NOT NULL;
-- squawk-ignore adding-not-nullable-field
ALTER TABLE roles            ALTER COLUMN tenant_id SET NOT NULL;
-- squawk-ignore adding-not-nullable-field
ALTER TABLE role_permissions ALTER COLUMN tenant_id SET NOT NULL;
-- squawk-ignore adding-not-nullable-field
ALTER TABLE user_roles       ALTER COLUMN tenant_id SET NOT NULL;
-- squawk-ignore adding-not-nullable-field
ALTER TABLE sessions         ALTER COLUMN tenant_id SET NOT NULL;
-- squawk-ignore adding-not-nullable-field
ALTER TABLE service_accounts ALTER COLUMN tenant_id SET NOT NULL;
-- squawk-ignore adding-not-nullable-field
ALTER TABLE settings         ALTER COLUMN tenant_id SET NOT NULL;
-- squawk-ignore adding-not-nullable-field
ALTER TABLE departments      ALTER COLUMN tenant_id SET NOT NULL;

-- ---------------------------------------------------------------- tenant_id → tenants

-- 每张表的 tenant_id 都要指得回去，不然可以写进一个根本不存在的租户 ——
-- 那种行谁都查不到，只能靠对账发现。
-- audit_logs 不加：它的 tenant_id 可空，而且分区表上的外键会给每次审计写入加成本，
-- 审计是热路径。
ALTER TABLE users            ADD CONSTRAINT fk_users_tenant            FOREIGN KEY (tenant_id) REFERENCES tenants (id) NOT VALID;
ALTER TABLE roles            ADD CONSTRAINT fk_roles_tenant            FOREIGN KEY (tenant_id) REFERENCES tenants (id) NOT VALID;
ALTER TABLE role_permissions ADD CONSTRAINT fk_role_permissions_tenant FOREIGN KEY (tenant_id) REFERENCES tenants (id) NOT VALID;
ALTER TABLE user_roles       ADD CONSTRAINT fk_user_roles_tenant       FOREIGN KEY (tenant_id) REFERENCES tenants (id) NOT VALID;
ALTER TABLE sessions         ADD CONSTRAINT fk_sessions_tenant         FOREIGN KEY (tenant_id) REFERENCES tenants (id) NOT VALID;
ALTER TABLE service_accounts ADD CONSTRAINT fk_service_accounts_tenant FOREIGN KEY (tenant_id) REFERENCES tenants (id) NOT VALID;
ALTER TABLE settings         ADD CONSTRAINT fk_settings_tenant         FOREIGN KEY (tenant_id) REFERENCES tenants (id) NOT VALID;
ALTER TABLE departments      ADD CONSTRAINT fk_departments_tenant      FOREIGN KEY (tenant_id) REFERENCES tenants (id) NOT VALID;

-- ================================================================ 唯一索引：租户内唯一

-- 两家公司都想要一个叫 admin 的账号，所以唯一约束基本都要改成租户内唯一（§2.2）。
-- **但不是全部** —— 认证用的那两条必须保持全平台唯一，见文件末尾。
-- 索引一律 tenant_id 打头（§8.4）：租户一多，不带前缀的索引每次都要先扫出
-- 一大堆别人的行再过滤。

DROP INDEX uk_users_username;
CREATE UNIQUE INDEX uk_users_username ON users (tenant_id, username) WHERE deleted_at IS NULL;

-- 别漏了 lower()：邮箱大小写不敏感是靠函数索引做到的，查询也必须用 lower()。
DROP INDEX uk_users_email;
CREATE UNIQUE INDEX uk_users_email ON users (tenant_id, lower(email))
    WHERE deleted_at IS NULL AND email IS NOT NULL;

DROP INDEX uk_users_phone;
CREATE UNIQUE INDEX uk_users_phone ON users (tenant_id, phone)
    WHERE deleted_at IS NULL AND phone IS NOT NULL;

DROP INDEX uk_roles_key;
CREATE UNIQUE INDEX uk_roles_key ON roles (tenant_id, key) WHERE deleted_at IS NULL;

DROP INDEX uk_departments_code;
CREATE UNIQUE INDEX uk_departments_code ON departments (tenant_id, code) WHERE deleted_at IS NULL;

DROP INDEX uk_departments_sibling_name;
CREATE UNIQUE INDEX uk_departments_sibling_name ON departments (tenant_id, parent_id, name)
    WHERE deleted_at IS NULL AND parent_id IS NOT NULL;

-- 根节点的 parent_id 是 NULL，NULL 在唯一索引里互不相等，所以单独一条。
DROP INDEX uk_departments_root_name;
CREATE UNIQUE INDEX uk_departments_root_name ON departments (tenant_id, name)
    WHERE deleted_at IS NULL AND parent_id IS NULL;

DROP INDEX uk_service_accounts_name;
CREATE UNIQUE INDEX uk_service_accounts_name ON service_accounts (tenant_id, name)
    WHERE deleted_at IS NULL;

-- ================================================================ 复合外键（§2.2.1）

-- 被引用的三张表要有 (tenant_id, id) 唯一索引，复合外键才指得过去。
-- ⚠️ 必须是**非部分**索引 —— PostgreSQL 不接受部分唯一索引作为外键目标。
CREATE UNIQUE INDEX uk_users_tenant_id       ON users       (tenant_id, id);
CREATE UNIQUE INDEX uk_roles_tenant_id       ON roles       (tenant_id, id);
CREATE UNIQUE INDEX uk_departments_tenant_id ON departments (tenant_id, id);

-- 原来的单列外键全部换成带 tenant_id 的复合外键。数据库这一层杜绝跨租户引用：
-- 把部门挂到别家公司的部门下面、把人分到别家的部门里、给自己的人绑别家的角色 ——
-- 这类脏数据一旦写进去，页面上表现成「部门凭空消失」，排查时极难联想到是跨租户。
--
-- ⚠️ §2.2.1 的表里写「六条」，实际数得出来是**七条**（user_roles 那一行是两条外键：
--    user_id 和 role_id）。这里按七条实现，一条不少。
--
-- ⚠️ sessions 那条尤其**不是洁癖**（§10.2）：租户本来可以从 users 推出来，
--    在 sessions 上再存一份就有了两份可能漂移的安全关键数据，而整套隔离都以
--    「会话里的租户」为唯一来源。这条复合外键就是让数据库保证两份永远一致。
--    谁要是觉得它多余想砍掉，那个洞就开了。

ALTER TABLE departments DROP CONSTRAINT departments_parent_id_fkey;
ALTER TABLE departments ADD CONSTRAINT fk_departments_parent
    FOREIGN KEY (tenant_id, parent_id) REFERENCES departments (tenant_id, id) NOT VALID;

ALTER TABLE users DROP CONSTRAINT fk_users_department;
ALTER TABLE users ADD CONSTRAINT fk_users_department
    FOREIGN KEY (tenant_id, department_id) REFERENCES departments (tenant_id, id) NOT VALID;

ALTER TABLE user_roles DROP CONSTRAINT user_roles_user_id_fkey;
ALTER TABLE user_roles ADD CONSTRAINT fk_user_roles_user
    FOREIGN KEY (tenant_id, user_id) REFERENCES users (tenant_id, id) ON DELETE CASCADE NOT VALID;

ALTER TABLE user_roles DROP CONSTRAINT user_roles_role_id_fkey;
ALTER TABLE user_roles ADD CONSTRAINT fk_user_roles_role
    FOREIGN KEY (tenant_id, role_id) REFERENCES roles (tenant_id, id) ON DELETE CASCADE NOT VALID;

ALTER TABLE role_permissions DROP CONSTRAINT role_permissions_role_id_fkey;
ALTER TABLE role_permissions ADD CONSTRAINT fk_role_permissions_role
    FOREIGN KEY (tenant_id, role_id) REFERENCES roles (tenant_id, id) ON DELETE CASCADE NOT VALID;

ALTER TABLE sessions DROP CONSTRAINT sessions_user_id_fkey;
ALTER TABLE sessions ADD CONSTRAINT fk_sessions_user
    FOREIGN KEY (tenant_id, user_id) REFERENCES users (tenant_id, id) ON DELETE CASCADE NOT VALID;

ALTER TABLE service_accounts DROP CONSTRAINT service_accounts_role_id_fkey;
ALTER TABLE service_accounts ADD CONSTRAINT fk_service_accounts_role
    FOREIGN KEY (tenant_id, role_id) REFERENCES roles (tenant_id, id) NOT VALID;

-- ================================================================ 主键与普通索引

-- 关联表的主键也 tenant_id 打头：既符合 §8.4，又顺手给上面那几条复合外键
-- 提供了引用侧的索引（外键的引用侧 PostgreSQL 不会自动建索引，级联删除会全表扫）。
--
-- 一律先建唯一索引、再 `ADD PRIMARY KEY USING INDEX` 把它提升成主键：
-- 直接 `ADD PRIMARY KEY (...)` 会在 ACCESS EXCLUSIVE 锁下现建索引，读写全挡；
-- 拆成两步之后，建索引只挡写，提升那一下才是瞬时的排他锁。
-- 索引名沿用原来的 `<表>_pkey`，约束名不变 —— 唯一冲突的报错翻译认的是这个名字（§8.3）。
ALTER TABLE user_roles       DROP CONSTRAINT user_roles_pkey;
CREATE UNIQUE INDEX user_roles_pkey ON user_roles (tenant_id, user_id, role_id);
ALTER TABLE user_roles       ADD PRIMARY KEY USING INDEX user_roles_pkey;

ALTER TABLE role_permissions DROP CONSTRAINT role_permissions_pkey;
CREATE UNIQUE INDEX role_permissions_pkey ON role_permissions (tenant_id, role_id, resource, action);
ALTER TABLE role_permissions ADD PRIMARY KEY USING INDEX role_permissions_pkey;

-- settings 从「按 key 取值」变成「按 (租户, key) 取值」。
ALTER TABLE settings DROP CONSTRAINT settings_pkey;
CREATE UNIQUE INDEX settings_pkey ON settings (tenant_id, key);
ALTER TABLE settings ADD PRIMARY KEY USING INDEX settings_pkey;

-- 列表查询用到的索引全部补上 tenant_id 前缀（§8.4）。
DROP INDEX idx_users_status;
CREATE INDEX idx_users_status ON users (tenant_id, status) WHERE deleted_at IS NULL;

DROP INDEX idx_users_department;
CREATE INDEX idx_users_department ON users (tenant_id, department_id) WHERE deleted_at IS NULL;

DROP INDEX idx_departments_parent;
CREATE INDEX idx_departments_parent ON departments (tenant_id, parent_id) WHERE deleted_at IS NULL;

-- user_roles 的新主键已经覆盖了 (tenant_id, user_id)，这条留给「哪些人有这个角色」。
DROP INDEX idx_user_roles_role;
CREATE INDEX idx_user_roles_role ON user_roles (tenant_id, role_id);

DROP INDEX idx_sessions_user;
CREATE INDEX idx_sessions_user ON sessions (tenant_id, user_id);

-- idx_sessions_expires 保持不带租户：清理过期会话的后台任务是跨租户的一条扫描。
-- idx_audit_logs_request 同理：按 request_id 追一次请求时还不知道租户是谁。

DROP INDEX idx_audit_logs_occurred;
CREATE INDEX idx_audit_logs_occurred ON audit_logs (tenant_id, occurred_at DESC);

DROP INDEX idx_audit_logs_actor;
CREATE INDEX idx_audit_logs_actor ON audit_logs (tenant_id, actor_id, occurred_at DESC);

DROP INDEX idx_audit_logs_resource;
CREATE INDEX idx_audit_logs_resource ON audit_logs (tenant_id, resource, resource_id, occurred_at DESC);

-- ================================================================ 审计哈希链按租户

-- 共用一条链的话，验证 A 租户的完整性要读到 B 租户的记录，既慢又不该（§2.4）。
-- 改成每租户一行。
--
-- ⚠️ tenant_id 可空，而 NULL 做不了主键（§10.3）。所以平台级 / 未认证事件
-- 用一个**全零哨兵 UUID** 归到自己那条链上。
-- 现有的那一行（多租户之前的审计，tenant_id 全是 NULL）正好就是这条哨兵链的链头，
-- 直接改造它，链不断。
ALTER TABLE audit_chain_head DROP CONSTRAINT audit_chain_head_pkey;
ALTER TABLE audit_chain_head ADD COLUMN tenant_id uuid
    NOT NULL DEFAULT '00000000-0000-0000-0000-000000000000';
ALTER TABLE audit_chain_head ALTER COLUMN tenant_id DROP DEFAULT;
-- only_row 是「保证只有一行」的那套单行约束，按租户分链之后它就没有意义了。
-- squawk 提醒「删列会让还在读它的客户端挂掉」—— 这张表只有触发器读，
-- 触发器在同一个迁移里一起换掉了。
-- squawk-ignore ban-drop-column
ALTER TABLE audit_chain_head DROP COLUMN only_row;
CREATE UNIQUE INDEX audit_chain_head_pkey ON audit_chain_head (tenant_id);
ALTER TABLE audit_chain_head ADD PRIMARY KEY USING INDEX audit_chain_head_pkey;

-- audit_chain_head.tenant_id 不加外键指向 tenants：哨兵那一行没有对应的租户。

-- 触发器改成按 NEW.tenant_id 取链头。
--
-- ⚠️ 这里**只能**用 NEW.tenant_id，不能用任何「当前租户」的概念（§2.4）：
-- 审计行是触发器代写的，它唯一可信的租户来源就是那一行自己带的值。
--
-- SECURITY DEFINER 保留：应用账号没有 audit_chain_head 的写权限也能插审计。
-- +goose StatementBegin
CREATE OR REPLACE FUNCTION audit_chain() RETURNS trigger AS $$
DECLARE
    -- 平台级 / 未认证事件（tenant_id IS NULL）归到全零哨兵链
    chain_key uuid := coalesce(NEW.tenant_id, '00000000-0000-0000-0000-000000000000'::uuid);
    prev      bytea;
BEGIN
    -- 新租户的第一条审计还没有链头行，先补一条空的。
    -- 并发时后到的那个会阻塞到先到的提交，然后什么都不做 —— 正是想要的。
    INSERT INTO audit_chain_head (tenant_id, hash) VALUES (chain_key, NULL)
        ON CONFLICT (tenant_id) DO NOTHING;

    -- FOR UPDATE 把同一条链上的并发插入排成队，链才不会分叉。
    -- 不同租户锁的是不同的行，互不阻塞 —— 这是按租户分链顺带拿到的好处。
    SELECT hash INTO prev FROM audit_chain_head WHERE tenant_id = chain_key FOR UPDATE;

    NEW.prev_hash := prev;
    NEW.hash := sha256(
        coalesce(prev, ''::bytea) ||
        convert_to(
            NEW.id::text || '|' ||
            to_char(NEW.occurred_at AT TIME ZONE 'UTC', 'YYYY-MM-DD"T"HH24:MI:SS.US') || '|' ||
            -- 租户进哈希：不然把一行从 A 的链搬到 B 的链，两边都验得过
            coalesce(NEW.tenant_id::text, '') || '|' ||
            NEW.actor_type || '|' || coalesce(NEW.actor_id::text, '') || '|' ||
            NEW.resource || '|' || NEW.action || '|' ||
            coalesce(NEW.resource_id::text, '') || '|' ||
            NEW.http_status::text || '|' || NEW.detail::text,
            'UTF8')
    );

    UPDATE audit_chain_head SET hash = NEW.hash WHERE tenant_id = chain_key;
    RETURN NEW;
END;
$$ LANGUAGE plpgsql SECURITY DEFINER;
-- +goose StatementEnd

-- ================================================================ settings 的 NOTIFY 带租户

-- 主键从 key 变成 (tenant_id, key) 之后，通知里必须带上租户（§9.4），
-- 否则一个租户改配置会让所有实例把**所有租户**的缓存全刷一遍。
--
-- 触发器从 STATEMENT 级改成 ROW 级 —— 语句级拿不到 NEW/OLD，也就拿不到 tenant_id。
-- 配置是一次改一个 key，行级的额外开销可以忽略。
-- +goose StatementBegin
CREATE OR REPLACE FUNCTION notify_settings_changed() RETURNS trigger AS $$
BEGIN
    PERFORM pg_notify('settings_changed', coalesce(NEW.tenant_id, OLD.tenant_id)::text);
    RETURN NULL;
END;
$$ LANGUAGE plpgsql;
-- +goose StatementEnd

DROP TRIGGER trg_settings_notify ON settings;
CREATE TRIGGER trg_settings_notify
    AFTER INSERT OR UPDATE OR DELETE ON settings
    FOR EACH ROW EXECUTE FUNCTION notify_settings_changed();

-- ================================================================ 受限角色授权

-- 00006 设过 DEFAULT PRIVILEGES，新表理论上会自动带上权限；这里显式再给一次，
-- 免得部署时 owner 和建表者不是同一个角色导致漏授（生产上表现成 permission denied）。
-- +goose StatementBegin
DO $$
BEGIN
    IF NOT EXISTS (SELECT FROM pg_roles WHERE rolname = 'fries_app') THEN
        RAISE NOTICE '没有 fries_app 角色，跳过多租户表权限授予（本地开发正常，生产必须建）';
        RETURN;
    END IF;

    EXECUTE 'GRANT SELECT, INSERT, UPDATE, DELETE ON tenants TO fries_app';
    EXECUTE 'GRANT SELECT, INSERT, UPDATE, DELETE ON platform_settings TO fries_app';
END $$;
-- +goose StatementEnd

-- ================================================================ 没有改的两条唯一索引

-- ⚠️ uk_sessions_token 和 uk_service_accounts_prefix **保持全平台唯一，不加 tenant_id**。
--
-- 改了认证就废了：验证会话是「拿 cookie 里的 token 去查这是谁」，那一刻还不知道
-- 租户是谁；API Key 同理。这两个值必须能在**不知道租户的前提下**唯一定位到一行，
-- 查出来的那一行再告诉我们 tenant_id（§2.2、§3.2 ③）。
--
-- 这也是为什么 sessions / service_accounts / tenants 这三张表进了
-- 「只能靠代码自觉」的豁免清单 —— 认证发生在租户上下文建立之前。
-- **清单就这三张，加第四张要单独讨论。**

-- +goose Down

SET LOCAL lock_timeout = '5s';
SET LOCAL statement_timeout = '60s';

-- 回滚只在开发期有意义：真上了生产之后这条路是单向的（§15）。
DROP TRIGGER IF EXISTS trg_settings_notify ON settings;
-- +goose StatementBegin
CREATE OR REPLACE FUNCTION notify_settings_changed() RETURNS trigger AS $$
BEGIN
    PERFORM pg_notify('settings_changed', '');
    RETURN NULL;
END;
$$ LANGUAGE plpgsql;
-- +goose StatementEnd
CREATE TRIGGER trg_settings_notify
    AFTER INSERT OR UPDATE OR DELETE ON settings
    FOR EACH STATEMENT EXECUTE FUNCTION notify_settings_changed();

-- +goose StatementBegin
CREATE OR REPLACE FUNCTION audit_chain() RETURNS trigger AS $$
DECLARE
    prev bytea;
BEGIN
    SELECT hash INTO prev FROM audit_chain_head WHERE only_row FOR UPDATE;
    NEW.prev_hash := prev;
    NEW.hash := sha256(
        coalesce(prev, ''::bytea) ||
        convert_to(
            NEW.id::text || '|' ||
            to_char(NEW.occurred_at AT TIME ZONE 'UTC', 'YYYY-MM-DD"T"HH24:MI:SS.US') || '|' ||
            NEW.actor_type || '|' || coalesce(NEW.actor_id::text, '') || '|' ||
            NEW.resource || '|' || NEW.action || '|' ||
            coalesce(NEW.resource_id::text, '') || '|' ||
            NEW.http_status::text || '|' || NEW.detail::text,
            'UTF8')
    );
    UPDATE audit_chain_head SET hash = NEW.hash WHERE only_row;
    RETURN NEW;
END;
$$ LANGUAGE plpgsql SECURITY DEFINER;
-- +goose StatementEnd

DELETE FROM audit_chain_head WHERE tenant_id <> '00000000-0000-0000-0000-000000000000';
ALTER TABLE audit_chain_head DROP CONSTRAINT audit_chain_head_pkey;
ALTER TABLE audit_chain_head ADD COLUMN only_row boolean NOT NULL DEFAULT true CHECK (only_row);
ALTER TABLE audit_chain_head DROP COLUMN tenant_id;
ALTER TABLE audit_chain_head ADD PRIMARY KEY (only_row);

DROP INDEX idx_audit_logs_resource;
CREATE INDEX idx_audit_logs_resource ON audit_logs (resource, resource_id, occurred_at DESC);
DROP INDEX idx_audit_logs_actor;
CREATE INDEX idx_audit_logs_actor ON audit_logs (actor_id, occurred_at DESC);
DROP INDEX idx_audit_logs_occurred;
CREATE INDEX idx_audit_logs_occurred ON audit_logs (occurred_at DESC);
DROP INDEX idx_sessions_user;
CREATE INDEX idx_sessions_user ON sessions (user_id);
DROP INDEX idx_user_roles_role;
CREATE INDEX idx_user_roles_role ON user_roles (role_id);
DROP INDEX idx_departments_parent;
CREATE INDEX idx_departments_parent ON departments (parent_id) WHERE deleted_at IS NULL;
DROP INDEX idx_users_department;
CREATE INDEX idx_users_department ON users (department_id) WHERE deleted_at IS NULL;
DROP INDEX idx_users_status;
CREATE INDEX idx_users_status ON users (status) WHERE deleted_at IS NULL;

ALTER TABLE settings DROP CONSTRAINT settings_pkey;
ALTER TABLE settings ADD PRIMARY KEY (key);
ALTER TABLE role_permissions DROP CONSTRAINT role_permissions_pkey;
ALTER TABLE role_permissions ADD PRIMARY KEY (role_id, resource, action);
ALTER TABLE user_roles DROP CONSTRAINT user_roles_pkey;
ALTER TABLE user_roles ADD PRIMARY KEY (user_id, role_id);

ALTER TABLE service_accounts DROP CONSTRAINT fk_service_accounts_role;
ALTER TABLE service_accounts ADD CONSTRAINT service_accounts_role_id_fkey
    FOREIGN KEY (role_id) REFERENCES roles (id);
ALTER TABLE sessions DROP CONSTRAINT fk_sessions_user;
ALTER TABLE sessions ADD CONSTRAINT sessions_user_id_fkey
    FOREIGN KEY (user_id) REFERENCES users (id) ON DELETE CASCADE;
ALTER TABLE role_permissions DROP CONSTRAINT fk_role_permissions_role;
ALTER TABLE role_permissions ADD CONSTRAINT role_permissions_role_id_fkey
    FOREIGN KEY (role_id) REFERENCES roles (id) ON DELETE CASCADE;
ALTER TABLE user_roles DROP CONSTRAINT fk_user_roles_role;
ALTER TABLE user_roles ADD CONSTRAINT user_roles_role_id_fkey
    FOREIGN KEY (role_id) REFERENCES roles (id) ON DELETE CASCADE;
ALTER TABLE user_roles DROP CONSTRAINT fk_user_roles_user;
ALTER TABLE user_roles ADD CONSTRAINT user_roles_user_id_fkey
    FOREIGN KEY (user_id) REFERENCES users (id) ON DELETE CASCADE;
ALTER TABLE users DROP CONSTRAINT fk_users_department;
ALTER TABLE users ADD CONSTRAINT fk_users_department
    FOREIGN KEY (department_id) REFERENCES departments (id) NOT VALID;
ALTER TABLE departments DROP CONSTRAINT fk_departments_parent;
ALTER TABLE departments ADD CONSTRAINT departments_parent_id_fkey
    FOREIGN KEY (parent_id) REFERENCES departments (id);

DROP INDEX uk_departments_tenant_id;
DROP INDEX uk_roles_tenant_id;
DROP INDEX uk_users_tenant_id;

DROP INDEX uk_service_accounts_name;
CREATE UNIQUE INDEX uk_service_accounts_name ON service_accounts (name) WHERE deleted_at IS NULL;
DROP INDEX uk_departments_root_name;
CREATE UNIQUE INDEX uk_departments_root_name ON departments (name)
    WHERE deleted_at IS NULL AND parent_id IS NULL;
DROP INDEX uk_departments_sibling_name;
CREATE UNIQUE INDEX uk_departments_sibling_name ON departments (parent_id, name)
    WHERE deleted_at IS NULL AND parent_id IS NOT NULL;
DROP INDEX uk_departments_code;
CREATE UNIQUE INDEX uk_departments_code ON departments (code) WHERE deleted_at IS NULL;
DROP INDEX uk_roles_key;
CREATE UNIQUE INDEX uk_roles_key ON roles (key) WHERE deleted_at IS NULL;
DROP INDEX uk_users_phone;
CREATE UNIQUE INDEX uk_users_phone ON users (phone) WHERE deleted_at IS NULL AND phone IS NOT NULL;
DROP INDEX uk_users_email;
CREATE UNIQUE INDEX uk_users_email ON users (lower(email))
    WHERE deleted_at IS NULL AND email IS NOT NULL;
DROP INDEX uk_users_username;
CREATE UNIQUE INDEX uk_users_username ON users (username) WHERE deleted_at IS NULL;

ALTER TABLE audit_logs       DROP COLUMN tenant_id;
ALTER TABLE departments      DROP COLUMN tenant_id;
ALTER TABLE settings         DROP COLUMN tenant_id;
ALTER TABLE service_accounts DROP COLUMN tenant_id;
ALTER TABLE sessions         DROP COLUMN tenant_id;
ALTER TABLE user_roles       DROP COLUMN tenant_id;
ALTER TABLE role_permissions DROP COLUMN tenant_id;
ALTER TABLE roles            DROP COLUMN tenant_id;
ALTER TABLE users            DROP COLUMN tenant_id;

-- 把搬走的那两个平台级配置放回 settings（此时 settings 已经没有 tenant_id 了）
INSERT INTO settings (key, value, description)
SELECT key, value, description FROM platform_settings
WHERE key IN ('audit.retention_days', 'ui.system_name')
ON CONFLICT (key) DO NOTHING;

DROP TABLE platform_settings;
DROP FUNCTION IF EXISTS notify_platform_settings_changed();
DROP TABLE tenants;
