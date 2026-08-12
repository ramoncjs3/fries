package middleware

import (
	"net/http"
	"strings"

	"github.com/labstack/echo/v5"

	"github.com/ramoncjs3/fries/internal/auth"
	"github.com/ramoncjs3/fries/internal/authz"
	"github.com/ramoncjs3/fries/internal/errs"
)

// CSRF 校验 double-submit token。
//
// **只对 cookie 认证生效。** Service Account 用 API Key 时没有 cookie，浏览器也不会
// 自动带上 API Key，本来就没有 CSRF 风险 —— 漏了这条豁免，外部系统对接会莫名其妙 403，
// 且很难排查（DECISIONS.md §6）。
// 两套会话都要管：租户端和平台端的 cookie 名不同，但 CSRF 的道理一样 ——
// 平台端尤其不能漏，它是最高价值的目标（§9.2）。
//
// ⚠️ **按请求打向哪一套接口挑配置，不是按当前主体挑。**
// 按主体挑会出这样的事（浏览器实测踩到的）：一个人在同一浏览器里登着平台端，
// 然后去登某个租户 —— 登录是公开接口，可它带着平台会话 cookie，
// 于是这里拿平台那套去校验，请求头里当然没有平台的 CSRF token，直接 403。
// 人看到的是「请求校验失败，请刷新页面」，刷多少次都没用。
//
// 两套 cookie 名不同正是为了让人能同时登着两边（§10.1），
// 那就不能让其中一边的存在把另一边的登录堵死。
func CSRF(cfg, platformCfg auth.SessionConfig, platformPathPrefix string) echo.MiddlewareFunc {
	return func(next echo.HandlerFunc) echo.HandlerFunc {
		return func(c *echo.Context) error {
			if isSafeMethod(c.Request().Method) {
				return next(c)
			}

			wantPlatform := strings.HasPrefix(c.Request().URL.Path, platformPathPrefix)
			active := cfg
			if wantPlatform {
				active = platformCfg
			}

			// 没带这一套的会话 cookie，就不是浏览器带着这套身份发起的请求，跳过。
			//
			// **Service Account 也走这条**：它用 API Key，没有 cookie，
			// 浏览器也不会自动带上 API Key，本来就没有 CSRF 风险 ——
			// 漏了这条豁免，外部系统对接会莫名其妙 403，且很难排查（DECISIONS.md §6）。
			if cookie, err := c.Cookie(active.CookieName); err != nil || cookie.Value == "" {
				return next(c)
			}

			principal, ok := authz.PrincipalFrom(c.Request().Context())
			if !ok {
				// cookie 无效时交给授权中间件报 401，这里不抢着报 CSRF 错
				return next(c)
			}
			// ⚠️ 认出来的主体可能来自**另一套**：浏览器里同时躺着两套 cookie 时，
			// 认证中间件先认出平台身份，而这个请求打的是租户接口。
			// 拿平台的会话 id 去验租户的 CSRF token 必然对不上 —— 那是一个假的 403，
			// 用户看到「请求校验失败，请刷新页面」，刷多少次都没用（浏览器实测踩过）。
			//
			// 两边对不上就交给授权中间件去判（它会按 Realm 对齐给出真正的原因）。
			if principal.IsPlatform() != wantPlatform {
				return next(c)
			}

			// **只认请求头。** 绝不能退回去读 CSRF cookie —— 跨站请求本来就会自动
			// 带上 cookie，从 cookie 取值等于把这道防线自己拆了。
			presented := c.Request().Header.Get(auth.HeaderCSRFToken)
			if !auth.VerifyCSRF(active.Secret, principal.SessionID, presented) {
				return errs.CSRFInvalid
			}
			return next(c)
		}
	}
}

// isSafeMethod 判断是不是只读方法。
func isSafeMethod(method string) bool {
	switch method {
	case http.MethodGet, http.MethodHead, http.MethodOptions:
		return true
	}
	return false
}
