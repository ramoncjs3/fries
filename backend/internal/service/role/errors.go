package role

import (
	"net/http"

	"github.com/ramoncjs3/fries/internal/errs"
)

// 角色模块自己的错误码。前缀是模块 key（DECISIONS.md §4.5）。
var (
	ErrKeyTaken = errs.Define("role.key_taken", http.StatusConflict,
		"角色标识已被占用")
	ErrBuiltinImmutable = errs.Define("role.builtin_immutable", http.StatusForbidden,
		"内置角色不允许修改或删除")
	ErrHasMembers = errs.Define("role.has_members", http.StatusConflict,
		"还有用户或系统账号在用这个角色，先解绑再删")
	ErrUnknownPermission = errs.Define("role.unknown_permission", http.StatusBadRequest,
		"勾选了不存在的权限点")
	ErrWildcardReserved = errs.Define("role.wildcard_reserved", http.StatusForbidden,
		"通配权限只保留给内置的超级管理员")
)
