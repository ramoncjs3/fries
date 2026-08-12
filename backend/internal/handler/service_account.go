package handler

import (
	"context"
	"net/http"
	"time"

	"github.com/danielgtaylor/huma/v2"
	"github.com/google/uuid"

	"github.com/ramoncjs3/fries/internal/audit"
	"github.com/ramoncjs3/fries/internal/errs"
	"github.com/ramoncjs3/fries/internal/httpx"
	"github.com/ramoncjs3/fries/internal/perm"
	"github.com/ramoncjs3/fries/internal/perm/modules"
	sasvc "github.com/ramoncjs3/fries/internal/service/service_account"
)

// ServiceAccount 是机器账号管理接口。
type ServiceAccount struct {
	svc *sasvc.Service
}

// NewServiceAccount 造机器账号 handler。
func NewServiceAccount(svc *sasvc.Service) *ServiceAccount {
	return &ServiceAccount{svc: svc}
}

// ListServiceAccountsInput 是列表查询入参。
type ListServiceAccountsInput struct {
	Page     int    `query:"page" default:"1" minimum:"1" doc:"页码，从 1 开始"`
	PageSize int    `query:"page_size" default:"20" minimum:"1" maximum:"100" doc:"每页条数"`
	Keyword  string `query:"keyword" maxLength:"64" doc:"按名称或说明搜索"`
	Status   string `query:"status" enum:"active,disabled" doc:"按状态筛选"`
}

// ServiceAccountBody 是新建 / 编辑的公共字段。
type ServiceAccountBody struct {
	Name        string `json:"name" minLength:"1" maxLength:"64" doc:"名称，组织内唯一"`
	Description string `json:"description,omitempty" maxLength:"500" doc:"说明：这个账号给谁用、干什么"`
	RoleID      string `json:"role_id" format:"uuid" doc:"绑定的角色，决定它能调哪些接口"`
	Status      string `json:"status,omitempty" enum:"active,disabled" default:"active"`
	// ExpiresAt 空表示不过期。到期后认证直接失败，不需要人工去停用。
	ExpiresAt *time.Time `json:"expires_at,omitempty" doc:"到期时间，空表示不过期"`
}

// CreateServiceAccountInput 是新建入参。
type CreateServiceAccountInput struct {
	Body ServiceAccountBody
}

// UpdateServiceAccountInput 是编辑入参。
type UpdateServiceAccountInput struct {
	ID   string `path:"id" format:"uuid"`
	Body struct {
		ServiceAccountBody
		Version int `json:"version" doc:"乐观锁版本号，从详情接口拿到什么就传什么"`
	}
}

// ServiceAccountIDInput 只需要 id 的操作（轮换密钥）。
type ServiceAccountIDInput struct {
	ID string `path:"id" format:"uuid"`
}

// DeleteServiceAccountInput 是删除入参。
type DeleteServiceAccountInput struct {
	ID   string `path:"id" format:"uuid"`
	Body struct {
		Version int `json:"version" doc:"乐观锁版本号"`
	}
}

// RegisterServiceAccount 注册机器账号路由。
func RegisterServiceAccount(api huma.API, h *ServiceAccount) {
	m := modules.ServiceAccount

	perm.Guard(api, m.Point(perm.ActionList), huma.Operation{
		OperationID: "list-service-accounts",
		Method:      http.MethodGet,
		Path:        "/service-accounts",
		Summary:     "查询机器账号",
		Description: "外部系统拿 API Key 调进来的身份。**返回里没有密钥** —— " +
			"密钥只在新建和轮换时出现一次，之后库里只有哈希。",
		Tags: []string{m.Key},
	}, h.list)

	perm.Guard(api, m.Point(perm.ActionList), huma.Operation{
		OperationID: "get-service-account",
		Method:      http.MethodGet,
		Path:        "/service-accounts/{id}",
		Summary:     "机器账号详情",
		Tags:        []string{m.Key},
	}, h.get)

	perm.Guard(api, m.Point(perm.ActionCreate), huma.Operation{
		OperationID:   "create-service-account",
		Method:        http.MethodPost,
		Path:          "/service-accounts",
		Summary:       "新增机器账号",
		Description:   "返回**只显示一次**的完整密钥。关掉页面就再也拿不到，只能轮换。",
		DefaultStatus: http.StatusCreated,
		Tags:          []string{m.Key},
	}, h.create)

	perm.Guard(api, m.Point(perm.ActionUpdate), huma.Operation{
		OperationID: "update-service-account",
		Method:      http.MethodPut,
		Path:        "/service-accounts/{id}",
		Summary:     "编辑机器账号",
		Description: "改名称、说明、角色、状态、到期时间。**不换密钥** —— 那是单独的动作。",
		Tags:        []string{m.Key},
	}, h.update)

	perm.Guard(api, m.Point("rotate_key"), huma.Operation{
		OperationID: "rotate-service-account-key",
		Method:      http.MethodPost,
		Path:        "/service-accounts/{id}/rotate-key",
		Summary:     "轮换密钥",
		Description: "换一副新密钥并返回一次。⚠️ **旧密钥当场失效**，" +
			"对接方在换上新密钥之前会一直 401。",
		Tags: []string{m.Key},
	}, h.rotateKey)

	perm.Guard(api, m.Point(perm.ActionDelete), huma.Operation{
		OperationID: "delete-service-account",
		Method:      http.MethodDelete,
		Path:        "/service-accounts/{id}",
		Summary:     "删除机器账号",
		Description: "软删。删掉之后它的密钥立刻认证不过。",
		Tags:        []string{m.Key},
	}, h.remove)
}

func (h *ServiceAccount) list(ctx context.Context, in *ListServiceAccountsInput) (*httpx.PageResponse[sasvc.ServiceAccount], error) {
	items, total, err := h.svc.List(ctx, sasvc.ListFilter{
		Keyword: in.Keyword, Status: in.Status,
		Page: in.Page, PageSize: in.PageSize,
	})
	if err != nil {
		return nil, err
	}
	return httpx.Paged(items, in.Page, in.PageSize, total), nil
}

func (h *ServiceAccount) get(ctx context.Context, in *ServiceAccountIDInput) (*httpx.Response[sasvc.ServiceAccount], error) {
	id, err := parseUUID(in.ID)
	if err != nil {
		return nil, err
	}
	account, err := h.svc.Get(ctx, id)
	if err != nil {
		return nil, err
	}
	return httpx.OK(account), nil
}

func (h *ServiceAccount) create(ctx context.Context, in *CreateServiceAccountInput) (*httpx.Response[sasvc.CreatedKey], error) {
	input, err := inputOf(in.Body)
	if err != nil {
		return nil, err
	}
	audit.Detail(ctx, "name", in.Body.Name)

	created, err := h.svc.Create(ctx, input)
	if err != nil {
		return nil, err
	}
	audit.SetResourceID(ctx, created.Account.ID)
	return httpx.OK(created), nil
}

func (h *ServiceAccount) update(ctx context.Context, in *UpdateServiceAccountInput) (*httpx.Response[sasvc.ServiceAccount], error) {
	id, err := parseUUID(in.ID)
	if err != nil {
		return nil, err
	}
	input, err := inputOf(in.Body.ServiceAccountBody)
	if err != nil {
		return nil, err
	}
	audit.SetResourceID(ctx, id)
	audit.Detail(ctx, "name", in.Body.Name)

	account, err := h.svc.Update(ctx, id, in.Body.Version, input)
	if err != nil {
		return nil, err
	}
	return httpx.OK(account), nil
}

func (h *ServiceAccount) rotateKey(ctx context.Context, in *ServiceAccountIDInput) (*httpx.Response[sasvc.CreatedKey], error) {
	id, err := parseUUID(in.ID)
	if err != nil {
		return nil, err
	}
	audit.SetResourceID(ctx, id)

	rotated, err := h.svc.RotateKey(ctx, id)
	if err != nil {
		return nil, err
	}
	return httpx.OK(rotated), nil
}

func (h *ServiceAccount) remove(ctx context.Context, in *DeleteServiceAccountInput) (*httpx.Response[struct{}], error) {
	id, err := parseUUID(in.ID)
	if err != nil {
		return nil, err
	}
	audit.SetResourceID(ctx, id)

	if err := h.svc.Delete(ctx, id, in.Body.Version); err != nil {
		return nil, err
	}
	return httpx.OK(struct{}{}), nil
}

// inputOf 把请求体翻成 service 要的形状。
//
// 状态留空当成 active：huma 的 `default:` 只写进文档、不填值（MEMORY.md 记过），
// 所以默认值要在这里兜。
func inputOf(body ServiceAccountBody) (sasvc.Input, error) {
	roleID, err := uuid.Parse(body.RoleID)
	if err != nil {
		return sasvc.Input{}, errs.ValidationFailed.WithField("body.role_id", "不是合法的 UUID")
	}
	status := body.Status
	if status == "" {
		status = sasvc.StatusActive
	}
	return sasvc.Input{
		Name: body.Name, Description: body.Description,
		RoleID: roleID, Status: status, ExpiresAt: body.ExpiresAt,
	}, nil
}

// parseUUID 解路径上的 id。
func parseUUID(raw string) (uuid.UUID, error) {
	id, err := uuid.Parse(raw)
	if err != nil {
		return uuid.Nil, errs.ValidationFailed.WithField("path.id", "不是合法的 UUID")
	}
	return id, nil
}
