-- Service Account 的角色变更也要触发权限策略重载。
--
-- # 为什么原来漏了
--
-- 00003 给 roles / role_permissions / user_roles 三张表挂了 authz_changed 触发器，
-- 但**没给 service_accounts 挂** —— 那时还没有管理界面，Service Account 只能上库里建，
-- 建完顺手重启服务就完事了。
--
-- 现在有界面了，这个缺口就是实打实的问题：
--
--   给 Service Account 换角色  → enforcer 里还是旧绑定 → **降权不生效**
--   新建 Service Account       → 策略里没有它 → 拿着新密钥认证过了，但一点权限都没有
--
-- 第一条是安全问题：把一个对接凭据从「管理员」降成「只读」之后，
-- 它还能以管理员身份跑到下一次有人改角色为止。
--
-- ⚠️ 停用/删除**不在**这个范围里 —— 那两件事在认证那一步就挡住了
-- （GetServiceAccountByPrefix 只认 status='active' 且未删除的行），
-- 内存里留着旧绑定没有风险。挂上只是顺带，图个一致。
--
-- # 🔴 为什么必须限定列
--
-- `TouchServiceAccount` 每个 API 请求都会写 `last_used_at`。挂成全表 UPDATE 的话，
-- **每一次 API 调用都会广播一次策略重载** —— 而重载是「整体重建 enforcer、
-- 遍历所有租户」（MULTI-TENANCY.md §8.5）。那不是性能退化，那是把系统打死。
--
-- `UPDATE OF` 按语句的 SET 列表里提到了哪些列来判，和值有没有真的变无关。
-- 所以 TouchServiceAccount 那条 `SET last_used_at = now()` 不会触发它。
--
-- 两半都有测试盯着（cmd/server 的 TestServiceAccountAuthzNotify）：
-- 改角色必须发通知、写 last_used_at 必须不发。

-- +goose Up

-- CREATE TRIGGER 要拿这张表的 ACCESS EXCLUSIVE 锁。表很小、动作很快，
-- 但忙的时候它会排在正在跑的查询后面，而排队期间新查询也会堵在它后面。
-- 加超时是为了「卡住就早点失败」，而不是把线上堵成一片（理由同 00001）。
SET LOCAL lock_timeout = '5s';
SET LOCAL statement_timeout = '60s';

CREATE TRIGGER trg_service_accounts_notify
    AFTER INSERT OR DELETE OR UPDATE OF role_id, status, deleted_at ON service_accounts
    FOR EACH STATEMENT EXECUTE FUNCTION notify_authz_changed();

-- +goose Down

DROP TRIGGER IF EXISTS trg_service_accounts_notify ON service_accounts;
