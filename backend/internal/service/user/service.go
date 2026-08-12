// Package user 是用户管理模块的业务层。
//
// 和 internal/auth 的分工：auth 管**登录链路**（验密码、发会话），
// 这里管**管理员对用户的操作**（建号、改资料、分配角色、重置密码）。
//
// handler 只调这里，不直接碰 repo（红线 #6）。
package user

import (
	"context"
	"errors"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/ramoncjs3/fries/internal/audit"
	"github.com/ramoncjs3/fries/internal/auth"
	"github.com/ramoncjs3/fries/internal/authz"
	"github.com/ramoncjs3/fries/internal/config"
	"github.com/ramoncjs3/fries/internal/errs"
	"github.com/ramoncjs3/fries/internal/perm"
	"github.com/ramoncjs3/fries/internal/repo"
)

// tempPasswordChars 是管理员重置密码时生成的临时密码有多少个字符。
const tempPasswordChars = 14

// Service 是用户管理服务。
type Service struct {
	store    *repo.Store
	settings *config.Settings
}

// New 造用户管理服务。
func New(store *repo.Store, settings *config.Settings) *Service {
	return &Service{store: store, settings: settings}
}

// tenant 取当前请求的租户句柄。**每个对外方法的第一行**都该是它 ——
// 没有租户就报错，不是放行（MULTI-TENANCY.md §1.2 ②）。
//
// 私有的 checkXxx / ensureXxx 一律由调用方把 q 传进来，不各自再取一次：
// 同一个请求里必须自始至终用同一个租户句柄。
func (s *Service) tenant(ctx context.Context) (*repo.TenantQueries, error) {
	id, err := authz.MustTenant(ctx)
	if err != nil {
		return nil, err
	}
	return s.store.ForTenant(id), nil
}

// User 是一个用户。
//
// **绝不包含 password_hash**：序列化的结构体里根本没有这个字段，
// 就不存在「哪天加了个 json tag 不小心漏出去」的可能。
type User struct {
	ID                 uuid.UUID  `json:"id"`
	Username           string     `json:"username" doc:"登录用户名，建后不可改"`
	DisplayName        string     `json:"display_name"`
	Email              string     `json:"email"`
	Phone              string     `json:"phone"`
	Status             string     `json:"status" doc:"active / disabled"`
	DepartmentID       *uuid.UUID `json:"department_id"`
	DepartmentName     string     `json:"department_name"`
	RoleIDs            []string   `json:"role_ids" doc:"绑定的角色 ID；列表接口不返回，详情才返回"`
	RoleNames          string     `json:"role_names" doc:"角色名，逗号分隔，列表直接显示"`
	MustChangePassword bool       `json:"must_change_password" doc:"下次登录必须改密码"`
	LockedUntil        *time.Time `json:"locked_until" doc:"登录失败过多被锁到什么时候"`
	LastLoginAt        *time.Time `json:"last_login_at"`
	CreatedAt          time.Time  `json:"created_at"`
	UpdatedAt          time.Time  `json:"updated_at"`
	Version            int        `json:"version" doc:"乐观锁版本号，更新时原样传回"`
}

// ListFilter 是用户查询条件。
type ListFilter struct {
	Keyword string
	Status  string
	// DepartmentIDs 可以多选：一次筛好几个部门的人，选完批量调岗。
	DepartmentIDs []uuid.UUID
	// IncludeUnassigned 把「没有部门的人」也算进来。
	//
	// **这个必须有**：没有它就没办法回答「谁还没分部门」——
	// 那些人不属于树上任何一个节点，在部门页永远看不到。
	IncludeUnassigned bool
	Page              int
	PageSize          int
}

// Input 是新增/编辑用户的入参。
type Input struct {
	// Username 只在新增时用；编辑时忽略。
	Username     string
	DisplayName  string
	Email        string
	Phone        string
	Status       string
	DepartmentID *uuid.UUID
	RoleIDs      []uuid.UUID
}

// CreatedUser 是新建用户的结果，带上只出现这一次的初始密码。
type CreatedUser struct {
	User User `json:"user"`
	// InitialPassword 只在这里出现一次，库里存的是哈希。
	InitialPassword string `json:"initial_password" doc:"初始密码，只显示这一次，请立即转交本人"`
}

// applyDefaults 给可选字段兜底（理由同 department.applyDefaults）。
func (in *Input) applyDefaults() {
	if in.Status == "" {
		in.Status = repo.StatusActive
	}
}

// List 查用户。
func (s *Service) List(ctx context.Context, f ListFilter) ([]User, int64, error) {
	q, err := s.tenant(ctx)
	if err != nil {
		return nil, 0, err
	}
	keyword := repo.LikePattern(f.Keyword)
	status := optional(f.Status)

	// sqlc 生成的是 []uuid.UUID，nil 会被当成 NULL 而不是空数组，SQL 里 cardinality(NULL) 也是 NULL
	deptIDs := f.DepartmentIDs
	if deptIDs == nil {
		deptIDs = []uuid.UUID{}
	}

	rows, err := q.ListUsers(ctx, repo.ListUsersArgs{
		Limit:             int32(f.PageSize),
		Offset:            int32((f.Page - 1) * f.PageSize),
		Keyword:           keyword,
		Status:            status,
		DepartmentIds:     deptIDs,
		IncludeUnassigned: f.IncludeUnassigned,
	})
	if err != nil {
		return nil, 0, errs.Internal.Wrap(err)
	}
	total, err := q.CountUsersFiltered(ctx, repo.CountUsersFilteredArgs{
		Keyword:           keyword,
		Status:            status,
		DepartmentIds:     deptIDs,
		IncludeUnassigned: f.IncludeUnassigned,
	})
	if err != nil {
		return nil, 0, errs.Internal.Wrap(err)
	}

	out := make([]User, 0, len(rows))
	for _, row := range rows {
		item := fromRow(row.User)
		item.DepartmentName = textOr(row.DepartmentName)
		item.RoleNames = row.RoleNames
		out = append(out, item)
	}
	return out, total, nil
}

// Get 取一个用户，带上角色 ID 列表。
func (s *Service) Get(ctx context.Context, id uuid.UUID) (User, error) {
	q, err := s.tenant(ctx)
	if err != nil {
		return User{}, err
	}
	row, err := q.GetUserWithDepartment(ctx, id)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return User{}, errs.NotFound
		}
		return User{}, errs.Internal.Wrap(err)
	}

	roleIDs, err := q.ListRoleIDsOfUser(ctx, id)
	if err != nil {
		return User{}, errs.Internal.Wrap(err)
	}

	out := fromRow(row.User)
	out.DepartmentName = textOr(row.DepartmentName)
	out.RoleIDs = make([]string, 0, len(roleIDs))
	for _, rid := range roleIDs {
		out.RoleIDs = append(out.RoleIDs, rid.String())
	}
	return out, nil
}

// Create 新建用户。
//
// 初始密码随机生成、只返回一次、且标记「首次登录必须改密」——
// 不让管理员自己设一个（大概率是 123456），也不写进任何日志（DECISIONS.md §6）。
func (s *Service) Create(ctx context.Context, in Input) (CreatedUser, error) {
	q, err := s.tenant(ctx)
	if err != nil {
		return CreatedUser{}, err
	}
	in.applyDefaults()
	if err := checkDepartment(ctx, q, in.DepartmentID); err != nil {
		return CreatedUser{}, err
	}
	if err := checkRoles(ctx, q, in.RoleIDs); err != nil {
		return CreatedUser{}, err
	}

	id, err := uuid.NewV7()
	if err != nil {
		return CreatedUser{}, errs.Internal.Wrap(err)
	}
	password := auth.RandomPassword(tempPasswordChars)

	var created repo.User
	err = q.InTx(ctx, func(q *repo.TenantQueries) error {
		created, err = q.CreateManagedUser(ctx, repo.CreateManagedUserArgs{
			ID:           id,
			Username:     in.Username,
			DisplayName:  in.DisplayName,
			Email:        optional(in.Email),
			Phone:        optional(in.Phone),
			PasswordHash: auth.HashPassword(password),
			Status:       in.Status,
			DepartmentID: in.DepartmentID,
			CreatedBy:    actorID(ctx),
		})
		if err != nil {
			return err
		}
		return assignRoles(ctx, q, id, in.RoleIDs)
	})
	if err != nil {
		return CreatedUser{}, identifierTakenOr(err)
	}

	audit.SetResourceID(ctx, id)
	return CreatedUser{User: fromRow(created), InitialPassword: password}, nil
}

// Update 编辑用户资料并重设角色。
func (s *Service) Update(ctx context.Context, id uuid.UUID, version int, in Input) (User, error) {
	q, err := s.tenant(ctx)
	if err != nil {
		return User{}, err
	}
	in.applyDefaults()
	if err := checkDepartment(ctx, q, in.DepartmentID); err != nil {
		return User{}, err
	}
	if err := checkRoles(ctx, q, in.RoleIDs); err != nil {
		return User{}, err
	}
	// 改完之后这个人还算不算「可用的超级管理员」。
	//
	// ⚠️ **不能只看 status**：把最后一个管理员的角色勾掉、状态仍然留在启用，
	// 一样会让所有人失去后台入口，而且更隐蔽 —— 页面上那个人看着还好好的。
	stillAdmin, err := s.rolesGrantWildcard(ctx, q, in.RoleIDs)
	if err != nil {
		return User{}, err
	}
	if in.Status != repo.StatusActive || !stillAdmin {
		if err := s.ensureNotLastAdmin(ctx, q, id); err != nil {
			return User{}, err
		}
	}

	var updated repo.User
	err = q.InTx(ctx, func(q *repo.TenantQueries) error {
		var err error
		updated, err = q.UpdateManagedUser(ctx, repo.UpdateManagedUserArgs{
			ID:           id,
			DisplayName:  in.DisplayName,
			Email:        optional(in.Email),
			Phone:        optional(in.Phone),
			Status:       in.Status,
			DepartmentID: in.DepartmentID,
			Version:      int32(version),
		})
		if err != nil {
			return err
		}
		if err := q.ClearUserRoles(ctx, id); err != nil {
			return err
		}
		return assignRoles(ctx, q, id, in.RoleIDs)
	})
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return User{}, conflictOrNotFound(ctx, q, id)
		}
		return User{}, identifierTakenOr(err)
	}

	// 停用之后要把会话踢掉，否则他还能继续用到 cookie 过期为止
	if updated.Status != repo.StatusActive {
		if err := q.RevokeUserSessions(ctx, id); err != nil {
			return User{}, errs.Internal.Wrap(err)
		}
	}

	audit.SetResourceID(ctx, id)
	return fromRow(updated), nil
}

// Delete 软删除用户，并立刻吊销他的所有会话。
func (s *Service) Delete(ctx context.Context, id uuid.UUID, version int) error {
	q, err := s.tenant(ctx)
	if err != nil {
		return err
	}
	if err := s.forbidSelf(ctx, id); err != nil {
		return err
	}
	if err := s.ensureNotLastAdmin(ctx, q, id); err != nil {
		return err
	}

	if _, err := q.SoftDeleteUser(ctx, repo.SoftDeleteUserArgs{
		ID:      id,
		Version: int32(version),
	}); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return conflictOrNotFound(ctx, q, id)
		}
		return errs.Internal.Wrap(err)
	}

	// 只软删不踢会话的话，人被「删掉」了还能继续用系统
	if err := q.RevokeUserSessions(ctx, id); err != nil {
		return errs.Internal.Wrap(err)
	}

	audit.SetResourceID(ctx, id)
	return nil
}

// ResetPassword 管理员重置某人的密码，返回只出现一次的临时密码。
//
// 同时把这个人的所有会话踢掉：密码被重置往往意味着号可能已经不安全了，
// 留着旧会话等于没重置。
func (s *Service) ResetPassword(ctx context.Context, id uuid.UUID) (string, error) {
	q, err := s.tenant(ctx)
	if err != nil {
		return "", err
	}
	if err := s.forbidSelf(ctx, id); err != nil {
		return "", err
	}

	password := auth.RandomPassword(tempPasswordChars)
	rows, err := q.ResetUserPassword(ctx, repo.ResetUserPasswordArgs{
		ID:           id,
		PasswordHash: auth.HashPassword(password),
	})
	if err != nil {
		return "", errs.Internal.Wrap(err)
	}
	if rows == 0 {
		return "", errs.NotFound
	}
	if err := q.RevokeUserSessions(ctx, id); err != nil {
		return "", errs.Internal.Wrap(err)
	}

	audit.SetResourceID(ctx, id)
	return password, nil
}

// SetDepartment 批量调整这些人的部门归属。deptID 为 nil 表示移出部门。
//
// 放在 user 这一层而不是 department：改的是 users 表，权限点也该是 user:update ——
// 「能编部门」和「能把人调来调去」不是一回事。
func (s *Service) SetDepartment(ctx context.Context, userIDs []uuid.UUID, deptID *uuid.UUID) (int64, error) {
	if len(userIDs) == 0 {
		return 0, nil
	}
	q, err := s.tenant(ctx)
	if err != nil {
		return 0, err
	}
	if err := checkDepartment(ctx, q, deptID); err != nil {
		return 0, err
	}

	affected, err := q.SetUsersDepartment(ctx, repo.SetUsersDepartmentArgs{
		UserIds:      userIDs,
		DepartmentID: deptID,
	})
	if err != nil {
		return 0, errs.Internal.Wrap(err)
	}
	if deptID != nil {
		audit.SetResourceID(ctx, *deptID)
	}
	return affected, nil
}

// Candidates 列出可以加进某个部门的人：**不在这个部门里的**活跃用户。
func (s *Service) Candidates(ctx context.Context, deptID *uuid.UUID, keyword string, limit int) ([]User, error) {
	q, err := s.tenant(ctx)
	if err != nil {
		return nil, err
	}
	rows, err := q.ListUsersNotInDepartment(ctx, repo.ListUsersNotInDepartmentArgs{
		Limit:        int32(limit),
		DepartmentID: deptID,
		Keyword:      repo.LikePattern(keyword),
	})
	if err != nil {
		return nil, errs.Internal.Wrap(err)
	}
	out := make([]User, 0, len(rows))
	for _, row := range rows {
		item := fromRow(row.User)
		item.DepartmentName = textOr(row.DepartmentName)
		out = append(out, item)
	}
	return out, nil
}

// forbidSelf 挡住「对自己动手」。
//
// 自己删自己 / 自己重置自己是典型的误操作，而且删完就再也进不来了。
// 改自己的密码走 /auth/change-password，那条路要验原密码。
func (s *Service) forbidSelf(ctx context.Context, id uuid.UUID) error {
	p, ok := authz.PrincipalFrom(ctx)
	if ok && p.IsUser() && p.ID == id {
		return ErrSelfTarget
	}
	return nil
}

// ensureNotLastAdmin 确认停用/删除之后还剩至少一个可用的超级管理员。
//
// 不拦的话，把最后一个管理员停掉，所有人就都被锁在门外了 —— 只能上数据库救。
func (s *Service) ensureNotLastAdmin(ctx context.Context, q *repo.TenantQueries, id uuid.UUID) error {
	remaining, err := q.CountActiveAdmins(ctx, id)
	if err != nil {
		return errs.Internal.Wrap(err)
	}
	if remaining > 0 {
		return nil
	}
	// 目标本人不是管理员的话，少他一个也无所谓
	isAdmin, err := s.hasWildcard(ctx, q, id)
	if err != nil {
		return err
	}
	if !isAdmin {
		return nil
	}
	return ErrLastAdmin
}

// hasWildcard 判断一个用户当前是不是超级管理员。
func (s *Service) hasWildcard(ctx context.Context, q *repo.TenantQueries, id uuid.UUID) (bool, error) {
	roleIDs, err := q.ListRoleIDsOfUser(ctx, id)
	if err != nil {
		return false, errs.Internal.Wrap(err)
	}
	return s.rolesGrantWildcard(ctx, q, roleIDs)
}

// rolesGrantWildcard 判断一组角色里有没有带通配权限的。
//
// **只认启用中的角色** —— 停用的角色不进 Casbin 策略，挂着它等于没有权限，
// 和 CountActiveAdmins 的口径必须一致，否则两边一个说是、一个说不是。
func (s *Service) rolesGrantWildcard(ctx context.Context, q *repo.TenantQueries, roleIDs []uuid.UUID) (bool, error) {
	if len(roleIDs) == 0 {
		return false, nil
	}
	roles, err := q.ListRolesByIDs(ctx, roleIDs)
	if err != nil {
		return false, errs.Internal.Wrap(err)
	}
	for _, r := range roles {
		if r.Status != repo.StatusActive {
			continue
		}
		points, err := q.ListRolePermissions(ctx, r.ID)
		if err != nil {
			return false, errs.Internal.Wrap(err)
		}
		for _, p := range points {
			if p.Resource == perm.Wildcard && p.Action == perm.Wildcard {
				return true, nil
			}
		}
	}
	return false, nil
}

func checkDepartment(ctx context.Context, q *repo.TenantQueries, id *uuid.UUID) error {
	if id == nil {
		return nil
	}
	if _, err := q.GetDepartment(ctx, *id); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return ErrDepartmentNotFound
		}
		return errs.Internal.Wrap(err)
	}
	return nil
}

// checkRoles 确认选的角色都存在且没被停用。
//
// 不校验的话 user_roles 里会留下指向已停用角色的行：页面上显示「有这个角色」，
// 实际一点权限都没有。
func checkRoles(ctx context.Context, q *repo.TenantQueries, ids []uuid.UUID) error {
	if len(ids) == 0 {
		return nil
	}
	found, err := q.ListRolesByIDs(ctx, ids)
	if err != nil {
		return errs.Internal.Wrap(err)
	}
	alive := make(map[uuid.UUID]bool, len(found))
	for _, r := range found {
		if r.Status == repo.StatusActive {
			alive[r.ID] = true
		}
	}
	for _, id := range ids {
		if !alive[id] {
			return ErrUnknownRole.Detailf("角色 %s 不存在或已停用", id)
		}
	}
	return nil
}

func conflictOrNotFound(ctx context.Context, q *repo.TenantQueries, id uuid.UUID) error {
	if _, err := q.GetUserByID(ctx, id); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return errs.NotFound
		}
		return errs.Internal.Wrap(err)
	}
	return errs.VersionConflict
}

func assignRoles(ctx context.Context, q *repo.TenantQueries, userID uuid.UUID, roleIDs []uuid.UUID) error {
	for _, rid := range roleIDs {
		if err := q.AssignUserRole(ctx, repo.AssignUserRoleArgs{UserID: userID, RoleID: rid}); err != nil {
			return err
		}
	}
	return nil
}

// identifierTakenOr 按索引名把唯一冲突翻成人话。
func identifierTakenOr(err error) error {
	switch {
	case repo.IsUniqueViolation(err, "uk_users_username"):
		return ErrUsernameTaken
	case repo.IsUniqueViolation(err, "uk_users_email"):
		return ErrEmailTaken
	case repo.IsUniqueViolation(err, "uk_users_phone"):
		return ErrPhoneTaken
	}
	return errs.Internal.Wrap(err)
}

func fromRow(row repo.User) User {
	return User{
		ID:                 row.ID,
		Username:           row.Username,
		DisplayName:        row.DisplayName,
		Email:              textOr(row.Email),
		Phone:              textOr(row.Phone),
		Status:             row.Status,
		DepartmentID:       row.DepartmentID,
		RoleIDs:            []string{},
		MustChangePassword: row.MustChangePassword,
		LockedUntil:        row.LockedUntil,
		LastLoginAt:        row.LastLoginAt,
		CreatedAt:          row.CreatedAt,
		UpdatedAt:          row.UpdatedAt,
		Version:            int(row.Version),
	}
}

func textOr(v *string) string {
	if v == nil {
		return ""
	}
	return *v
}

func optional(s string) *string {
	s = strings.TrimSpace(s)
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
