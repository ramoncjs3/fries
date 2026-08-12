// Package modules 是所有权限模块的声明处。
//
// **一个模块一处声明**，产出菜单、Casbin 资源、角色配置页的勾选树、AI 工具列表
// （DECISIONS.md §3.1）。第 ⑤ 步的生成器会往这里加文件。
//
// 加模块只要在这里写一段 perm.Register，然后在 handler 里用 perm.Guard 注册路由；
// 启动自检会检查两边对得上（声明了却没接口 / 有接口却没声明，都起不来）。
package modules

import "github.com/ramoncjs3/fries/internal/perm"

// Audit 是审计日志模块。共享资源，不参与数据权限（Scoped: false）——
// 能看审计的人就该看得全。
//
// ⚠️ 「看得全」= **看得全本组织的**（MULTI-TENANCY.md §3.2 ⑥）。
// 别照着「给管理员看全」写出一条不带租户条件的审计查询 ——
// 那是把所有客户的操作记录端给一个人看。`tenant_id IS NULL` 的平台级记录
// 只有平台管理端查得到。
var Audit = perm.Register(perm.Module{
	Key:    "audit",
	Name:   "审计日志",
	Realm:  perm.RealmTenant,
	Scoped: false,
	Menu:   perm.Menu{Path: "/audit", Icon: "scroll-text", ShowIf: perm.ActionList, Order: 900},
	Actions: []perm.Action{
		{
			Key: perm.ActionList, Name: "查询",
			AITool: true,
			AIDesc: "查询审计日志，可按操作人、资源、动作、时间范围筛选",
		},
	},
})
