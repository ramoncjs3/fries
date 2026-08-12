package handler

import (
	"context"
	"log/slog"
	"net/http"
	"time"

	"github.com/danielgtaylor/huma/v2"
	"github.com/google/uuid"

	"github.com/ramoncjs3/fries/internal/audit"
	"github.com/ramoncjs3/fries/internal/auth"
	"github.com/ramoncjs3/fries/internal/authz"
	"github.com/ramoncjs3/fries/internal/errs"
	"github.com/ramoncjs3/fries/internal/httpx"
	"github.com/ramoncjs3/fries/internal/middleware"
	"github.com/ramoncjs3/fries/internal/perm"
)

// tagAuth 是认证相关接口在 OpenAPI 里的分组名。
const tagAuth = "auth"

// Auth 是认证相关接口。**不写业务逻辑**，都在 internal/auth（红线 #6）。
type Auth struct {
	svc     *auth.Service
	checker authz.Checker
}

// NewAuth 造认证 handler。
func NewAuth(svc *auth.Service, checker authz.Checker) *Auth {
	return &Auth{svc: svc, checker: checker}
}

// Identity 是登录用户的基本信息，登录和 /me 都会返回它。
type Identity struct {
	ID          uuid.UUID `json:"id"`
	Username    string    `json:"username"`
	DisplayName string    `json:"display_name"`
	Roles       []string  `json:"roles" doc:"角色 key 列表"`
	Scope       string    `json:"scope" doc:"数据范围：all 看本组织全部，self 只看自己创建的"`
}

func identityOf(p *authz.Principal) Identity {
	roles := p.Roles
	if roles == nil {
		roles = []string{}
	}
	return Identity{
		ID:          p.ID,
		Username:    p.Name,
		DisplayName: p.DisplayName,
		Roles:       roles,
		Scope:       string(p.Scope),
	}
}

// LoginInput 是登录入参。
type LoginInput struct {
	Body struct {
		// 公司代码。账号只在**组织内**唯一，两家公司都可以有一个叫 admin 的人，
		// 所以登录必须先说清楚是哪一家（MULTI-TENANCY.md §4.1）。
		TenantCode string `json:"tenant_code" minLength:"2" maxLength:"32" doc:"公司代码"`
		// 登录标识符。现在支持用户名，邮箱和手机号的登录方式随用户管理模块一起开放；
		// 字段名从一开始就取中性名，将来不用改前端契约。
		Account  string `json:"account" minLength:"1" maxLength:"255" doc:"用户名 / 邮箱 / 手机号"`
		Password string `json:"password" minLength:"1" maxLength:"200" doc:"密码"`
	}
	UserAgent string `header:"User-Agent" doc:"浏览器标识，进审计"`
}

// LoginResult 是登录结果。**会话 token 不在响应体里**，只在 httpOnly cookie 里
// （DECISIONS.md §1：token 不进 localStorage）。
type LoginResult struct {
	User Identity `json:"user"`
	// CSRFToken 前端要放进后续写请求的 X-CSRF-Token 头。
	// 它同时写在一个非 httpOnly 的 cookie 里，方便前端读。
	CSRFToken string `json:"csrf_token"`
	// MustChangePassword 为 true 时前端要直接跳改密页。
	MustChangePassword bool      `json:"must_change_password"`
	ExpiresAt          time.Time `json:"expires_at"`
}

// LoginOutput 带 Set-Cookie。
type LoginOutput struct {
	SetCookie []string `header:"Set-Cookie"`
	Body      httpx.Data[LoginResult]
}

// LogoutOutput 只清 cookie。
type LogoutOutput struct {
	SetCookie []string `header:"Set-Cookie"`
	Body      httpx.Data[struct{}]
}

// MeResult 是「我是谁、我能干什么」。
//
// 菜单由后端算好，前端只渲染 —— 前后端不可能不一致（DECISIONS.md §3.6）。
type MeResult struct {
	User Identity `json:"user"`
	// Tenant 是「当前是哪家公司」。多租户下不显示它，人会迷路 ——
	// 尤其是同一个人在两家公司都有账号的时候（MULTI-TENANCY.md §7.4）。
	Tenant      auth.CurrentTenant `json:"tenant"`
	Permissions []string           `json:"permissions" doc:"拥有的权限点，形如 audit:list"`
	Menus       []perm.MenuItem    `json:"menus" doc:"过滤后的菜单树"`
}

// ChangePasswordInput 是改密码入参。
type ChangePasswordInput struct {
	Body struct {
		OldPassword string `json:"old_password" minLength:"1" maxLength:"200" doc:"原密码"`
		NewPassword string `json:"new_password" minLength:"1" maxLength:"200" doc:"新密码"`
	}
}

// RegisterAuth 注册认证相关路由。
//
// 三档访问要求都在这里出现了一次：登录是 Public，其余是 Authenticated。
// 需要权限点的接口用 perm.Guard（见 audit.go）。
func RegisterAuth(api huma.API, h *Auth) {
	perm.Public(api, huma.Operation{
		OperationID: "login",
		Method:      http.MethodPost,
		Path:        "/auth/login",
		Summary:     "登录",
		Description: "校验账号密码，成功后种下 httpOnly 会话 cookie 和 CSRF cookie。",
		Tags:        []string{tagAuth},
	}, h.login)

	perm.Authenticated(api, huma.Operation{
		OperationID: middleware.LogoutOperationID,
		Method:      http.MethodPost,
		Path:        "/auth/logout",
		Summary:     "登出",
		Description: "吊销当前会话并清除 cookie。服务端立即失效，不等 cookie 过期。",
		Tags:        []string{tagAuth},
	}, h.logout)

	perm.Authenticated(api, huma.Operation{
		OperationID: "me",
		Method:      http.MethodGet,
		Path:        "/me",
		Summary:     "当前用户与菜单",
		Description: "返回当前登录者、他拥有的权限点，以及过滤后的菜单树。",
		Tags:        []string{tagAuth},
	}, h.me)

	perm.Authenticated(api, huma.Operation{
		// 这个 OperationID 是「必须改密」状态下唯一放行的接口，改名要同步改
		// middleware.PasswordChangeOperationID。
		OperationID: middleware.PasswordChangeOperationID,
		Method:      http.MethodPost,
		Path:        "/me/password",
		Summary:     "修改自己的密码",
		Description: "改完会踢掉自己的其它会话，当前会话保留。",
		Tags:        []string{tagAuth},
	}, h.changePassword)

	// 忘记密码两条都是公开接口（用户还没登录）。
	perm.Public(api, huma.Operation{
		OperationID: "forgot-password",
		Method:      http.MethodPost,
		Path:        "/auth/forgot-password",
		Summary:     "忘记密码：申请重置邮件",
		Description: "按公司代码 + 标识找到用户，发一封带一次性重置链接的邮件。" +
			"⚠️ 无论账号是否存在都返回同一句成功（防用户枚举）。",
		Tags: []string{tagAuth},
	}, h.forgotPassword)

	perm.Public(api, huma.Operation{
		OperationID: "reset-password",
		Method:      http.MethodPost,
		Path:        "/auth/reset-password",
		Summary:     "忘记密码：用 token 设新密码",
		Description: "校验邮件里的一次性 token，设置新密码并踢掉该用户的全部会话。",
		Tags:        []string{tagAuth},
	}, h.resetPassword)
}

func (h *Auth) login(ctx context.Context, in *LoginInput) (*LoginOutput, error) {
	// 登录失败也要留痕，所以这两行放在校验之前（DECISIONS.md §6）。
	audit.SetAction(ctx, "auth", "login")
	audit.Detail(ctx, "account", in.Body.Account)
	audit.Detail(ctx, "tenant_code", in.Body.TenantCode)

	result, err := h.svc.Login(ctx, auth.LoginInput{
		TenantCode: in.Body.TenantCode,
		Account:    in.Body.Account,
		Password:   in.Body.Password,
		IP:         httpx.ClientIP(ctx),
		UserAgent:  in.UserAgent,
	})
	if err != nil {
		return nil, err
	}

	// 登录成功之前请求上下文里还没有主体，中间件读不到是谁 —— 这里补上，
	// 否则「谁登录了」这条最该看清楚的审计会记成匿名。
	audit.SetActor(ctx, audit.Actor{
		Type: audit.ActorUser,
		ID:   result.Principal.ID,
		Name: result.Principal.Name,
	})
	audit.SetResourceID(ctx, result.Principal.ID)

	cfg := h.svc.Config()
	return &LoginOutput{
		SetCookie: []string{
			cfg.SessionCookie(result.Token, result.ExpiresAt).String(),
			cfg.CSRFCookie(result.CSRFToken, result.ExpiresAt).String(),
		},
		Body: httpx.Data[LoginResult]{Data: LoginResult{
			User:               identityOf(result.Principal),
			CSRFToken:          result.CSRFToken,
			MustChangePassword: result.Principal.MustChangePassword || result.Principal.PasswordExpired,
			ExpiresAt:          result.ExpiresAt,
		}},
	}, nil
}

func (h *Auth) logout(ctx context.Context, _ *struct{}) (*LogoutOutput, error) {
	audit.SetAction(ctx, "auth", "logout")

	principal, ok := authz.PrincipalFrom(ctx)
	if !ok {
		return nil, errs.Unauthenticated
	}
	if err := h.svc.Logout(ctx, principal.SessionID); err != nil {
		return nil, err
	}

	cleared := h.svc.Config().ClearCookies()
	out := &LogoutOutput{SetCookie: make([]string, 0, len(cleared))}
	for _, c := range cleared {
		out.SetCookie = append(out.SetCookie, c.String())
	}
	return out, nil
}

func (h *Auth) me(ctx context.Context, _ *struct{}) (*httpx.Response[MeResult], error) {
	audit.SetAction(ctx, "auth", "me")

	principal, ok := authz.PrincipalFrom(ctx)
	if !ok {
		return nil, errs.Unauthenticated
	}

	points := h.checker.Points(principal)
	permissions := make([]string, 0, len(points))
	for _, p := range points {
		permissions = append(permissions, p.String())
	}

	menus := perm.MenuFor(func(p perm.Point) bool { return h.checker.Allow(principal, p) })

	tenant, err := h.svc.CurrentTenant(ctx)
	if err != nil {
		return nil, err
	}

	return httpx.OK(MeResult{
		User:        identityOf(principal),
		Tenant:      tenant,
		Permissions: permissions,
		Menus:       menus,
	}), nil
}

// ForgotPasswordInput 是「申请重置邮件」入参。
type ForgotPasswordInput struct {
	Body struct {
		TenantCode string `json:"tenant_code" minLength:"2" maxLength:"32" doc:"公司代码"`
		Identifier string `json:"identifier" minLength:"1" maxLength:"255" doc:"用户名 / 邮箱 / 手机号"`
	}
}

// ResetByTokenInput 是「用 token 设新密码」入参。
type ResetByTokenInput struct {
	Body struct {
		Token       string `json:"token" minLength:"1" maxLength:"200" doc:"重置邮件里的一次性 token"`
		NewPassword string `json:"new_password" minLength:"1" maxLength:"200" doc:"新密码"`
	}
}

func (h *Auth) forgotPassword(ctx context.Context, in *ForgotPasswordInput) (*httpx.Response[struct{}], error) {
	audit.SetAction(ctx, "auth", "forgot_password")
	audit.Detail(ctx, "tenant_code", in.Body.TenantCode)
	audit.Detail(ctx, "identifier", in.Body.Identifier)

	// ⚠️ 防用户枚举：无论用户存不存在、内部有没有出错，都回同一句话、同一个 200。
	// 内部错误只在服务端记日志，绝不能从响应上泄露「这个账号是不是存在」。
	if err := h.svc.RequestPasswordReset(ctx, auth.ResetRequestInput{
		TenantCode: in.Body.TenantCode,
		Identifier: in.Body.Identifier,
	}); err != nil {
		slog.ErrorContext(ctx, "处理忘记密码申请失败（对前端仍回成功，防枚举）",
			slog.String("error", err.Error()))
	}
	return httpx.OK(struct{}{}), nil
}

func (h *Auth) resetPassword(ctx context.Context, in *ResetByTokenInput) (*httpx.Response[struct{}], error) {
	audit.SetAction(ctx, "auth", "reset_password")
	if err := h.svc.ResetPassword(ctx, auth.ResetPasswordInput{
		Token:       in.Body.Token,
		NewPassword: in.Body.NewPassword,
	}); err != nil {
		return nil, err
	}
	return httpx.OK(struct{}{}), nil
}

func (h *Auth) changePassword(ctx context.Context, in *ChangePasswordInput) (*httpx.Response[struct{}], error) {
	audit.SetAction(ctx, "auth", "change_password")

	principal, ok := authz.PrincipalFrom(ctx)
	if !ok {
		return nil, errs.Unauthenticated
	}
	audit.SetResourceID(ctx, principal.ID)

	if err := h.svc.ChangePassword(ctx, principal.ID,
		in.Body.OldPassword, in.Body.NewPassword, principal.SessionID); err != nil {
		return nil, err
	}
	return httpx.OK(struct{}{}), nil
}
