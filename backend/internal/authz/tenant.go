package authz

import (
	"context"

	"github.com/google/uuid"

	"github.com/ramoncjs3/fries/internal/errs"
)

// MustTenant 取当前请求属于哪个租户。
//
// **默认拒绝**：context 里没有主体、或者主体身上没有租户，一律报错，**不是放行**
// （MULTI-TENANCY.md §1.2 ②）。这和 MustScope 是同一个范式 ——
// 漏注入这类东西静态查不出来，只能靠运行时报错和测试兜住。
//
// 租户的唯一来源是**会话**（`sessions.tenant_id`，由中间件填进 Principal）。
// 别从别处推：不要从请求参数取，不要从用户 id 反查 —— 那都是可以被伪造或漂移的。
//
//	id, err := authz.MustTenant(ctx)
//	if err != nil {
//	    return err
//	}
//	q := store.ForTenant(id)
//
// ⚠️ 它只回答「有没有租户」，**不负责给查询加条件** —— 它看不见 SQL。
// 「查询真的带了 tenant_id」是另外两件事在管：ForTenant 包装（调用方填不了）
// 和 `gen lint-sql` 静态检查（SQL 里真的用了）。
func MustTenant(ctx context.Context) (uuid.UUID, error) {
	p, ok := PrincipalFrom(ctx)
	if !ok {
		return uuid.Nil, errs.PermDenied.Wrap(errNoTenant{"context 里没有认证主体"})
	}
	if p.TenantID == uuid.Nil {
		// 能走到这里说明认证中间件没把租户填进来，是代码 bug，不是权限问题。
		// 但对外仍然只能是拒绝 —— 放行等于让这次请求跑在 uuid.Nil 上，
		// 那会静默查到 0 行，和 RLS 漏设上下文一模一样。
		return uuid.Nil, errs.PermDenied.Wrap(errNoTenant{"认证主体上没有租户"})
	}
	return p.TenantID, nil
}

// 用 PermDenied 而不是新造一个错误码：走到这里说明中间件没把租户填进来，
// 那是代码 bug，不是用户能理解或修正的事 —— 对外一律 403 通用文案就够了。
// 真正给用户看的租户错误（tenant.not_found / tenant.suspended）在登录链路上，
// 是第 ③ 步的事（MULTI-TENANCY.md §7.6）。
type errNoTenant struct{ why string }

func (e errNoTenant) Error() string { return "取不到当前租户：" + e.why }
