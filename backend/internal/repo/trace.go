package repo

import (
	"context"
	"fmt"
	"sync"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/ramoncjs3/fries/internal/tenantsql"
)

// 运行期兜底的第二层：核**真正发给 PostgreSQL 的那条 SQL**（MULTI-TENANCY.md §12.2）。
//
// # 为什么还要一层
//
// assert.go 那层核的是**查回来的行**，所以它有两个天生的盲区：
//
//	写操作      没有返回行可核（execrows 只回一个数字）
//	count 查询  一个 int64 里没有租户信息
//
// 而「批量按 id 写」恰恰落在第一个盲区里（§10.8），那也是审计里发现的
// 最危险的一类：`UPDATE users … AND department_id IN (SELECT … WHERE tenant_id = $1)`
// —— 条件绑在 departments 上，users 光着，传一串别家的 user_id 就能把人一起停掉。
//
// 这一层从**另一个方向**看同一件事：不看结果，看语句。pgx 的 tracer 是整个进程里
// 唯一能看见所有 SQL 的位置 —— sqlc 生成的、手写的、动态拼的，一视同仁。
//
// # 判据和构建期是同一份
//
// 规则不在这里实现，在 internal/tenantsql。`gen lint-sql` 用的是同一个 Analyze。
// 分成两份的话，中间那道缝就是下一个漏洞的位置 —— 这个项目在
// 「两个检查看的不是同一件事」上已经栽过一次（MEMORY.md 记过）。
//
// # 三档开关
//
// 和 assert.go 共用 `tenantAssertions`：集成测试始终开（testdb.Start 里翻开）、
// staging 用 `server.tenant_assertions: true` 打开、生产关掉。
//
// # 它抓不到什么
//
//   - 动态拼出来的表名（`"SELECT * FROM " + table`）—— 正则认不出来
//   - 「条件在但值错」以外的语义问题，比如把两个租户的数据在内存里拼一起。
//     那是 §1.2 说的「手写代码」缺口，只能靠 review 和跨租户测试

// requestTenantKey 是「这次请求属于哪个租户」在 context 里的键。
type requestTenantKey struct{}

// WithRequestTenant 把「这次请求的租户」放进 context，**只给运行期兜底用**。
//
// 由认证中间件在认出主体的同一处调用（和 httpx.WithTenant 一起）。
// 关掉兜底时它什么也不影响 —— 就是 context 里多一个没人读的值。
//
// ⚠️ 为什么不复用 httpx 里那个：repo 是数据层，不能 import HTTP 那一层
// （AGENTS.md 红线 #6，分层不许穿透）。反过来让中间件 import repo 是**向下**依赖，
// 方向是对的。
//
// 租户的唯一来源仍然是会话行（§4.2），这里只是把它捎给断言。
func WithRequestTenant(ctx context.Context, tenantID uuid.UUID) context.Context {
	if tenantID == uuid.Nil {
		// 平台管理员没有租户，未认证请求也没有 —— 不放进去，让断言跳过比对
		return ctx
	}
	return context.WithValue(ctx, requestTenantKey{}, tenantID)
}

// requestTenantOf 取出这次请求的租户，没有就返回 false。
func requestTenantOf(ctx context.Context) (uuid.UUID, bool) {
	if ctx == nil {
		return uuid.Nil, false
	}
	id, ok := ctx.Value(requestTenantKey{}).(uuid.UUID)
	return id, ok && id != uuid.Nil
}

// tenantTracer 是挂在连接池上的 pgx.QueryTracer。
//
// 它是**无状态**的（那个 cache 只是备忘），所以整个进程共用一个实例就够。
type tenantTracer struct {
	// clean 记住「这条 SQL 的文本已经核过、没问题」。
	//
	// sqlc 生成的 SQL 是常量，命中率接近 100% —— 没有它的话每条查询都要跑一遍
	// 正则，staging 上那是白烧 CPU。**只缓存文本结论**，值比对每次都做
	// （同一条 SQL 每次绑的租户不一样，那正是要比的东西）。
	clean sync.Map // map[string]struct{}
}

// TraceQueryStart 实现 pgx.QueryTracer。
func (t *tenantTracer) TraceQueryStart(
	ctx context.Context, _ *pgx.Conn, data pgx.TraceQueryStartData,
) context.Context {
	if tenantAssertions.Load() {
		t.check(ctx, data.SQL, data.Args)
	}
	return ctx
}

// TraceQueryEnd 实现 pgx.QueryTracer。这一层只关心「发出去的是什么」，结果不看。
func (*tenantTracer) TraceQueryEnd(context.Context, *pgx.Conn, pgx.TraceQueryEndData) {}

// check 核一条即将执行的语句，有问题直接 panic。
//
// panic 而不是返回错误，理由同 assert.go：走到这里说明代码有 bug，
// 不是运行时的意外。返回错误会被一路 `if err != nil` 抛成一个 500，
// 而这本该是「测试红了，改代码」。
func (t *tenantTracer) check(ctx context.Context, sql string, args []any) {
	// 一张需要管的租户表都不碰：探活、平台查询、DDL、TRUNCATE 全在这里被放过
	if !tenantsql.TouchesGuardedTable(sql) {
		return
	}
	// 只管读写行的语句。TRUNCATE 尤其要放过 —— 测试基建每个用例前都靠它清场，
	// 那是**故意跨租户**的。
	if !tenantsql.IsDML(sql) {
		return
	}
	// 审过的豁免，两条路：
	//
	//	sqlc 的查询   查生成出来的清单。sqlc 把 `-- tenant-exempt:` 剥掉了，
	//	              标记到不了这里，只能靠查询名对（理由留在 db/queries/*.sql）
	//	手写的 SQL    标记就写在 SQL 里面，构建期和运行期看的是同一行
	//
	// ⚠️ 两条都不命中就是**默认拒绝**。特别是「认不出查询名」不等于豁免 ——
	// 反过来的话，所有手写 SQL 都拿到一张免票，而它们正是这层网要覆盖的东西。
	if exemptQueries[tenantsql.QueryName(sql)] || tenantsql.HasExemptComment(sql) {
		return
	}

	t.checkCondition(sql)
	t.checkValue(ctx, sql, args)
}

// checkCondition 核「每一处取行的地方都绑到租户上了」。判据来自 tenantsql。
func (t *tenantTracer) checkCondition(sql string) {
	if _, done := t.clean.Load(sql); done {
		return
	}
	if problems := tenantsql.Analyze(sql); len(problems) > 0 {
		panic(fmt.Sprintf(
			"repo: 这条 SQL 没有把每一处都绑到租户上 —— %s\n\n%s\n\n"+
				"（运行期兜底，MULTI-TENANCY.md §12.2。判据和 `make lint-sql` 是同一份，"+
				"所以构建期本该已经拦住它 —— 能跑到这里说明它是动态拼出来的）",
			problems[0], sql))
	}
	t.clean.Store(sql, struct{}{})
}

// checkValue 核「绑的是**对的**那个租户」。
//
// 这一条是别的几层都做不到的：条件在、语法对、静态检查也过 ——
// 拿着 A 的会话去查 B 的数据，只有比对值才看得出来。
// 它守的是 §4.2 那条红线：**租户的唯一来源是会话**，不许从请求参数取。
// 那条规矩到今天为止没有任何机械强制，全靠 review。
//
// 只在两个条件同时成立时比：这次请求有租户（平台管理员和未认证请求都没有），
// 且这条 SQL 真的把租户绑到了某个位置参数上。任一不成立就跳过 ——
// 宁可漏判也不能误报，误报会让人开始忽略这个兜底，那就白装了。
func (*tenantTracer) checkValue(ctx context.Context, sql string, args []any) {
	want, ok := requestTenantOf(ctx)
	if !ok {
		return
	}
	idx, found := tenantsql.BoundTenantArgIndex(sql)
	if !found || idx >= len(args) {
		return
	}
	got, isUUID := args[idx].(uuid.UUID)
	if !isUUID || got == want {
		return
	}
	panic(fmt.Sprintf(
		"repo: 这次请求属于组织 %s，但这条 SQL 查的是 %s 的数据\n\n%s\n\n"+
			"（租户的唯一来源是会话行，不许从请求参数、请求头或前端状态里取 —— "+
			"MULTI-TENANCY.md §4.2。检查一下 ForTenant() 的入参是不是来自 authz.MustTenant）",
		want, got, sql))
}
