-- 部门：组织结构。
--
-- 注意它**不是**数据范围的维度 —— data_scope 仍然只有 all / self（DECISIONS.md §3.3）。
-- 部门现在的用途是「这个人属于哪个组」，将来真要做 dept 范围是加一档枚举 + 加一个
-- owner_dept 过滤，不用重建表。
--
-- +goose Up

-- 超时设置，理由见 00001_init.sql。
SET LOCAL lock_timeout = '5s';
SET LOCAL statement_timeout = '60s';

CREATE TABLE departments (
    id         uuid         PRIMARY KEY,
    -- 自引用外键做树。层级不深（内部系统撑死四五层），递归 CTE 够用，
    -- 不上 ltree/闭包表 —— 那两个的维护成本在这个数据量下不划算。
    parent_id  uuid         REFERENCES departments (id),
    name       varchar(64)  NOT NULL,
    -- code 给对接外部系统用（工资系统、OA 里的部门编号）
    code       varchar(64)  NOT NULL,
    -- 同级排序，小的在前
    sort_order integer      NOT NULL DEFAULT 0,
    remark     text         NOT NULL DEFAULT '',
    status     varchar(16)  NOT NULL DEFAULT 'active'
                            CHECK (status IN ('active', 'disabled')),

    created_at timestamptz  NOT NULL DEFAULT now(),
    updated_at timestamptz  NOT NULL DEFAULT now(),
    deleted_at timestamptz,
    created_by uuid,
    version    integer      NOT NULL DEFAULT 0
);

-- 软删除会让删掉的 code 继续占位，所以用部分唯一索引（DECISIONS.md §2.3）
CREATE UNIQUE INDEX uk_departments_code ON departments (code) WHERE deleted_at IS NULL;
-- 同一个父节点下不许重名。根节点的 parent_id 是 NULL，
-- NULL 在唯一索引里互不相等，所以根节点这条得单独建一个索引。
CREATE UNIQUE INDEX uk_departments_sibling_name
    ON departments (parent_id, name) WHERE deleted_at IS NULL AND parent_id IS NOT NULL;
CREATE UNIQUE INDEX uk_departments_root_name
    ON departments (name) WHERE deleted_at IS NULL AND parent_id IS NULL;
CREATE INDEX idx_departments_parent ON departments (parent_id) WHERE deleted_at IS NULL;

CREATE TRIGGER trg_departments_updated_at
    BEFORE UPDATE ON departments
    FOR EACH ROW EXECUTE FUNCTION set_updated_at();

-- 人属于哪个部门。ON DELETE 不设级联：部门下还有人就不该删得掉，
-- 这条规则在 service 层显式检查并给出人话报错，比外键报 23503 好。
-- 外键写成 NOT VALID：加约束时不扫全表，也就不用拿着 SHARE ROW EXCLUSIVE 锁等 users 扫完。
-- 这里不跟一句 VALIDATE —— 列是刚加的，存量行全是 NULL，没有要校验的东西；
-- 而且 VALIDATE 和 NOT VALID 挤在同一个事务里等于白写（squawk 会报）。
-- ⚠️ 这条外键在 00007 会被换成带 tenant_id 的复合外键，别在这里加东西。
ALTER TABLE users ADD COLUMN department_id uuid;
ALTER TABLE users ADD CONSTRAINT fk_users_department
    FOREIGN KEY (department_id) REFERENCES departments (id) NOT VALID;
CREATE INDEX idx_users_department ON users (department_id) WHERE deleted_at IS NULL;

-- 受限角色补授权。
--
-- ⚠️ 00004 里那句 `GRANT ... ON ALL TABLES` 只是**当时的快照**，之后新建的表
-- 一律不在里面。生产上用 fries_app 连库的话，新表会直接「permission denied」。
-- 所以这里除了给 departments 补一次，还顺手设了 DEFAULT PRIVILEGES ——
-- 以后再建表就自动带上，不用每张表都记得回来加。
-- +goose StatementBegin
DO $$
DECLARE
    owner_name text;
BEGIN
    IF NOT EXISTS (SELECT FROM pg_roles WHERE rolname = 'fries_app') THEN
        RAISE NOTICE '没有 fries_app 角色，跳过部门表权限授予（本地开发正常，生产必须建）';
        RETURN;
    END IF;

    EXECUTE 'GRANT SELECT, INSERT, UPDATE, DELETE ON departments TO fries_app';

    -- 默认权限是「按建表者」生效的，所以要绑到当前这个 owner 上
    SELECT current_user INTO owner_name;
    EXECUTE format(
        'ALTER DEFAULT PRIVILEGES FOR ROLE %I IN SCHEMA public '
        'GRANT SELECT, INSERT, UPDATE, DELETE ON TABLES TO fries_app', owner_name);
    EXECUTE format(
        'ALTER DEFAULT PRIVILEGES FOR ROLE %I IN SCHEMA public '
        'GRANT USAGE, SELECT ON SEQUENCES TO fries_app', owner_name);
END $$;
-- +goose StatementEnd

-- +goose Down
ALTER TABLE users DROP COLUMN department_id;
DROP TABLE departments;
