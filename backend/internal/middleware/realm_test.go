package middleware_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/danielgtaylor/huma/v2"
	"github.com/danielgtaylor/huma/v2/adapters/humago"
	"github.com/google/uuid"

	"github.com/ramoncjs3/fries/internal/authz"
	"github.com/ramoncjs3/fries/internal/middleware"
	"github.com/ramoncjs3/fries/internal/perm"
)

// 这一组直接测**授权中间件那条 Realm 对齐**（MULTI-TENANCY.md §10.4）。
//
// 为什么不在集成测试里测：那里两条防线叠着 —— 中间件放行了，
// service 层的 MustTenant 也会把平台主体挡在租户接口外面，
// 于是「去掉中间件那条」看不出任何差别（实测过：变异之后集成测试照样绿）。
//
// 这里的 handler 只要跑到就返回 200，所以中间件放没放行是**直接可见**的。

var (
	realmTenantModule = perm.Register(perm.Module{
		Key:     "realm_tenant_demo",
		Name:    "租户侧示例",
		Realm:   perm.RealmTenant,
		Actions: []perm.Action{{Key: perm.ActionList, Name: "查询"}},
	})
	realmPlatformModule = perm.Register(perm.Module{
		Key:     "realm_platform_demo",
		Name:    "平台侧示例",
		Realm:   perm.RealmPlatform,
		Actions: []perm.Action{{Key: perm.ActionList, Name: "查询"}},
	})
)

// allowAll 是一个什么都放行的 Checker —— 这样测出来的差别只可能来自 Realm 对齐。
type allowAll struct{}

func (allowAll) Allow(*authz.Principal, perm.Point) bool { return true }
func (allowAll) Points(*authz.Principal) []perm.Point    { return nil }
func (allowAll) Identity(uuid.UUID) ([]string, authz.Scope, bool) {
	return nil, authz.ScopeAll, false
}
func (allowAll) Reload(context.Context) error { return nil }

func TestAuthorizeRequiresMatchingRealm(t *testing.T) {
	mux := http.NewServeMux()
	api := humago.New(mux, huma.DefaultConfig("test", "1"))
	api.UseMiddleware(middleware.Authorize(api, allowAll{}))

	hit := func(_ context.Context, _ *struct{}) (*struct{ Body struct{} }, error) {
		return &struct{ Body struct{} }{}, nil
	}
	perm.Guard(api, realmTenantModule.Point(perm.ActionList), huma.Operation{
		OperationID: "realm-tenant-route", Method: http.MethodGet, Path: "/tenant-side",
	}, hit)
	perm.Guard(api, realmPlatformModule.Point(perm.ActionList), huma.Operation{
		OperationID: "realm-platform-route", Method: http.MethodGet, Path: "/platform-side",
	}, hit)

	tenantUser := &authz.Principal{
		Type: authz.PrincipalUser, ID: uuid.New(), TenantID: uuid.New(), Scope: authz.ScopeAll,
	}
	platformAdmin := &authz.Principal{
		Type: authz.PrincipalPlatform, ID: uuid.New(), Scope: authz.ScopeAll,
	}

	cases := []struct {
		name      string
		path      string
		principal *authz.Principal
		want      int
	}{
		{"租户用户走租户接口", "/tenant-side", tenantUser, http.StatusOK},
		{"平台管理员走平台接口", "/platform-side", platformAdmin, http.StatusOK},
		{
			// 拿别人的组织开关不了
			"租户用户走平台接口", "/platform-side", tenantUser, http.StatusForbidden,
		},
		{
			// 这一条是「平台端结构上碰不到客户数据」的运行期那一半（§10.11）
			"平台管理员走租户接口", "/tenant-side", platformAdmin, http.StatusForbidden,
		},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			req := httptest.NewRequestWithContext(
				authz.WithPrincipal(t.Context(), c.principal), http.MethodGet, c.path, nil)
			rec := httptest.NewRecorder()
			mux.ServeHTTP(rec, req)

			if rec.Code != c.want {
				t.Fatalf("期望 %d，得到 %d：%s", c.want, rec.Code, rec.Body)
			}
		})
	}
}
