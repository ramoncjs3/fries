-- 地基迁移：只放所有业务表都要用的东西，不建业务表。
-- 业务表由 make gen-module 按 modules/<key>.yaml 生成增量迁移（DECISIONS.md §10）。

-- +goose Up

-- 每个迁移开头都设这两个超时（squawk 的 require-lock-timeout / require-statement-timeout）。
-- lock_timeout：ALTER 等不到锁就放弃，而不是排在长事务后面，把后面所有查询一起堵死 ——
--   这是迁移把生产打挂的最常见方式。
-- statement_timeout：单条语句跑太久就中止，整个迁移随之回滚。
-- SET LOCAL 只在本事务内生效（goose 把每个迁移包在事务里），不会漏给应用连接。
SET LOCAL lock_timeout = '5s';
SET LOCAL statement_timeout = '60s';

-- pg_trgm：模糊搜索字段的 GIN 索引要用它。
-- ILIKE '%x%' 用不到普通索引，这是后台系统最常见的性能坑（DECISIONS.md §2.6）。
CREATE EXTENSION IF NOT EXISTS pg_trgm;

-- 每张表的 updated_at 由触发器维护，不靠应用层记得写（DECISIONS.md §2.2）。
-- 建表时挂上：
--   CREATE TRIGGER trg_<table>_updated_at BEFORE UPDATE ON <table>
--     FOR EACH ROW EXECUTE FUNCTION set_updated_at();
-- +goose StatementBegin
CREATE OR REPLACE FUNCTION set_updated_at() RETURNS trigger AS $$
BEGIN
    NEW.updated_at = now();
    RETURN NEW;
END;
$$ LANGUAGE plpgsql;
-- +goose StatementEnd

-- +goose Down
DROP FUNCTION IF EXISTS set_updated_at();
DROP EXTENSION IF EXISTS pg_trgm;
