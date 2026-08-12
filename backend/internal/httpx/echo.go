package httpx

import (
	"net/http"

	"github.com/labstack/echo/v5"

	"github.com/ramoncjs3/fries/internal/errs"
)

// EchoErrorHandler 让 huma 之外的错误（路由未命中、请求体超限、Echo 中间件报错）
// 也输出同一套 RFC 9457 响应。否则 404 会是 Echo 默认的 `{"message":"Not Found"}`，
// 前端就得处理两种错误格式。
func EchoErrorHandler(c *echo.Context, err error) {
	resp, status := echo.ResolveResponseStatus(c.Response(), err)
	if resp != nil && resp.Committed {
		return
	}

	// 只把带注册错误码的 error 交给 NewProblem —— Echo 自己的英文提示不进响应体，
	// 客户端拿到的是错误码对应的中文文案。
	var causes []error
	if _, ok := errs.From(err); ok {
		causes = append(causes, err)
	}

	p := NewProblem(status, "", causes...)
	if id := RequestID(c.Request().Context()); id != "" {
		p.RequestID = id
	}
	if p.Status >= http.StatusInternalServerError {
		logInternal(p.RequestID, c.Request().Method, c.Path(), err.Error(), []error{err})
	}

	c.Response().Header().Set(echo.HeaderContentType, "application/problem+json; charset=UTF-8")
	if writeErr := c.JSON(p.Status, p); writeErr != nil {
		c.Logger().Error("写错误响应失败", "error", writeErr, "request_id", p.RequestID)
	}
}
