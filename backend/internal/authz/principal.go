// Package authz 是授权：功能权限（Casbin RBAC）+ 数据权限（自研 data scope）。
//
// 认证（你是谁）在 internal/auth，授权（你能干什么）在这里。
// 两者都包在接口后面，可替换（DECISIONS.md §1、§3）。
package authz

import (
	"context"

	"github.com/google/uuid"

	"github.com/ramoncjs3/fries/internal/perm"
)

// PrincipalType 区分「人」和「机器」。审计里靠它分清是张三点的还是某系统调的
// （DECISIONS.md §8.1）。
type PrincipalType string

const (
	// PrincipalUser 是登录的人。
	PrincipalUser PrincipalType = "user"
	// PrincipalService 是 Service Account，外部系统用 API Key 调进来。
	PrincipalService PrincipalType = "service"
	// PrincipalPlatform 是平台管理员。**不属于任何租户**（MULTI-TENANCY.md §2.3）：
	// 他开租户、停租户，但碰不到客户的业务数据。
	//
	// ⚠️ 这种主体的 TenantID 是零值，而且**只能走 Realm=platform 的路由** ——
	// 授权中间件按「路由的 Realm 必须和主体的 Realm 一致」判（§10.4）。
	PrincipalPlatform PrincipalType = "platform"
)

// Principal 是已经认证过的主体。没认证过的请求 context 里没有它。
type Principal struct {
	Type PrincipalType
	ID   uuid.UUID
	// TenantID 是这个主体属于哪个租户，来源只有一个：会话行上的 tenant_id
	// （MULTI-TENANCY.md §4.2、§10.2）。整套租户隔离都以它为准，
	// **不要**从别处推导，也不要接受请求参数里传进来的租户。
	TenantID uuid.UUID
	// Name 是用户名或 Service Account 名，进审计。
	Name string
	// DisplayName 是显示名，给前端看。
	DisplayName string
	// Roles 是角色 key 列表。
	Roles []string
	// Scope 是数据范围，多角色取最宽（DECISIONS.md §3.3）。
	Scope Scope
	// SessionID 只有人登录才有，登出时用它精确吊销。
	SessionID uuid.UUID
	// MustChangePassword 为 true 时，除了改密接口，其它一律拦住（DECISIONS.md §6）。
	MustChangePassword bool
	// PasswordExpired 是密码超过有效期，同样只能去改密码。
	PasswordExpired bool
}

// IsUser 判断是不是租户里的人。
func (p *Principal) IsUser() bool { return p != nil && p.Type == PrincipalUser }

// IsPlatform 判断是不是平台管理员。
func (p *Principal) IsPlatform() bool { return p != nil && p.Type == PrincipalPlatform }

// Realm 返回这个主体属于哪个世界。授权中间件用它和路由的 Realm 对齐（§10.4）。
func (p *Principal) Realm() perm.Realm {
	if p.IsPlatform() {
		return perm.RealmPlatform
	}
	return perm.RealmTenant
}

type principalKey struct{}

// WithPrincipal 把认证结果放进 context。
func WithPrincipal(ctx context.Context, p *Principal) context.Context {
	return context.WithValue(ctx, principalKey{}, p)
}

// PrincipalFrom 取出当前主体。没有就是没认证。
func PrincipalFrom(ctx context.Context) (*Principal, bool) {
	if ctx == nil {
		return nil, false
	}
	p, ok := ctx.Value(principalKey{}).(*Principal)
	return p, ok && p != nil
}
