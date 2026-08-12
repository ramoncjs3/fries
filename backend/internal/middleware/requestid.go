// Package middleware 是全站 HTTP 中间件：requestID、访问日志、panic 兜底、
// 幂等键、请求体限制、超时、Prometheus 指标。
//
// 顺序很重要，装配在 cmd/server/app.go 里，别在别处零散地加。
package middleware

import (
	"github.com/labstack/echo/v5"

	"github.com/ramoncjs3/fries/internal/httpx"
)

// RequestID 给每个请求分配 ID，写进 context 和响应头。
//
// 响应头一定要打（覆盖 204 这类没有 body 的场景，DECISIONS.md §4.4）；
// context 里的值由 httpx 的 Transformer 取出来塞进响应体。
func RequestID() echo.MiddlewareFunc {
	return func(next echo.HandlerFunc) echo.HandlerFunc {
		return func(c *echo.Context) error {
			req := c.Request()

			id := httpx.SanitizeRequestID(req.Header.Get(httpx.HeaderRequestID))
			if id == "" {
				id = httpx.NewRequestID()
			}

			ctx := httpx.WithRequestID(req.Context(), id)
			// 顺手把客户端 IP 也放进 context：service 层要用（登录记 IP），
			// 但它不该认识 echo.Context。
			ctx = httpx.WithClientIP(ctx, clientIP(c))

			c.SetRequest(req.WithContext(ctx))
			c.Response().Header().Set(httpx.HeaderRequestID, id)
			return next(c)
		}
	}
}
