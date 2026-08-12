# 错误码对照表

> **本文件由 `make errdoc` 自动生成，请勿手改。**
> 错误码在代码里用 `errs.Define(...)` 声明，改代码后重跑生成。
> `make check` 会校验本文件是最新的。

当前共 **18** 个错误码。前端只读 `code`（机器判断）和 `detail`（中文文案）。

## common —— 通用

| code | HTTP | 文案 |
|---|---|---|
| `common.idempotency_conflict` | 409 | 重复请求 |
| `common.internal_error` | 500 | 服务器内部错误，请稍后重试 |
| `common.not_found` | 404 | 资源不存在 |
| `common.rate_limited` | 429 | 操作太频繁，请稍后再试 |
| `common.service_unavailable` | 503 | 服务暂时不可用 |
| `common.validation_failed` | 400 | 请求参数校验失败 |
| `common.version_conflict` | 409 | 数据已被他人修改，请刷新后重试 |

## auth —— 认证与会话

| code | HTTP | 文案 |
|---|---|---|
| `auth.account_locked` | 403 | 账号已锁定 |
| `auth.csrf_invalid` | 403 | 请求校验失败，请刷新页面 |
| `auth.invalid_credentials` | 401 | 用户名或密码错误 |
| `auth.must_change_password` | 403 | 首次登录请修改密码 |
| `auth.password_expired` | 403 | 密码已过期，请修改 |
| `auth.session_expired` | 401 | 登录已过期，请重新登录 |
| `auth.unauthenticated` | 401 | 请先登录 |

## perm —— 权限

| code | HTTP | 文案 |
|---|---|---|
| `perm.denied` | 403 | 无权限执行此操作 |
| `perm.scope_denied` | 403 | 无权访问该数据 |

## settings —— 模块

| code | HTTP | 文案 |
|---|---|---|
| `settings.unknown_key` | 400 | 没有这个配置项 |

## tenant —— 模块

| code | HTTP | 文案 |
|---|---|---|
| `tenant.suspended` | 403 | 所属组织已停用，请联系管理员 |
