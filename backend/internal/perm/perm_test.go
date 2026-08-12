package perm

import (
	"testing"
)

func demoModule() Module {
	return Module{
		Key:    "demo",
		Name:   "示例",
		Realm:  RealmTenant,
		Scoped: true,
		Menu:   Menu{Path: "/demo", Icon: "box", ShowIf: "list"},
		Actions: []Action{
			{Key: "list", Name: "查看列表"},
			{Key: "create", Name: "新增"},
		},
	}
}

func TestRegisterRejectsBadDeclarations(t *testing.T) {
	cases := map[string]func(Module) Module{
		"模块 key 不合法": func(m Module) Module { m.Key = "Demo-Module"; return m },
		"没有中文名":      func(m Module) Module { m.Name = ""; return m },
		"一个权限点都没有":   func(m Module) Module { m.Actions = nil; return m },
		"动作 key 不合法": func(m Module) Module { m.Actions[0].Key = "List"; return m },
		"动作重复":       func(m Module) Module { m.Actions[1].Key = "list"; return m },
		"权限点没中文名":    func(m Module) Module { m.Actions[0].Name = ""; return m },
		"没写 Realm":   func(m Module) Module { m.Realm = ""; return m },
		"Realm 不认识":  func(m Module) Module { m.Realm = "everything"; return m },
		"有菜单却没写 ShowIf": func(m Module) Module {
			m.Menu.ShowIf = ""
			return m
		},
		"ShowIf 指向不存在的权限点": func(m Module) Module {
			m.Menu.ShowIf = "nope"
			return m
		},
		"AI 工具没写描述": func(m Module) Module {
			m.Actions[0].AITool = true
			return m
		},
	}

	for name, mutate := range cases {
		t.Run(name, func(t *testing.T) {
			reset()
			defer func() {
				if recover() == nil {
					t.Fatalf("%s 必须 panic —— 权限配错了不如起不来", name)
				}
			}()
			Register(mutate(demoModule()))
		})
	}
}

func TestRegisterRejectsDuplicateModule(t *testing.T) {
	reset()
	Register(demoModule())

	defer func() {
		if recover() == nil {
			t.Fatal("同一个模块注册两次必须 panic")
		}
	}()
	Register(demoModule())
}

func TestPointPanicsOnUnknownAction(t *testing.T) {
	reset()
	m := Register(demoModule())

	if p := m.Point("list"); p.Resource != "demo" || p.Action != "list" || !p.Scoped {
		t.Fatalf("Point 返回不对：%+v", p)
	}

	defer func() {
		if recover() == nil {
			t.Fatal("取不存在的动作必须 panic —— 这是编码错误，不该等到运行时才发现")
		}
	}()
	m.Point("nope")
}

func TestMenuFollowsPermissions(t *testing.T) {
	reset()
	Register(demoModule())
	Register(Module{
		Key:     "hidden",
		Name:    "没有菜单的模块",
		Realm:   RealmTenant,
		Actions: []Action{{Key: "list", Name: "查看"}},
	})

	none := MenuFor(func(Point) bool { return false })
	if len(none) != 0 {
		t.Errorf("没权限就不该看到菜单，得到 %+v", none)
	}

	// 只有 create 权限：ShowIf 指的是 list，菜单仍然不该出现
	onlyCreate := MenuFor(func(p Point) bool { return p.Action == "create" })
	if len(onlyCreate) != 0 {
		t.Errorf("ShowIf 指定的权限点没有就不该显示菜单，得到 %+v", onlyCreate)
	}

	all := MenuFor(func(Point) bool { return true })
	if len(all) != 1 || all[0].Key != "demo" {
		t.Fatalf("有权限就该看到菜单，且没菜单的模块不出现：%+v", all)
	}
}

func TestMenuShowIfAny(t *testing.T) {
	reset()
	Register(Module{
		Key:   "settings",
		Name:  "系统设置",
		Realm: RealmTenant,
		Menu:  Menu{Path: "/settings", ShowIf: ShowIfAny},
		Actions: []Action{
			{Key: "read", Name: "查看"},
			{Key: "update", Name: "修改"},
		},
	})

	// 任意一个权限点即可（DECISIONS.md §3.4）
	items := MenuFor(func(p Point) bool { return p.Action == "update" })
	if len(items) != 1 {
		t.Fatalf("ShowIf=any 时有任意一个权限点就该显示菜单，得到 %+v", items)
	}
}

func TestPointsSortedAndComplete(t *testing.T) {
	reset()
	Register(demoModule())

	points := Points()
	if len(points) != 2 {
		t.Fatalf("应该有 2 个权限点，得到 %d", len(points))
	}
	if points[0].String() != "demo:create" || points[1].String() != "demo:list" {
		t.Errorf("Points 应按 resource:action 排序：%v %v", points[0], points[1])
	}
	if !Has("demo", "list") || Has("demo", "nope") || Has("nope", "list") {
		t.Error("Has 判断不对")
	}
}
