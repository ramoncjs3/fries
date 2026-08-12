// Package platform 是平台管理端的业务层：开组织、停组织、看组织列表。
//
// ⚠️ 它拿的是 `store.Platform()` —— 那个句柄上**只有碰平台级表的查询**
// （tenants / platform_admins / platform_sessions / platform_settings）。
// 所以这一层**结构上就查不到任何客户的业务数据**（MULTI-TENANCY.md §6）。
//
// 这不是「我们保证不查」，是「代码里根本没有那条路」——
// 那才是将来跟客户解释隔离时能拿出来讲的话（§10.11）。
//
// 唯一的例外是**开组织**：那一步要往新组织里写内置角色和第一个管理员，
// 所以它显式换到那个租户的句柄上（见 Create）。换句柄这个动作在代码里看得见。
package platform

import (
	"context"
	"errors"
	"regexp"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/ramoncjs3/fries/internal/audit"
	"github.com/ramoncjs3/fries/internal/auth"
	"github.com/ramoncjs3/fries/internal/authz"
	"github.com/ramoncjs3/fries/internal/errs"
	"github.com/ramoncjs3/fries/internal/perm"
	"github.com/ramoncjs3/fries/internal/repo"
)

// 组织状态。和 DB 的 CHECK 约束一致（tenants 只停用不删除，§9.3）。
const (
	StatusActive    = "active"
	StatusSuspended = "suspended"
)

// tempPasswordChars 决定交付给客户的那串初始密码有多少个字符。
const tempPasswordChars = 14

// rxTenantCode 和迁移里 tenants 的 CHECK 约束**必须一致**（§9.1）。
//
// 在这里再校验一遍不是多余：DB 那条 CHECK 报出来是 23514，用户看不懂；
// 这里能给出人话。DB 那条是兜底 —— 种子脚本、手工 INSERT 也绕不过去。
var rxTenantCode = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{0,30}[a-z0-9]$`)

// reservedCodes 是不许用的公司代码（§9.1）。
//
// ⚠️ 和迁移里 `ck_tenants_code_reserved` 那份清单**必须一致**，改一边就要改另一边。
// 不拦的话，一个客户注册成 `platform`，将来做子域名时就直接撞上平台端。
var reservedCodes = map[string]bool{
	"platform": true, "admin": true, "api": true, "www": true, "app": true,
	"static": true, "assets": true, "auth": true, "login": true, "logout": true,
	"docs": true, "health": true, "healthz": true, "status": true, "system": true,
	"root": true, "internal": true, "public": true, "support": true, "help": true,
	"mail": true, "test": true, "dev": true, "staging": true, "fries": true,
}

// Service 是平台管理端服务。
type Service struct {
	store *repo.Store
}

// New 造平台管理端服务。
func New(store *repo.Store) *Service { return &Service{store: store} }

// Tenant 是列表和详情里的一个组织。
//
// 类型名会原样进 OpenAPI 的 schema 名，所以叫 Tenant 而不是 Item。
type Tenant struct {
	ID        uuid.UUID `json:"id"`
	Code      string    `json:"code" doc:"公司代码，客户登录时要填的那个"`
	Name      string    `json:"name" doc:"组织名"`
	Status    string    `json:"status" doc:"active / suspended"`
	UserCount int       `json:"user_count" doc:"成员数"`
	CreatedAt time.Time `json:"created_at"`
	Version   int       `json:"version" doc:"乐观锁版本号，更新时原样传回"`
}

// ListFilter 是组织查询条件。
type ListFilter struct {
	Keyword  string
	Status   string
	Page     int
	PageSize int
}

// CreatedTenant 是开组织的结果：**一次性凭据只在这里出现一次**。
type CreatedTenant struct {
	Tenant Tenant `json:"tenant"`
	// AdminUsername 是给客户的管理员账号。
	AdminUsername string `json:"admin_username"`
	// AdminPassword 是初始密码，**只显示这一次**，客户首次登录必须改。
	AdminPassword string `json:"admin_password" doc:"初始密码，只显示这一次"`
}

// List 查组织列表。
//
// 人数来自 `tenants.user_count` 冗余列 —— **不去关联业务表**（§6）。
// 那个数由 users 上的触发器维护，应用层不用记得改。
func (s *Service) List(ctx context.Context, f ListFilter) ([]Tenant, int64, error) {
	q := s.store.Platform()
	keyword := repo.LikePattern(f.Keyword)
	status := optional(f.Status)

	rows, err := q.ListTenantsForPlatform(ctx, repo.ListTenantsForPlatformParams{
		Limit:   int32(f.PageSize),
		Offset:  int32((f.Page - 1) * f.PageSize),
		Keyword: keyword,
		Status:  status,
	})
	if err != nil {
		return nil, 0, errs.Internal.Wrap(err)
	}
	total, err := q.CountTenantsForPlatform(ctx, repo.CountTenantsForPlatformParams{
		Keyword: keyword, Status: status,
	})
	if err != nil {
		return nil, 0, errs.Internal.Wrap(err)
	}

	out := make([]Tenant, 0, len(rows))
	for _, row := range rows {
		out = append(out, fromRow(row))
	}
	return out, total, nil
}

// Create 开一个组织，并把它的第一个管理员一起建好。
//
// ⚠️ **整件事必须在一个事务里**（§8.6）：建组织 → 建它的内置 admin 角色 →
// 给角色赋通配权限 → 建第一个管理员用户 → 绑角色。中间任何一步失败都会留下一个
// 半残的组织（有组织没管理员，客户根本登不进去），而平台端看着像开成功了。
//
// 返回的初始密码**只出现这一次** —— 平台管理员把它连同公司代码一起交给客户
// （§5：一次性凭据由管理员转交，零依赖，不用等发信能力）。
func (s *Service) Create(ctx context.Context, code, name string) (CreatedTenant, error) {
	password := auth.RandomPassword(tempPasswordChars)
	created, err := ProvisionTenant(ctx, s.store, ProvisionParams{
		Code:              code,
		Name:              name,
		AdminUsername:     adminUsername,
		AdminPasswordHash: auth.HashPassword(password),
		// 临时密码经过了两个人的手（平台管理员 → 客户），本人必须马上换掉。
		MustChangePassword: true,
		CreatedBy:          actorID(ctx),
	})
	if err != nil {
		return CreatedTenant{}, err
	}
	audit.SetResourceID(ctx, created.ID)
	return CreatedTenant{
		Tenant:        fromRow(created),
		AdminUsername: adminUsername,
		AdminPassword: password,
	}, nil
}

// ProvisionParams 是「建一个组织 + 它的第一个管理员」所需的一切。
type ProvisionParams struct {
	Code               string     // 公司代码，会做格式和保留字校验
	Name               string     // 组织名
	AdminUsername      string     // 首个管理员用户名（一般是 adminUsername="admin"）
	AdminEmail         *string    // 管理员邮箱，自助注册时带上；平台开租户时可空
	AdminPasswordHash  string     // 已经 argon2 哈希过的密码
	MustChangePassword bool       // 是否强制首次登录改密
	CreatedBy          *uuid.UUID // 谁建的；自助注册没有操作人，传 nil
}

// ProvisionTenant 在一个事务里建组织 → 内置 admin 角色 → 赋通配权限 → 首个管理员 → 绑角色。
//
// ⚠️ **平台端开租户和自助注册都走这一个函数** —— 建租户的步骤只有这一处，
// 两条路绝不会长歪。中间任何一步失败整个事务回滚，不会留下半残的组织（§8.6）。
// code 非法 / 保留 / 已被占用分别返回 ErrCodeInvalid / ErrCodeReserved / ErrCodeTaken。
func ProvisionTenant(ctx context.Context, store *repo.Store, p ProvisionParams) (repo.Tenant, error) {
	code := strings.ToLower(strings.TrimSpace(p.Code))
	if !rxTenantCode.MatchString(code) {
		return repo.Tenant{}, ErrCodeInvalid
	}
	if reservedCodes[code] {
		return repo.Tenant{}, ErrCodeReserved
	}

	tenantID, err := uuid.NewV7()
	if err != nil {
		return repo.Tenant{}, errs.Internal.Wrap(err)
	}
	roleID, err := uuid.NewV7()
	if err != nil {
		return repo.Tenant{}, errs.Internal.Wrap(err)
	}
	adminID, err := uuid.NewV7()
	if err != nil {
		return repo.Tenant{}, errs.Internal.Wrap(err)
	}

	var created repo.Tenant
	err = store.Platform().InTx(ctx, func(pq *repo.PlatformQueries) error {
		created, err = pq.CreateTenant(ctx, repo.CreateTenantParams{
			ID: tenantID, Code: code, Name: p.Name,
			Status: StatusActive, CreatedBy: p.CreatedBy,
		})
		if err != nil {
			return err
		}
		// 换到新组织的句柄上往里写东西。**这一步在代码里看得见**，只碰刚建出来的那个组织。
		tq := pq.ForTenant(created.ID)
		if _, err := tq.CreateBuiltinAdminRole(ctx, roleID); err != nil {
			return err
		}
		if err := tq.AddRolePermission(ctx, repo.AddRolePermissionArgs{
			RoleID: roleID, Resource: perm.Wildcard, Action: perm.Wildcard,
		}); err != nil {
			return err
		}
		if _, err := tq.CreateUser(ctx, repo.CreateUserArgs{
			ID:                 adminID,
			Username:           p.AdminUsername,
			DisplayName:        "管理员",
			Email:              p.AdminEmail,
			PasswordHash:       p.AdminPasswordHash,
			MustChangePassword: p.MustChangePassword,
			CreatedBy:          p.CreatedBy,
		}); err != nil {
			return err
		}
		return tq.AssignUserRole(ctx, repo.AssignUserRoleArgs{UserID: adminID, RoleID: roleID})
	})
	if err != nil {
		if repo.IsUniqueViolation(err, "uk_tenants_code") {
			return repo.Tenant{}, ErrCodeTaken
		}
		return repo.Tenant{}, errs.Internal.Wrap(err)
	}
	return created, nil
}

// adminUsername 是每个新组织第一个管理员的用户名。
//
// 固定成 admin：客户拿到的凭据是「公司代码 + admin + 密码」，少记一样东西。
// 用户名只在组织内唯一，所以每家公司都可以有自己的 admin。
const adminUsername = "admin"

// SetStatus 停用 / 启用一个组织。
//
// **没有删除**（§9.3）：审计链要完整、误删无法恢复、客户过两个月又回来也很常见。
// 真要彻底删（合规要求）走一次性脚本 + 人工确认，不做成界面上的按钮。
//
// 停用是立刻生效的：认证时会核 `tenants.status`，那家公司已经发出去的会话
// 下一个请求就失效（§8.2）。
func (s *Service) SetStatus(ctx context.Context, id uuid.UUID, version int, status string) (Tenant, error) {
	if status != StatusActive && status != StatusSuspended {
		return Tenant{}, errs.ValidationFailed.WithField("body.status", "只能是 active 或 suspended")
	}

	row, err := s.store.Platform().SetTenantStatus(ctx, repo.SetTenantStatusParams{
		ID: id, Version: int32(version), Status: status,
	})
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return Tenant{}, s.conflictOrNotFound(ctx, id)
		}
		return Tenant{}, errs.Internal.Wrap(err)
	}

	audit.SetResourceID(ctx, id)
	return fromRow(row), nil
}

// conflictOrNotFound 区分「版本对不上」和「组织本来就没有」。
func (s *Service) conflictOrNotFound(ctx context.Context, id uuid.UUID) error {
	if _, err := s.store.Platform().GetTenantByID(ctx, id); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return errs.NotFound
		}
		return errs.Internal.Wrap(err)
	}
	return errs.VersionConflict
}

func fromRow(row repo.Tenant) Tenant {
	return Tenant{
		ID:        row.ID,
		Code:      row.Code,
		Name:      row.Name,
		Status:    row.Status,
		UserCount: int(row.UserCount),
		CreatedAt: row.CreatedAt,
		Version:   int(row.Version),
	}
}

func optional(s string) *string {
	if s == "" {
		return nil
	}
	return &s
}

// actorID 取当前操作者（平台管理员）的 id，进 created_by。
func actorID(ctx context.Context) *uuid.UUID {
	p, ok := authz.PrincipalFrom(ctx)
	if !ok || !p.IsPlatform() {
		return nil
	}
	id := p.ID
	return &id
}

// ValidateTenantCode 校验公司代码的格式和保留字，给注册这类外部入口在建租户之前先挡一道。
// 校验规则和 ProvisionTenant 内部一致。
func ValidateTenantCode(code string) error {
	code = strings.ToLower(strings.TrimSpace(code))
	if !rxTenantCode.MatchString(code) {
		return ErrCodeInvalid
	}
	if reservedCodes[code] {
		return ErrCodeReserved
	}
	return nil
}
