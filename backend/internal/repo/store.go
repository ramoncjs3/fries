// Package repo 是数据访问层。
//
// # 为什么多了一层 internal/sqlcgen
//
// sqlc 的产物在 `internal/repo/internal/sqlcgen/`。Go 规定 `internal` 下的包
// **只能被它父目录那棵树 import** —— 也就是只有 `internal/repo/...` 碰得到。
// 于是 service、handler 这些业务代码**在编译期就拿不到不带租户的查询句柄**，
// 不是「不该用」，是「用不了」（MULTI-TENANCY.md §1.2 ①）。
//
// 这是不用 PostgreSQL RLS 之后能拿到的最强保证。中间件只能拒绝请求，
// 包可见性能拒绝**编译**。
//
// # 业务代码怎么用
//
//	id, err := authz.MustTenant(ctx)   // 没有租户就报错，不是放行
//	if err != nil {
//	    return err
//	}
//	q := s.store.ForTenant(id)
//	rows, err := q.ListUsers(ctx, repo.ListUsersArgs{Keyword: kw})  // 没有 TenantID 这个字段
//
// 拿不到 `*sqlcgen.Queries`，也就没法写出一条漏了租户条件的查询。
package repo

import (
	"context"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/ramoncjs3/fries/internal/repo/internal/sqlcgen"
)

// DBTX 是能执行 SQL 的东西：连接池、连接、事务都满足。
type DBTX = sqlcgen.DBTX

// TxBeginner 能开事务。`*pgxpool.Pool` 天然满足。
//
// service 需要「多步写要么全成要么全不成」时依赖它，而不是直接依赖 pgxpool ——
// 测试里换一个替身就行。
type TxBeginner interface {
	Begin(ctx context.Context) (pgx.Tx, error)
}

// Store 是 repo 的入口，整个进程一个。
//
// 它自己**不提供任何查询方法** —— 想查数据必须先说明是哪个租户（ForTenant），
// 或者明确声明这是一条绕过隔离的路（Unscoped）。这个「必须先选一条路」的设计
// 是刻意的：让绕过隔离这件事在代码里看得见。
type Store struct {
	q  *sqlcgen.Queries
	db TxBeginner
}

// New 建一个 Store。db 通常是 `*pgxpool.Pool`。
func New(db DBTX) *Store {
	s := &Store{q: sqlcgen.New(db)}
	if b, ok := db.(TxBeginner); ok {
		s.db = b
	}
	return s
}

// ForTenant 是业务代码拿查询句柄的**唯一入口**。
//
// 租户从哪来只有一个答案：`authz.MustTenant(ctx)`，它最终来自会话行上的 tenant_id。
// 别从请求参数取，别从用户 id 反查。
//
// ⚠️ 传零值 UUID 直接 panic。这不是洁癖：uuid.Nil 不是任何租户，
// 拿它去查每一条 SQL 都会安安静静地返回 0 行 —— 正是不用 RLS 想避开的那种静默失败。
// 零值只会来自「某个字段忘了填」，那是 bug，越早炸越好。
// （全零 UUID 在这套系统里另有用途：审计哈希链里平台级事件的链头键，见 §10.3。
// 那是链头表的主键，不是租户，别混。）
func (s *Store) ForTenant(tenantID uuid.UUID) *TenantQueries {
	if tenantID == uuid.Nil {
		panic("repo: ForTenant 收到零值租户 —— 租户只能来自 authz.MustTenant(ctx)，" +
			"零值意味着上游漏填了，再往下走就是静默查到 0 行")
	}
	return &TenantQueries{q: s.q, db: s.db, tenantID: tenantID}
}

// Unscoped 返回**不带租户**的句柄。
//
// ⚠️ 它上面只有三类查询：认证链路（拿 token / API Key / 公司代码定位身份，
// 那一刻还不知道租户）、写审计（未认证请求的 tenant_id 是 NULL）、
// 跨租户的后台任务（清过期会话、建审计分区）。详见 UnscopedQueries 的注释。
//
// **写业务代码时不该出现这个调用。** 出现了就说明有一条路绕过了隔离，
// review 时要问清楚为什么。
func (s *Store) Unscoped() *UnscopedQueries {
	return &UnscopedQueries{q: s.q, db: s.db}
}

// Platform 返回**平台管理端**的句柄。
//
// 它上面只有碰平台级表（tenants / platform_admins / platform_sessions /
// platform_settings）的查询 —— 平台管理员开租户、停租户，但**结构上够不到
// 客户的业务数据**（MULTI-TENANCY.md §6）。
//
// 分流是生成器按 SQL 里的表名做的，不是靠人分类。所以这个性质不是「我们保证不查」，
// 是「代码里根本没有那条路」—— 那才是能拿去跟客户讲的话（§10.11）。
func (s *Store) Platform() *PlatformQueries {
	return &PlatformQueries{q: s.q, db: s.db}
}

// InTx 在一个事务里跑 fn，fn 拿到的仍然是**同一个租户**绑定的句柄。
//
// 这一点很要紧（MULTI-TENANCY.md §9.6）：多步写包（改角色权限、将来的开租户）
// 恰恰都在事务里，如果事务里那个句柄退化成裸 Queries，事务内部就整段绕过了强制机制。
//
// **写多张表的操作必须包在这里面**。典型的坑是「先删光角色的权限行、再逐条插回去」：
// 中间失败的话角色会变成零权限，而调用方以为只是没改成功。
func (q *TenantQueries) InTx(ctx context.Context, fn func(*TenantQueries) error) error {
	return inTx(ctx, q.db, func(tx pgx.Tx) error {
		return fn(&TenantQueries{q: q.q.WithTx(tx), db: q.db, tenantID: q.tenantID})
	})
}

// InTx 同上，但句柄不带租户。开租户这类平台级的多步写会用到。
func (q *UnscopedQueries) InTx(ctx context.Context, fn func(*UnscopedQueries) error) error {
	return inTx(ctx, q.db, func(tx pgx.Tx) error {
		return fn(&UnscopedQueries{q: q.q.WithTx(tx), db: q.db})
	})
}

// InTx 同上，平台级的多步写用它 —— 开租户就是一个（§8.6）。
func (q *PlatformQueries) InTx(ctx context.Context, fn func(*PlatformQueries) error) error {
	return inTx(ctx, q.db, func(tx pgx.Tx) error {
		return fn(&PlatformQueries{q: q.q.WithTx(tx), db: q.db})
	})
}

// ForTenant 在事务里换一个租户的句柄。
//
// 平台端**开组织**时用得到：那件事本身是平台级的，但中间要往刚建出来的那个组织里
// 写内置角色和第一个管理员（§8.6）。换句柄这个动作在代码里看得见 ——
// 平台服务里出现 ForTenant 的地方应该屈指可数，多了就要问为什么。
func (q *PlatformQueries) ForTenant(tenantID uuid.UUID) *TenantQueries {
	return forTenant(q.q, q.db, tenantID)
}

// ForTenant 同上。跨租户的后台任务偶尔要按租户操作。
func (q *UnscopedQueries) ForTenant(tenantID uuid.UUID) *TenantQueries {
	return forTenant(q.q, q.db, tenantID)
}

func forTenant(q *sqlcgen.Queries, db TxBeginner, tenantID uuid.UUID) *TenantQueries {
	if tenantID == uuid.Nil {
		panic("repo: ForTenant 收到零值租户（理由见 Store.ForTenant）")
	}
	return &TenantQueries{q: q, db: db, tenantID: tenantID}
}

func inTx(ctx context.Context, db TxBeginner, fn func(pgx.Tx) error) error {
	if db == nil {
		return fmt.Errorf("这个 Store 不能开事务（建它的时候传的不是连接池）")
	}
	tx, err := db.Begin(ctx)
	if err != nil {
		return fmt.Errorf("开事务: %w", err)
	}
	// 提交成功之后再 Rollback 会返回 ErrTxClosed，忽略即可
	defer func() { _ = tx.Rollback(ctx) }()

	if err := fn(tx); err != nil {
		return err
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("提交事务: %w", err)
	}
	return nil
}
