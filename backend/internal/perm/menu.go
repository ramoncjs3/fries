package perm

import "sort"

// MenuItem 是菜单树上的一项。
//
// 菜单由后端算好再返回，前端只负责渲染 —— 前后端因此不可能不一致
// （DECISIONS.md §3.6 第 ① 层）。
type MenuItem struct {
	Key  string `json:"key" doc:"模块 key"`
	Name string `json:"name" doc:"菜单名"`
	Path string `json:"path" doc:"前端路由路径"`
	Icon string `json:"icon" doc:"图标名"`
}

// MenuFor 按「这个人有哪些权限点」过滤出他能看到的菜单。
//
// allowed 传进来的是判定函数，perm 包因此不依赖 authz。
func MenuFor(allowed func(Point) bool) []MenuItem {
	mu.RLock()
	defer mu.RUnlock()

	type entry struct {
		item  MenuItem
		order int
	}
	var entries []entry

	for _, key := range order {
		m := modules[key]
		if m.Menu.Path == "" {
			continue
		}
		if !menuVisible(m, allowed) {
			continue
		}
		entries = append(entries, entry{
			item: MenuItem{
				Key:  m.Key,
				Name: m.Name,
				Path: m.Menu.Path,
				Icon: m.Menu.Icon,
			},
			order: m.Menu.Order,
		})
	}

	sort.SliceStable(entries, func(i, j int) bool {
		if entries[i].order != entries[j].order {
			return entries[i].order < entries[j].order
		}
		return entries[i].item.Key < entries[j].item.Key
	})

	items := make([]MenuItem, 0, len(entries))
	for _, e := range entries {
		items = append(items, e.item)
	}
	return items
}

// menuVisible 判断一个模块的菜单该不该显示。
//
// ⚠️ Point **一律用 m.point(a) 造，别在这里手拼字段**。
// 这里原来是手拼的，给 Module 加 Realm 字段时就漏了这一处 —— 判定方拿到的
// Realm 是空串，于是「不是租户权限点」，**所有菜单一起消失**。
// 集成测试当场红了（「admin 应该看得到菜单」），但那正是文档警告过的现象：
// 登录进去什么都没有，很难第一时间想到是权限点少了个字段。
func menuVisible(m *Module, allowed func(Point) bool) bool {
	if m.Menu.ShowIf == ShowIfAny {
		for _, a := range m.Actions {
			if allowed(m.point(a)) {
				return true
			}
		}
		return false
	}
	for _, a := range m.Actions {
		if a.Key == m.Menu.ShowIf {
			return allowed(m.point(a))
		}
	}
	return false
}
