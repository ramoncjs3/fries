package handler

import (
	"context"
	"net/http"

	"github.com/danielgtaylor/huma/v2"
	"github.com/google/uuid"

	"github.com/ramoncjs3/fries/internal/httpx"
	"github.com/ramoncjs3/fries/internal/perm"
	"github.com/ramoncjs3/fries/internal/perm/modules"
	usersvc "github.com/ramoncjs3/fries/internal/service/user"
)

// unassignedDepartment 是 department_id 的特殊取值，表示「没有部门的人」。
const unassignedDepartment = "none"

// User 是用户管理接口。
type User struct {
	svc *usersvc.Service
}

// NewUser 造用户 handler。
func NewUser(svc *usersvc.Service) *User { return &User{svc: svc} }

// ListUserInput 是用户查询入参。
type ListUserInput struct {
	Page     int    `query:"page" default:"1" minimum:"1"`
	PageSize int    `query:"page_size" default:"20" minimum:"1" maximum:"100"`
	Keyword  string `query:"keyword" maxLength:"64" doc:"按用户名、显示名、邮箱、手机号模糊匹配"`
	Status   string `query:"status" enum:"active,disabled"`
	// 可以传多个：?department_id=a&department_id=b。
	// 传 `none` 表示「没有部门的人」——**没有它就查不出谁还没分部门**。
	DepartmentID []string `query:"department_id" doc:"部门 ID，可传多个；传 none 表示未分配部门的人"`
}

// GetUserInput 是用户详情入参。
type GetUserInput struct {
	ID string `path:"id" format:"uuid"`
}

// UserBody 是新增/编辑用户的公共字段。
//
// **没有 password**：初始密码由后端随机生成、只返回一次；
// 改自己的密码走 /auth/change-password，管理员改别人走 reset-password。
type UserBody struct {
	DisplayName  string   `json:"display_name" minLength:"1" maxLength:"64"`
	Email        string   `json:"email,omitempty" maxLength:"255" doc:"可空。填了就必须全局唯一"`
	Phone        string   `json:"phone,omitempty" maxLength:"32" doc:"可空。填了就必须全局唯一"`
	Status       string   `json:"status,omitempty" enum:"active,disabled" default:"active"`
	DepartmentID string   `json:"department_id,omitempty" format:"uuid" doc:"不传表示不属于任何部门"`
	RoleIDs      []string `json:"role_ids,omitempty" doc:"绑定的角色 ID 列表"`
}

// CreateUserInput 是新增用户入参。
type CreateUserInput struct {
	Body struct {
		Username string `json:"username" minLength:"2" maxLength:"64" pattern:"^[a-zA-Z0-9._-]+$" doc:"登录用户名，建后不可改"`
		UserBody
	}
}

// UpdateUserInput 是编辑用户入参。
type UpdateUserInput struct {
	ID   string `path:"id" format:"uuid"`
	Body struct {
		UserBody
		Version int `json:"version" minimum:"0" doc:"乐观锁版本号"`
	}
}

// DeleteUserInput 是删除用户入参。
type DeleteUserInput struct {
	ID   string `path:"id" format:"uuid"`
	Body struct {
		Version int `json:"version" minimum:"0"`
	}
}

// ResetPasswordInput 是重置密码入参。
type ResetPasswordInput struct {
	ID string `path:"id" format:"uuid"`
}

// ResetPasswordResult 是重置密码的结果。
type ResetPasswordResult struct {
	Password string `json:"password" doc:"临时密码，只显示这一次。本人下次登录必须改掉"`
}

// RegisterUser 注册用户管理路由。
func RegisterUser(api huma.API, h *User) {
	perm.Guard(api, modules.User.Point(perm.ActionList), huma.Operation{
		OperationID: "list-users",
		Method:      http.MethodGet,
		Path:        "/users",
		Summary:     "查询用户",
		Tags:        []string{modules.User.Key},
	}, h.list)

	perm.Guard(api, modules.User.Point(perm.ActionList), huma.Operation{
		OperationID: "get-user",
		Method:      http.MethodGet,
		Path:        "/users/{id}",
		Summary:     "用户详情",
		Description: "带上角色 ID 列表。",
		Tags:        []string{modules.User.Key},
	}, h.get)

	perm.Guard(api, modules.User.Point(perm.ActionCreate), huma.Operation{
		OperationID:   "create-user",
		Method:        http.MethodPost,
		Path:          "/users",
		Summary:       "新增用户",
		Description:   "初始密码由系统随机生成，**只在响应里出现一次**，且本人首次登录必须修改。",
		Tags:          []string{modules.User.Key},
		DefaultStatus: http.StatusCreated,
	}, h.create)

	perm.Guard(api, modules.User.Point(perm.ActionUpdate), huma.Operation{
		OperationID: "update-user",
		Method:      http.MethodPut,
		Path:        "/users/{id}",
		Summary:     "编辑用户",
		Description: "用户名建后不可改。停用会立刻踢掉这个人的全部会话。",
		Tags:        []string{modules.User.Key},
	}, h.update)

	perm.Guard(api, modules.User.Point(perm.ActionDelete), huma.Operation{
		OperationID: "delete-user",
		Method:      http.MethodDelete,
		Path:        "/users/{id}",
		Summary:     "删除用户",
		Description: "软删除，并立刻吊销全部会话。不能删自己，也不能删掉最后一个管理员。",
		Tags:        []string{modules.User.Key},
	}, h.remove)

	perm.Guard(api, modules.User.Point("reset_password"), huma.Operation{
		OperationID: "reset-user-password",
		Method:      http.MethodPost,
		Path:        "/users/{id}/reset-password",
		Summary:     "重置密码",
		Description: "生成临时密码并踢掉该用户全部会话。临时密码只在响应里出现一次。",
		Tags:        []string{modules.User.Key},
	}, h.resetPassword)
}

func (h *User) list(ctx context.Context, in *ListUserInput) (*httpx.PageResponse[usersvc.User], error) {
	filter := usersvc.ListFilter{
		Keyword:  in.Keyword,
		Status:   in.Status,
		Page:     in.Page,
		PageSize: in.PageSize,
	}
	for _, raw := range in.DepartmentID {
		if raw == unassignedDepartment {
			filter.IncludeUnassigned = true
			continue
		}
		id, err := uuid.Parse(raw)
		if err != nil {
			return nil, errInvalidField("query.department_id", "不是合法的 UUID")
		}
		filter.DepartmentIDs = append(filter.DepartmentIDs, id)
	}

	items, total, err := h.svc.List(ctx, filter)
	if err != nil {
		return nil, err
	}
	return httpx.Paged(items, in.Page, in.PageSize, total), nil
}

func (h *User) get(ctx context.Context, in *GetUserInput) (*httpx.Response[usersvc.User], error) {
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

func (h *User) create(ctx context.Context, in *CreateUserInput) (*httpx.Response[usersvc.CreatedUser], error) {
	input, err := toUserInput(in.Body.UserBody)
	if err != nil {
		return nil, err
	}
	input.Username = in.Body.Username

	created, err := h.svc.Create(ctx, input)
	if err != nil {
		return nil, err
	}
	return httpx.OK(created), nil
}

func (h *User) update(ctx context.Context, in *UpdateUserInput) (*httpx.Response[usersvc.User], error) {
	id, err := parsePathID(in.ID)
	if err != nil {
		return nil, err
	}
	input, err := toUserInput(in.Body.UserBody)
	if err != nil {
		return nil, err
	}
	updated, err := h.svc.Update(ctx, id, in.Body.Version, input)
	if err != nil {
		return nil, err
	}
	return httpx.OK(updated), nil
}

func (h *User) remove(ctx context.Context, in *DeleteUserInput) (*httpx.Response[struct{}], error) {
	id, err := parsePathID(in.ID)
	if err != nil {
		return nil, err
	}
	if err := h.svc.Delete(ctx, id, in.Body.Version); err != nil {
		return nil, err
	}
	return httpx.OK(struct{}{}), nil
}

func (h *User) resetPassword(ctx context.Context, in *ResetPasswordInput) (*httpx.Response[ResetPasswordResult], error) {
	id, err := parsePathID(in.ID)
	if err != nil {
		return nil, err
	}
	password, err := h.svc.ResetPassword(ctx, id)
	if err != nil {
		return nil, err
	}
	return httpx.OK(ResetPasswordResult{Password: password}), nil
}

func toUserInput(body UserBody) (usersvc.Input, error) {
	in := usersvc.Input{
		DisplayName: body.DisplayName,
		Email:       body.Email,
		Phone:       body.Phone,
		Status:      body.Status,
	}
	if body.DepartmentID != "" {
		id, err := uuid.Parse(body.DepartmentID)
		if err != nil {
			return in, errInvalidField("body.department_id", "不是合法的 UUID")
		}
		in.DepartmentID = &id
	}
	for _, raw := range body.RoleIDs {
		id, err := uuid.Parse(raw)
		if err != nil {
			return in, errInvalidField("body.role_ids", "含有不合法的 UUID")
		}
		in.RoleIDs = append(in.RoleIDs, id)
	}
	return in, nil
}
