package department

import (
	"net/http"

	"github.com/ramoncjs3/fries/internal/errs"
)

// 部门模块自己的错误码。前缀是模块 key（DECISIONS.md §4.5）。
//
// 这些都是**业务规则说不通**，不是参数格式错，所以不用 common.validation_failed ——
// 前端要能分辨「你填错了」和「这事不让你干」。
var (
	ErrCodeTaken = errs.Define("department.code_taken", http.StatusConflict,
		"部门编号已被占用")
	ErrNameTaken = errs.Define("department.name_taken", http.StatusConflict,
		"同一个上级下已经有同名部门")
	ErrParentNotFound = errs.Define("department.parent_not_found", http.StatusBadRequest,
		"上级部门不存在")
	ErrCycle = errs.Define("department.cycle", http.StatusBadRequest,
		"不能把部门挂到自己或自己的下级里")
	ErrHasChildren = errs.Define("department.has_children", http.StatusConflict,
		"该部门下还有子部门，先处理掉再删")
	ErrHasUsers = errs.Define("department.has_users", http.StatusConflict,
		"该部门下还有成员，先把人转走再删")
)
