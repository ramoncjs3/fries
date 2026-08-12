-- 校验 00009 里那条 NOT VALID 的 CHECK。
--
-- 单独一个文件的理由和 00008 一样：goose 一个迁移一个事务，
-- NOT VALID 的意义就在于加约束的那个事务不扫表，塞回去就等于没拆。

-- +goose Up

SET LOCAL lock_timeout = '5s';
SET LOCAL statement_timeout = '300s';

ALTER TABLE audit_logs VALIDATE CONSTRAINT audit_logs_actor_type_check;

-- +goose Down

-- 校验状态没法单独撤销，约束本身在 00009 的 Down 里会被换回窄的那版。
SELECT 1;
