-- 为「用户名 / 邮箱 / 手机号 都能登录」做准备。
--
-- 唯一约束现在就加：等库里有了重复邮箱再补，就得先清数据，那时候只会更疼。

-- +goose Up

-- 超时设置，理由见 00001_init.sql。
SET LOCAL lock_timeout = '5s';
SET LOCAL statement_timeout = '60s';

ALTER TABLE users ADD COLUMN phone varchar(32);

-- 邮箱大小写不敏感：Zhang@x.com 和 zhang@x.com 是同一个人，
-- 所以索引建在 lower(email) 上，查询也必须用 lower()，否则用不到索引。
CREATE UNIQUE INDEX uk_users_email ON users (lower(email))
    WHERE deleted_at IS NULL AND email IS NOT NULL;

CREATE UNIQUE INDEX uk_users_phone ON users (phone)
    WHERE deleted_at IS NULL AND phone IS NOT NULL;

-- +goose Down
DROP INDEX IF EXISTS uk_users_phone;
DROP INDEX IF EXISTS uk_users_email;
ALTER TABLE users DROP COLUMN phone;
