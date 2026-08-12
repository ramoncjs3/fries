package role

import (
	"testing"

	"github.com/ramoncjs3/fries/internal/errs"
	"github.com/ramoncjs3/fries/internal/perm"
)

// 这里注册两个只在测试里存在的模块。注册表是全局的，但每个测试二进制各跑各的，
// 不会影响启动自检（自检在 cmd/server 里跑）。
var (
	tenantModule = perm.Register(perm.Module{
		Key:     "demo_tenant",
		Name:    "示例业务",
		Realm:   perm.RealmTenant,
		Actions: []perm.Action{{Key: perm.ActionList, Name: "查询"}},
	})

	// platformModule 模拟平台管理端的模块（真正的那些在第 ⑤ 步）。
	platformModule = perm.Register(perm.Module{
		Key:     "demo_platform",
		Name:    "示例平台功能",
		Realm:   perm.RealmPlatform,
		Actions: []perm.Action{{Key: perm.ActionCreate, Name: "开通租户"}},
	})
)

// TestValidatePermissionsRejectsPlatformPoints 是 MULTI-TENANCY.md §3.2 ②⑤ 的守门测试。
//
// DECISIONS.md §3.5 承认了一条边界：能改用户角色 = 能把自己提成管理员。
// 单个组织内部这可以接受（人少、互相认识、且有审计）。
//
// **SaaS 下它仍然可以接受，但上限必须钉死在「本租户 admin」** ——
// 租户管理员用尽一切办法都不该够到平台管理员的权限。
// 这不是自然成立的：角色配置页的勾选树来自整个权限注册表，
// 平台模块一旦注册进去，不拦就能勾。
func TestValidatePermissionsRejectsPlatformPoints(t *testing.T) {
	// 正常的租户权限点当然要放行
	if err := validatePermissions([]string{tenantModule.Key + ":" + perm.ActionList}); err != nil {
		t.Fatalf("租户自己的权限点不该被拦：%v", err)
	}

	point := platformModule.Key + ":" + perm.ActionCreate
	err := validatePermissions([]string{point})
	if err == nil {
		t.Fatal("平台权限点被租户的角色勾上了 —— 租户管理员可以自我提权到平台管理员")
	}
	// 报「不存在」而不是「无权限」：回「无权限」等于告诉租户
	// 「平台确实有这么个权限点」，那是一个可以拿来摸清平台功能的探针（§11.2 同理）。
	coded, ok := errs.From(err)
	if !ok {
		t.Fatalf("应该是注册过的错误码，得到：%v", err)
	}
	if coded.Code != ErrUnknownPermission {
		t.Errorf("跨 Realm 的权限点应该报成 %s，得到 %s", ErrUnknownPermission.Code, coded.Code.Code)
	}
}
