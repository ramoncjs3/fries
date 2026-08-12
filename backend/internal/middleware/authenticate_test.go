package middleware_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/google/uuid"
	"github.com/labstack/echo/v5"

	"github.com/ramoncjs3/fries/internal/auth"
	"github.com/ramoncjs3/fries/internal/authz"
	"github.com/ramoncjs3/fries/internal/middleware"
)

// 一个人**同时登着平台端和某个租户**是设计上支持的（两套 cookie 名不同，§10.1）。
// 所以认哪套会话只能看请求路径，不能按 cookie 出现的顺序挑。
//
// 按顺序挑的后果不是「多认了一个身份」那么轻：租户请求被认成平台管理员之后，
// 授权那条 Realm 对齐会回 perm.denied，而前端认不出这个码，只会把人踢回登录页。
// 表现就是「登录明明成功了，一进去又被弹回登录页」—— 死循环，客户永远进不了系统。
func TestAuthenticatePicksSessionByPath(t *testing.T) {
	const (
		tenantCookie   = "fries_session"
		platformCookie = "fries_platform_session"
		platformPrefix = "/api/v1/platform"
	)

	tenantID := uuid.New()
	e := echo.New()
	e.Use(middleware.Authenticate(
		fakeAuthenticator{cookieName: tenantCookie, tenantID: tenantID},
		fakePlatformAuthenticator{cookieName: platformCookie},
		platformPrefix,
	))

	// handler 把认出来的主体如实报出来，中间件选了谁一目了然
	e.Any("/*", func(c *echo.Context) error {
		principal, ok := authz.PrincipalFrom(c.Request().Context())
		if !ok {
			return c.String(http.StatusOK, "none")
		}
		return c.String(http.StatusOK, string(principal.Realm()))
	})

	cases := []struct {
		name    string
		path    string
		cookies []string
		want    string
	}{
		{"两套都带着，走租户接口", "/api/v1/me", []string{tenantCookie, platformCookie}, "tenant"},
		{"两套都带着，走平台接口", "/api/v1/platform/tenants", []string{tenantCookie, platformCookie}, "platform"},
		{"只带租户的，走租户接口", "/api/v1/me", []string{tenantCookie}, "tenant"},
		{"只带平台的，走平台接口", "/api/v1/platform/tenants", []string{platformCookie}, "platform"},

		// 带错了就是没带 —— 认不出主体，由授权中间件按接口要求决定拒不拒
		{"只带平台的，走租户接口", "/api/v1/me", []string{platformCookie}, "none"},
		{"只带租户的，走平台接口", "/api/v1/platform/tenants", []string{tenantCookie}, "none"},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			req := httptest.NewRequestWithContext(t.Context(), http.MethodGet, c.path, nil)
			for _, name := range c.cookies {
				req.AddCookie(&http.Cookie{Name: name, Value: "valid-token"})
			}
			rec := httptest.NewRecorder()
			e.ServeHTTP(rec, req)

			if got := rec.Body.String(); got != c.want {
				t.Fatalf("认出来的应该是 %q，得到 %q", c.want, got)
			}
		})
	}
}

type fakeAuthenticator struct {
	cookieName string
	tenantID   uuid.UUID
}

func (f fakeAuthenticator) AuthenticateSession(_ context.Context, _ string) (*authz.Principal, error) {
	return &authz.Principal{
		Type: authz.PrincipalUser, ID: uuid.New(), TenantID: f.tenantID, Scope: authz.ScopeAll,
	}, nil
}

func (f fakeAuthenticator) AuthenticateAPIKey(context.Context, string) (*authz.Principal, error) {
	return nil, http.ErrNoCookie
}

func (f fakeAuthenticator) Config() auth.SessionConfig {
	return auth.SessionConfig{CookieName: f.cookieName}
}

type fakePlatformAuthenticator struct{ cookieName string }

func (fakePlatformAuthenticator) AuthenticateSession(context.Context, string) (*authz.Principal, error) {
	return &authz.Principal{Type: authz.PrincipalPlatform, ID: uuid.New(), Scope: authz.ScopeAll}, nil
}

func (f fakePlatformAuthenticator) Config() auth.SessionConfig {
	return auth.SessionConfig{CookieName: f.cookieName}
}
