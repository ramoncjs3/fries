package handler

import (
	"context"
	"net/http"

	"github.com/danielgtaylor/huma/v2"
	"github.com/google/uuid"

	"github.com/ramoncjs3/fries/internal/audit"
	"github.com/ramoncjs3/fries/internal/auth"
	"github.com/ramoncjs3/fries/internal/authz"
	"github.com/ramoncjs3/fries/internal/errs"
	"github.com/ramoncjs3/fries/internal/httpx"
	"github.com/ramoncjs3/fries/internal/middleware"
	"github.com/ramoncjs3/fries/internal/perm"
	"github.com/ramoncjs3/fries/internal/perm/modules"
	platformsvc "github.com/ramoncjs3/fries/internal/service/platform"
)

// PlatformPrefix 是平台管理端的路由前缀。
//
// 它和租户端的接口在同一个 API 下，但**主体、会话、cookie 全是分开的**
// （MULTI-TENANCY.md §6、§10.1）。把它们放在一个前缀下是为了让
// 「这是平台端」在路径上一眼看得出来 —— 排查和审计时都省事。
const PlatformPrefix = "/platform"

// platformTag 是平台端接口在 OpenAPI 里的分组名。
const platformTag = "platform"

// Platform 是平台管理端的 handler。
type Platform struct {
	auth *auth.PlatformService
	svc  *platformsvc.Service
}

// NewPlatform 造平台管理端 handler。
func NewPlatform(a *auth.PlatformService, svc *platformsvc.Service) *Platform {
	return &Platform{auth: a, svc: svc}
}

// PlatformLoginInput 是平台登录入参。**没有公司代码** —— 平台管理员不属于任何组织。
type PlatformLoginInput struct {
	Body struct {
		Username string `json:"username" minLength:"1" maxLength:"64" doc:"平台管理员账号"`
		Password string `json:"password" minLength:"1" maxLength:"200" doc:"密码"`
	}
	UserAgent string `header:"User-Agent" doc:"浏览器标识，进审计"`
}

// PlatformLoginOutput 带 Set-Cookie。
type PlatformLoginOutput struct {
	SetCookie []string `header:"Set-Cookie"`
	Body      httpx.Data[LoginResult]
}

// PlatformLogoutOutput 用过期 cookie 覆盖掉登录态。
type PlatformLogoutOutput struct {
	SetCookie []string `header:"Set-Cookie"`
	Body      httpx.Data[struct{}]
}

// PlatformMeResult 是「我是谁」。平台端**没有菜单树** ——
// 这一轮平台管理员即全权（§6），外壳里就那么两个页面，不值得走一套菜单机制。
type PlatformMeResult struct {
	User Identity `json:"user"`
}

// ListTenantsInput 是组织列表的查询条件。
type ListTenantsInput struct {
	Page     int    `query:"page" default:"1" minimum:"1"`
	PageSize int    `query:"page_size" default:"20" minimum:"1" maximum:"100"`
	Keyword  string `query:"keyword" maxLength:"100" doc:"按组织名或公司代码搜"`
	Status   string `query:"status" enum:",active,suspended"`
}

// CreateTenantInput 是开组织的入参。
type CreateTenantInput struct {
	Body struct {
		Code string `json:"code" minLength:"2" maxLength:"32" doc:"公司代码，客户登录时要填；只能小写字母、数字、中划线"`
		Name string `json:"name" minLength:"1" maxLength:"64" doc:"组织名"`
	}
}

// SetTenantStatusInput 是停用 / 启用的入参。
type SetTenantStatusInput struct {
	ID   string `path:"id" format:"uuid"`
	Body struct {
		Status  string `json:"status" enum:"active,suspended"`
		Version int    `json:"version" minimum:"0" doc:"乐观锁版本号"`
	}
}

// RegisterPlatform 注册平台管理端路由。
//
// ⚠️ 三档访问要求这里都用**平台版本**的注册器：`perm.PlatformAuthenticated`
// 和挂着 `Realm: platform` 权限点的 `perm.Guard`。授权中间件按
// 「路由的 Realm 必须和主体的 Realm 一致」判 —— 用错注册器的话，
// 租户用户就能打进来（§10.4）。
func RegisterPlatform(api huma.API, h *Platform) {
	perm.Public(api, huma.Operation{
		OperationID: "platform-login",
		Method:      http.MethodPost,
		Path:        PlatformPrefix + "/auth/login",
		Summary:     "平台管理员登录",
		Description: "和租户登录完全分开：独立的会话表、独立的 cookie、更严的失败节流。",
		Tags:        []string{platformTag},
	}, h.login)

	perm.PlatformAuthenticated(api, huma.Operation{
		OperationID: middleware.PlatformLogoutOperationID,
		Method:      http.MethodPost,
		Path:        PlatformPrefix + "/auth/logout",
		Summary:     "平台管理员退出登录",
		Tags:        []string{platformTag},
	}, h.logout)

	perm.PlatformAuthenticated(api, huma.Operation{
		OperationID: "platform-me",
		Method:      http.MethodGet,
		Path:        PlatformPrefix + "/me",
		Summary:     "当前平台管理员",
		Tags:        []string{platformTag},
	}, h.me)

	perm.PlatformAuthenticated(api, huma.Operation{
		OperationID: middleware.PlatformPasswordChangeOperationID,
		Method:      http.MethodPost,
		Path:        PlatformPrefix + "/me/password",
		Summary:     "平台管理员改自己的密码",
		Description: "平台走自己的强密码要求，不吃租户级的密码策略。",
		Tags:        []string{platformTag},
	}, h.changePassword)

	perm.Guard(api, modules.Tenant.Point(perm.ActionList), huma.Operation{
		OperationID: "list-tenants",
		Method:      http.MethodGet,
		Path:        PlatformPrefix + "/tenants",
		Summary:     "组织列表",
		Description: "人数来自 tenants.user_count 冗余列，平台端不查客户的业务表。",
		Tags:        []string{platformTag},
	}, h.listTenants)

	perm.Guard(api, modules.Tenant.Point(perm.ActionCreate), huma.Operation{
		OperationID:   "create-tenant",
		Method:        http.MethodPost,
		Path:          PlatformPrefix + "/tenants",
		Summary:       "开通组织",
		Description:   "同时建好这个组织的第一个管理员，返回只显示一次的初始密码。",
		DefaultStatus: http.StatusCreated,
		Tags:          []string{platformTag},
	}, h.createTenant)

	perm.Guard(api, modules.Tenant.Point("suspend"), huma.Operation{
		OperationID: "set-tenant-status",
		Method:      http.MethodPost,
		Path:        PlatformPrefix + "/tenants/{id}/status",
		Summary:     "停用 / 启用组织",
		Description: "停用立刻生效：那家公司已经发出去的会话下一个请求就失效。组织只停用，不删除。",
		Tags:        []string{platformTag},
	}, h.setTenantStatus)
}

func (h *Platform) login(ctx context.Context, in *PlatformLoginInput) (*PlatformLoginOutput, error) {
	// 登录失败也要留痕，所以这两行放在校验之前
	audit.SetAction(ctx, "platform", "login")
	audit.Detail(ctx, "username", in.Body.Username)

	result, err := h.auth.Login(ctx, auth.PlatformLoginInput{
		Username:  in.Body.Username,
		Password:  in.Body.Password,
		IP:        httpx.ClientIP(ctx),
		UserAgent: in.UserAgent,
	})
	if err != nil {
		return nil, err
	}

	// 登录成功之前上下文里还没有主体，这里补上，否则「谁登录了」会记成匿名
	audit.SetActor(ctx, audit.Actor{
		Type: audit.ActorPlatform,
		ID:   result.Principal.ID,
		Name: result.Principal.Name,
	})
	audit.SetResourceID(ctx, result.Principal.ID)

	cfg := h.auth.Config()
	return &PlatformLoginOutput{
		SetCookie: []string{
			cfg.SessionCookie(result.Token, result.ExpiresAt).String(),
			cfg.CSRFCookie(result.CSRFToken, result.ExpiresAt).String(),
		},
		Body: httpx.Data[LoginResult]{Data: LoginResult{
			User:               identityOf(result.Principal),
			CSRFToken:          result.CSRFToken,
			MustChangePassword: result.Principal.MustChangePassword,
			ExpiresAt:          result.ExpiresAt,
		}},
	}, nil
}

func (h *Platform) logout(ctx context.Context, _ *struct{}) (*PlatformLogoutOutput, error) {
	audit.SetAction(ctx, "platform", "logout")

	principal, ok := authz.PrincipalFrom(ctx)
	if !ok {
		return nil, errs.Unauthenticated
	}
	if err := h.auth.Logout(ctx, principal.SessionID); err != nil {
		return nil, err
	}

	cleared := h.auth.Config().ClearCookies()
	out := &PlatformLogoutOutput{SetCookie: make([]string, 0, len(cleared))}
	for _, c := range cleared {
		out.SetCookie = append(out.SetCookie, c.String())
	}
	return out, nil
}

func (h *Platform) me(ctx context.Context, _ *struct{}) (*httpx.Response[PlatformMeResult], error) {
	audit.SetAction(ctx, "platform", "me")

	principal, ok := authz.PrincipalFrom(ctx)
	if !ok {
		return nil, errs.Unauthenticated
	}
	return httpx.OK(PlatformMeResult{User: identityOf(principal)}), nil
}

func (h *Platform) changePassword(ctx context.Context, in *ChangePasswordInput) (*httpx.Response[struct{}], error) {
	audit.SetAction(ctx, "platform", "change_password")

	principal, ok := authz.PrincipalFrom(ctx)
	if !ok {
		return nil, errs.Unauthenticated
	}
	audit.SetResourceID(ctx, principal.ID)

	if err := h.auth.ChangePassword(ctx, principal.ID,
		in.Body.OldPassword, in.Body.NewPassword, principal.SessionID); err != nil {
		return nil, err
	}
	return httpx.OK(struct{}{}), nil
}

func (h *Platform) listTenants(ctx context.Context, in *ListTenantsInput) (*httpx.PageResponse[platformsvc.Tenant], error) {
	items, total, err := h.svc.List(ctx, platformsvc.ListFilter{
		Keyword:  in.Keyword,
		Status:   in.Status,
		Page:     in.Page,
		PageSize: in.PageSize,
	})
	if err != nil {
		return nil, err
	}
	return httpx.Paged(items, in.Page, in.PageSize, total), nil
}

func (h *Platform) createTenant(ctx context.Context, in *CreateTenantInput) (*httpx.Response[platformsvc.CreatedTenant], error) {
	audit.Detail(ctx, "code", in.Body.Code)
	audit.Detail(ctx, "name", in.Body.Name)

	created, err := h.svc.Create(ctx, in.Body.Code, in.Body.Name)
	if err != nil {
		return nil, err
	}
	return httpx.OK(created), nil
}

func (h *Platform) setTenantStatus(ctx context.Context, in *SetTenantStatusInput) (*httpx.Response[platformsvc.Tenant], error) {
	id, err := uuid.Parse(in.ID)
	if err != nil {
		return nil, errs.ValidationFailed.WithField("path.id", "不是合法的 UUID")
	}
	audit.Detail(ctx, "status", in.Body.Status)

	updated, err := h.svc.SetStatus(ctx, id, in.Body.Version, in.Body.Status)
	if err != nil {
		return nil, err
	}
	return httpx.OK(updated), nil
}
