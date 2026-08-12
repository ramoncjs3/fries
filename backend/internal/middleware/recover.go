package middleware

import (
	"fmt"
	"log/slog"
	"net/http"
	"runtime"

	"github.com/labstack/echo/v5"

	"github.com/ramoncjs3/fries/internal/errs"
	"github.com/ramoncjs3/fries/internal/httpx"
)

// stackBufSize 是抓 panic 堆栈的缓冲区大小。
const stackBufSize = 8 << 10

// Recover 兜住任何 panic：堆栈只进日志，客户端只拿到通用文案 + request_id（红线 #5）。
func Recover(logger *slog.Logger) echo.MiddlewareFunc {
	return func(next echo.HandlerFunc) echo.HandlerFunc {
		return func(c *echo.Context) (err error) {
			defer func() {
				r := recover()
				if r == nil {
					return
				}
				if r == http.ErrAbortHandler { //nolint:errorlint // 哨兵值比较，不是 error 链
					panic(r)
				}

				buf := make([]byte, stackBufSize)
				buf = buf[:runtime.Stack(buf, false)]

				ctx := c.Request().Context()
				logger.ErrorContext(ctx, "handler panic",
					slog.String("request_id", httpx.RequestID(ctx)),
					slog.String("method", c.Request().Method),
					slog.String("path", c.Path()),
					slog.Any("panic", r),
					slog.String("stack", string(buf)),
				)

				err = errs.Internal.Wrap(fmt.Errorf("panic: %v", r))
			}()
			return next(c)
		}
	}
}
