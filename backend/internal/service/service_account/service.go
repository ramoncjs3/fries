// Package serviceaccount 是机器账号的业务层：外部系统拿 API Key 调进来的那种身份
// （DECISIONS.md §8.1）。
//
// 🔴 **密钥只在两个时刻以明文出现：新建、轮换。** 之后库里只有哈希，
// 谁都取不回来 —— 包括平台管理员、包括直接查库的人。丢了就轮换，没有第二条路。
// 这是刻意的：能取回的凭据等于明文存储。
package serviceaccount

import (
	"context"
	"errors"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/ramoncjs3/fries/internal/auth"
	"github.com/ramoncjs3/fries/internal/authz"
	"github.com/ramoncjs3/fries/internal/errs"
	"github.com/ramoncjs3/fries/internal/repo"
)

// 账号状态。和 DB 的 CHECK 约束一致。
const (
	StatusActive   = "active"
	StatusDisabled = "disabled"
)

// Service 是机器账号服务。
type Service struct {
	store *repo.Store
}

// New 造机器账号服务。
func New(store *repo.Store) *Service { return &Service{store: store} }

// ServiceAccount 是列表和详情里的一个机器账号。
//
// ⚠️ **没有密钥字段，这是有意的。** 它的形状就是「密钥取不回来」这条规矩的体现 ——
// 加一个 `Key string` 字段进来，后面总有人会去填它。
type ServiceAccount struct {
	ID          uuid.UUID `json:"id"`
	Name        string    `json:"name"`
	Description string    `json:"description"`
	// KeyPrefix 是密钥的前半段，明文存的那部分。
	// 展示它是为了让人**认得出手里那串是哪一个** —— 对接方报「我的 key 不好使了」，
	// 你得能对上号。后半段（secret）只有哈希。
	KeyPrefix  string     `json:"key_prefix"`
	RoleID     uuid.UUID  `json:"role_id"`
	RoleName   string     `json:"role_name"`
	Status     string     `json:"status" doc:"active / disabled"`
	ExpiresAt  *time.Time `json:"expires_at" doc:"到期后认证直接失败，空表示不过期"`
	LastUsedAt *time.Time `json:"last_used_at" doc:"最后一次用它调接口的时间，空表示从没用过"`
	CreatedAt  time.Time  `json:"created_at"`
	Version    int        `json:"version" doc:"乐观锁版本号，更新时原样传回"`
}

// CreatedKey 是新建或轮换的结果：**一次性密钥只在这里出现**。
type CreatedKey struct {
	Account ServiceAccount `json:"account"`
	// Key 是完整的 API Key，形如 `fsa_<prefix>_<secret>`。**只显示这一次。**
	Key string `json:"key" doc:"完整密钥，只显示这一次，关掉就再也拿不到了"`
}

// ListFilter 是列表查询条件。
type ListFilter struct {
	Keyword  string
	Status   string
	Page     int
	PageSize int
}

// Input 是新建 / 编辑的入参。
type Input struct {
	Name        string
	Description string
	RoleID      uuid.UUID
	Status      string
	ExpiresAt   *time.Time
}

// List 查机器账号列表。
func (s *Service) List(ctx context.Context, f ListFilter) ([]ServiceAccount, int64, error) {
	q, err := s.tenant(ctx)
	if err != nil {
		return nil, 0, err
	}
	keyword := repo.LikePattern(f.Keyword)
	status := optional(f.Status)

	rows, err := q.ListServiceAccounts(ctx, repo.ListServiceAccountsArgs{
		Limit:   int32(f.PageSize),
		Offset:  int32((f.Page - 1) * f.PageSize),
		Keyword: keyword,
		Status:  status,
	})
	if err != nil {
		return nil, 0, errs.Internal.Wrap(err)
	}
	total, err := q.CountServiceAccounts(ctx, repo.CountServiceAccountsArgs{
		Keyword: keyword, Status: status,
	})
	if err != nil {
		return nil, 0, errs.Internal.Wrap(err)
	}

	out := make([]ServiceAccount, 0, len(rows))
	for _, row := range rows {
		out = append(out, fromListRow(row))
	}
	return out, total, nil
}

// Get 查一个机器账号。
func (s *Service) Get(ctx context.Context, id uuid.UUID) (ServiceAccount, error) {
	q, err := s.tenant(ctx)
	if err != nil {
		return ServiceAccount{}, err
	}
	row, err := q.GetServiceAccount(ctx, id)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			// 跨租户访问表现成「不存在」，不是「无权限」（MULTI-TENANCY.md §11.2）
			return ServiceAccount{}, errs.NotFound
		}
		return ServiceAccount{}, errs.Internal.Wrap(err)
	}
	return fromGetRow(row), nil
}

// Create 建一个机器账号，返回**只显示一次**的完整密钥。
func (s *Service) Create(ctx context.Context, in Input) (CreatedKey, error) {
	q, err := s.tenant(ctx)
	if err != nil {
		return CreatedKey{}, err
	}
	if err := validate(ctx, q, in); err != nil {
		return CreatedKey{}, err
	}

	id, err := uuid.NewV7()
	if err != nil {
		return CreatedKey{}, errs.Internal.Wrap(err)
	}
	fullKey, prefix, hash := auth.NewAPIKey()

	row, err := q.CreateServiceAccount(ctx, repo.CreateServiceAccountArgs{
		ID: id, Name: in.Name, Description: in.Description,
		KeyPrefix: prefix, KeyHash: []byte(hash),
		RoleID: in.RoleID, Status: in.Status, ExpiresAt: in.ExpiresAt,
		CreatedBy: actorID(ctx),
	})
	if err != nil {
		if repo.IsUniqueViolation(err, "uk_service_accounts_name") {
			return CreatedKey{}, ErrNameTaken
		}
		return CreatedKey{}, errs.Internal.Wrap(err)
	}

	account := fromCreateRow(row)
	account.RoleName = roleNameOf(ctx, q, in.RoleID)
	return CreatedKey{Account: account, Key: fullKey}, nil
}

// Update 改一个机器账号。**不碰密钥** —— 换密钥走 RotateKey。
func (s *Service) Update(ctx context.Context, id uuid.UUID, version int, in Input) (ServiceAccount, error) {
	q, err := s.tenant(ctx)
	if err != nil {
		return ServiceAccount{}, err
	}
	if err := validate(ctx, q, in); err != nil {
		return ServiceAccount{}, err
	}

	row, err := q.UpdateServiceAccount(ctx, repo.UpdateServiceAccountArgs{
		ID: id, Version: int32(version),
		Name: in.Name, Description: in.Description,
		RoleID: in.RoleID, Status: in.Status, ExpiresAt: in.ExpiresAt,
	})
	if err != nil {
		switch {
		case errors.Is(err, pgx.ErrNoRows):
			return ServiceAccount{}, s.conflictOrNotFound(ctx, id)
		case repo.IsUniqueViolation(err, "uk_service_accounts_name"):
			return ServiceAccount{}, ErrNameTaken
		}
		return ServiceAccount{}, errs.Internal.Wrap(err)
	}

	account := fromUpdateRow(row)
	account.RoleName = roleNameOf(ctx, q, in.RoleID)
	return account, nil
}

// RotateKey 换一副新密钥，**旧的当场失效**。
//
// 认证是按 key_prefix 定位记录的，prefix 一换，对接方手里那串就再也匹配不到任何行。
// 所以这个动作没有「宽限期」—— 点下去对方就开始 401 了，界面上必须说清楚。
func (s *Service) RotateKey(ctx context.Context, id uuid.UUID) (CreatedKey, error) {
	q, err := s.tenant(ctx)
	if err != nil {
		return CreatedKey{}, err
	}
	// 先确认这一行存在且属于本租户 —— 直接 update 的话，别家的 id 会影响 0 行，
	// 而 0 行和「版本冲突」分不开。
	current, err := s.Get(ctx, id)
	if err != nil {
		return CreatedKey{}, err
	}

	fullKey, prefix, hash := auth.NewAPIKey()
	rows, err := q.RotateServiceAccountKey(ctx, repo.RotateServiceAccountKeyArgs{
		ID: id, KeyPrefix: prefix, KeyHash: []byte(hash),
	})
	if err != nil {
		return CreatedKey{}, errs.Internal.Wrap(err)
	}
	if rows == 0 {
		return CreatedKey{}, errs.NotFound
	}

	current.KeyPrefix = prefix
	current.Version++
	return CreatedKey{Account: current, Key: fullKey}, nil
}

// Delete 软删一个机器账号。删掉之后它的密钥立刻认证不过。
func (s *Service) Delete(ctx context.Context, id uuid.UUID, version int) error {
	q, err := s.tenant(ctx)
	if err != nil {
		return err
	}
	if _, err := q.SoftDeleteServiceAccount(ctx, repo.SoftDeleteServiceAccountArgs{
		ID: id, Version: int32(version),
	}); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return s.conflictOrNotFound(ctx, id)
		}
		return errs.Internal.Wrap(err)
	}
	return nil
}

// validate 校验入参：名称、状态、角色、过期时间。
func validate(ctx context.Context, q *repo.TenantQueries, in Input) error {
	if strings.TrimSpace(in.Name) == "" {
		return errs.ValidationFailed.WithField("body.name", "名称不能为空")
	}
	if in.Status != StatusActive && in.Status != StatusDisabled {
		return errs.ValidationFailed.WithField("body.status", "只能是 active 或 disabled")
	}
	// 过去的时间会造出一个「建出来就已经失效」的账号
	if in.ExpiresAt != nil && in.ExpiresAt.Before(time.Now()) {
		return ErrExpiresInPast
	}

	// 角色必须存在、启用、**且属于本租户** —— 后一条由 ListRolesByIDs 自带的
	// 租户条件保证，别家的 role_id 在这里就是「不存在」。
	found, err := q.ListRolesByIDs(ctx, []uuid.UUID{in.RoleID})
	if err != nil {
		return errs.Internal.Wrap(err)
	}
	for _, r := range found {
		if r.ID == in.RoleID && r.Status == repo.StatusActive {
			return nil
		}
	}
	return ErrUnknownRole
}

// roleNameOf 取角色名，取不到就留空 —— 名字只是展示用，取不到不该让整个请求失败。
func roleNameOf(ctx context.Context, q *repo.TenantQueries, roleID uuid.UUID) string {
	found, err := q.ListRolesByIDs(ctx, []uuid.UUID{roleID})
	if err != nil {
		return ""
	}
	for _, r := range found {
		if r.ID == roleID {
			return r.Name
		}
	}
	return ""
}

// conflictOrNotFound 区分「版本对不上」和「本来就没有」。
func (s *Service) conflictOrNotFound(ctx context.Context, id uuid.UUID) error {
	if _, err := s.Get(ctx, id); err != nil {
		return err
	}
	return errs.VersionConflict
}

// tenant 取当前请求的租户句柄。
func (s *Service) tenant(ctx context.Context) (*repo.TenantQueries, error) {
	id, err := authz.MustTenant(ctx)
	if err != nil {
		return nil, err
	}
	return s.store.ForTenant(id), nil
}

func fromListRow(row repo.ListServiceAccountsRow) ServiceAccount {
	return ServiceAccount{
		ID: row.ID, Name: row.Name, Description: row.Description,
		KeyPrefix: row.KeyPrefix, RoleID: row.RoleID, RoleName: row.RoleName,
		Status: row.Status, ExpiresAt: row.ExpiresAt, LastUsedAt: row.LastUsedAt,
		CreatedAt: row.CreatedAt, Version: int(row.Version),
	}
}

func fromGetRow(row repo.GetServiceAccountRow) ServiceAccount {
	return ServiceAccount{
		ID: row.ID, Name: row.Name, Description: row.Description,
		KeyPrefix: row.KeyPrefix, RoleID: row.RoleID, RoleName: row.RoleName,
		Status: row.Status, ExpiresAt: row.ExpiresAt, LastUsedAt: row.LastUsedAt,
		CreatedAt: row.CreatedAt, Version: int(row.Version),
	}
}

func fromCreateRow(row repo.CreateServiceAccountRow) ServiceAccount {
	return ServiceAccount{
		ID: row.ID, Name: row.Name, Description: row.Description,
		KeyPrefix: row.KeyPrefix, RoleID: row.RoleID, Status: row.Status,
		ExpiresAt: row.ExpiresAt, CreatedAt: row.CreatedAt, Version: int(row.Version),
	}
}

func fromUpdateRow(row repo.UpdateServiceAccountRow) ServiceAccount {
	return ServiceAccount{
		ID: row.ID, Name: row.Name, Description: row.Description,
		KeyPrefix: row.KeyPrefix, RoleID: row.RoleID, Status: row.Status,
		ExpiresAt: row.ExpiresAt, CreatedAt: row.CreatedAt, Version: int(row.Version),
	}
}

func optional(s string) *string {
	if s == "" {
		return nil
	}
	return &s
}

// actorID 取当前操作者的 id，进 created_by。
func actorID(ctx context.Context) *uuid.UUID {
	p, ok := authz.PrincipalFrom(ctx)
	if !ok {
		return nil
	}
	id := p.ID
	return &id
}
