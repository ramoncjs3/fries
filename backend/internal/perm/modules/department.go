package modules

import "github.com/ramoncjs3/fries/internal/perm"

// Department 是部门模块。
//
// Scoped: false —— 组织结构是**全公司共享**的，看得到部门管理的人就该看到整棵树。
// 只让人看到自己那一支的话，树是断的，根本没法用（DECISIONS.md §3.2）。
var Department = perm.Register(perm.Module{
	Key:    "department",
	Name:   "部门管理",
	Realm:  perm.RealmTenant,
	Scoped: false,
	Menu:   perm.Menu{Path: "/departments", Icon: "network", ShowIf: perm.ActionList, Order: 300},
	Actions: []perm.Action{
		{
			Key: perm.ActionList, Name: "查询",
			AITool: true,
			AIDesc: "查询部门树，可按名称、编号、状态筛选",
		},
		{Key: perm.ActionCreate, Name: "新增"},
		{Key: perm.ActionUpdate, Name: "编辑"},
		// 删除不给 AI：这类操作让模型自己决定太危险（DECISIONS.md §8.4）
		{Key: perm.ActionDelete, Name: "删除"},
	},
})
