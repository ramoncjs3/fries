package handler

import (
	"context"
	"net/http"

	"github.com/danielgtaylor/huma/v2"
	"github.com/google/uuid"

	"github.com/ramoncjs3/fries/internal/httpx"
	"github.com/ramoncjs3/fries/internal/perm"
	"github.com/ramoncjs3/fries/internal/perm/modules"
	deptsvc "github.com/ramoncjs3/fries/internal/service/department"
	usersvc "github.com/ramoncjs3/fries/internal/service/user"
)

// Department 是部门管理接口。
//
// 它同时管「部门成员」——那几个接口改的是 users 表，所以走 usersvc，
// 权限点也用 user:update：**「能编部门」和「能把人调来调去」不是一回事**。
type Department struct {
	svc   *deptsvc.Service
	users *usersvc.Service
}

// NewDepartment 造部门 handler。
func NewDepartment(svc *deptsvc.Service, users *usersvc.Service) *Department {
	return &Department{svc: svc, users: users}
}

// ListDepartmentInput 是部门查询入参。**没有分页**：树切成一页页就拼不起来了。
type ListDepartmentInput struct {
	Keyword string `query:"keyword" maxLength:"64" doc:"按名称或编号模糊匹配"`
	Status  string `query:"status" enum:"active,disabled" doc:"按状态筛选"`
}

// DepartmentBody 是新增/编辑部门的请求体。
//
// 校验规则写在 tag 上，huma 自动校验并产出 OpenAPI —— 不要在 service 里再抄一遍
// 格式校验（DECISIONS.md §4.1）。service 只管业务规则（重名、成环、还有人）。
type DepartmentBody struct {
	ParentID  string `json:"parent_id,omitempty" format:"uuid" doc:"上级部门；不传或空串表示根节点"`
	Name      string `json:"name" minLength:"1" maxLength:"64" doc:"部门名称"`
	Code      string `json:"code" minLength:"1" maxLength:"64" pattern:"^[A-Za-z0-9_-]+$" doc:"部门编号，字母数字下划线中划线"`
	SortOrder int    `json:"sort_order,omitempty" minimum:"0" maximum:"9999" doc:"同级排序，小的在前"`
	Remark    string `json:"remark,omitempty" maxLength:"500"`
	Status    string `json:"status,omitempty" enum:"active,disabled" default:"active"`
}

// CreateDepartmentInput 是新增部门入参。
type CreateDepartmentInput struct {
	Body DepartmentBody
}

// UpdateDepartmentInput 是编辑部门入参。
type UpdateDepartmentInput struct {
	ID   string `path:"id" format:"uuid"`
	Body struct {
		DepartmentBody
		Version int `json:"version" minimum:"0" doc:"乐观锁版本号，取自上次读到的值"`
	}
}

// DeleteDepartmentInput 是删除部门入参。
type DeleteDepartmentInput struct {
	ID   string `path:"id" format:"uuid"`
	Body struct {
		Version int `json:"version" minimum:"0" doc:"乐观锁版本号"`
	}
}

// RegisterDepartment 注册部门路由。
func RegisterDepartment(api huma.API, h *Department) {
	perm.Guard(api, modules.Department.Point(perm.ActionList), huma.Operation{
		OperationID: "list-departments",
		Method:      http.MethodGet,
		Path:        "/departments",
		Summary:     "查询部门",
		Description: "一次返回全部部门节点（不分页），前端自己拼树。",
		Tags:        []string{modules.Department.Key},
	}, h.list)

	perm.Guard(api, modules.Department.Point(perm.ActionCreate), huma.Operation{
		OperationID:   "create-department",
		Method:        http.MethodPost,
		Path:          "/departments",
		Summary:       "新增部门",
		Tags:          []string{modules.Department.Key},
		DefaultStatus: http.StatusCreated,
	}, h.create)

	perm.Guard(api, modules.Department.Point(perm.ActionUpdate), huma.Operation{
		OperationID: "update-department",
		Method:      http.MethodPut,
		Path:        "/departments/{id}",
		Summary:     "编辑部门",
		Tags:        []string{modules.Department.Key},
	}, h.update)

	perm.Guard(api, modules.Department.Point(perm.ActionDelete), huma.Operation{
		OperationID: "delete-department",
		Method:      http.MethodDelete,
		Path:        "/departments/{id}",
		Summary:     "删除部门",
		Description: "软删除。下面还有子部门或成员时拒绝。",
		Tags:        []string{modules.Department.Key},
	}, h.remove)

	// ---- 部门成员。改的是 users 表，所以守的是 user 的权限点 ----

	perm.Guard(api, modules.User.Point(perm.ActionList), huma.Operation{
		OperationID: "list-department-candidates",
		Method:      http.MethodGet,
		Path:        "/departments/{id}/candidates",
		Summary:     "可加入该部门的人",
		Description: "列出**不在**这个部门里的活跃用户，给「添加成员」用。",
		Tags:        []string{modules.Department.Key},
	}, h.candidates)

	perm.Guard(api, modules.User.Point(perm.ActionUpdate), huma.Operation{
		OperationID: "add-department-members",
		Method:      http.MethodPost,
		Path:        "/departments/{id}/members",
		Summary:     "把人加入部门",
		Description: "一个人只能属于一个部门，加入即从原部门移出。",
		Tags:        []string{modules.Department.Key},
	}, h.addMembers)

	perm.Guard(api, modules.User.Point(perm.ActionUpdate), huma.Operation{
		OperationID: "remove-department-members",
		Method:      http.MethodDelete,
		Path:        "/departments/{id}/members",
		Summary:     "把人移出部门",
		Description: "移出后这些人不属于任何部门，账号本身不受影响。",
		Tags:        []string{modules.Department.Key},
	}, h.removeMembers)
}

// DepartmentCandidatesInput 是候选人查询入参。
type DepartmentCandidatesInput struct {
	ID      string `path:"id" format:"uuid"`
	Keyword string `query:"keyword" maxLength:"64" doc:"按用户名或显示名模糊匹配"`
	Limit   int    `query:"limit" default:"50" minimum:"1" maximum:"200"`
}

// DepartmentMembersInput 是加入/移出部门的入参。
type DepartmentMembersInput struct {
	ID   string `path:"id" format:"uuid"`
	Body struct {
		UserIDs []string `json:"user_ids" minItems:"1" doc:"要调整的用户 ID"`
	}
}

// MemberChangeResult 是调整成员的结果。
type MemberChangeResult struct {
	Affected int `json:"affected" doc:"实际调整了几个人"`
}

func (h *Department) candidates(ctx context.Context, in *DepartmentCandidatesInput) (*httpx.PageResponse[usersvc.User], error) {
	id, err := parsePathID(in.ID)
	if err != nil {
		return nil, err
	}
	items, err := h.users.Candidates(ctx, &id, in.Keyword, in.Limit)
	if err != nil {
		return nil, err
	}
	return httpx.Paged(items, 1, in.Limit, int64(len(items))), nil
}

func (h *Department) addMembers(ctx context.Context, in *DepartmentMembersInput) (*httpx.Response[MemberChangeResult], error) {
	id, userIDs, err := parseMembers(in)
	if err != nil {
		return nil, err
	}
	// 部门存在与否由 service 判，这里只管翻译形状
	affected, err := h.users.SetDepartment(ctx, userIDs, &id)
	if err != nil {
		return nil, err
	}
	return httpx.OK(MemberChangeResult{Affected: int(affected)}), nil
}

func (h *Department) removeMembers(ctx context.Context, in *DepartmentMembersInput) (*httpx.Response[MemberChangeResult], error) {
	if _, err := parsePathID(in.ID); err != nil {
		return nil, err
	}
	_, userIDs, err := parseMembers(in)
	if err != nil {
		return nil, err
	}
	affected, err := h.users.SetDepartment(ctx, userIDs, nil)
	if err != nil {
		return nil, err
	}
	return httpx.OK(MemberChangeResult{Affected: int(affected)}), nil
}

func parseMembers(in *DepartmentMembersInput) (uuid.UUID, []uuid.UUID, error) {
	id, err := parsePathID(in.ID)
	if err != nil {
		return uuid.Nil, nil, err
	}
	userIDs := make([]uuid.UUID, 0, len(in.Body.UserIDs))
	for _, raw := range in.Body.UserIDs {
		uid, err := uuid.Parse(raw)
		if err != nil {
			return uuid.Nil, nil, errInvalidField("body.user_ids", "含有不合法的 UUID")
		}
		userIDs = append(userIDs, uid)
	}
	return id, userIDs, nil
}

func (h *Department) list(ctx context.Context, in *ListDepartmentInput) (*httpx.PageResponse[deptsvc.Department], error) {
	items, err := h.svc.List(ctx, deptsvc.ListFilter{Keyword: in.Keyword, Status: in.Status})
	if err != nil {
		return nil, err
	}
	// 不分页，但仍然走分页封套：列表接口的响应形状全站一致，前端不用为它写特例
	return httpx.Paged(items, 1, len(items), int64(len(items))), nil
}

func (h *Department) create(ctx context.Context, in *CreateDepartmentInput) (*httpx.Response[deptsvc.Department], error) {
	input, err := toDepartmentInput(in.Body)
	if err != nil {
		return nil, err
	}
	created, err := h.svc.Create(ctx, input)
	if err != nil {
		return nil, err
	}
	return httpx.OK(created), nil
}

func (h *Department) update(ctx context.Context, in *UpdateDepartmentInput) (*httpx.Response[deptsvc.Department], error) {
	id, err := parsePathID(in.ID)
	if err != nil {
		return nil, err
	}
	input, err := toDepartmentInput(in.Body.DepartmentBody)
	if err != nil {
		return nil, err
	}
	updated, err := h.svc.Update(ctx, id, in.Body.Version, input)
	if err != nil {
		return nil, err
	}
	return httpx.OK(updated), nil
}

func (h *Department) remove(ctx context.Context, in *DeleteDepartmentInput) (*httpx.Response[struct{}], error) {
	id, err := parsePathID(in.ID)
	if err != nil {
		return nil, err
	}
	if err := h.svc.Delete(ctx, id, in.Body.Version); err != nil {
		return nil, err
	}
	return httpx.OK(struct{}{}), nil
}

func toDepartmentInput(body DepartmentBody) (deptsvc.Input, error) {
	in := deptsvc.Input{
		Name:      body.Name,
		Code:      body.Code,
		SortOrder: body.SortOrder,
		Remark:    body.Remark,
		Status:    body.Status,
	}
	if body.ParentID != "" {
		parent, err := uuid.Parse(body.ParentID)
		if err != nil {
			return in, errInvalidField("body.parent_id", "不是合法的 UUID")
		}
		in.ParentID = &parent
	}
	return in, nil
}
