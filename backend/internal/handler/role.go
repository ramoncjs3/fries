package handler

import (
	"context"
	"net/http"

	"github.com/danielgtaylor/huma/v2"

	"github.com/ramoncjs3/fries/internal/httpx"
	"github.com/ramoncjs3/fries/internal/perm"
	"github.com/ramoncjs3/fries/internal/perm/modules"
	rolesvc "github.com/ramoncjs3/fries/internal/service/role"
)

// Role 是角色管理接口。
type Role struct {
	svc *rolesvc.Service
}

// NewRole 造角色 handler。
func NewRole(svc *rolesvc.Service) *Role { return &Role{svc: svc} }

// ListRoleInput 是角色查询入参。
type ListRoleInput struct {
	Page     int    `query:"page" default:"1" minimum:"1"`
	PageSize int    `query:"page_size" default:"20" minimum:"1" maximum:"100"`
	Keyword  string `query:"keyword" maxLength:"64" doc:"按名称或标识模糊匹配"`
	Status   string `query:"status" enum:"active,disabled"`
}

// GetRoleInput 是角色详情入参。
type GetRoleInput struct {
	ID string `path:"id" format:"uuid"`
}

// RoleBody 是新增/编辑角色的公共字段。
type RoleBody struct {
	Name        string   `json:"name" minLength:"1" maxLength:"64"`
	Description string   `json:"description,omitempty" maxLength:"500"`
	DataScope   string   `json:"data_scope,omitempty" enum:"all,self" default:"self" doc:"all=看本组织全部 / self=只看自己创建的"`
	Status      string   `json:"status,omitempty" enum:"active,disabled" default:"active"`
	Permissions []string `json:"permissions,omitempty" doc:"勾选的权限点，形如 user:list"`
}

// CreateRoleInput 是新增角色入参。
type CreateRoleInput struct {
	Body struct {
		// key 只在新增时给：它是 Casbin 策略里的身份，建好就不能改了
		Key string `json:"key" minLength:"1" maxLength:"64" pattern:"^[a-z][a-z0-9_]*$" doc:"角色标识，小写字母数字下划线，建后不可改"`
		RoleBody
	}
}

// UpdateRoleInput 是编辑角色入参。
type UpdateRoleInput struct {
	ID   string `path:"id" format:"uuid"`
	Body struct {
		RoleBody
		Version int `json:"version" minimum:"0" doc:"乐观锁版本号"`
	}
}

// DeleteRoleInput 是删除角色入参。
type DeleteRoleInput struct {
	ID   string `path:"id" format:"uuid"`
	Body struct {
		Version int `json:"version" minimum:"0"`
	}
}

// PermissionAction 是权限目录里的一个动作。
type PermissionAction struct {
	Key   string `json:"key" doc:"动作标识，如 list"`
	Name  string `json:"name" doc:"中文名"`
	Point string `json:"point" doc:"完整权限点，如 user:list —— 勾选时提交的就是它"`
}

// PermissionModule 是权限目录里的一个模块。
type PermissionModule struct {
	Key     string             `json:"key"`
	Name    string             `json:"name"`
	Scoped  bool               `json:"scoped" doc:"是否参与数据权限"`
	Actions []PermissionAction `json:"actions"`
}

// RegisterRole 注册角色路由。
func RegisterRole(api huma.API, h *Role) {
	perm.Guard(api, modules.Role.Point(perm.ActionList), huma.Operation{
		OperationID: "list-roles",
		Method:      http.MethodGet,
		Path:        "/roles",
		Summary:     "查询角色",
		Tags:        []string{modules.Role.Key},
	}, h.list)

	// 权限目录：给角色配置页渲染勾选树用。
	// **来源是后端的权限点注册表**，前端不许自己维护一份 —— 那必然会漏（§3.1）。
	perm.Guard(api, modules.Role.Point(perm.ActionList), huma.Operation{
		OperationID: "list-permission-catalog",
		Method:      http.MethodGet,
		Path:        "/roles/permission-catalog",
		Summary:     "权限点目录",
		Description: "全部可勾选的权限点，按模块分组。角色配置页用它渲染勾选树。",
		Tags:        []string{modules.Role.Key},
	}, h.catalog)

	perm.Guard(api, modules.Role.Point(perm.ActionList), huma.Operation{
		OperationID: "get-role",
		Method:      http.MethodGet,
		Path:        "/roles/{id}",
		Summary:     "角色详情",
		Description: "带上已勾选的权限点。",
		Tags:        []string{modules.Role.Key},
	}, h.get)

	perm.Guard(api, modules.Role.Point(perm.ActionCreate), huma.Operation{
		OperationID:   "create-role",
		Method:        http.MethodPost,
		Path:          "/roles",
		Summary:       "新增角色",
		Tags:          []string{modules.Role.Key},
		DefaultStatus: http.StatusCreated,
	}, h.create)

	perm.Guard(api, modules.Role.Point(perm.ActionUpdate), huma.Operation{
		OperationID: "update-role",
		Method:      http.MethodPut,
		Path:        "/roles/{id}",
		Summary:     "编辑角色",
		Description: "角色标识（key）建后不可改。内置角色整体不可改。",
		Tags:        []string{modules.Role.Key},
	}, h.update)

	perm.Guard(api, modules.Role.Point(perm.ActionDelete), huma.Operation{
		OperationID: "delete-role",
		Method:      http.MethodDelete,
		Path:        "/roles/{id}",
		Summary:     "删除角色",
		Description: "软删除。还有用户或系统账号在用时拒绝。",
		Tags:        []string{modules.Role.Key},
	}, h.remove)
}

func (h *Role) list(ctx context.Context, in *ListRoleInput) (*httpx.PageResponse[rolesvc.Role], error) {
	items, total, err := h.svc.List(ctx, rolesvc.ListFilter{
		Keyword:  in.Keyword,
		Status:   in.Status,
		Page:     in.Page,
		PageSize: in.PageSize,
	})
	if err != nil {
		return nil, err
	}
	return httpx.Paged(items, in.Page, in.PageSize, total), nil
}

func (h *Role) get(ctx context.Context, in *GetRoleInput) (*httpx.Response[rolesvc.Role], error) {
	id, err := parsePathID(in.ID)
	if err != nil {
		return nil, err
	}
	found, err := h.svc.Get(ctx, id)
	if err != nil {
		return nil, err
	}
	return httpx.OK(found), nil
}

func (h *Role) catalog(_ context.Context, _ *struct{}) (*httpx.Response[[]PermissionModule], error) {
	registry := perm.Modules()
	out := make([]PermissionModule, 0, len(registry))
	for _, m := range registry {
		// ⚠️ 只吐**租户世界**的权限点（MULTI-TENANCY.md §3.2 ②）。
		// 这个接口的设计就是「把整个注册表倒出来」，好处是新模块不会漏 ——
		// 但平台管理端的模块（开租户、停租户）一旦注册进同一张表，
		// 租户管理员在自己的角色页上就能看到并勾上它们。
		if m.Realm != perm.RealmTenant {
			continue
		}
		actions := make([]PermissionAction, 0, len(m.Actions))
		for _, a := range m.Actions {
			actions = append(actions, PermissionAction{
				Key:   a.Key,
				Name:  a.Name,
				Point: m.Key + ":" + a.Key,
			})
		}
		out = append(out, PermissionModule{
			Key:     m.Key,
			Name:    m.Name,
			Scoped:  m.Scoped,
			Actions: actions,
		})
	}
	return httpx.OK(out), nil
}

func (h *Role) create(ctx context.Context, in *CreateRoleInput) (*httpx.Response[rolesvc.Role], error) {
	created, err := h.svc.Create(ctx, rolesvc.Input{
		Key:         in.Body.Key,
		Name:        in.Body.Name,
		Description: in.Body.Description,
		DataScope:   in.Body.DataScope,
		Status:      in.Body.Status,
		Permissions: in.Body.Permissions,
	})
	if err != nil {
		return nil, err
	}
	return httpx.OK(created), nil
}

func (h *Role) update(ctx context.Context, in *UpdateRoleInput) (*httpx.Response[rolesvc.Role], error) {
	id, err := parsePathID(in.ID)
	if err != nil {
		return nil, err
	}
	updated, err := h.svc.Update(ctx, id, in.Body.Version, rolesvc.Input{
		Name:        in.Body.Name,
		Description: in.Body.Description,
		DataScope:   in.Body.DataScope,
		Status:      in.Body.Status,
		Permissions: in.Body.Permissions,
	})
	if err != nil {
		return nil, err
	}
	return httpx.OK(updated), nil
}

func (h *Role) remove(ctx context.Context, in *DeleteRoleInput) (*httpx.Response[struct{}], error) {
	id, err := parsePathID(in.ID)
	if err != nil {
		return nil, err
	}
	if err := h.svc.Delete(ctx, id, in.Body.Version); err != nil {
		return nil, err
	}
	return httpx.OK(struct{}{}), nil
}
