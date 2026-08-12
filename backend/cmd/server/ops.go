package main

import (
	"context"
	"net/http"
	"time"

	"github.com/labstack/echo/v5"

	"github.com/ramoncjs3/fries/internal/errs"
	"github.com/ramoncjs3/fries/internal/httpx"
	"github.com/ramoncjs3/fries/internal/repo"
)

// readyTimeout 是就绪探针查库的超时。探针要快，卡住就当没就绪。
const readyTimeout = 2 * time.Second

// opsStatus 是运维探针的响应体。它们不走 /api/v1 的封套 —— 这是给 k8s / compose
// 看的，不是给前端看的接口。
type opsStatus struct {
	Status    string `json:"status"`
	Version   string `json:"version,omitempty"`
	RequestID string `json:"request_id,omitempty"`
}

// registerOps 注册运维路由：存活、就绪、指标。
func (a *app) registerOps() {
	// 存活探针：进程还在就算活着。**不查库** —— 库挂了不该让编排系统重启进程。
	a.echo.GET(opsPaths.Health, func(c *echo.Context) error {
		return c.JSON(http.StatusOK, opsStatus{
			Status:    "ok",
			Version:   a.version,
			RequestID: httpx.RequestID(c.Request().Context()),
		})
	})

	// 就绪探针：查库通了才算能接流量。
	a.echo.GET(opsPaths.Ready, func(c *echo.Context) error {
		if a.pool == nil {
			return errs.ServiceUnavailable.Detailf("数据库未初始化")
		}
		ctx, cancel := context.WithTimeout(c.Request().Context(), readyTimeout)
		defer cancel()
		// 真发一条查询，而不是只看连接池状态 —— 池子里有连接不代表库还在响应。
		if _, err := repo.New(a.pool).Unscoped().DatabaseNow(ctx); err != nil {
			// 具体原因只进日志，客户端只看到 503 + 通用文案（红线 #5）。
			return errs.ServiceUnavailable.Wrap(err)
		}
		return c.JSON(http.StatusOK, opsStatus{
			Status:    "ready",
			Version:   a.version,
			RequestID: httpx.RequestID(c.Request().Context()),
		})
	})

	a.echo.GET(opsPaths.Metrics, echo.WrapHandler(a.metrics.Handler()))
}
