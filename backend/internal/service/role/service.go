// Package role 是角色模块的业务层。
//
// handler 只调这里，不直接碰 repo（红线 #6）。
package role

import (
	"context"
	"errors"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/ramoncjs3/fries/internal/audit"
	"github.com/ramoncjs3/fries/internal/authz"
	"github.com/ramoncjs3/fries/internal/errs"
	"github.com/ramoncjs3/fries/internal/perm"
	"github.com/ramoncjs3/fries/internal/repo"
)

// 数据范围。和 DB 的 CHECK 约束一致（DECISIONS.md §3.3）。
const (
	ScopeAll  = "all"
	ScopeSelf = "self"
)

// Service 是角色服务。
type Service struct {
	store *repo.Store
}

// New 造角色服务。改权限是「清空再插回去」，必须原子 —— 事务从租户句柄上开
// （q.InTx），事务里拿到的仍然是同一个租户绑定的句柄（MULTI-TENANCY.md §9.6）。
func New(store *repo.Store) *Service { return &Service{store: store} }

// tenant 取当前请求的租户句柄。**每个对外方法的第一行**都该是它 ——
// 没有租户就报错，不是放行（MULTI-TENANCY.md §1.2 ②）。
func (s *Service) tenant(ctx context.Context) (*repo.TenantQueries, error) {
	id, err := authz.MustTenant(ctx)
	if err != nil {
		return nil, err
	}
	return s.store.ForTenant(id), nil
}

// Role 是一个角色。
type Role struct {
	ID          uuid.UUID `json:"id"`
	Key         string    `json:"key" doc:"角色标识，创建后不可改 —— 它是 Casbin 策略里的身份"`
	Name        string    `json:"name"`
	Description string    `json:"description"`
	DataScope   string    `json:"data_scope" doc:"all=看本组织全部 / self=只看自己创建的"`
	Builtin     bool      `json:"builtin" doc:"内置角色不可改不可删"`
	Status      string    `json:"status" doc:"active / disabled"`
	// Permissions 形如 ["user:list", "user:create"]。列表接口不返回它，详情才返回。
	Permissions     []string  `json:"permissions"`
	UserCount       int       `json:"user_count" doc:"绑定了这个角色的用户数"`
	PermissionCount int       `json:"permission_count"`
	CreatedAt       time.Time `json:"created_at"`
	UpdatedAt       time.Time `json:"updated_at"`
	Version         int       `json:"version" doc:"乐观锁版本号，更新时原样传回"`
}

// ListFilter 是角色查询条件。
type ListFilter struct {
	Keyword  string
	Status   string
	Page     int
	PageSize int
}

// Input 是新增/编辑角色的入参。
type Input struct {
	// Key 只在新增时用；编辑时忽略。
	Key         string
	Name        string
	Description string
	DataScope   string
	Status      string
	Permissions []string
}

// applyDefaults 给可选字段兜底（理由同 department.applyDefaults）。
func (in *Input) applyDefaults() {
	if in.Status == "" {
		in.Status = repo.StatusActive
	}
	if in.DataScope == "" {
		// 默认给最窄的范围。默认宽的话，建角色时忘了选就等于开了后门。
		in.DataScope = ScopeSelf
	}
}

// List 查角色。
func (s *Service) List(ctx context.Context, f ListFilter) ([]Role, int64, error) {
	q, err := s.tenant(ctx)
	if err != nil {
		return nil, 0, err
	}
	keyword := repo.LikePattern(f.Keyword)
	status := optional(f.Status)

	rows, err := q.ListRoles(ctx, repo.ListRolesArgs{
		Limit:   int32(f.PageSize),
		Offset:  int32((f.Page - 1) * f.PageSize),
		Keyword: keyword,
		Status:  status,
	})
	if err != nil {
		return nil, 0, errs.Internal.Wrap(err)
	}
	total, err := q.CountRoles(ctx, repo.CountRolesArgs{Keyword: keyword, Status: status})
	if err != nil {
		return nil, 0, errs.Internal.Wrap(err)
	}

	out := make([]Role, 0, len(rows))
	for _, row := range rows {
		out = append(out, Role{
			ID:              row.ID,
			Key:             row.Key,
			Name:            row.Name,
			Description:     row.Description,
			DataScope:       row.DataScope,
			Builtin:         row.Builtin,
			Status:          row.Status,
			Permissions:     []string{},
			UserCount:       int(row.UserCount),
			PermissionCount: int(row.PermissionCount),
			CreatedAt:       row.CreatedAt,
			UpdatedAt:       row.UpdatedAt,
			Version:         int(row.Version),
		})
	}
	return out, total, nil
}

// Get 取一个角色，**带上勾选的权限点**。
func (s *Service) Get(ctx context.Context, id uuid.UUID) (Role, error) {
	q, err := s.tenant(ctx)
	if err != nil {
		return Role{}, err
	}
	row, err := q.GetRole(ctx, id)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return Role{}, errs.NotFound
		}
		return Role{}, errs.Internal.Wrap(err)
	}

	points, err := permissionsOf(ctx, q, id)
	if err != nil {
		return Role{}, err
	}

	out := fromRow(repo.Role{
		ID:          row.ID,
		Key:         row.Key,
		Name:        row.Name,
		Description: row.Description,
		DataScope:   row.DataScope,
		Builtin:     row.Builtin,
		Status:      row.Status,
		CreatedAt:   row.CreatedAt,
		UpdatedAt:   row.UpdatedAt,
		Version:     row.Version,
	})
	out.Permissions = points
	out.PermissionCount = len(points)
	// 成员数由 SQL 一并算出来，和列表接口用的是同一段子查询 —— 详情页要显示它，
	// 两个接口对同一个字段给出不同答案是最难查的那类问题
	out.UserCount = int(row.UserCount)
	return out, nil
}

// Create 新增角色。
func (s *Service) Create(ctx context.Context, in Input) (Role, error) {
	q, err := s.tenant(ctx)
	if err != nil {
		return Role{}, err
	}
	in.applyDefaults()
	if err := validatePermissions(in.Permissions); err != nil {
		return Role{}, err
	}

	id, err := uuid.NewV7()
	if err != nil {
		return Role{}, errs.Internal.Wrap(err)
	}

	var created repo.Role
	err = q.InTx(ctx, func(q *repo.TenantQueries) error {
		created, err = q.CreateRole(ctx, repo.CreateRoleArgs{
			ID:          id,
			Key:         in.Key,
			Name:        in.Name,
			Description: in.Description,
			DataScope:   in.DataScope,
			Status:      in.Status,
			CreatedBy:   actorID(ctx),
		})
		if err != nil {
			return err
		}
		return writePermissions(ctx, q, id, in.Permissions)
	})
	if err != nil {
		if repo.IsUniqueViolation(err, "uk_roles_key") {
			return Role{}, ErrKeyTaken
		}
		return Role{}, errs.Internal.Wrap(err)
	}

	audit.SetResourceID(ctx, id)
	out := fromRow(created)
	out.Permissions = in.Permissions
	out.PermissionCount = len(in.Permissions)
	return out, nil
}

// Update 编辑角色。
//
// key 不给改：它是 Casbin 策略里的身份，改了等于换了个角色，
// 而已经签发出去的会话还带着老 key。
func (s *Service) Update(ctx context.Context, id uuid.UUID, version int, in Input) (Role, error) {
	q, err := s.tenant(ctx)
	if err != nil {
		return Role{}, err
	}
	current, err := q.GetRole(ctx, id)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return Role{}, errs.NotFound
		}
		return Role{}, errs.Internal.Wrap(err)
	}
	// 内置角色是权限体系的兜底：admin 被改成没权限的话，没人能再进后台改回来
	if current.Builtin {
		return Role{}, ErrBuiltinImmutable
	}
	in.applyDefaults()
	if err := validatePermissions(in.Permissions); err != nil {
		return Role{}, err
	}

	var updated repo.Role
	err = q.InTx(ctx, func(q *repo.TenantQueries) error {
		updated, err = q.UpdateRole(ctx, repo.UpdateRoleArgs{
			ID:          id,
			Name:        in.Name,
			Description: in.Description,
			DataScope:   in.DataScope,
			Status:      in.Status,
			Version:     int32(version),
		})
		if err != nil {
			return err
		}
		if err := q.ClearRolePermissions(ctx, id); err != nil {
			return err
		}
		return writePermissions(ctx, q, id, in.Permissions)
	})
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return Role{}, errs.VersionConflict
		}
		return Role{}, errs.Internal.Wrap(err)
	}

	audit.SetResourceID(ctx, id)
	out := fromRow(updated)
	out.Permissions = in.Permissions
	out.PermissionCount = len(in.Permissions)
	return out, nil
}

// Delete 软删除角色。
//
// 还有人在用就不给删 —— 直接删的话那些人会静默失去权限，
// 而管理员完全不知道自己刚刚动了谁。
func (s *Service) Delete(ctx context.Context, id uuid.UUID, version int) error {
	q, err := s.tenant(ctx)
	if err != nil {
		return err
	}
	current, err := q.GetRole(ctx, id)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return errs.NotFound
		}
		return errs.Internal.Wrap(err)
	}
	if current.Builtin {
		return ErrBuiltinImmutable
	}

	users, err := q.CountRoleUsers(ctx, id)
	if err != nil {
		return errs.Internal.Wrap(err)
	}
	accounts, err := q.CountRoleServiceAccounts(ctx, id)
	if err != nil {
		return errs.Internal.Wrap(err)
	}
	if users+accounts > 0 {
		return ErrHasMembers
	}

	if _, err := q.SoftDeleteRole(ctx, repo.SoftDeleteRoleArgs{
		ID:      id,
		Version: int32(version),
	}); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return errs.VersionConflict
		}
		return errs.Internal.Wrap(err)
	}

	audit.SetResourceID(ctx, id)
	return nil
}

func permissionsOf(ctx context.Context, q *repo.TenantQueries, id uuid.UUID) ([]string, error) {
	rows, err := q.ListRolePermissions(ctx, id)
	if err != nil {
		return nil, errs.Internal.Wrap(err)
	}
	out := make([]string, 0, len(rows))
	for _, row := range rows {
		out = append(out, row.Resource+":"+row.Action)
	}
	return out, nil
}

// validatePermissions 确认勾的每个权限点都是注册表里真有的。
//
// 不校验的话，前端传错一个字符串就会在 role_permissions 里留下一条永远匹配不上的
// 死策略 —— 页面上显示「勾了」，实际一点用没有，排查起来极其难受。
func validatePermissions(points []string) error {
	for _, raw := range points {
		resource, action, ok := splitPoint(raw)
		if !ok {
			return ErrUnknownPermission.Detailf("权限点 %q 格式不对，应该是 resource:action", raw)
		}
		// 通配只给内置 admin，普通角色勾不到（DECISIONS.md §3.1）
		if resource == perm.Wildcard || action == perm.Wildcard {
			return ErrWildcardReserved
		}
		if !perm.Has(resource, action) {
			return ErrUnknownPermission.Detailf("权限点 %q 不存在", raw)
		}
		// ⚠️ 平台管理端的权限点，租户的角色一个都不能勾（MULTI-TENANCY.md §3.2 ②、⑤）。
		//
		// 这是「拿到 user:update 就能自我提权」那条已知边界的**上限**：
		// 组织内部谁把自己提成管理员都还在组织里，但**永远够不到平台管理员**。
		// 报「不存在」而不是「无权限」—— 别把平台有哪些权限点透给租户（§11.2 同理）。
		if perm.IsPlatform(resource, action) {
			return ErrUnknownPermission.Detailf("权限点 %q 不存在", raw)
		}
	}
	return nil
}

func writePermissions(ctx context.Context, q *repo.TenantQueries, roleID uuid.UUID, points []string) error {
	for _, raw := range points {
		resource, action, ok := splitPoint(raw)
		if !ok {
			continue // validatePermissions 已经挡过了，这里只是防御
		}
		if err := q.AddRolePermission(ctx, repo.AddRolePermissionArgs{
			RoleID:   roleID,
			Resource: resource,
			Action:   action,
		}); err != nil {
			return err
		}
	}
	return nil
}

// splitPoint 把 "user:list" 拆成 resource 和 action。
// 用最后一个冒号切：模块 key 允许带点分组（settings.security），但不含冒号。
func splitPoint(raw string) (resource, action string, ok bool) {
	for i := len(raw) - 1; i >= 0; i-- {
		if raw[i] != ':' {
			continue
		}
		resource, action = raw[:i], raw[i+1:]
		return resource, action, resource != "" && action != ""
	}
	return "", "", false
}

func fromRow(row repo.Role) Role {
	return Role{
		ID:          row.ID,
		Key:         row.Key,
		Name:        row.Name,
		Description: row.Description,
		DataScope:   row.DataScope,
		Builtin:     row.Builtin,
		Status:      row.Status,
		Permissions: []string{},
		CreatedAt:   row.CreatedAt,
		UpdatedAt:   row.UpdatedAt,
		Version:     int(row.Version),
	}
}

func optional(s string) *string {
	if s == "" {
		return nil
	}
	return &s
}

func actorID(ctx context.Context) *uuid.UUID {
	p, ok := authz.PrincipalFrom(ctx)
	if !ok || !p.IsUser() {
		return nil
	}
	id := p.ID
	return &id
}
