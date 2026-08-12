package middleware

import (
	"context"
	"errors"
	"net/http"
	"time"

	"github.com/labstack/echo/v5"

	"github.com/ramoncjs3/fries/internal/errs"
)

// BodyLimit 限制请求体大小（DECISIONS.md §6）。
//
// 两道关：先看 Content-Length 挡掉声明就超标的，再用 MaxBytesReader 挡掉
// 谎报长度或分块传输的 —— 后者会在 handler 读 body 时报错，变成 400。
func BodyLimit(maxBytes int64) echo.MiddlewareFunc {
	return func(next echo.HandlerFunc) echo.HandlerFunc {
		return func(c *echo.Context) error {
			req := c.Request()
			if req.ContentLength > maxBytes {
				return errs.ValidationFailed.Detailf("请求体超过上限 %d 字节", maxBytes)
			}
			if req.Body != nil {
				req.Body = http.MaxBytesReader(c.Response(), req.Body, maxBytes)
			}
			return next(c)
		}
	}
}

// Timeout 给每个请求设处理超时。
//
// 用 context deadline 而不是替换 ResponseWriter 的那类超时中间件：后者和流式响应
// （将来 AI 的 SSE）打架。这里只把取消信号传下去，DB 查询会跟着一起断。
func Timeout(d time.Duration) echo.MiddlewareFunc {
	return func(next echo.HandlerFunc) echo.HandlerFunc {
		return func(c *echo.Context) error {
			req := c.Request()
			ctx, cancel := context.WithTimeout(req.Context(), d)
			defer cancel()

			c.SetRequest(req.WithContext(ctx))

			err := next(c)
			if errors.Is(err, context.DeadlineExceeded) {
				return errs.ServiceUnavailable.Wrap(err)
			}
			if err == nil && errors.Is(ctx.Err(), context.DeadlineExceeded) && !committed(c) {
				return errs.ServiceUnavailable.Wrap(ctx.Err())
			}
			return err
		}
	}
}

// committed 判断响应是否已经发出去了 —— 发出去之后再改状态码是没用的。
func committed(c *echo.Context) bool {
	resp, err := echo.UnwrapResponse(c.Response())
	return err == nil && resp.Committed
}
