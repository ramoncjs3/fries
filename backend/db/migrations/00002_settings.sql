-- 配置分层的第二层：可在后台改、改完立即生效的配置（DECISIONS.md §5）。
-- 第一层是 config/config.yaml，只放启动必需的东西。

-- +goose Up

-- 超时设置，理由见 00001_init.sql。
SET LOCAL lock_timeout = '5s';
SET LOCAL statement_timeout = '60s';

-- settings 不是业务表，不套 §2.2 的标准列：它按 key 取值，没有软删除和乐观锁的必要。
CREATE TABLE settings (
    key         varchar(100) PRIMARY KEY,
    value       jsonb        NOT NULL,
    description text         NOT NULL DEFAULT '',
    updated_at  timestamptz  NOT NULL DEFAULT now(),
    updated_by  uuid
);

CREATE TRIGGER trg_settings_updated_at
    BEFORE UPDATE ON settings
    FOR EACH ROW EXECUTE FUNCTION set_updated_at();

-- 改配置后通知所有实例刷新缓存：写库 → NOTIFY → 各实例重新加载（DECISIONS.md §5）。
-- 不引 Redis。
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

-- 默认值。代码里每个 key 都有兜底默认，这里插一份是为了后台页面能看到、能改。
INSERT INTO settings (key, value, description) VALUES
    ('security.password_min_length',  '10',    '密码最少多少位'),
    ('security.password_require_mix', 'true',  '密码是否必须混合大小写字母、数字'),
    ('security.password_max_age_days','0',     '密码多少天后过期，0 表示不过期'),
    ('security.login_max_failures',   '5',     '连续失败多少次锁定账号'),
    ('security.login_lock_minutes',   '15',    '锁定多少分钟'),
    ('audit.retention_days',          '180',   '审计日志保留天数，过期整分区删除'),
    ('ui.system_name',                '"fries"', '系统名称，显示在页面标题和登录页');

-- +goose Down
DROP TABLE settings;
DROP FUNCTION IF EXISTS notify_settings_changed();
