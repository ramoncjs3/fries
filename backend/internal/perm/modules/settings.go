package modules

import "github.com/ramoncjs3/fries/internal/perm"

// SettingsSecurity 是「安全设置」模块 —— 租户自己的密码策略和登录锁定。
//
// ⚠️ **模块 key 现在就带分组后缀（`settings.security`），不叫 `settings`。**
//
// MODULES.md 写的是「`settings.*`（按分组拆分资源）」。先注册成 `settings`、
// 将来有第二组配置时再拆，会**改掉 Casbin 的资源名** —— 库里那些
// `role_permissions` 行还写着旧名字，于是所有勾过这个权限的角色一起失效。
// 而这类失败是**静默的**：页面上勾还在，实际什么都点不动。
// 和 §8.3 那个「索引改名让唯一冲突翻译退化成 500」是同一类。
//
// 反过来先带后缀，将来加 `settings.notify` 是纯增量，谁都不用动。
//
// Scoped: false —— 配置是共享资源，不参与数据权限（DECISIONS.md §3.2）。
// 「只能看自己创建的配置项」这种需求不存在。
var SettingsSecurity = perm.Register(perm.Module{
	Key:    "settings.security",
	Name:   "安全设置",
	Realm:  perm.RealmTenant,
	Scoped: false,
	Menu:   perm.Menu{Path: "/settings/security", Icon: "shield", ShowIf: perm.ActionList, Order: 800},
	Actions: []perm.Action{
		{
			Key: perm.ActionList, Name: "查看",
			AITool: true,
			AIDesc: "查看本组织的安全设置：密码策略、登录锁定阈值，以及平台允许的取值范围",
		},
		// 改密码策略是能影响全组织所有人的动作，单独一个权限点。
		// **不给 AI 工具**：危险操作不暴露给模型（DECISIONS.md §8.4）。
		{Key: perm.ActionUpdate, Name: "修改", Confirm: true},
	},
})
