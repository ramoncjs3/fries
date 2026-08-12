-- 幂等键的持久化存储（SCALING.md §1）。
--
-- 内存版每副本一份，多副本部署时同一个键落到不同副本上认不出重复，重放能溜过去。
-- 这张表让所有副本共享同一份「见过的键」。默认仍走内存版（单副本够用、零延迟），
-- 多副本时把 server.shared_state_store 设成 postgres 才用它。
--
-- 不是业务表：没有 tenant_id 列（租户和主体已经拼进 key 字符串里了，见
-- internal/middleware/idempotency.go 的 scopeOf），所以不进 tenantsql 的租户表清单。

-- +goose Up

-- 超时设置，理由见 00001_init.sql。
SET LOCAL lock_timeout = '5s';
SET LOCAL statement_timeout = '60s';

CREATE TABLE idempotency_keys (
    -- key 是「主体 + 方法 + 路由 + 客户端给的键」拼出来的，中间件负责组合。
    key        text        PRIMARY KEY,
    expires_at timestamptz NOT NULL
);

-- 清理过期键按 expires_at 扫，给它建索引。
CREATE INDEX idx_idempotency_keys_expires ON idempotency_keys (expires_at);

-- 权限走 00006 设的 DEFAULT PRIVILEGES：fries_app 自动拿到 SELECT/INSERT/UPDATE/DELETE，
-- 这里不用显式 GRANT（它不是审计表那种需要特意收权的表）。

-- +goose Down
DROP TABLE idempotency_keys;
