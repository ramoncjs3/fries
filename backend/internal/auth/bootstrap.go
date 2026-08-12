package auth

import (
	"context"
	"fmt"

	"github.com/google/uuid"

	"github.com/ramoncjs3/fries/internal/repo"
)

// apiKeyScheme 是 Service Account 的 API Key 前缀：fsa_<prefix>_<secret>。
const apiKeyScheme = "fsa"

// API Key 两段的长度。prefix 用来定位记录（明文存），secret 只存哈希。
const (
	apiKeyPrefixBytes = 6
	apiKeySecretBytes = 24
)

// 首个管理员的固定信息。密码是随机生成的，只在启动时给出一次。
const (
	bootstrapUsername = "admin"
	bootstrapRoleKey  = "admin"
	// bootstrapPasswordChars 决定随机初始密码有多少个字符。
	bootstrapPasswordChars = 16
)

// BootstrapResult 是首个管理员的账号密码。
//
// 密码只在这里出现一次 —— 由调用方（cmd/server）打印给人看。
// **service 层不往 stdout 打东西**，那是 main 的事。
type BootstrapResult struct {
	Created bool
	// TenantCode 是要在登录框「公司代码」里敲的那串。多租户之后不给出它就登不进去。
	TenantCode string
	Username   string
	Password   string
}

// Bootstrap 在库里一个用户都没有时创建首个管理员。
//
// 密码随机生成、只出现一次、且标记为「首次登录必须改密」——
// 不用固定的 admin/admin123，也不用把密码写进配置文件（DECISIONS.md §6）。
//
// 已经有用户就什么都不做，重复启动是安全的。
//
// ⚠️ 多租户之后它只在**恰好只有一个租户**时干活（不分状态） —— 也就是迁移刚建完库的那一刻。
// 租户多于一个说明系统已经在用了，首个管理员这条路早就该关掉；
// 一个都没有说明库不完整，也不该猜。
//
// 这条链路将来会变成「首个**平台**管理员」的引导（MULTI-TENANCY.md §10.10，第 ⑤ 步）。
// 那时它建的是 platform_admins 里的人，不是某个租户里的用户。
func Bootstrap(ctx context.Context, store *repo.Store) (BootstrapResult, error) {
	var result BootstrapResult

	tenants, err := store.Platform().ListTenants(ctx)
	if err != nil {
		return result, fmt.Errorf("读租户列表: %w", err)
	}
	if len(tenants) != 1 {
		return result, nil
	}
	tenant := tenants[0]
	q := store.ForTenant(tenant.ID)

	count, err := q.CountUsers(ctx)
	if err != nil {
		return result, fmt.Errorf("统计用户数: %w", err)
	}
	if count > 0 {
		return result, nil
	}

	role, err := q.GetRoleByKey(ctx, bootstrapRoleKey)
	if err != nil {
		return result, fmt.Errorf("找内置角色 %s: %w", bootstrapRoleKey, err)
	}

	password := RandomPassword(bootstrapPasswordChars)
	id, err := uuid.NewV7()
	if err != nil {
		return result, fmt.Errorf("生成用户 ID: %w", err)
	}

	user, err := q.CreateUser(ctx, repo.CreateUserArgs{
		ID:                 id,
		Username:           bootstrapUsername,
		DisplayName:        "系统管理员",
		PasswordHash:       HashPassword(password),
		MustChangePassword: true,
	})
	if err != nil {
		return result, fmt.Errorf("创建首个管理员: %w", err)
	}
	if err := q.AssignUserRole(ctx, repo.AssignUserRoleArgs{UserID: user.ID, RoleID: role.ID}); err != nil {
		return result, fmt.Errorf("给首个管理员赋角色: %w", err)
	}

	return BootstrapResult{
		Created:    true,
		TenantCode: tenant.Code,
		Username:   bootstrapUsername,
		Password:   password,
	}, nil
}

// NewAPIKey 生成一个 Service Account 的 API Key。
//
// 返回的完整 key 只有这一次能看到，库里存的是 prefix + 哈希 ——
// 和用户密码一个道理，库被读走也拿不到能用的凭据。
func NewAPIKey() (fullKey, prefix, hash string) {
	// prefix 用十六进制，避免 base64 里的 `_` 把 key 切错段。
	prefix = randomHex(apiKeyPrefixBytes)
	secret := randomToken(apiKeySecretBytes)
	return apiKeyScheme + "_" + prefix + "_" + secret, prefix, HashPassword(secret)
}
