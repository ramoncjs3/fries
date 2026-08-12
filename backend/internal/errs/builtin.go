package errs

import "net/http"

// 内置通用错误码 —— DECISIONS.md §4.6 的 16 个，一个不少。
//
// 这些是全站共用的，前端有对应的全局处理（跳登录页、弹刷新确认框、toast…）。
// 业务模块自己的错误码写在各自的 errors.go 里，前缀用模块 key。
var (
	// common.* —— 与业务无关的通用结果
	ValidationFailed    = Define("common.validation_failed", http.StatusBadRequest, "请求参数校验失败")
	NotFound            = Define("common.not_found", http.StatusNotFound, "资源不存在")
	VersionConflict     = Define("common.version_conflict", http.StatusConflict, "数据已被他人修改，请刷新后重试")
	IdempotencyConflict = Define("common.idempotency_conflict", http.StatusConflict, "重复请求")
	RateLimited         = Define("common.rate_limited", http.StatusTooManyRequests, "操作太频繁，请稍后再试")
	Internal            = Define("common.internal_error", http.StatusInternalServerError, "服务器内部错误，请稍后重试")
	ServiceUnavailable  = Define("common.service_unavailable", http.StatusServiceUnavailable, "服务暂时不可用")

	// auth.* —— 登录与会话
	Unauthenticated    = Define("auth.unauthenticated", http.StatusUnauthorized, "请先登录")
	SessionExpired     = Define("auth.session_expired", http.StatusUnauthorized, "登录已过期，请重新登录")
	InvalidCredentials = Define("auth.invalid_credentials", http.StatusUnauthorized, "用户名或密码错误")
	AccountLocked      = Define("auth.account_locked", http.StatusForbidden, "账号已锁定")
	MustChangePassword = Define("auth.must_change_password", http.StatusForbidden, "首次登录请修改密码")
	PasswordExpired    = Define("auth.password_expired", http.StatusForbidden, "密码已过期，请修改")
	CSRFInvalid        = Define("auth.csrf_invalid", http.StatusForbidden, "请求校验失败，请刷新页面")

	// perm.* —— 功能权限与数据权限
	PermDenied  = Define("perm.denied", http.StatusForbidden, "无权限执行此操作")
	ScopeDenied = Define("perm.scope_denied", http.StatusForbidden, "无权访问该数据")

	// tenant.* —— 组织（多租户，MULTI-TENANCY.md §7.6）
	//
	// ⚠️ **登录链路上不许返回它**：登录失败的三种原因（公司代码不存在、
	// 租户被停用、账号密码错）必须给一模一样的回应，否则登录接口就成了
	// 「这家公司是不是你们客户」的探测器（§4.1）。
	// 它只用在**已经认证过**的请求上 —— 那时人已经进来了，告诉他真实原因才有用。
	TenantSuspended = Define("tenant.suspended", http.StatusForbidden,
		"所属组织已停用，请联系管理员")
)

// Builtin 是内置错误码，selfcheck 用它校验注册表完整。
//
// DECISIONS.md §4.6 定的是 16 个；多租户之后多了一个 tenant.suspended（§7.6）。
func Builtin() []*Code {
	return []*Code{
		ValidationFailed, NotFound, VersionConflict, IdempotencyConflict,
		RateLimited, Internal, ServiceUnavailable,
		Unauthenticated, SessionExpired, InvalidCredentials, AccountLocked,
		MustChangePassword, PasswordExpired, CSRFInvalid,
		PermDenied, ScopeDenied,
		TenantSuspended,
	}
}

// ForStatus 按 HTTP 状态码兜底选一个通用错误码。
//
// 只在「错误没带注册过的错误码」时用得上（例如框架自己产生的 405、413）。
// 业务代码不应该依赖它 —— 红线 #7 要求返回注册过的 *Code。
func ForStatus(status int) *Code {
	switch status {
	case http.StatusBadRequest, http.StatusUnprocessableEntity, http.StatusRequestEntityTooLarge,
		http.StatusUnsupportedMediaType, http.StatusRequestedRangeNotSatisfiable:
		return ValidationFailed
	case http.StatusUnauthorized:
		return Unauthenticated
	case http.StatusForbidden:
		return PermDenied
	case http.StatusNotFound, http.StatusMethodNotAllowed, http.StatusGone:
		return NotFound
	case http.StatusConflict:
		return VersionConflict
	case http.StatusTooManyRequests:
		return RateLimited
	case http.StatusServiceUnavailable, http.StatusGatewayTimeout, http.StatusRequestTimeout:
		return ServiceUnavailable
	}
	if status >= 400 && status < 500 {
		return ValidationFailed
	}
	return Internal
}
