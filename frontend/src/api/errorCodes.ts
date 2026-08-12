// 本文件由 `make errcodes-ts` 自动生成，请勿手改。
// 数据源是 Go 侧错误码注册表（internal/errs）+ 前端合成码，改后重跑生成。
// `make check` 会校验它是最新的（DECISIONS.md §4.7.3）。

// 后端注册表里的错误码。前端只按 code 判分支，文案读 detail（后端给的中文）。
export type ServerErrorCode =
  | 'auth.account_locked'
  | 'auth.csrf_invalid'
  | 'auth.invalid_credentials'
  | 'auth.must_change_password'
  | 'auth.password_expired'
  | 'auth.session_expired'
  | 'auth.unauthenticated'
  | 'common.idempotency_conflict'
  | 'common.internal_error'
  | 'common.not_found'
  | 'common.rate_limited'
  | 'common.service_unavailable'
  | 'common.validation_failed'
  | 'common.version_conflict'
  | 'perm.denied'
  | 'perm.scope_denied'
  | 'settings.unknown_key'
  | 'tenant.suspended'

// 前端自己合成、后端不会下发的 code（网络层失败等）。
export type ClientErrorCode =
  | 'common.network_error'

export type ErrorCode = ServerErrorCode | ClientErrorCode
