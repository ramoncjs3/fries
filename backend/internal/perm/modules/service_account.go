package modules

import "github.com/ramoncjs3/fries/internal/perm"

// ServiceAccount 是机器账号模块 —— 外部系统拿 API Key 调进来的那种身份
// （DECISIONS.md §8.1）。
//
// Scoped: false —— 「只能看自己建的机器账号」这种需求不存在；
// 能进这个页面的人就该看到全部（同 user、role）。
//
// ⚠️ **rotate_key 单独一个权限点**，不并进 update。理由和用户模块里
// 「重置密码」不并进 update 是同一条：能改个描述，和能**当场作废对方正在用的凭据**，
// 完全是两个量级的事 —— 后者会让对接方的系统立刻开始报 401。
var ServiceAccount = perm.Register(perm.Module{
	Key:    "service_account",
	Name:   "机器账号",
	Realm:  perm.RealmTenant,
	Scoped: false,
	Menu:   perm.Menu{Path: "/service-accounts", Icon: "key-round", ShowIf: perm.ActionList, Order: 300},
	Actions: []perm.Action{
		{
			Key: perm.ActionList, Name: "查询",
			AITool: true,
			AIDesc: "查询机器账号，可按名称、说明、状态筛选。返回里没有密钥 —— 密钥只在创建和轮换时出现一次",
		},
		{Key: perm.ActionCreate, Name: "新增"},
		{Key: perm.ActionUpdate, Name: "编辑"},
		{Key: perm.ActionDelete, Name: "删除"},
		// **不给 AI 工具**：危险操作不暴露给模型（DECISIONS.md §8.4）
		{Key: "rotate_key", Name: "轮换密钥", Confirm: true},
	},
})
