package authz

import (
	"context"
	"fmt"

	"github.com/ramoncjs3/fries/internal/repo"
)

// DBPolicySource 从数据库读授权数据。
//
// 权限点的**目录**在代码里（perm 注册表），库里存的只是「谁勾了哪些」——
// 目录落库就会和代码漂（DECISIONS.md §3）。
//
// 多租户下它**按租户逐个加载**再汇总成一份策略（遍历全部租户，不按状态过滤 ——
// 理由见 ListTenants 的注释：停用的租户认证就过不去，把它的策略留在内存里没有风险，
// 反而省掉「停用再启用之后权限没了」那一类问题）。角色 key 只在租户内唯一
// （两家公司都有自己的 admin），所以每条 Grant / Binding 都带上租户，
// 由 CasbinChecker 在拼 Casbin 策略时限定住 —— 否则 A 公司 admin 的通配权限
// 会落到 B 公司的 admin 头上。
//
// ⚠️ 这里一次读全部租户，规模上去了会慢（§8.5）。100 个租户之后要改成
// 「按租户一个 enforcer + LRU」，现在不优化，但记着这个阈值。
type DBPolicySource struct {
	store *repo.Store
}

// NewDBPolicySource 造一个从库里读策略的 PolicySource。
func NewDBPolicySource(store *repo.Store) *DBPolicySource {
	return &DBPolicySource{store: store}
}

// LoadPolicy 实现 PolicySource。
func (s *DBPolicySource) LoadPolicy(ctx context.Context) (Policy, error) {
	var policy Policy

	tenants, err := s.store.Platform().ListTenants(ctx)
	if err != nil {
		return policy, fmt.Errorf("读租户列表: %w", err)
	}

	for _, t := range tenants {
		q := s.store.ForTenant(t.ID)

		grants, err := q.ListRolePolicies(ctx)
		if err != nil {
			return policy, fmt.Errorf("读角色权限（租户 %s）: %w", t.Code, err)
		}
		for _, g := range grants {
			policy.Grants = append(policy.Grants, Grant{
				TenantID: t.ID,
				RoleKey:  g.RoleKey,
				Resource: g.Resource,
				Action:   g.Action,
			})
		}

		users, err := q.ListUserRoleBindings(ctx)
		if err != nil {
			return policy, fmt.Errorf("读用户角色（租户 %s）: %w", t.Code, err)
		}
		for _, b := range users {
			policy.Bindings = append(policy.Bindings, Binding{
				TenantID:  t.ID,
				SubjectID: b.UserID,
				RoleKey:   b.RoleKey,
				Scope:     Scope(b.DataScope),
			})
		}

		accounts, err := q.ListServiceAccountRoleBindings(ctx)
		if err != nil {
			return policy, fmt.Errorf("读 Service Account 角色（租户 %s）: %w", t.Code, err)
		}
		for _, b := range accounts {
			policy.Bindings = append(policy.Bindings, Binding{
				TenantID:  t.ID,
				SubjectID: b.ServiceAccountID,
				RoleKey:   b.RoleKey,
				Scope:     Scope(b.DataScope),
			})
		}
	}
	return policy, nil
}
