package middleware

import (
	"log/slog"
	"net/http"
	"time"

	"github.com/labstack/echo/v5"
)

// PathSkipper 决定某个路径要不要跳过。访问日志和审计都用它 ——
// 探活和抓指标每几秒一次，记了只是噪音。
type PathSkipper func(path string) bool

// AccessLog 记结构化访问日志：谁、什么请求、结果码、耗时，全都带 request_id。
//
// 这不是审计日志 —— 审计是第 ② 步的事，落库且防篡改（DECISIONS.md §6）。
func AccessLog(logger *slog.Logger, skip PathSkipper) echo.MiddlewareFunc {
	return func(next echo.HandlerFunc) echo.HandlerFunc {
		return func(c *echo.Context) error {
			if skip != nil && skip(c.Request().URL.Path) {
				return next(c)
			}

			start := time.Now()
			err := next(c)

			req := c.Request()
			// 错误处理器在中间件返回之后才跑，此时状态码可能还没写。
			// ResolveResponseStatus 会按 Echo 自己的规则推出最终状态码，日志才对得上。
			_, status := echo.ResolveResponseStatus(c.Response(), err)

			// request_id 和 tenant_id 不在这里写：httpx.LogHandler 从 ctx 里自动补，
			// 每条日志都有，不只是访问日志（§12.1）。这里再写一遍就是重复字段
			attrs := []any{
				slog.String("method", req.Method),
				slog.String("path", req.URL.Path),
				slog.String("route", c.Path()),
				slog.Int("status", status),
				slog.Duration("took", time.Since(start)),
				slog.String("ip", c.RealIP()),
				slog.String("ua", req.UserAgent()),
			}
			if err != nil {
				attrs = append(attrs, slog.String("error", err.Error()))
			}

			switch {
			case status >= http.StatusInternalServerError:
				logger.ErrorContext(req.Context(), "请求完成", attrs...)
			case status >= http.StatusBadRequest:
				logger.WarnContext(req.Context(), "请求完成", attrs...)
			default:
				logger.InfoContext(req.Context(), "请求完成", attrs...)
			}
			return err
		}
	}
}
