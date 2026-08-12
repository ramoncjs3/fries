package user

import (
	"net/http"

	"github.com/ramoncjs3/fries/internal/errs"
)

// 用户模块自己的错误码。前缀是模块 key（DECISIONS.md §4.5）。
var (
	ErrUsernameTaken = errs.Define("user.username_taken", http.StatusConflict,
		"用户名已被占用")
	ErrEmailTaken = errs.Define("user.email_taken", http.StatusConflict,
		"邮箱已被占用")
	ErrPhoneTaken = errs.Define("user.phone_taken", http.StatusConflict,
		"手机号已被占用")
	ErrUnknownRole = errs.Define("user.unknown_role", http.StatusBadRequest,
		"选了不存在或已停用的角色")
	ErrDepartmentNotFound = errs.Define("user.department_not_found", http.StatusBadRequest,
		"部门不存在")
	ErrLastAdmin = errs.Define("user.last_admin", http.StatusConflict,
		"这是最后一个可用的超级管理员，停用或删除后就没人能进后台了")
	ErrSelfTarget = errs.Define("user.self_target", http.StatusForbidden,
		"不能对自己做这个操作")
)
