-- 校验 00007 里那批 NOT VALID 的外键。
--
-- 为什么要单独一个文件：goose 一个迁移一个事务，而 `NOT VALID` 的意义就在于
-- **加约束的那个事务不扫表**。把 VALIDATE 塞回 00007 就等于没拆 —— 全表扫描照样
-- 发生在同一个事务里，锁一直握到提交（squawk 的 constraint-missing-not-valid 会报）。
--
-- VALIDATE 拿的是 SHARE UPDATE EXCLUSIVE 锁：不挡读、不挡写，只挡并发 DDL 和 VACUUM。
--
-- 跑完之后这些外键才真正「可信」—— 在此之前它只约束新写入，不保证存量行干净。

-- +goose Up

-- 超时设置，理由见 00001_init.sql。
-- 校验要扫全表，statement_timeout 给得比别的迁移宽一些。
SET LOCAL lock_timeout = '5s';
SET LOCAL statement_timeout = '300s';

-- 每张表的 tenant_id 指得回 tenants
ALTER TABLE users            VALIDATE CONSTRAINT fk_users_tenant;
ALTER TABLE roles            VALIDATE CONSTRAINT fk_roles_tenant;
ALTER TABLE role_permissions VALIDATE CONSTRAINT fk_role_permissions_tenant;
ALTER TABLE user_roles       VALIDATE CONSTRAINT fk_user_roles_tenant;
ALTER TABLE sessions         VALIDATE CONSTRAINT fk_sessions_tenant;
ALTER TABLE service_accounts VALIDATE CONSTRAINT fk_service_accounts_tenant;
ALTER TABLE settings         VALIDATE CONSTRAINT fk_settings_tenant;
ALTER TABLE departments      VALIDATE CONSTRAINT fk_departments_tenant;

-- 七条复合外键：跨租户引用在数据库这一层被杜绝（§2.2.1）
ALTER TABLE departments      VALIDATE CONSTRAINT fk_departments_parent;
ALTER TABLE users            VALIDATE CONSTRAINT fk_users_department;
ALTER TABLE user_roles       VALIDATE CONSTRAINT fk_user_roles_user;
ALTER TABLE user_roles       VALIDATE CONSTRAINT fk_user_roles_role;
ALTER TABLE role_permissions VALIDATE CONSTRAINT fk_role_permissions_role;
ALTER TABLE sessions         VALIDATE CONSTRAINT fk_sessions_user;
ALTER TABLE service_accounts VALIDATE CONSTRAINT fk_service_accounts_role;

-- +goose Down

-- 校验状态没法单独撤销（也没必要撤）：约束本身在 00007 的 Down 里会被一起删掉。
SELECT 1;
