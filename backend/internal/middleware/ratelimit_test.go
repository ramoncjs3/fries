package middleware_test

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/google/uuid"
	"github.com/labstack/echo/v5"

	"github.com/ramoncjs3/fries/internal/authz"
	"github.com/ramoncjs3/fries/internal/middleware"
)

// TestTenantRateLimiterIsolatesTenants 是「限流按租户分桶」的守门测试
// （MULTI-TENANCY.md §3.2 ⑦、§12.3）。
//
// 只按 IP 限流是不够的：一家公司十个人十个 IP，每人都在自己的额度内，
// 加起来照样能把服务压住，而**别家客户没有任何办法**。这是 SaaS 最典型的
// 噪声邻居问题，OWASP 那份多租户清单里明确要求 per-tenant rate limiting。
//
// 断言的是「A 打爆之后 B 照样能用」——不是「A 会被挡」。
// 后者只证明限流器在工作，前者才证明它是**分桶**的。
func TestTenantRateLimiterIsolatesTenants(t *testing.T) {
	tenantA, tenantB := uuid.New(), uuid.New()
	e := newTenantLimitedEcho(t)

	// 先把 A 打爆。桶容量是有限的，打到被拒为止。
	var blocked bool
	for range 1000 {
		if statusFor(t, e, tenantA) == http.StatusTooManyRequests {
			blocked = true
			break
		}
	}
	if !blocked {
		t.Fatal("连打 1000 次都没被限流，租户维度的限流没生效")
	}

	// 关键一条：B 是另一家公司，不该被 A 牵连。
	if got := statusFor(t, e, tenantB); got != http.StatusOK {
		t.Fatalf("A 打爆之后 B 应该照常，得到 %d —— 限流没有按租户分桶，是共用一个桶", got)
	}
}

// TestTenantRateLimiterSkipsUnauthenticated 确认未认证请求不落进租户桶。
//
// 它们没有租户，落进去只能共用一个空串键 —— 那等于给所有未登录流量
// 加一个全局桶，登录接口会被互相挤掉。那一层归 IP 维度管。
func TestTenantRateLimiterSkipsUnauthenticated(t *testing.T) {
	e := newTenantLimitedEcho(t)

	for i := range 1000 {
		req := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/", nil)
		rec := httptest.NewRecorder()
		e.ServeHTTP(rec, req)
		if rec.Code != http.StatusOK {
			t.Fatalf("第 %d 次未认证请求被租户限流挡了（%d）—— 它没有租户，本该直接放过", i+1, rec.Code)
		}
	}
}

// newTenantLimitedEcho 造一个只装了租户限流的 echo。
//
// 主体由 query 参数 tenant 决定：这是为了在测试里**直接控制**中间件读到什么，
// 不用把整条认证链搭起来。
func newTenantLimitedEcho(t *testing.T) *echo.Echo {
	t.Helper()

	e := echo.New()
	e.Use(injectPrincipalFunc(func(c *echo.Context) *authz.Principal {
		id, err := uuid.Parse(c.QueryParam("tenant"))
		if err != nil {
			return nil // 没带 tenant 参数就是「未认证请求」
		}
		return &authz.Principal{
			Type: authz.PrincipalUser, ID: uuid.New(), TenantID: id, Scope: authz.ScopeAll,
		}
	}))
	e.Use(middleware.NewTenantRateLimiter().Middleware())
	e.Any("/*", func(c *echo.Context) error { return c.String(http.StatusOK, "ok") })
	return e
}

func statusFor(t *testing.T, e *echo.Echo, tenantID uuid.UUID) int {
	t.Helper()

	req := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/?tenant="+tenantID.String(), nil)
	rec := httptest.NewRecorder()
	e.ServeHTTP(rec, req)
	return rec.Code
}
