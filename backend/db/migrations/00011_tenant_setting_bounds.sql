-- 平台给租户级安全设置划的区间（MULTI-TENANCY.md §10.5）。
--
-- 为什么需要：§7.2 把密码策略、登录锁定改成了租户级，于是**租户管理员可以把
-- 密码策略调到 1 位、把锁定时间调到 0** —— 他给自己公司挖坑，
-- 但出了事是平台的品牌在承担。这是 SaaS 的常规做法。
--
-- 存成平台设置而不是写死在代码里，是因为它本身要能调：
-- 接了一个合规要求更高的客户，平台整体收紧一档，不用发版。
--
-- 命名规则：`limits.<租户级 key>.min` / `.max`。
-- 没有对应 limits 行的租户级 key **不受限**（比如 password_require_mix 是个开关，
-- 卡不出区间来）—— 想限就补一行，代码不用改。

-- +goose Up

INSERT INTO platform_settings (key, value, description) VALUES
    -- 10 位是 defaultSecurity 的默认值，也就是「不能比出厂设置更松」
    ('limits.security.password_min_length.min', '10',  '密码最少多少位：租户不能设得比这个更短'),
    ('limits.security.password_min_length.max', '64',  '密码最少多少位：租户也不能设成没人记得住的长度'),
    -- 0 表示永不过期，是个合法值，所以下界只能是 0
    ('limits.security.password_max_age_days.max', '365', '密码多少天过期：租户不能设得比这个更长（0 表示不过期）'),
    ('limits.security.login_max_failures.max',  '10',  '连错多少次锁定：租户不能设得比这个更宽松'),
    ('limits.security.login_max_failures.min',  '3',   '连错多少次锁定：也不能严到自己人天天被锁'),
    ('limits.security.login_lock_minutes.min',  '5',   '锁多少分钟：租户不能设得比这个更短')
ON CONFLICT (key) DO NOTHING;

-- +goose Down

DELETE FROM platform_settings WHERE key LIKE 'limits.%';
