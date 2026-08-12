package repo

import (
	"context"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

// TraceForTest 把 pgx tracer 透给外部测试：炸了返回 panic 消息，没炸返回空串。
//
// 不真发 SQL：这一层要验的是「什么该拦、什么该放过」，那是纯文本判断，
// 不需要数据库。真实链路上它接在 pool 上，由集成测试整体覆盖
// （那一整套跑得过，就说明它没有误报）。
//
// ⚠️ 走 **TraceQueryStart** 而不是里面那个 check：开关那道门在 TraceQueryStart 上，
// 直接调 check 会让「关掉兜底」这件事在测试里失效 —— 于是所有用例都在
// 「开关其实没起作用」的状态下通过。写这个函数时第一版就这么错了，
// 被 TestTracerIsOffByDefault 当场抓住。**测试要从真正的入口进。**
func TraceForTest(ctx context.Context, sql string, args ...any) (msg string) {
	defer func() {
		if r := recover(); r != nil {
			msg = fmt.Sprint(r)
		}
	}()
	(&tenantTracer{}).TraceQueryStart(ctx, nil, pgx.TraceQueryStartData{SQL: sql, Args: args})
	return ""
}

// RequestTenantForTest 造一个带「请求租户」的 context，给值比对那条用。
func RequestTenantForTest(ctx context.Context, tenantID uuid.UUID) context.Context {
	return WithRequestTenant(ctx, tenantID)
}

// AssertTenantForTest 把 assertTenant 的行为透给外部测试：炸了返回 panic 消息，
// 没炸返回空串。
//
// 放在 `export_test.go` 里，所以**只存在于测试构建中** —— 生产代码里没有这个函数，
// 也就没人能拿它当业务 API 用。
func AssertTenantForTest(tenantID uuid.UUID, v any) (msg string) {
	defer func() {
		if r := recover(); r != nil {
			msg = fmt.Sprint(r)
		}
	}()
	_, _ = assertTenant(tenantID, v, nil)
	return ""
}
