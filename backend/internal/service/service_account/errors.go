package serviceaccount

import (
	"net/http"

	"github.com/ramoncjs3/fries/internal/errs"
)

// 机器账号模块自己的错误码。前缀是模块 key（DECISIONS.md §4.5）。
var (
	ErrNameTaken = errs.Define("service_account.name_taken", http.StatusConflict,
		"这个名称已经被占用")
	ErrUnknownRole = errs.Define("service_account.unknown_role", http.StatusBadRequest,
		"选的角色不存在或已停用")
	// ErrExpiresInPast 拦「过期时间填在过去」。
	//
	// 不拦的话会造出一个**建出来就已经失效**的账号：认证那一步查 expires_at，
	// 对接方拿到密钥就报 401，而页面上看着一切正常。
	ErrExpiresInPast = errs.Define("service_account.expires_in_past", http.StatusBadRequest,
		"过期时间不能是过去的时间")
)
