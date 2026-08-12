-- 审计日志（DECISIONS.md §6）。三条硬要求：
--   1. 按月分区，保留期到了整分区 DROP
--   2. 哈希链：每行记住上一行的哈希，改一行后面全对不上
--   3. DB 层撤销应用账号的 UPDATE / DELETE，只留 INSERT

-- +goose Up

-- 超时设置，理由见 00001_init.sql。
SET LOCAL lock_timeout = '5s';
SET LOCAL statement_timeout = '60s';

CREATE TABLE audit_logs (
    id           uuid         NOT NULL,
    occurred_at  timestamptz  NOT NULL DEFAULT now(),
    request_id   varchar(64)  NOT NULL DEFAULT '',

    -- 谁：user（人）/ service（Service Account）/ anonymous（没登录）/ system（后台任务）
    actor_type   varchar(16)  NOT NULL
                              CHECK (actor_type IN ('user', 'service', 'anonymous', 'system')),
    actor_id     uuid,
    actor_name   varchar(64)  NOT NULL DEFAULT '',

    -- 干了什么：resource 是模块 key，action 是权限点动作；
    -- 登录登出这类没有模块的事件用 resource='auth'
    resource     varchar(64)  NOT NULL,
    action       varchar(64)  NOT NULL,
    -- 哪条记录 —— 中间件看不到（新增的 ID 在响应里），由 handler 层补（§6）
    resource_id  uuid,

    method       varchar(8)   NOT NULL DEFAULT '',
    path         text         NOT NULL DEFAULT '',
    ip           inet,
    user_agent   text         NOT NULL DEFAULT '',
    http_status  integer      NOT NULL DEFAULT 0,
    duration_ms  integer      NOT NULL DEFAULT 0,
    -- 参数摘要，敏感字段已脱敏截断
    detail       jsonb        NOT NULL DEFAULT '{}'::jsonb,

    -- 哈希链，由触发器维护，应用写不了
    prev_hash    bytea,
    hash         bytea        NOT NULL,

    PRIMARY KEY (id, occurred_at)
) PARTITION BY RANGE (occurred_at);

CREATE INDEX idx_audit_logs_occurred ON audit_logs (occurred_at DESC);
CREATE INDEX idx_audit_logs_actor ON audit_logs (actor_id, occurred_at DESC);
CREATE INDEX idx_audit_logs_resource ON audit_logs (resource, resource_id, occurred_at DESC);
CREATE INDEX idx_audit_logs_request ON audit_logs (request_id);

-- 链头单独存一行：插入时锁这一行，天然串行化，不用扫全表找上一条。
CREATE TABLE audit_chain_head (
    only_row boolean PRIMARY KEY DEFAULT true CHECK (only_row),
    hash     bytea
);
INSERT INTO audit_chain_head (only_row, hash) VALUES (true, NULL);

-- 哈希链由触发器算，**不由应用算** —— 应用能算就意味着应用能伪造。
-- SECURITY DEFINER：应用账号没有 audit_chain_head 的写权限也能插审计。
-- +goose StatementBegin
CREATE OR REPLACE FUNCTION audit_chain() RETURNS trigger AS $$
DECLARE
    prev bytea;
BEGIN
    -- FOR UPDATE 把并发插入排成队，链才不会分叉
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

CREATE TRIGGER trg_audit_logs_chain
    BEFORE INSERT ON audit_logs
    FOR EACH ROW EXECUTE FUNCTION audit_chain();

-- 按月分区。新分区由后台任务提前建（internal/task），这里先把当月和下月备好。
-- SECURITY DEFINER：应用账号没有建表权限也能调它。
-- +goose StatementBegin
CREATE OR REPLACE FUNCTION ensure_audit_partition(target timestamptz) RETURNS text AS $$
DECLARE
    start_at date := date_trunc('month', target)::date;
    end_at   date := (date_trunc('month', target) + interval '1 month')::date;
    part     text := 'audit_logs_' || to_char(start_at, 'YYYYMM');
BEGIN
    IF to_regclass(part) IS NULL THEN
        EXECUTE format(
            'CREATE TABLE %I PARTITION OF audit_logs FOR VALUES FROM (%L) TO (%L)',
            part, start_at, end_at);
    END IF;
    RETURN part;
END;
$$ LANGUAGE plpgsql SECURITY DEFINER;
-- +goose StatementEnd

SELECT ensure_audit_partition(now());
SELECT ensure_audit_partition(now() + interval '1 month');

-- 保留期到了整分区 DROP，不做逐行删除（DECISIONS.md §2.6）。
-- +goose StatementBegin
CREATE OR REPLACE FUNCTION drop_audit_partitions_before(cutoff timestamptz) RETURNS integer AS $$
DECLARE
    part    record;
    dropped integer := 0;
BEGIN
    FOR part IN
        SELECT c.relname AS name
        FROM pg_class c
        JOIN pg_inherits i ON i.inhrelid = c.oid
        JOIN pg_class p ON p.oid = i.inhparent
        WHERE p.relname = 'audit_logs'
    LOOP
        -- 分区名固定是 audit_logs_YYYYMM，整月都早于 cutoff 才删
        IF to_date(right(part.name, 6), 'YYYYMM') + interval '1 month' <= cutoff THEN
            EXECUTE format('DROP TABLE %I', part.name);
            dropped := dropped + 1;
        END IF;
    END LOOP;
    RETURN dropped;
END;
$$ LANGUAGE plpgsql SECURITY DEFINER;
-- +goose StatementEnd

-- 防篡改的最后一道：**DB 层撤销应用账号的 UPDATE / DELETE**（DECISIONS.md §6）。
--
-- 只有部署时按规范建了受限角色 fries_app 才生效；本地开发直接用库 owner 连，
-- owner / superuser 会绕过所有权限检查 —— 这条防线在本地是不生效的，服务启动时会警告。
-- +goose StatementBegin
DO $$
BEGIN
    IF NOT EXISTS (SELECT FROM pg_roles WHERE rolname = 'fries_app') THEN
        RAISE NOTICE '没有 fries_app 角色，跳过审计表权限收口（本地开发正常，生产必须建）';
        RETURN;
    END IF;

    EXECUTE 'GRANT USAGE ON SCHEMA public TO fries_app';
    EXECUTE 'GRANT SELECT, INSERT, UPDATE, DELETE ON ALL TABLES IN SCHEMA public TO fries_app';

    -- 审计表只能写入和查询，改不了也删不了
    EXECUTE 'REVOKE ALL ON audit_logs FROM fries_app';
    EXECUTE 'GRANT SELECT, INSERT ON audit_logs TO fries_app';
    EXECUTE 'REVOKE ALL ON audit_chain_head FROM fries_app';
    EXECUTE 'GRANT SELECT ON audit_chain_head TO fries_app';
END;
$$;
-- +goose StatementEnd

-- +goose Down
DROP FUNCTION IF EXISTS drop_audit_partitions_before(timestamptz);
DROP TABLE audit_logs;
DROP TABLE audit_chain_head;
DROP FUNCTION IF EXISTS audit_chain();
DROP FUNCTION IF EXISTS ensure_audit_partition(timestamptz);
