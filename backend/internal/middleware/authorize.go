package middleware

import (
	"net/http"

	"github.com/danielgtaylor/huma/v2"
	"github.com/google/uuid"

	"github.com/ramoncjs3/fries/internal/audit"
	"github.com/ramoncjs3/fries/internal/authz"
	"github.com/ramoncjs3/fries/internal/errs"
	"github.com/ramoncjs3/fries/internal/perm"
)

// Authorize 是**真正的拦截线**（DECISIONS.md §3.6 第 ③ 层）。
//
// 它跑在 huma 这一层而不是 Echo 那一层，因为只有这里拿得到「这个接口要什么权限点」
// —— 那是注册路由时写进 huma.Operation.Metadata 的。
//
// 顺带把权限点填进审计记录：中间件层的审计因此天然知道「什么资源、什么动作」。
func Authorize(api huma.API, checker authz.Checker) func(huma.Context, func(huma.Context)) {
	return func(ctx huma.Context, next func(huma.Context)) {
		access, point, ok := perm.RequirementOf(ctx.Operation())
		if !ok {
			// 走了 huma.Register 而没走 perm 的注册器 —— 这是编码错误。
			// 启动自检会拦住它；万一漏了，运行时也必须拒绝，不能默认放行。
			writeErr(api, ctx, errs.PermDenied.Wrap(errMissingRequirement{ctx.Operation().OperationID}))
			return
		}

		// 先把审计的「资源 + 动作」定下来：被这里拦掉的请求 handler 根本不会跑，
		// 不先填就只能记成一条看不出是哪个接口的 http:request。
		// handler 里的 audit.SetAction 会覆盖它（登录那种没有权限点的接口靠它）。
		goCtx := ctx.Context()
		if access == perm.AccessPermission {
			audit.SetAction(goCtx, point.Resource, point.Action)
		} else {
			audit.SetAction(goCtx, "http", ctx.Operation().OperationID)
		}

		if access == perm.AccessPublic {
			next(ctx)
			return
		}

		principal, authenticated := authz.PrincipalFrom(goCtx)
		if !authenticated {
			// 认证阶段记下的原因（会话过期 / 账号停用）比笼统的「请先登录」有用
			if err := AuthError(goCtx); err != nil {
				writeErr(api, ctx, err)
				return
			}
			writeErr(api, ctx, errs.Unauthenticated)
			return
		}

		// 🔴 **路由的 Realm 必须和主体的 Realm 一致**（MULTI-TENANCY.md §10.4）。
		//
		// §10.4 说「/platform 要进 MustTenant 的白名单」——「白名单」的意思是
		// **换一个人把守**，不是没人把守。这里就是那个人，而且是对称的两条：
		//
		//	租户用户  → 走不了 /platform 的接口（拿别人的组织开关不了）
		//	平台管理员 → 走不了租户的业务接口（结构上碰不到客户数据）
		//
		// 后一条尤其要紧：它让「平台管理员看不了客户业务数据」成为一条**判定**，
		// 而不只是「平台端没写那些接口」。将来谁不小心把业务接口注册进平台路由，
		// 这里也会拦下来。
		if point.Realm != principal.Realm() {
			writeErr(api, ctx, errs.PermDenied.Wrap(errRealmMismatch{
				operationID: ctx.Operation().OperationID,
				route:       string(point.Realm),
				subject:     string(principal.Realm()),
			}))
			return
		}

		// 租户主体必须带租户（MULTI-TENANCY.md §1.2 ②）。
		//
		// 这是**兜底**，不是主防线：中间件看不见 SQL，拦不住漏写租户条件的查询。
		// 它只管一件事 —— 该有租户却没有的请求一律拒绝，绝不放行。
		//
		// ⚠️ 走到这里还没有租户，说明认证链路有 bug。这时**必须拒绝** ——
		// 放行等于让这次请求跑在 uuid.Nil 上，静默查到 0 行，
		// 和 RLS 漏设上下文一模一样的静默失败。
		//
		// 平台管理员本来就没有租户（§2.3），所以这一条只管租户主体。
		if !principal.IsPlatform() && principal.TenantID == uuid.Nil {
			writeErr(api, ctx, errs.PermDenied.Wrap(errNoTenantOnPrincipal{ctx.Operation().OperationID}))
			return
		}

		// 必须改密码的人，除了改密接口哪都去不了（DECISIONS.md §6）
		if blocked := passwordBlock(principal, ctx.Operation().OperationID); blocked != nil {
			writeErr(api, ctx, blocked)
			return
		}

		if access == perm.AccessAuthenticated {
			next(ctx)
			return
		}

		// 平台管理员这一轮**即全权**（§6）：平台端只有开租户、停租户这几个动作，
		// 细粒度分权等真需要时再按 perm 那套加。Realm 已经在上面对齐过了。
		if principal.IsPlatform() {
			next(ctx)
			return
		}

		if !checker.Allow(principal, point) {
			writeErr(api, ctx, errs.PermDenied)
			return
		}
		next(ctx)
	}
}

// 「必须改密」状态下仍然放行的操作。
//
// ⚠️ **两套都要列全**（租户端 + 平台管理端）。少列一个就是死锁：
// 平台管理员首次登录被要求改密，而改密接口本身也被挡住 —— 他连改都改不了，
// 只能上数据库救。浏览器实测踩到过这一条。
//
// 退出登录也要放行，不然人被卡在改密页，连换个账号都做不到。
const (
	// PasswordChangeOperationID 是租户端用来解锁的操作。
	PasswordChangeOperationID = "change-own-password"
	// LogoutOperationID 是租户端的退出登录。
	LogoutOperationID = "logout"
	// PlatformPasswordChangeOperationID 是平台端用来解锁的操作。
	PlatformPasswordChangeOperationID = "platform-change-password"
	// PlatformLogoutOperationID 是平台端的退出登录。
	PlatformLogoutOperationID = "platform-logout"
)

// passwordBlock 判断是不是卡在改密码这一步。
func passwordBlock(p *authz.Principal, operationID string) error {
	switch operationID {
	case PasswordChangeOperationID, LogoutOperationID,
		PlatformPasswordChangeOperationID, PlatformLogoutOperationID:
		return nil
	}
	switch {
	case p.MustChangePassword:
		return errs.MustChangePassword
	case p.PasswordExpired:
		return errs.PasswordExpired
	}
	return nil
}

// writeErr 用我们的 RFC 9457 格式写错误响应。
func writeErr(api huma.API, ctx huma.Context, err error) {
	status := http.StatusForbidden
	if e, ok := errs.From(err); ok {
		status = e.Code.Status
	}
	_ = huma.WriteErr(api, ctx, status, "", err)
}

type errRealmMismatch struct {
	operationID string
	route       string
	subject     string
}

func (e errRealmMismatch) Error() string {
	return "接口 " + e.operationID + " 属于 " + e.route + " 世界，" +
		"而当前主体属于 " + e.subject + " 世界 —— 两边的权限体系是分开的"
}

type errNoTenantOnPrincipal struct{ operationID string }

func (e errNoTenantOnPrincipal) Error() string {
	return "接口 " + e.operationID + " 的认证主体上没有租户 —— 认证链路没把 sessions.tenant_id 填进 Principal"
}

type errMissingRequirement struct{ operationID string }

func (e errMissingRequirement) Error() string {
	return "接口 " + e.operationID + " 没有声明访问要求，必须用 perm.Public / perm.Authenticated / perm.Guard 注册"
}
