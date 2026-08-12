package middleware

import (
	"context"
	"net/netip"
	"strings"

	"github.com/labstack/echo/v5"

	"github.com/ramoncjs3/fries/internal/auth"
	"github.com/ramoncjs3/fries/internal/authz"
	"github.com/ramoncjs3/fries/internal/httpx"
	"github.com/ramoncjs3/fries/internal/repo"
)

// HeaderAPIKey 是 Service Account 用的请求头（DECISIONS.md §8.1）。
const HeaderAPIKey = "X-API-Key"

// bearerPrefix 是 Authorization 头里 API Key 的前缀。
const bearerPrefix = "Bearer "

// Authenticator 是认证服务需要提供的能力。放接口后面，测试里好替换。
type Authenticator interface {
	AuthenticateSession(ctx context.Context, token string) (*authz.Principal, error)
	AuthenticateAPIKey(ctx context.Context, key string) (*authz.Principal, error)
	Config() auth.SessionConfig
}

type authErrorKey struct{}

// withAuthError 把认证失败原因挂进 context。
//
// **这里不直接拒绝**：公开接口（登录）本来就允许带着一张过期 cookie 进来。
// 到底要不要拒绝，由后面的授权中间件按接口的访问要求决定。
func withAuthError(ctx context.Context, err error) context.Context {
	return context.WithValue(ctx, authErrorKey{}, err)
}

// AuthError 取出认证失败原因，没有则返回 nil。
func AuthError(ctx context.Context) error {
	if ctx == nil {
		return nil
	}
	err, _ := ctx.Value(authErrorKey{}).(error)
	return err
}

// PlatformAuthenticator 是平台管理端的认证服务。
//
// 它和租户端是两套：独立的会话表、独立的 cookie 名（MULTI-TENANCY.md §10.1）。
// **cookie 名必须不同** —— 同名的话，一个人在同一浏览器里登了平台又登租户，
// 后登的会把先登的顶掉，两边都莫名其妙掉线。
type PlatformAuthenticator interface {
	AuthenticateSession(ctx context.Context, token string) (*authz.Principal, error)
	Config() auth.SessionConfig
}

// Authenticate 解析请求里的身份。**认哪套会话由请求路径决定**：
// /platform 前缀认平台会话，其余认 API Key 或租户会话。
//
// 解析成功就把主体放进 context；失败只记原因，不拦请求
// （公开接口本来就允许带着一张过期 cookie 进来，拦不拦由授权中间件决定）。
//
// 🔴 **不能写成「先看平台 cookie，有就用它」** —— 一个人在同一个浏览器里
// 同时登着平台端和某个租户是**设计上支持的**（两套 cookie 名不同，见 §10.1）。
// 按顺序挑的话，他访问租户接口时会被认成平台管理员，撞上授权那条 Realm 对齐，
// 拿到一个 perm.denied。而前端认不出这个码，只会把人踢回登录页 ——
// 表现是「登录成功了，但一进去就被弹回登录页」，死循环。浏览器实测踩到过：
// 平台管理员开完新组织，转头用客户的初始密码登录，就再也进不去了。
//
// Realm 对齐仍然守在授权那一层，但它是**兜底**：这里选对了，那条就永远不该触发。
func Authenticate(a Authenticator, platform PlatformAuthenticator, platformPathPrefix string) echo.MiddlewareFunc {
	cookieName := a.Config().CookieName
	platformCookieName := platform.Config().CookieName

	return func(next echo.HandlerFunc) echo.HandlerFunc {
		return func(c *echo.Context) error {
			req := c.Request()
			ctx := req.Context()

			if strings.HasPrefix(req.URL.Path, platformPathPrefix) {
				cookie, err := c.Cookie(platformCookieName)
				if err != nil || cookie.Value == "" {
					return next(c)
				}
				principal, err := platform.AuthenticateSession(ctx, cookie.Value)
				if err != nil {
					c.SetRequest(req.WithContext(withAuthError(ctx, err)))
					return next(c)
				}
				c.SetRequest(req.WithContext(withIdentity(ctx, principal)))
				return next(c)
			}

			if key := apiKeyOf(c); key != "" {
				principal, err := a.AuthenticateAPIKey(ctx, key)
				if err != nil {
					c.SetRequest(req.WithContext(withAuthError(ctx, err)))
					return next(c)
				}
				c.SetRequest(req.WithContext(withIdentity(ctx, principal)))
				return next(c)
			}

			cookie, err := c.Cookie(cookieName)
			if err != nil || cookie.Value == "" {
				return next(c)
			}

			principal, err := a.AuthenticateSession(ctx, cookie.Value)
			if err != nil {
				c.SetRequest(req.WithContext(withAuthError(ctx, err)))
				return next(c)
			}
			c.SetRequest(req.WithContext(withIdentity(ctx, principal)))
			return next(c)
		}
	}
}

// withIdentity 把认出来的主体挂进 context，顺手把租户分别捎给**日志**和**运行期兜底**。
//
// 三件事绑在一个函数里，是因为它们必须同时发生：三条认证路径（平台会话、API Key、
// 租户会话）各自 SetRequest 一次，漏掉哪一条，那条路径进来的请求就少一样东西 ——
// 日志里没有租户（§12.1，排障时最想知道的就是它），或者运行期兜底比不了值（§12.2）。
//
// ⚠️ 两个租户去处看着重复，其实不能合：`httpx` 是 HTTP 那一层，
// 而 `repo` 是数据层、不能 import 它（红线 #6）。所以各自有一个入口，
// **同一个值喂给两边**。
func withIdentity(ctx context.Context, p *authz.Principal) context.Context {
	ctx = authz.WithPrincipal(ctx, p)
	ctx = httpx.WithTenant(ctx, p.TenantID)
	return repo.WithRequestTenant(ctx, p.TenantID)
}

// apiKeyOf 从请求头里取 API Key，支持 X-API-Key 和 Authorization: Bearer 两种写法。
func apiKeyOf(c *echo.Context) string {
	if key := c.Request().Header.Get(HeaderAPIKey); key != "" {
		return key
	}
	authorization := c.Request().Header.Get("Authorization")
	if strings.HasPrefix(authorization, bearerPrefix) {
		return strings.TrimPrefix(authorization, bearerPrefix)
	}
	return ""
}

// clientIP 把 Echo 解析出的客户端 IP 转成可以入库的类型。
func clientIP(c *echo.Context) *netip.Addr {
	addr, err := netip.ParseAddr(c.RealIP())
	if err != nil {
		return nil
	}
	return &addr
}
