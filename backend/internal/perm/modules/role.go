package modules

import "github.com/ramoncjs3/fries/internal/perm"

// Role 是角色管理模块。
//
// Scoped: false —— 角色是全局配置，不参与数据权限（DECISIONS.md §3.2）。
//
// **一个权限点都不给 AI**：改角色等于改所有人的权限边界，
// 这种事必须是人点的（DECISIONS.md §8.4）。
var Role = perm.Register(perm.Module{
	Key:    "role",
	Name:   "角色管理",
	Realm:  perm.RealmTenant,
	Scoped: false,
	Menu:   perm.Menu{Path: "/roles", Icon: "shield-check", ShowIf: perm.ActionList, Order: 200},
	Actions: []perm.Action{
		{Key: perm.ActionList, Name: "查询"},
		{Key: perm.ActionCreate, Name: "新增"},
		{Key: perm.ActionUpdate, Name: "编辑"},
		{Key: perm.ActionDelete, Name: "删除"},
	},
})
