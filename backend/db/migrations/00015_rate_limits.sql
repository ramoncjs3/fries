-- 限流器的持久化存储（SCALING.md §1）。
--
-- 内存版每副本一份桶，多副本部署时实际阈值放大 N 倍。这张表让所有副本共享同一份计数。
-- 默认仍走内存版（单副本够用、零延迟），server.shared_state_store=postgres 才用它。
--
-- ⚠️ 语义和内存版**不完全一样**：内存版是令牌桶（perSecond 稳态 + burst 峰值），这张表是
-- **固定窗口计数**（每个 window_start 一个桶，桶里 count 超上限就拒）。固定窗口在窗口边界处
-- 会略松（跨界瞬间可能放到 2×上限），但对「防手滑脚本」这类粗粒度保护够用。
-- ⚠️ 每请求打一次库是有代价的 —— 高并发下 Redis 才是更合适的存储；PG 版是「多副本又暂时
-- 不想引 Redis」时的过渡选择。
--
-- 不是业务表：没有 tenant_id（维度已拼进 key，见 internal/middleware/ratelimit.go）。

-- +goose Up

-- 超时设置，理由见 00001_init.sql。
SET LOCAL lock_timeout = '5s';
SET LOCAL statement_timeout = '60s';

CREATE TABLE rate_limits (
    key          text        NOT NULL,
    window_start timestamptz NOT NULL,
    count        integer     NOT NULL DEFAULT 0,
    PRIMARY KEY (key, window_start)
);

-- 清理旧窗口按 window_start 扫。
CREATE INDEX idx_rate_limits_window ON rate_limits (window_start);

-- 权限走 00006 的 DEFAULT PRIVILEGES，fries_app 自动拿到读写。

-- +goose Down
DROP TABLE rate_limits;
