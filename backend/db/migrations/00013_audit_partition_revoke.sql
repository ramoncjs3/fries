-- 修复 H1：审计防篡改在分区上失效，应用账号能直接改删审计（DECISIONS.md §6）。
--
-- 背景：audit_logs 是分区表，数据落在子分区 audit_logs_YYYYMM 里。00004 的
-- 「REVOKE UPDATE/DELETE」只打在**父表** audit_logs 上，而 PostgreSQL 直接寻址分区
-- （DELETE FROM audit_logs_202608 ...）校验的是**分区自己的 ACL**，父表的 REVOKE 不下沉。
-- 偏偏 00004 的 `GRANT ... UPDATE, DELETE ON ALL TABLES`（在建完初始两个分区之后执行）
-- 把完整 DML 授给了那些分区，00006 的 DEFAULT PRIVILEGES 又让每个未来分区自动带上。
-- 结果 fries_app 能 `DELETE FROM audit_logs_202608` 抹掉审计，且能连哈希一并改自洽，
-- 预防和检测双双失效。已用受限角色在真库复现。
--
-- 修法：① 重定义 ensure_audit_partition，建完新分区立刻对它 REVOKE UPDATE/DELETE，
-- 未来分区一劳永逸；② 给已存在的分区补一次 REVOKE。
-- 自检 warnIfAuditTamperable 也一并改成查分区而非父表（见 cmd/server/tasks.go）。

-- +goose Up

SET LOCAL lock_timeout = '5s';
SET LOCAL statement_timeout = '60s';

-- ① 重定义分区创建函数：新分区建出来就收权，和父表 00004 的收口保持一致。
--    只有部署时按规范建了受限角色 fries_app 才需要收；本地 owner 连库时角色不存在，跳过。
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
        -- 审计防篡改：新分区只能写入和查询，改不了也删不了。
        -- DEFAULT PRIVILEGES（00006）会给新表 UPDATE/DELETE，这里当场收回，
        -- 否则「直接寻址分区」就绕过了父表那道 REVOKE（H1）。
        IF EXISTS (SELECT FROM pg_roles WHERE rolname = 'fries_app') THEN
            EXECUTE format('REVOKE UPDATE, DELETE ON %I FROM fries_app', part);
        END IF;
    END IF;
    RETURN part;
END;
$$ LANGUAGE plpgsql SECURITY DEFINER;
-- +goose StatementEnd

-- ② 给已经存在的分区补收权（00004 建的初始两个，以及此前滚动出来的任何一个）。
-- +goose StatementBegin
DO $$
DECLARE
    part record;
BEGIN
    IF NOT EXISTS (SELECT FROM pg_roles WHERE rolname = 'fries_app') THEN
        RAISE NOTICE '没有 fries_app 角色，跳过审计分区权限收口（本地开发正常，生产必须建）';
        RETURN;
    END IF;
    FOR part IN
        SELECT c.relname AS name
        FROM pg_inherits i
        JOIN pg_class p ON p.oid = i.inhparent
        JOIN pg_class c ON c.oid = i.inhrelid
        WHERE p.relname = 'audit_logs'
    LOOP
        EXECUTE format('REVOKE UPDATE, DELETE ON %I FROM fries_app', part.name);
    END LOOP;
END;
$$;
-- +goose StatementEnd

-- +goose Down

-- 只还原函数定义（回到 00004 那版，不对新分区收权）。
-- **不主动把 UPDATE/DELETE 加回已存在的分区** —— 回滚不该重新打开审计篡改的口子。
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
