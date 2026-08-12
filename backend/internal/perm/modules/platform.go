package modules

import "github.com/ramoncjs3/fries/internal/perm"

// Tenant 是平台管理端的「组织管理」模块 —— **平台世界**的第一个模块。
//
// ⚠️ `Realm: perm.RealmPlatform` 是这里唯一要紧的一行：
//
//   - 租户的角色配置页只吐 RealmTenant 的模块，所以这几个权限点
//     **不会出现在客户的勾选树上**（§3.2 ②）
//   - 授权中间件按「路由的 Realm 必须和主体的 Realm 一致」判，
//     所以租户里那个拿通配 `*:*` 的 admin 也够不到这些接口（§10.4）
//
// 后面这条不是自然成立的：Casbin 匹配器里的 `p.obj == "*"` 对**任何**资源名都成立，
// 包括这个模块的。实测过 —— 不显式按 Realm 断掉的话，租户管理员能通过这里的判定。
//
// 这一轮平台管理员**即全权**（§6），权限点声明出来是为了让路由有东西可挂、
// 审计里能看出是什么动作；真需要给平台分权时再按 perm 那套细化。
var Tenant = perm.Register(perm.Module{
	Key:    "tenant",
	Name:   "组织管理",
	Realm:  perm.RealmPlatform,
	Scoped: false,
	// 平台端是另一套外壳，菜单不走租户的菜单树
	Actions: []perm.Action{
		{Key: perm.ActionList, Name: "查看组织"},
		{Key: perm.ActionCreate, Name: "开通组织"},
		// 只停用不删除（§9.3）：审计链要完整，误删无法恢复，
		// 而且「客户过两个月又回来了」这种事很常见。所以没有 delete。
		{Key: "suspend", Name: "停用 / 启用组织"},
	},
})

// PlatformSetting 是平台管理端的「平台设置」模块。
//
// 它管两类东西：
//
//	平台自己的配置    审计保留期、产品名 —— 对所有组织生效，没法按组织分
//	给租户划的上下界  租户能把密码策略调多松、锁定时间调多短（MULTI-TENANCY.md §10.5）
//
// 第二类是这个模块存在的主要理由：那些界原来只能上数据库改，
// 而 §10.5 承诺的是「接了合规要求更高的客户，平台整体收紧一档，不用发版」。
//
// 和 Tenant 模块一样，这一轮平台管理员**即全权**（§6），
// 权限点声明出来是为了路由有东西可挂、审计里看得出是什么动作。
var PlatformSetting = perm.Register(perm.Module{
	Key:    "platform_setting",
	Name:   "平台设置",
	Realm:  perm.RealmPlatform,
	Scoped: false,
	Actions: []perm.Action{
		{Key: perm.ActionList, Name: "查看平台设置"},
		{Key: perm.ActionUpdate, Name: "修改平台设置"},
	},
})
