package handler

import (
	"context"
	"net/http"
	"time"

	"github.com/danielgtaylor/huma/v2"
	"github.com/google/uuid"

	"github.com/ramoncjs3/fries/internal/httpx"
	"github.com/ramoncjs3/fries/internal/perm"
	"github.com/ramoncjs3/fries/internal/perm/modules"
	auditsvc "github.com/ramoncjs3/fries/internal/service/audit"
)

// Audit 是审计日志查询接口。
type Audit struct {
	svc *auditsvc.Service
}

// NewAudit 造审计 handler。
func NewAudit(svc *auditsvc.Service) *Audit {
	return &Audit{svc: svc}
}

// ListAuditInput 是审计查询入参。
type ListAuditInput struct {
	Page     int    `query:"page" default:"1" minimum:"1" doc:"页码，从 1 开始"`
	PageSize int    `query:"page_size" default:"20" minimum:"1" maximum:"100" doc:"每页条数"`
	Resource string `query:"resource" maxLength:"64" doc:"按资源筛选，如 audit"`
	Action   string `query:"action" maxLength:"64" doc:"按动作筛选，如 list"`
	ActorID  string `query:"actor_id" format:"uuid" doc:"按操作人筛选"`
	From     string `query:"from" format:"date-time" doc:"起始时间（含），RFC3339"`
	To       string `query:"to" format:"date-time" doc:"结束时间（不含），RFC3339"`
}

// RegisterAudit 注册审计查询路由。
//
// 这里是 perm.Guard：**权限点是必填参数**，写不出没有权限的接口（DECISIONS.md §3.7）。
func RegisterAudit(api huma.API, h *Audit) {
	perm.Guard(api, modules.Audit.Point(perm.ActionList), huma.Operation{
		OperationID: "list-audit-logs",
		Method:      http.MethodGet,
		Path:        "/audit-logs",
		Summary:     "查询审计日志",
		Description: "按操作人、资源、动作、时间范围翻审计日志。审计表只增不改不删。",
		Tags:        []string{modules.Audit.Key},
	}, h.list)
}

func (h *Audit) list(ctx context.Context, in *ListAuditInput) (*httpx.PageResponse[auditsvc.AuditEntry], error) {
	filter := auditsvc.Filter{Page: in.Page, PageSize: in.PageSize}

	if in.ActorID != "" {
		id, err := uuid.Parse(in.ActorID)
		if err != nil {
			return nil, errInvalidQuery("actor_id", "不是合法的 UUID")
		}
		filter.ActorID = &id
	}
	if in.Resource != "" {
		filter.Resource = &in.Resource
	}
	if in.Action != "" {
		filter.Action = &in.Action
	}
	from, err := parseTime("from", in.From)
	if err != nil {
		return nil, err
	}
	filter.From = from
	to, err := parseTime("to", in.To)
	if err != nil {
		return nil, err
	}
	filter.To = to

	entries, total, err := h.svc.List(ctx, filter)
	if err != nil {
		return nil, err
	}
	return httpx.Paged(entries, in.Page, in.PageSize, total), nil
}

func parseTime(field, raw string) (*time.Time, error) {
	if raw == "" {
		return nil, nil
	}
	t, err := time.Parse(time.RFC3339, raw)
	if err != nil {
		return nil, errInvalidQuery(field, "时间格式要 RFC3339，如 2026-08-07T00:00:00Z")
	}
	return &t, nil
}
