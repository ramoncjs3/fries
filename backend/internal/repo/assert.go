package repo

import (
	"fmt"
	"reflect"
	"sync/atomic"

	"github.com/google/uuid"
)

// 运行期兜底：把 ForTenant 查回来的每一行的 tenant_id 核一遍（MULTI-TENANCY.md §12.2）。
//
// # 这是干什么的
//
// 不用 RLS 的代价是**没有兜底网，也就没有「被兜住」的信号**：
// 一条漏了租户条件的查询会安安静静地返回别人的数据，没有任何报警，
// 测试也照样绿（断言通常只看「我要的那条在不在」，不看「多出来的是谁的」）。
//
// 这一层就是那张网。开着的时候，漏条件的查询会在**返回结果的那一刻**炸掉，
// 而不是等到某天客户说「我在系统里看到了别的公司的人」。
//
// # 为什么是 panic 而不是返回 error
//
// 走到这里说明代码有 bug，不是运行时的意外。返回 error 的话调用方会 `if err != nil`
// 一路往上抛，最后变成一个 500 —— 而这本该是「测试红了，改代码」。
// panic 才会让人当场停下来。
//
// # 为什么默认关
//
// 每行都过一遍反射，热路径上不该背这个成本。**集成测试全程开着**
// （`testdb.Start` 里打开），那里才是它该炸的地方。
//
// # 它抓不到什么
//
//   - 写操作：没有返回行可核。写的隔离靠「影响行数必须是 0」，另有测试守（§10.8）
//   - 返回 count 的查询：一个 int64 里没有租户信息
//   - `Unscoped()` / `Platform()`：那两条路本来就声明了绕过隔离
var tenantAssertions atomic.Bool

// EnableTenantAssertions 打开运行期兜底。**只在测试和非生产环境调用**。
func EnableTenantAssertions() { tenantAssertions.Store(true) }

// DisableTenantAssertions 关掉运行期兜底。
func DisableTenantAssertions() { tenantAssertions.Store(false) }

// TenantAssertionsEnabled 返回当前是否开着。
func TenantAssertionsEnabled() bool { return tenantAssertions.Load() }

// assertTenant 核对一次查询结果，然后原样返回。
//
// 签名是 `(T, error) -> (T, error)`，这样生成的包装可以直接写成
// `return assertTenant(q.tenantID, q.q.GetUser(...))` —— Go 允许把多返回值
// 整个传进参数匹配的函数，一行都不用拆。
func assertTenant[T any](tenantID uuid.UUID, v T, err error) (T, error) {
	// err != nil 时结果是零值，核它没有意义
	if err != nil || !tenantAssertions.Load() {
		return v, err
	}
	checkTenantOf(tenantID, reflect.ValueOf(v), "")
	return v, err
}

// checkTenantOf 递归找 TenantID 字段并比对。path 只用来把 panic 消息说清楚。
func checkTenantOf(want uuid.UUID, v reflect.Value, path string) {
	switch v.Kind() {
	case reflect.Pointer, reflect.Interface:
		if v.IsNil() {
			return
		}
		checkTenantOf(want, v.Elem(), path)

	case reflect.Slice, reflect.Array:
		for i := range v.Len() {
			checkTenantOf(want, v.Index(i), fmt.Sprintf("%s[%d]", path, i))
		}

	case reflect.Struct:
		// uuid.UUID 自己就是 [16]byte 的具名数组，别往里钻
		if v.Type() == reflect.TypeOf(uuid.UUID{}) {
			return
		}
		field := v.FieldByName("TenantID")
		if !field.IsValid() {
			// 顶层没有 TenantID 字段。两种情况：
			//   · sqlc.embed 出来的投影行（形如 {Entity, xLabel}）—— TenantID 藏在嵌套的
			//     Entity 里，往下钻把它核了（生成模块带 ref JOIN 的 List/Get、user 模块都是这形状）。
			//   · 真没有租户信息的（count 的 int64、纯标量投影）—— 钻进去也找不到，自然跳过。
			// ForTenant 查询恒为单租户，嵌套结构体里的 TenantID 也该等于 want；不等就是漏了
			// 租户条件的泄露，正该在这里炸。只钻导出字段（未导出的 reflect 取不出值）。
			for i := range v.NumField() {
				if v.Type().Field(i).IsExported() {
					checkTenantOf(want, v.Field(i), path)
				}
			}
			return
		}
		got, ok := tenantIDOf(field)
		if !ok {
			return
		}
		if got != want {
			panic(fmt.Sprintf(
				"repo: 跨租户数据泄露 —— ForTenant(%s) 查回了属于 %s 的行（%s%s）。\n"+
					"这一定是某条查询漏了租户条件，去 db/queries/ 找它，"+
					"「按 id 查一行」也必须带 tenant_id（MULTI-TENANCY.md §11.1）",
				want, got, v.Type(), path))
		}

	default:
	}
}

// tenantIDOf 从字段里取出 uuid.UUID，取不到就返回 false。
//
// 可空的 tenant_id（`audit_logs` 那张，平台级事件记 NULL）是 *uuid.UUID。
// **NULL 一律跳过**：带了租户条件的查询本来就不会返回 NULL 行，
// 而在这里判它「不等于当前租户」就是一次误报。
func tenantIDOf(field reflect.Value) (uuid.UUID, bool) {
	if field.Kind() == reflect.Pointer {
		if field.IsNil() {
			return uuid.Nil, false
		}
		field = field.Elem()
	}
	if field.Type() != reflect.TypeOf(uuid.UUID{}) {
		return uuid.Nil, false
	}
	id, ok := field.Interface().(uuid.UUID)
	return id, ok
}
