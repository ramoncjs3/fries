package modules

import "github.com/ramoncjs3/fries/internal/perm"

// User 是用户管理模块。
//
// Scoped: false —— 「只能看自己创建的用户」这种需求不存在；
// 能进用户管理的人就该看到全部人（DECISIONS.md §3.2）。
//
// **重置密码单独是一个权限点**，不并进 update：
// 能改个人显示名，和能把任何人的密码换掉，完全是两个量级的事。
var User = perm.Register(perm.Module{
	Key:    "user",
	Name:   "用户管理",
	Realm:  perm.RealmTenant,
	Scoped: false,
	Menu:   perm.Menu{Path: "/users", Icon: "users", ShowIf: perm.ActionList, Order: 100},
	Actions: []perm.Action{
		{
			Key: perm.ActionList, Name: "查询",
			AITool: true,
			AIDesc: "查询用户，可按用户名、显示名、邮箱、手机号、状态、部门筛选",
		},
		{Key: perm.ActionCreate, Name: "新增"},
		{Key: perm.ActionUpdate, Name: "编辑"},
		{Key: perm.ActionDelete, Name: "删除"},
		{Key: "reset_password", Name: "重置密码"},
	},
})
