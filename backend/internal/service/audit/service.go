// Package audit 是审计日志模块的业务层。
//
// 注意和 internal/audit 的分工：
//   - internal/audit —— **写**审计的基础设施（中间件用的 Recorder、Writer、哈希链校验）
//   - 这里          —— **读**审计的业务逻辑，是 audit 这个业务模块的 service 层
//
// handler 只调这里，不直接碰 repo（红线 #6）。
package audit

import (
	"context"
	"encoding/json"
	"net/netip"
	"time"

	"github.com/google/uuid"

	"github.com/ramoncjs3/fries/internal/authz"
	"github.com/ramoncjs3/fries/internal/errs"
	"github.com/ramoncjs3/fries/internal/repo"
)

// Service 是审计查询服务。
type Service struct {
	store *repo.Store
}

// New 造审计查询服务。
func New(store *repo.Store) *Service { return &Service{store: store} }

// tenant 取当前请求的租户句柄。**每个方法的第一行**都该是它 ——
// 没有租户就报错，不是放行（MULTI-TENANCY.md §1.2 ②）。
func (s *Service) tenant(ctx context.Context) (*repo.TenantQueries, error) {
	id, err := authz.MustTenant(ctx)
	if err != nil {
		return nil, err
	}
	return s.store.ForTenant(id), nil
}

// Filter 是审计查询条件。全是可空的，一条 SQL 覆盖所有组合
// —— 不引 squirrel（DECISIONS.md §1）。
//
// Resource / Action 存的是**用户敲进来的关键词**，不是 SQL 模式；
// 转成 ILIKE 模式是这一层的事，handler 不该知道底下用的是 LIKE 还是全文检索。
type Filter struct {
	ActorID  *uuid.UUID
	Resource *string
	Action   *string
	From     *time.Time
	To       *time.Time
	Page     int
	PageSize int
}

// keyword 把可空的关键词转成 ILIKE 模式，空的就返回 nil（条件不生效）。
func keyword(s *string) *string {
	if s == nil {
		return nil
	}
	return repo.LikePattern(*s)
}

// AuditEntry 是一条审计记录。
//
// 类型名会原样进 OpenAPI 的 schema 名，所以带上模块前缀 —— 叫 Entry 的话，
// 将来别的模块也有 Entry 时 huma 只能自动加后缀去重，前端类型名就飘了。
type AuditEntry struct {
	ID         uuid.UUID      `json:"id"`
	OccurredAt time.Time      `json:"occurred_at"`
	RequestID  string         `json:"request_id"`
	ActorType  string         `json:"actor_type" doc:"user / service / anonymous / system"`
	ActorID    *uuid.UUID     `json:"actor_id"`
	ActorName  string         `json:"actor_name"`
	Resource   string         `json:"resource"`
	Action     string         `json:"action"`
	ResourceID *uuid.UUID     `json:"resource_id" doc:"操作的是哪条记录"`
	Method     string         `json:"method"`
	Path       string         `json:"path"`
	IP         string         `json:"ip"`
	UserAgent  string         `json:"user_agent"`
	HTTPStatus int            `json:"http_status" doc:"HTTP 响应状态码"`
	DurationMs int            `json:"duration_ms"`
	Detail     map[string]any `json:"detail" doc:"参数摘要，敏感字段已脱敏"`
}

// List 查审计日志。
//
// 审计是共享资源（Scoped: false），不参与数据权限 —— 能看审计的人就该看得全。
func (s *Service) List(ctx context.Context, f Filter) ([]AuditEntry, int64, error) {
	q, err := s.tenant(ctx)
	if err != nil {
		return nil, 0, err
	}
	offset := int32((f.Page - 1) * f.PageSize)
	resource, action := keyword(f.Resource), keyword(f.Action)

	rows, err := q.ListAuditLogs(ctx, repo.ListAuditLogsArgs{
		Limit:    int32(f.PageSize),
		Offset:   offset,
		ActorID:  f.ActorID,
		Resource: resource,
		Action:   action,
		From:     f.From,
		To:       f.To,
	})
	if err != nil {
		return nil, 0, errs.Internal.Wrap(err)
	}

	total, err := q.CountAuditLogs(ctx, repo.CountAuditLogsArgs{
		ActorID:  f.ActorID,
		Resource: resource,
		Action:   action,
		From:     f.From,
		To:       f.To,
	})
	if err != nil {
		return nil, 0, errs.Internal.Wrap(err)
	}

	entries := make([]AuditEntry, 0, len(rows))
	for _, row := range rows {
		entries = append(entries, AuditEntry{
			ID:         row.ID,
			OccurredAt: row.OccurredAt,
			RequestID:  row.RequestID,
			ActorType:  row.ActorType,
			ActorID:    row.ActorID,
			ActorName:  row.ActorName,
			Resource:   row.Resource,
			Action:     row.Action,
			ResourceID: row.ResourceID,
			Method:     row.Method,
			Path:       row.Path,
			IP:         addrText(row.IP),
			UserAgent:  row.UserAgent,
			HTTPStatus: int(row.HTTPStatus),
			DurationMs: int(row.DurationMs),
			Detail:     decodeDetail(row.Detail),
		})
	}
	return entries, total, nil
}

func addrText(addr *netip.Addr) string {
	if addr == nil {
		return ""
	}
	return addr.String()
}

func decodeDetail(raw []byte) map[string]any {
	out := map[string]any{}
	if len(raw) == 0 {
		return out
	}
	_ = json.Unmarshal(raw, &out)
	return out
}
