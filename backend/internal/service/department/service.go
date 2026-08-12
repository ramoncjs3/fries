// Package department 是部门模块的业务层。
//
// handler 只调这里，不直接碰 repo（红线 #6）。
package department

import (
	"context"
	"errors"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/ramoncjs3/fries/internal/audit"
	"github.com/ramoncjs3/fries/internal/authz"
	"github.com/ramoncjs3/fries/internal/errs"
	"github.com/ramoncjs3/fries/internal/repo"
)

// Service 是部门服务。
type Service struct {
	store *repo.Store
}

// New 造部门服务。
func New(store *repo.Store) *Service { return &Service{store: store} }

// tenant 取当前请求的租户句柄。**每个对外方法的第一行**都该是它 ——
// 没有租户就报错，不是放行（MULTI-TENANCY.md §1.2 ②）。
//
// 内部的 checkXxx 之类不再各自取一次，由调用方把 q 传进来，
// 免得同一个请求里两次取到不同的句柄。
func (s *Service) tenant(ctx context.Context) (*repo.TenantQueries, error) {
	id, err := authz.MustTenant(ctx)
	if err != nil {
		return nil, err
	}
	return s.store.ForTenant(id), nil
}

// Department 是一个部门节点。
//
// 类型名会原样进 OpenAPI 的 schema 名，所以带模块前缀 —— 别的模块也会有
// 叫 Entry / Item 的东西，撞了 huma 只能自动加后缀去重，前端类型名就飘了。
type Department struct {
	ID        uuid.UUID  `json:"id"`
	ParentID  *uuid.UUID `json:"parent_id" doc:"上级部门；根节点为 null"`
	Name      string     `json:"name"`
	Code      string     `json:"code" doc:"部门编号，对接外部系统用"`
	SortOrder int        `json:"sort_order" doc:"同级排序，小的在前"`
	Remark    string     `json:"remark"`
	Status    string     `json:"status" doc:"active / disabled"`
	UserCount int        `json:"user_count" doc:"直属成员数，不含下级部门"`
	CreatedAt time.Time  `json:"created_at"`
	UpdatedAt time.Time  `json:"updated_at"`
	Version   int        `json:"version" doc:"乐观锁版本号，更新时原样传回"`
}

// ListFilter 是部门查询条件。
//
// **没有分页**：部门是树，切成一页页的话父节点可能在第 2 页、子节点在第 1 页，
// 前端根本拼不出树来。内部系统撑死几百个节点，一次全取更简单也更快。
type ListFilter struct {
	Keyword string
	Status  string
}

// Input 是新增/编辑部门的入参。
type Input struct {
	ParentID  *uuid.UUID
	Name      string
	Code      string
	SortOrder int
	Remark    string
	Status    string
}

// applyDefaults 给可选字段兜底。
//
// **默认值必须在 service 兜，不能指望 handler**：huma 的 `default:` tag 只写进
// OpenAPI 文档，反序列化时不会填值；漏了就会往库里写空串，撞 CHECK 约束变成 500。
func (in *Input) applyDefaults() {
	if in.Status == "" {
		in.Status = repo.StatusActive
	}
}

// List 查部门树的全部节点（前端自己拼树）。
func (s *Service) List(ctx context.Context, f ListFilter) ([]Department, error) {
	q, err := s.tenant(ctx)
	if err != nil {
		return nil, err
	}
	rows, err := q.ListDepartments(ctx, repo.ListDepartmentsArgs{
		Keyword: repo.LikePattern(f.Keyword),
		Status:  optional(f.Status),
	})
	if err != nil {
		return nil, errs.Internal.Wrap(err)
	}

	out := make([]Department, 0, len(rows))
	for _, row := range rows {
		out = append(out, Department{
			ID:        row.ID,
			ParentID:  row.ParentID,
			Name:      row.Name,
			Code:      row.Code,
			SortOrder: int(row.SortOrder),
			Remark:    row.Remark,
			Status:    row.Status,
			UserCount: int(row.UserCount),
			CreatedAt: row.CreatedAt,
			UpdatedAt: row.UpdatedAt,
			Version:   int(row.Version),
		})
	}
	return out, nil
}

// Get 按 ID 取一个部门。
func (s *Service) Get(ctx context.Context, id uuid.UUID) (Department, error) {
	q, err := s.tenant(ctx)
	if err != nil {
		return Department{}, err
	}
	row, err := q.GetDepartment(ctx, id)
	if err != nil {
		return Department{}, notFoundOr(err)
	}
	return fromRow(row), nil
}

// Create 新增部门。
func (s *Service) Create(ctx context.Context, in Input) (Department, error) {
	q, err := s.tenant(ctx)
	if err != nil {
		return Department{}, err
	}
	in.applyDefaults()
	if err := checkParent(ctx, q, in.ParentID); err != nil {
		return Department{}, err
	}

	id, err := uuid.NewV7()
	if err != nil {
		return Department{}, errs.Internal.Wrap(err)
	}

	row, err := q.CreateDepartment(ctx, repo.CreateDepartmentArgs{
		ID:        id,
		ParentID:  in.ParentID,
		Name:      in.Name,
		Code:      in.Code,
		SortOrder: int32(in.SortOrder),
		Remark:    in.Remark,
		Status:    in.Status,
		CreatedBy: actorID(ctx),
	})
	if err != nil {
		return Department{}, uniqueOr(err)
	}

	audit.SetResourceID(ctx, row.ID)
	return fromRow(row), nil
}

// Update 编辑部门。version 对不上返回 common.version_conflict（§2.4）。
func (s *Service) Update(ctx context.Context, id uuid.UUID, version int, in Input) (Department, error) {
	q, err := s.tenant(ctx)
	if err != nil {
		return Department{}, err
	}
	in.applyDefaults()
	if err := checkParent(ctx, q, in.ParentID); err != nil {
		return Department{}, err
	}
	if err := checkCycle(ctx, q, id, in.ParentID); err != nil {
		return Department{}, err
	}

	row, err := q.UpdateDepartment(ctx, repo.UpdateDepartmentArgs{
		ID:        id,
		ParentID:  in.ParentID,
		Name:      in.Name,
		Code:      in.Code,
		SortOrder: int32(in.SortOrder),
		Remark:    in.Remark,
		Status:    in.Status,
		Version:   int32(version),
	})
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return Department{}, conflictOrNotFound(ctx, q, id)
		}
		return Department{}, uniqueOr(err)
	}

	audit.SetResourceID(ctx, row.ID)
	return fromRow(row), nil
}

// Delete 软删除部门。
//
// 下面还有子部门或成员就不给删 —— 直接删会留下一堆挂在不存在的父节点上的孤儿，
// 树就散了。外键能挡住一部分，但报出来是 23503，用户看不懂。
func (s *Service) Delete(ctx context.Context, id uuid.UUID, version int) error {
	q, err := s.tenant(ctx)
	if err != nil {
		return err
	}

	children, err := q.CountDepartmentChildren(ctx, &id)
	if err != nil {
		return errs.Internal.Wrap(err)
	}
	if children > 0 {
		return ErrHasChildren
	}

	users, err := q.CountDepartmentUsers(ctx, &id)
	if err != nil {
		return errs.Internal.Wrap(err)
	}
	if users > 0 {
		return ErrHasUsers
	}

	if _, err := q.SoftDeleteDepartment(ctx, repo.SoftDeleteDepartmentArgs{
		ID:      id,
		Version: int32(version),
	}); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return conflictOrNotFound(ctx, q, id)
		}
		return errs.Internal.Wrap(err)
	}

	audit.SetResourceID(ctx, id)
	return nil
}

// checkParent 确认上级部门存在。
//
// 句柄由调用方传进来 —— 它已经绑定了本次请求的租户，所以「上级部门存在」
// 天然是「**在本租户内**存在」。别在这里另取一次句柄。
func checkParent(ctx context.Context, q *repo.TenantQueries, parentID *uuid.UUID) error {
	if parentID == nil {
		return nil
	}
	if _, err := q.GetDepartment(ctx, *parentID); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return ErrParentNotFound
		}
		return errs.Internal.Wrap(err)
	}
	return nil
}

// checkCycle 挡住「把部门挂到自己或自己的下级里」。
//
// 不挡的话会造出一个从根断开的环：那一支再也查不出来，页面上直接消失，
// 而数据库层面完全合法（自引用外键管不了环）。
func checkCycle(ctx context.Context, q *repo.TenantQueries, id uuid.UUID, parentID *uuid.UUID) error {
	if parentID == nil {
		return nil
	}
	if *parentID == id {
		return ErrCycle
	}
	subtree, err := q.ListDepartmentSubtreeIDs(ctx, id)
	if err != nil {
		return errs.Internal.Wrap(err)
	}
	for _, node := range subtree {
		if node == *parentID {
			return ErrCycle
		}
	}
	return nil
}

// conflictOrNotFound 区分「版本对不上」和「记录本来就没了」。
//
// UPDATE ... WHERE id = ? AND version = ? 影响 0 行有两种可能，
// 全都报 version_conflict 的话，删掉的记录会提示「刷新后重试」，刷新也没用。
func conflictOrNotFound(ctx context.Context, q *repo.TenantQueries, id uuid.UUID) error {
	if _, err := q.GetDepartment(ctx, id); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return errs.NotFound
		}
		return errs.Internal.Wrap(err)
	}
	return errs.VersionConflict
}

func fromRow(row repo.Department) Department {
	return Department{
		ID:        row.ID,
		ParentID:  row.ParentID,
		Name:      row.Name,
		Code:      row.Code,
		SortOrder: int(row.SortOrder),
		Remark:    row.Remark,
		Status:    row.Status,
		CreatedAt: row.CreatedAt,
		UpdatedAt: row.UpdatedAt,
		Version:   int(row.Version),
	}
}

func notFoundOr(err error) error {
	if errors.Is(err, pgx.ErrNoRows) {
		return errs.NotFound
	}
	return errs.Internal.Wrap(err)
}

// uniqueOr 把唯一索引冲突翻成人话。
//
// 靠索引名分辨是编号重了还是同级重名 —— 让数据库来判，不要先 SELECT 再 INSERT：
// 那中间有竞态窗口，并发下照样会撞。
func uniqueOr(err error) error {
	switch {
	case repo.IsUniqueViolation(err, "uk_departments_code"):
		return ErrCodeTaken
	case repo.IsUniqueViolation(err, "uk_departments_sibling_name"),
		repo.IsUniqueViolation(err, "uk_departments_root_name"):
		return ErrNameTaken
	}
	return errs.Internal.Wrap(err)
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
