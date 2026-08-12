package perm

import (
	"context"
	"fmt"
	"sync"

	"github.com/danielgtaylor/huma/v2"
)

// 操作的三档访问要求。**注册路由时必须选一档**，写不出「忘了配权限」的接口
// （DECISIONS.md §3.7）。
const (
	// AccessPublic：不需要登录。只有登录接口和健康检查是这一档。
	AccessPublic = "public"
	// AccessAuthenticated：登录即可，不看权限点。/me、改自己的密码属于这一档。
	AccessAuthenticated = "authenticated"
	// AccessPermission：需要具体权限点。业务接口一律这一档。
	AccessPermission = "permission"
)

// metaAccess / metaPoint 是写进 huma.Operation.Metadata 的键，
// 授权中间件靠它们知道这个接口要什么。
const (
	metaAccess = "fries.access"
	metaPoint  = "fries.point"
)

// Route 是一条已注册路由的权限信息，自检和文档都用它。
type Route struct {
	OperationID string
	Method      string
	Path        string
	Access      string
	Point       Point
}

var (
	routesMu sync.RWMutex
	routes   []Route
)

// Routes 返回全部已注册路由。
func Routes() []Route {
	routesMu.RLock()
	defer routesMu.RUnlock()
	return append([]Route(nil), routes...)
}

// ResetRoutes 清空路由登记表。**在 newApp 开始注册路由前调一次**。
//
// 路由登记是包级全局、且注册是有副作用的（每调一次 Register* 就 append）。一个进程里
// 建两次 app（测试就是这样）时，不清就会累积成真实路由数的两倍。它只清 routes，
// **不碰 modules** —— 那些是 internal/perm/modules 里的包级 var 一次性注册的，
// 清了不会再回来。清 routes 是安全的：它本来就该跟着「这一个 app 实例」走。
func ResetRoutes() {
	routesMu.Lock()
	defer routesMu.Unlock()
	routes = nil
}

// Public 注册一个不需要登录的接口。
func Public[I, O any](api huma.API, op huma.Operation, handler func(context.Context, *I) (*O, error)) {
	register(api, op, AccessPublic, Point{}, handler)
}

// Authenticated 注册一个「登录即可」的**租户端**接口。/me、改自己的密码属于这一档。
func Authenticated[I, O any](api huma.API, op huma.Operation, handler func(context.Context, *I) (*O, error)) {
	register(api, op, AccessAuthenticated, Point{Realm: RealmTenant}, handler)
}

// PlatformAuthenticated 注册一个「登录即可」的**平台端**接口。
//
// 和 Authenticated 分开是为了让 Realm 写在注册处：授权中间件按
// 「路由的 Realm 必须和主体的 Realm 一致」判（MULTI-TENANCY.md §10.4），
// 这一档没有权限点，Realm 就只能从这里来。
//
// 不这么分的话，租户用户能打到 /platform/me 上、平台管理员能打到租户的 /me 上 ——
// 两个都不是数据泄露，但那是「碰巧没数据可拿」，不是「拦住了」。
func PlatformAuthenticated[I, O any](api huma.API, op huma.Operation, handler func(context.Context, *I) (*O, error)) {
	register(api, op, AccessAuthenticated, Point{Realm: RealmPlatform}, handler)
}

// Guard 注册一个需要权限点的接口。**权限点是必填参数**，这是防漏配的第一道
// （DECISIONS.md §3.7）。
func Guard[I, O any](api huma.API, point Point, op huma.Operation, handler func(context.Context, *I) (*O, error)) {
	if !Has(point.Resource, point.Action) {
		panic(fmt.Sprintf("perm: 接口 %s 引用了没声明的权限点 %s", op.OperationID, point))
	}
	register(api, op, AccessPermission, point, handler)
}

func register[I, O any](api huma.API, op huma.Operation, access string, point Point, handler func(context.Context, *I) (*O, error)) {
	if op.Metadata == nil {
		op.Metadata = map[string]any{}
	}
	op.Metadata[metaAccess] = access
	// 权限点一律写进去：AccessAuthenticated 那一档虽然没有 Resource/Action，
	// 但 Realm 要带过去（中间件靠它对齐主体）。
	op.Metadata[metaPoint] = point

	routesMu.Lock()
	routes = append(routes, Route{
		OperationID: op.OperationID,
		Method:      op.Method,
		Path:        op.Path,
		Access:      access,
		Point:       point,
	})
	routesMu.Unlock()

	huma.Register(api, op, handler)
}

// RequirementOf 从 huma 操作上读出访问要求。中间件用它决定放不放行。
func RequirementOf(op *huma.Operation) (access string, point Point, ok bool) {
	if op == nil || op.Metadata == nil {
		return "", Point{}, false
	}
	access, ok = op.Metadata[metaAccess].(string)
	if !ok {
		return "", Point{}, false
	}
	if p, has := op.Metadata[metaPoint].(Point); has {
		point = p
	}
	return access, point, true
}
