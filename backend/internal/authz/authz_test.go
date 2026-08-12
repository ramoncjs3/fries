package authz_test

import (
	"context"
	"testing"

	"github.com/google/uuid"

	"github.com/ramoncjs3/fries/internal/authz"
	"github.com/ramoncjs3/fries/internal/errs"
	"github.com/ramoncjs3/fries/internal/perm"
)

// 测试用的模块。perm 注册表是全局的，key 取得独特些，别和真模块撞。
var (
	scopedModule = perm.Register(perm.Module{
		Key:     "authztest_orders",
		Name:    "测试订单",
		Realm:   perm.RealmTenant,
		Scoped:  true,
		Actions: []perm.Action{{Key: "list", Name: "查看"}, {Key: "delete", Name: "删除"}},
	})
	// platformModule 模拟平台管理端的模块（真正的那些在第 ⑤ 步）。
	// 它的权限点绝不该出现在任何租户的角色里。
	platformModule = perm.Register(perm.Module{
		Key:     "tenant_admin",
		Name:    "租户管理",
		Realm:   perm.RealmPlatform,
		Actions: []perm.Action{{Key: "create", Name: "开通租户"}},
	})

	sharedModule = perm.Register(perm.Module{
		Key:     "authztest_settings",
		Name:    "测试设置",
		Realm:   perm.RealmTenant,
		Scoped:  false,
		Actions: []perm.Action{{Key: "read", Name: "查看"}},
	})
)

type staticSource struct{ policy authz.Policy }

func (s staticSource) LoadPolicy(context.Context) (authz.Policy, error) { return s.policy, nil }

func TestWidestScope(t *testing.T) {
	if authz.Widest(authz.ScopeSelf, authz.ScopeAll) != authz.ScopeAll {
		t.Error("多角色应取最宽的数据范围")
	}
	if authz.Widest(authz.ScopeSelf, authz.ScopeSelf) != authz.ScopeSelf {
		t.Error("都是 self 就该是 self")
	}
	if authz.Widest() != authz.ScopeSelf {
		t.Error("没有角色时应保守取 self")
	}
}

func TestMustScopeDeniesWithoutPrincipal(t *testing.T) {
	// **默认拒绝**：context 里没有主体就报错，而不是放行（DECISIONS.md §3.3）
	if _, err := authz.MustScope(context.Background(), scopedModule.Key); err == nil {
		t.Fatal("没有认证主体时取数据范围必须报错")
	} else if e, ok := errs.From(err); !ok || e.Code != errs.ScopeDenied {
		t.Errorf("错误码应是 %s，得到 %v", errs.ScopeDenied.Code, err)
	}

	if _, err := authz.OwnerFilter(context.Background(), scopedModule.Key); err == nil {
		t.Fatal("OwnerFilter 同样必须默认拒绝")
	}
}

func TestOwnerFilterFollowsScope(t *testing.T) {
	userID := uuid.New()

	self := authz.WithPrincipal(context.Background(), &authz.Principal{
		Type: authz.PrincipalUser, ID: userID, Scope: authz.ScopeSelf,
	})
	owner, err := authz.OwnerFilter(self, scopedModule.Key)
	if err != nil {
		t.Fatal(err)
	}
	if owner == nil || *owner != userID {
		t.Errorf("self 范围应过滤成自己：%v", owner)
	}

	all := authz.WithPrincipal(context.Background(), &authz.Principal{
		Type: authz.PrincipalUser, ID: userID, Scope: authz.ScopeAll,
	})
	owner, err = authz.OwnerFilter(all, scopedModule.Key)
	if err != nil {
		t.Fatal(err)
	}
	if owner != nil {
		t.Errorf("all 范围不该过滤：%v", owner)
	}

	// 共享资源不参与数据权限，即使主体是 self
	owner, err = authz.OwnerFilter(self, sharedModule.Key)
	if err != nil {
		t.Fatal(err)
	}
	if owner != nil {
		t.Errorf("共享资源不该被 scope 过滤：%v", owner)
	}
}

func TestCheckerEnforcesPolicies(t *testing.T) {
	tenant := uuid.New()
	viewer := uuid.New()
	admin := uuid.New()
	stranger := uuid.New()

	checker, err := authz.NewCasbinChecker(t.Context(), staticSource{policy: authz.Policy{
		Grants: []authz.Grant{
			{TenantID: tenant, RoleKey: "viewer", Resource: scopedModule.Key, Action: "list"},
			{TenantID: tenant, RoleKey: "admin", Resource: perm.Wildcard, Action: perm.Wildcard},
		},
		Bindings: []authz.Binding{
			{TenantID: tenant, SubjectID: viewer, RoleKey: "viewer", Scope: authz.ScopeSelf},
			{TenantID: tenant, SubjectID: admin, RoleKey: "admin", Scope: authz.ScopeAll},
		},
	}})
	if err != nil {
		t.Fatal(err)
	}

	viewerP := &authz.Principal{Type: authz.PrincipalUser, ID: viewer, TenantID: tenant}
	adminP := &authz.Principal{Type: authz.PrincipalUser, ID: admin, TenantID: tenant}
	strangerP := &authz.Principal{Type: authz.PrincipalUser, ID: stranger, TenantID: tenant}

	if !checker.Allow(viewerP, scopedModule.Point("list")) {
		t.Error("viewer 应该能看列表")
	}
	if checker.Allow(viewerP, scopedModule.Point("delete")) {
		t.Error("viewer 不该能删除")
	}
	if !checker.Allow(adminP, scopedModule.Point("delete")) {
		t.Error("通配角色应该什么都能干")
	}
	if checker.Allow(strangerP, scopedModule.Point("list")) {
		t.Error("没有角色的人什么都不该能干")
	}
	if checker.Allow(nil, scopedModule.Point("list")) {
		t.Error("空主体必须判否")
	}

	// 请求侧不许出现通配符，否则等于自封超级管理员
	if checker.Allow(viewerP, perm.Point{Resource: perm.Wildcard, Action: perm.Wildcard}) {
		t.Error("请求侧的通配符必须判否")
	}

	roles, scope, found := checker.Identity(admin)
	if !found || len(roles) != 1 || roles[0] != "admin" || scope != authz.ScopeAll {
		t.Errorf("Identity 返回不对：%v %v %v", roles, scope, found)
	}
	if _, scope, found := checker.Identity(stranger); found || scope != authz.ScopeSelf {
		t.Error("没有绑定的主体应该退化成最窄的数据范围")
	}
}

// TestCheckerIsolatesTenantsByDomain 是 §3.1 那条要求的守门测试。
//
// 两家公司**各有一个叫 admin 的角色**，一个是通配 `*:*`，一个只能看列表。
// 不带 domain 的话它们在 Casbin 策略表里就是同一条 `p, admin, ...`，
// B 公司那个受限的 admin 会白捡 A 公司的全部权限。
func TestCheckerIsolatesTenantsByDomain(t *testing.T) {
	tenantA, tenantB := uuid.New(), uuid.New()
	adminA, adminB := uuid.New(), uuid.New()

	checker, err := authz.NewCasbinChecker(t.Context(), staticSource{policy: authz.Policy{
		Grants: []authz.Grant{
			// 同名角色，权限完全不同
			{TenantID: tenantA, RoleKey: "admin", Resource: perm.Wildcard, Action: perm.Wildcard},
			{TenantID: tenantB, RoleKey: "admin", Resource: scopedModule.Key, Action: "list"},
		},
		Bindings: []authz.Binding{
			{TenantID: tenantA, SubjectID: adminA, RoleKey: "admin", Scope: authz.ScopeAll},
			{TenantID: tenantB, SubjectID: adminB, RoleKey: "admin", Scope: authz.ScopeAll},
		},
	}})
	if err != nil {
		t.Fatal(err)
	}

	pA := &authz.Principal{Type: authz.PrincipalUser, ID: adminA, TenantID: tenantA}
	pB := &authz.Principal{Type: authz.PrincipalUser, ID: adminB, TenantID: tenantB}

	// A 公司的 admin 是通配的，什么都能干
	if !checker.Allow(pA, scopedModule.Point("delete")) {
		t.Error("A 公司的通配 admin 应该什么都能干 —— 匹配器里那两段 p.obj == \"*\" 是不是丢了？")
	}
	// B 公司的 admin 只配了 list，**不该**捡到 A 公司同名角色的通配权限
	if checker.Allow(pB, scopedModule.Point("delete")) {
		t.Fatal("B 公司的 admin 捡到了 A 公司同名角色的权限 —— domain 没起作用")
	}
	if !checker.Allow(pB, scopedModule.Point("list")) {
		t.Error("B 公司的 admin 应该能看列表")
	}

	// 换个租户冒充：拿 A 的人配 B 的 domain，什么都不该有
	spoof := &authz.Principal{Type: authz.PrincipalUser, ID: adminA, TenantID: tenantB}
	if checker.Allow(spoof, scopedModule.Point("list")) {
		t.Fatal("A 公司的人挂着 B 公司的 domain 居然有权限 —— 角色绑定没带 domain")
	}

	// 没有租户的主体一律判否：判不了就拒绝，不能默认放行
	noTenant := &authz.Principal{Type: authz.PrincipalUser, ID: adminA}
	if checker.Allow(noTenant, scopedModule.Point("list")) {
		t.Fatal("没有租户的主体必须判否")
	}
}

// TestCheckerRejectsPlatformPointInTenantRole 是 §3.2 ②⑤ 的守门测试。
//
// 有人绕过应用直接往 role_permissions 里插了一条平台权限点 ——
// 加载策略时必须直接失败，绝不能让它进 enforcer。
func TestCheckerRejectsPlatformPointInTenantRole(t *testing.T) {
	tenant := uuid.New()
	_, err := authz.NewCasbinChecker(t.Context(), staticSource{policy: authz.Policy{
		Grants: []authz.Grant{
			{TenantID: tenant, RoleKey: "admin", Resource: platformModule.Key, Action: "create"},
		},
	}})
	if err == nil {
		t.Fatal("租户角色里出现平台权限点，加载策略必须失败")
	}
}

// TestWildcardAdminCannotReachPlatformPoints 是 §3.2 ②⑤ 那条上限的守门测试。
//
// ⚠️ 这一条是 review 时用探针**实测出来的洞**，不是照着文档补的：
// 内置 admin 的策略是一条 `*, *`，而匹配器里的 `p.obj == "*"` 对任何 obj 都成立 ——
// 包括平台模块的资源名。也就是说租户里那个通配 admin **本来能通过平台接口的权限判定**。
//
// 现在 Allow 里显式按 Realm 断掉了。这条测试就是防它被人「简化」掉。
func TestWildcardAdminCannotReachPlatformPoints(t *testing.T) {
	tenant, admin := uuid.New(), uuid.New()

	checker, err := authz.NewCasbinChecker(t.Context(), staticSource{policy: authz.Policy{
		Grants: []authz.Grant{
			{TenantID: tenant, RoleKey: "admin", Resource: perm.Wildcard, Action: perm.Wildcard},
		},
		Bindings: []authz.Binding{
			{TenantID: tenant, SubjectID: admin, RoleKey: "admin", Scope: authz.ScopeAll},
		},
	}})
	if err != nil {
		t.Fatal(err)
	}

	p := &authz.Principal{Type: authz.PrincipalUser, ID: admin, TenantID: tenant}

	// 本租户的东西照样全都能干 —— 别把通配匹配一起弄坏了
	if !checker.Allow(p, scopedModule.Point("delete")) {
		t.Fatal("租户里的通配 admin 应该什么都能干")
	}
	// 但平台的权限点一个都够不到
	if checker.Allow(p, platformModule.Point("create")) {
		t.Fatal("租户的通配 admin 通过了平台权限点的判定 —— 租户管理员可以自我提权到平台管理员")
	}
	// 权限清单里也不该出现平台的点（/me 和菜单都读它）
	for _, point := range checker.Points(p) {
		if point.Realm == perm.RealmPlatform {
			t.Fatalf("权限清单里漏出了平台权限点 %s", point)
		}
	}
}
