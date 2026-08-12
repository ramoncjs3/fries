package authz

import (
	"context"
	"fmt"
	"sync"

	"github.com/casbin/casbin/v2"
	"github.com/casbin/casbin/v2/model"
	"github.com/google/uuid"

	"github.com/ramoncjs3/fries/internal/perm"
)

// casbinModel 是带 domain 的 RBAC 模型：主体 → (租户内的)角色 → (资源, 动作)。
//
// `dom` 就是 `tenant_id`（MULTI-TENANCY.md §3.1）。角色 key 只在租户内唯一 ——
// 两家公司各有一个叫 admin 的角色，不带 domain 的话它们在策略表里直接撞车，
// A 公司 admin 的 `*:*` 会落到 B 公司那个同名但受限的 admin 头上。
//
// 用 Casbin 官方的 domain 模型，**不自己在 key 前面拼租户前缀**：
// 拼字符串迟早会在某个忘了拼的地方漏掉，而 domain 是匹配器的一等公民。
//
// 🔴 **最后两段通配匹配一个字都不能少：**
//
//	(p.obj == "*" || r.obj == p.obj) && (p.act == "*" || r.act == p.act)
//
// 内置 admin 角色的策略就是一条 `*, *`，靠的正是它们。照搬 Casbin 官网那份
// domain 示例（用的是 `r.obj == p.obj`）会**当场让所有租户的管理员失去全部权限**，
// 而现象是「登录进去什么菜单都没有」，很难第一时间联想到是匹配器。
//
// 通配符只在 policy 侧生效，请求侧永远是具体的资源和动作 ——
// 免得有人构造出 obj=* 的请求把自己变成超级管理员（Allow 里也拦了一道）。
const casbinModel = `
[request_definition]
r = sub, dom, obj, act

[policy_definition]
p = sub, dom, obj, act

[role_definition]
g = _, _, _

[policy_effect]
e = some(where (p.eft == allow))

[matchers]
m = g(r.sub, p.sub, r.dom) && r.dom == p.dom && (p.obj == "*" || r.obj == p.obj) && (p.act == "*" || r.act == p.act)
`

// PolicyChannel 是授权变更的 PG 通知频道，由 roles / role_permissions / user_roles
// 三张表的触发器发出（DECISIONS.md §5 的同一套机制）。
const PolicyChannel = "authz_changed"

// Policy 是一次加载出来的全部授权数据。
type Policy struct {
	// Grants 是「角色拥有哪些权限点」。
	Grants []Grant
	// Bindings 是「主体属于哪些角色」。
	Bindings []Binding
}

// Grant 是角色到权限点的授权。
type Grant struct {
	// TenantID 是这个角色属于哪个租户。角色 key 只在租户内唯一 ——
	// 两家公司各有一个自己的 admin（MULTI-TENANCY.md §3.2 ①）。
	TenantID uuid.UUID
	RoleKey  string
	Resource string
	Action   string
}

// Binding 是主体到角色的绑定。
type Binding struct {
	TenantID  uuid.UUID
	SubjectID uuid.UUID
	RoleKey   string
	Scope     Scope
}

// PolicySource 提供授权数据。放接口后面，换存储不用动 Checker。
type PolicySource interface {
	LoadPolicy(ctx context.Context) (Policy, error)
}

// EmptySource 是一份空策略。只给 `server --selfcheck` 用 ——
// 自检是纯内存的，不碰数据库（DECISIONS.md §3.7）。
type EmptySource struct{}

// LoadPolicy 实现 PolicySource。
func (EmptySource) LoadPolicy(context.Context) (Policy, error) { return Policy{}, nil }

// Checker 是授权判定入口。Casbin 包在它后面，可替换（DECISIONS.md §1）。
type Checker interface {
	// Allow 判断主体有没有某个权限点。
	Allow(p *Principal, point perm.Point) bool
	// Points 列出主体拥有的全部权限点，菜单树和 /me 用它。
	Points(p *Principal) []perm.Point
	// Identity 返回主体的角色和数据范围。认证时用它填 Principal。
	Identity(subject uuid.UUID) (roles []string, scope Scope, found bool)
	// Reload 重新加载授权数据。角色或授权变了就调它。
	Reload(ctx context.Context) error
}

// CasbinChecker 是基于 Casbin 的实现。
//
// 策略全量放内存，变更靠 PG 的 LISTEN/NOTIFY 触发重载 —— 每个请求都查库
// 太贵，而这套数据一天也改不了几次。
type CasbinChecker struct {
	source PolicySource

	mu       sync.RWMutex
	enforcer *casbin.Enforcer
	identity map[uuid.UUID]identity
}

type identity struct {
	roles []string
	scope Scope
}

// NewCasbinChecker 造一个 Checker 并立即加载一次策略。
func NewCasbinChecker(ctx context.Context, source PolicySource) (*CasbinChecker, error) {
	c := &CasbinChecker{source: source}
	if err := c.Reload(ctx); err != nil {
		return nil, err
	}
	return c, nil
}

// Reload 实现 Checker。整体换掉 enforcer，不做增量修改 —— 换一半的状态最难查。
func (c *CasbinChecker) Reload(ctx context.Context) error {
	policy, err := c.source.LoadPolicy(ctx)
	if err != nil {
		return fmt.Errorf("加载授权策略: %w", err)
	}

	m, err := model.NewModelFromString(casbinModel)
	if err != nil {
		return fmt.Errorf("解析 Casbin 模型: %w", err)
	}
	enforcer, err := casbin.NewEnforcer(m)
	if err != nil {
		return fmt.Errorf("创建 Casbin enforcer: %w", err)
	}
	enforcer.EnableAutoSave(false)

	for _, g := range policy.Grants {
		// ⚠️ 平台权限点绝不允许出现在租户的角色里（§3.2 ②、⑤）。
		// 角色服务在写入前就拦了一道，这里是**兜底**：有人绕过应用直接往
		// role_permissions 里插一行，也别想进 enforcer。
		//
		// 启动时这个错误会让服务起不来；运行中刷新时会被记成 ERROR 并保留旧策略
		// —— 两种情况下那条脏授权都不会生效。
		if perm.IsPlatform(g.Resource, g.Action) {
			return fmt.Errorf("租户 %s 的角色 %s 里出现了平台权限点 %s:%s —— "+
				"平台管理端的权限只属于平台管理员，租户的角色够不到它",
				g.TenantID, g.RoleKey, g.Resource, g.Action)
		}
		if _, err := enforcer.AddPolicy(g.RoleKey, g.TenantID.String(), g.Resource, g.Action); err != nil {
			return fmt.Errorf("加载授权 %s/%s:%s: %w", g.RoleKey, g.Resource, g.Action, err)
		}
	}

	ids := make(map[uuid.UUID]identity, len(policy.Bindings))
	for _, b := range policy.Bindings {
		if _, err := enforcer.AddGroupingPolicy(
			b.SubjectID.String(), b.RoleKey, b.TenantID.String()); err != nil {
			return fmt.Errorf("加载角色绑定 %s/%s: %w", b.SubjectID, b.RoleKey, err)
		}
		cur := ids[b.SubjectID]
		cur.roles = append(cur.roles, b.RoleKey)
		cur.scope = Widest(cur.scope, b.Scope)
		ids[b.SubjectID] = cur
	}

	c.mu.Lock()
	c.enforcer = enforcer
	c.identity = ids
	c.mu.Unlock()
	return nil
}

// Allow 实现 Checker。任何异常一律判否 —— 授权出错时拒绝比放行安全。
func (c *CasbinChecker) Allow(p *Principal, point perm.Point) bool {
	if p == nil || point.Resource == "" || point.Action == "" {
		return false
	}
	// 请求侧不许出现通配符，否则等于自封超级管理员。
	if point.Resource == perm.Wildcard || point.Action == perm.Wildcard {
		return false
	}
	// 没有租户就没有 domain，判不了 —— 判不了一律拒绝。
	if p.TenantID == uuid.Nil {
		return false
	}
	// 🔴 **租户的主体永远够不到平台管理端的权限点**（MULTI-TENANCY.md §3.2 ②⑤）。
	//
	// 这一条不是多余的。内置 admin 的策略是一条 `*, *`，而匹配器里的
	// `p.obj == "*"` 对**任何** obj 都成立 —— 包括平台模块的资源名。
	// 也就是说：不在这里拦，租户里那个通配 admin 会直接通过平台接口的权限判定。
	// 验证过：加这一条之前，探针测试判定结果是 true。
	//
	// §10.4 说 /platform 还要另外要求「必须是平台管理员主体」，那是另一道；
	// 但**光靠那一道等于把整个隔离压在一个 if 上**，这里必须先结构性地断掉。
	//
	// CasbinChecker 服务的是租户世界。平台管理端第 ⑤ 步会走自己的判定路径。
	if point.Realm != perm.RealmTenant {
		return false
	}

	c.mu.RLock()
	enforcer := c.enforcer
	c.mu.RUnlock()
	if enforcer == nil {
		return false
	}

	// 主体身上的租户就是 domain。它来自会话行，不是请求参数 ——
	// 能自己传 domain 的话，隔离就成了「请求方自觉」（§10.2）。
	ok, err := enforcer.Enforce(p.ID.String(), p.TenantID.String(), point.Resource, point.Action)
	return err == nil && ok
}

// Points 实现 Checker。
//
// 只吐**租户世界**的权限点：平台管理端的权限点不属于任何租户用户，
// 出现在这里就会漏进 /me 的权限清单和菜单（§3.2 ②）。
// 平台管理端将来走自己的判定路径（第 ⑤ 步）。
func (c *CasbinChecker) Points(p *Principal) []perm.Point {
	all := perm.PointsIn(perm.RealmTenant)
	out := make([]perm.Point, 0, len(all))
	for _, point := range all {
		if c.Allow(p, point) {
			out = append(out, point)
		}
	}
	return out
}

// Identity 实现 Checker。
func (c *CasbinChecker) Identity(subject uuid.UUID) ([]string, Scope, bool) {
	c.mu.RLock()
	defer c.mu.RUnlock()
	id, ok := c.identity[subject]
	if !ok {
		// 没有任何角色的主体：能登录，但什么都看不到。
		return nil, ScopeSelf, false
	}
	return id.roles, id.scope, true
}
