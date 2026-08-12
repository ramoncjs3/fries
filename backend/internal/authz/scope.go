package authz

import (
	"context"

	"github.com/google/uuid"

	"github.com/ramoncjs3/fries/internal/errs"
	"github.com/ramoncjs3/fries/internal/perm"
)

// Scope 是数据范围。只有两档，够用且好解释（DECISIONS.md §3.3）。
//
// ⚠️ 多租户之后 all 的含义是「**本组织内**的全部数据」（MULTI-TENANCY.md §3.2 ④）。
// 租户是**硬边界，不是一档 scope**：data_scope 在角色上可配，tenant_id 不可配，
// 任何角色、任何 scope 都跨不过去。
type Scope string

const (
	// ScopeAll 看本组织的全部数据。
	ScopeAll Scope = "all"
	// ScopeSelf 只看自己创建的。
	ScopeSelf Scope = "self"
)

// Valid 判断是不是合法的数据范围。
func (s Scope) Valid() bool { return s == ScopeAll || s == ScopeSelf }

// Widest 取最宽的数据范围：多角色时有一个 all 就是 all。
func Widest(scopes ...Scope) Scope {
	for _, s := range scopes {
		if s == ScopeAll {
			return ScopeAll
		}
	}
	return ScopeSelf
}

// MustScope 取当前请求对某个模块的数据范围。
//
// **默认拒绝**：context 里没有主体就返回错误，而不是放行（DECISIONS.md §3.3）。
// 漏注入 scope 是静态查不出来的，只能靠运行时报错和测试兜住。
func MustScope(ctx context.Context, resource string) (Scope, error) {
	p, ok := PrincipalFrom(ctx)
	if !ok {
		return "", errs.ScopeDenied.Wrap(errNoPrincipal{resource})
	}
	m, known := perm.Lookup(resource)
	if !known {
		return "", errs.Internal.Wrap(errUnknownResource{resource})
	}
	// 共享资源不参与数据权限，谁看到的都一样。
	if !m.Scoped {
		return ScopeAll, nil
	}
	if !p.Scope.Valid() {
		return "", errs.ScopeDenied.Wrap(errNoPrincipal{resource})
	}
	return p.Scope, nil
}

// OwnerFilter 把数据范围翻译成 sqlc 查询要的可空参数：
// all 传 nil（不过滤），self 传当前主体 ID（DECISIONS.md §3.3）。
//
//	owner, err := authz.OwnerFilter(ctx, "orders")
//	rows, err := q.ListOrders(ctx, repo.ListOrdersParams{OwnerID: owner})
func OwnerFilter(ctx context.Context, resource string) (*uuid.UUID, error) {
	scope, err := MustScope(ctx, resource)
	if err != nil {
		return nil, err
	}
	if scope == ScopeAll {
		return nil, nil
	}
	p, _ := PrincipalFrom(ctx)
	id := p.ID
	return &id, nil
}

type errNoPrincipal struct{ resource string }

func (e errNoPrincipal) Error() string {
	return "context 里没有认证主体，取不到 " + e.resource + " 的数据范围"
}

type errUnknownResource struct{ resource string }

func (e errUnknownResource) Error() string {
	return "模块 " + e.resource + " 没有在 perm 注册表里声明"
}
