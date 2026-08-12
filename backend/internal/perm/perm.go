// Package perm 是权限点注册表：菜单权限、API 权限、数据权限**是同一份声明的三个投影面**
// （DECISIONS.md §3）。
//
// 一个模块声明一次：
//
//	var AuditModule = perm.Register(perm.Module{
//	    Key: "audit", Name: "审计日志", Scoped: false,
//	    Menu: perm.Menu{Path: "/audit", Icon: "scroll-text", ShowIf: "list"},
//	    Actions: []perm.Action{{Key: "list", Name: "查询"}},
//	})
//
// 就同时得到：Casbin 的资源清单、菜单树、角色配置页的勾选树、将来 MCP 的工具列表。
package perm

import (
	"fmt"
	"regexp"
	"sort"
	"strings"
	"sync"
)

// ShowIfAny 用在 Menu.ShowIf 上：拥有该模块任意一个权限点就显示菜单。
const ShowIfAny = "any"

// 标准 CRUD 动作。**新模块优先用这几个**，别自己发明 query / add / edit / remove ——
// 动作 key 会进 Casbin 策略和审计记录，各模块叫法不一样的话根本没法统一查。
const (
	ActionList   = "list"
	ActionCreate = "create"
	ActionUpdate = "update"
	ActionDelete = "delete"
)

// Wildcard 是通配权限点，只给内置的超级管理员角色用 —— 以后新增模块自动覆盖，
// 不用记得回来补授权。
//
// ⚠️ 它是**租户内**的通配：内置 admin 拿到的是「本组织所有权限」，
// 永远够不到平台管理端的权限点（那些是 RealmPlatform，见下）。
const Wildcard = "*"

// Realm 区分「这个权限点属于谁的世界」（MULTI-TENANCY.md §3.2 ②）。
//
// 不分的话会出事：角色配置页直接吐整个注册表（这是 DECISIONS.md §3.7 的设计，
// 好处是不会漏），平台管理端的模块（开租户、停租户）一旦注册进同一张表，
// **租户管理员在自己的角色页上就能看到并勾上它们**。
type Realm string

const (
	// RealmTenant 是租户世界的权限点：用户、角色、部门、审计……绝大多数模块。
	RealmTenant Realm = "tenant"
	// RealmPlatform 是平台管理端的权限点：开租户、停租户、看平台配置。
	// **只有平台管理员碰得到**，租户的角色配置页看不见它们。
	RealmPlatform Realm = "platform"
)

// Valid 判断是不是合法的 Realm。
func (r Realm) Valid() bool { return r == RealmTenant || r == RealmPlatform }

var (
	moduleKeyPattern = regexp.MustCompile(`^[a-z][a-z0-9_]*(\.[a-z][a-z0-9_]*)*$`)
	actionKeyPattern = regexp.MustCompile(`^[a-z][a-z0-9_]*$`)
)

// Menu 是模块在左侧菜单上的样子。Path 留空表示这个模块不出现在菜单里。
type Menu struct {
	// Path 是前端路由路径，如 /audit。
	Path string
	// Icon 是 lucide 图标名。
	Icon string
	// ShowIf 是「有这个权限点才显示菜单」；填 ShowIfAny 表示有任意一个即可。
	ShowIf string
	// Order 决定菜单排序，小的在前。
	Order int
}

// Action 是模块下的一个动作，也就是一个权限点。
type Action struct {
	// Key 形如 list / read / create / update / delete / export。
	Key string
	// Name 是勾选框上显示的中文名。
	Name string
	// AITool 表示这个动作要不要暴露成 AI/MCP 工具（第 ⑥ 步用）。
	// **危险操作不给 AI**（DECISIONS.md §8.4）。
	AITool bool
	// AIDesc 是给模型看的工具描述，AITool 为 true 时必填。
	AIDesc string
	// Confirm 表示 AI 调用前要不要让人确认。
	Confirm bool
}

// Module 是一个权限模块。
type Module struct {
	// Key 是模块标识，同时是错误码前缀、Casbin 资源名、前端目录名。
	// 可以带点做分组，如 settings.security（DECISIONS.md §3.4）。
	Key string
	// Name 是中文名。
	Name string
	// Realm 是这个模块属于租户世界还是平台管理端（MULTI-TENANCY.md §3.2 ②）。
	//
	// **必填，没有默认值。** 这是个安全边界，忘了填就该起不来 ——
	// 默认成 tenant 的话，某天有人写平台模块忘了填，租户管理员当场就能勾上它。
	Realm Realm
	// Menu 是菜单信息。
	Menu Menu
	// Scoped 表示这个模块参与数据权限；共享资源（角色、设置）填 false。
	Scoped bool
	// Actions 是这个模块下的全部权限点。
	Actions []Action
}

// Point 是一个具体的权限点，即「模块 + 动作」。
type Point struct {
	Resource string
	Action   string
	Name     string
	Scoped   bool
	Realm    Realm
}

// String 返回 `resource:action` 形式，日志和审计里用它。
func (p Point) String() string { return p.Resource + ":" + p.Action }

var (
	mu      sync.RWMutex
	modules = map[string]*Module{}
	order   []string
)

// Register 注册一个模块。声明有问题就 panic —— 权限配错了不如起不来。
func Register(m Module) *Module {
	if !moduleKeyPattern.MatchString(m.Key) {
		panic(fmt.Sprintf("perm: 模块 key %q 不合法，要求小写下划线，可用点分组", m.Key))
	}
	if strings.TrimSpace(m.Name) == "" {
		panic(fmt.Sprintf("perm: 模块 %q 缺中文名", m.Key))
	}
	if len(m.Actions) == 0 {
		panic(fmt.Sprintf("perm: 模块 %q 一个权限点都没有", m.Key))
	}
	if !m.Realm.Valid() {
		panic(fmt.Sprintf("perm: 模块 %q 必须写 Realm（perm.RealmTenant 或 perm.RealmPlatform）—— "+
			"它决定这个模块的权限点会不会出现在租户的角色配置页上，没有默认值", m.Key))
	}

	seen := map[string]bool{}
	for _, a := range m.Actions {
		if !actionKeyPattern.MatchString(a.Key) {
			panic(fmt.Sprintf("perm: 模块 %q 的动作 key %q 不合法", m.Key, a.Key))
		}
		if seen[a.Key] {
			panic(fmt.Sprintf("perm: 模块 %q 的动作 %q 重复声明", m.Key, a.Key))
		}
		seen[a.Key] = true
		if strings.TrimSpace(a.Name) == "" {
			panic(fmt.Sprintf("perm: 权限点 %s:%s 缺中文名", m.Key, a.Key))
		}
		if a.AITool && strings.TrimSpace(a.AIDesc) == "" {
			panic(fmt.Sprintf("perm: 权限点 %s:%s 要暴露成 AI 工具，必须写 AIDesc", m.Key, a.Key))
		}
	}

	if m.Menu.Path != "" {
		switch {
		case m.Menu.ShowIf == "":
			panic(fmt.Sprintf("perm: 模块 %q 有菜单就必须写 ShowIf，否则没权限的人也看得到", m.Key))
		case m.Menu.ShowIf == ShowIfAny:
		case !seen[m.Menu.ShowIf]:
			panic(fmt.Sprintf("perm: 模块 %q 的 Menu.ShowIf=%q 不是它自己的权限点", m.Key, m.Menu.ShowIf))
		}
	}

	mu.Lock()
	defer mu.Unlock()
	if _, dup := modules[m.Key]; dup {
		panic(fmt.Sprintf("perm: 模块 %q 重复注册", m.Key))
	}
	saved := m
	modules[m.Key] = &saved
	order = append(order, m.Key)
	return &saved
}

// Point 取模块下的一个权限点。动作不存在直接 panic —— 这是编码错误，不是运行时错误。
func (m *Module) Point(action string) Point {
	for _, a := range m.Actions {
		if a.Key == action {
			return m.point(a)
		}
	}
	panic(fmt.Sprintf("perm: 模块 %q 没有动作 %q", m.Key, action))
}

// point 把一个 Action 变成完整的 Point。
func (m *Module) point(a Action) Point {
	return Point{Resource: m.Key, Action: a.Key, Name: a.Name, Scoped: m.Scoped, Realm: m.Realm}
}

// Modules 返回全部注册的模块，按注册顺序。
func Modules() []*Module {
	mu.RLock()
	defer mu.RUnlock()
	out := make([]*Module, 0, len(order))
	for _, key := range order {
		out = append(out, modules[key])
	}
	return out
}

// Lookup 按模块 key 找模块。
func Lookup(key string) (*Module, bool) {
	mu.RLock()
	defer mu.RUnlock()
	m, ok := modules[key]
	return m, ok
}

// Points 返回全部权限点，按 resource:action 排序。启动自检用它（要看全）。
//
// ⚠️ **角色配置页别用它** —— 那里要的是 PointsIn(RealmTenant)，
// 否则平台管理端的权限点会出现在租户的勾选树上（MULTI-TENANCY.md §3.2 ②）。
func Points() []Point {
	mu.RLock()
	defer mu.RUnlock()

	var out []Point
	for _, m := range modules {
		for _, a := range m.Actions {
			out = append(out, m.point(a))
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].String() < out[j].String() })
	return out
}

// PointsIn 返回某个 Realm 下的全部权限点。
//
// 租户的角色配置页、租户用户的 /me 权限清单一律走 RealmTenant；
// 平台管理端走 RealmPlatform。
func PointsIn(realm Realm) []Point {
	all := Points()
	out := make([]Point, 0, len(all))
	for _, p := range all {
		if p.Realm == realm {
			out = append(out, p)
		}
	}
	return out
}

// IsPlatform 判断一个权限点是不是平台管理端的。
//
// 租户的角色里**绝不允许**出现这种点 —— 那等于租户管理员能开租户、停别人家的租户。
// 角色服务在写入前拦一道，启动加载策略时再兜一道（§3.2 ②、⑤）。
func IsPlatform(resource, action string) bool {
	mu.RLock()
	defer mu.RUnlock()
	m, ok := modules[resource]
	if !ok {
		// 注册表里没有的资源交给别的校验去报，这里只回答「是不是平台的」
		return false
	}
	for _, a := range m.Actions {
		if a.Key == action {
			return m.Realm == RealmPlatform
		}
	}
	return false
}

// Has 判断某个权限点是否声明过。
func Has(resource, action string) bool {
	mu.RLock()
	defer mu.RUnlock()
	m, ok := modules[resource]
	if !ok {
		return false
	}
	for _, a := range m.Actions {
		if a.Key == action {
			return true
		}
	}
	return false
}

// reset 只给测试用：清空注册表。
func reset() {
	mu.Lock()
	defer mu.Unlock()
	modules = map[string]*Module{}
	order = nil
	routes = nil
}
