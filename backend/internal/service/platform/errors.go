package platform

import (
	"net/http"

	"github.com/ramoncjs3/fries/internal/errs"
)

// 平台管理端自己的错误码。前缀是模块 key（DECISIONS.md §4.5）。
var (
	ErrCodeTaken = errs.Define("tenant.code_taken", http.StatusConflict,
		"这个公司代码已经被占用了")
	ErrCodeInvalid = errs.Define("tenant.code_invalid", http.StatusBadRequest,
		"公司代码只能用小写字母、数字和中划线，长度 2–32，首尾不能是中划线")
	ErrCodeReserved = errs.Define("tenant.code_reserved", http.StatusBadRequest,
		"这个公司代码是保留字，换一个")
)
