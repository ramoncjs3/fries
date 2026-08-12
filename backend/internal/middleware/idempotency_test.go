package middleware_test

import (
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/labstack/echo/v5"

	"github.com/ramoncjs3/fries/internal/middleware"
)

// recoverToJSON 是个极简的 recover 中间件，模拟生产里 Recover 兜在 Idempotency **外层**：
// handler panic 时不打挂进程，兜成 500。
func recoverToJSON(next echo.HandlerFunc) echo.HandlerFunc {
	return func(c *echo.Context) (err error) {
		defer func() {
			if r := recover(); r != nil {
				err = c.String(http.StatusInternalServerError, "panic")
			}
		}()
		return next(c)
	}
}

// TestIdempotencyReleasesKeyOnPanic 是 S1 回归：handler panic 后幂等键必须被释放，
// 否则同一个 key 的重试会被当成重放直接 409，把这个操作锁死到 TTL（PG 版默认 24h）。
//
// 关键点：释放走 defer（panic 会跳过非 defer 的释放）。这里用内存 store 验中间件层的行为。
func TestIdempotencyReleasesKeyOnPanic(t *testing.T) {
	store := middleware.NewMemoryIdempotencyStore(time.Hour)

	var calls atomic.Int32
	e := echo.New()
	e.Use(recoverToJSON) // 外层：兜 panic
	e.Use(middleware.Idempotency(store))
	e.POST("/x", func(c *echo.Context) error {
		n := calls.Add(1)
		if n == 1 {
			panic("第一次调用炸了")
		}
		return c.String(http.StatusOK, "ok")
	})

	do := func() int {
		req := httptest.NewRequestWithContext(t.Context(), http.MethodPost, "/x", nil)
		req.Header.Set(middleware.HeaderIdempotencyKey, "same-key")
		rec := httptest.NewRecorder()
		e.ServeHTTP(rec, req)
		return rec.Code
	}

	// 第 1 次：handler panic → 外层兜成 500。键应在 panic 路径上被释放。
	if code := do(); code != http.StatusInternalServerError {
		t.Fatalf("第一次应 panic 兜成 500，得到 %d", code)
	}
	// 第 2 次：同 key 重试。若键没释放会是 409；释放了则重新执行 → 200。
	if code := do(); code != http.StatusOK {
		t.Fatalf("panic 后同 key 重试应能重新执行（200），得到 %d —— 键没释放，被孤儿锁了", code)
	}
	if got := calls.Load(); got != 2 {
		t.Fatalf("handler 应被调用 2 次（panic 一次 + 成功一次），实际 %d", got)
	}
}

// TestIdempotencyReleasesKeyOnFailure handler 返回失败（4xx/5xx）也要释放键：操作没做成，
// 客户端改完参数用同一个 key 重试还得能用。
func TestIdempotencyReleasesKeyOnFailure(t *testing.T) {
	store := middleware.NewMemoryIdempotencyStore(time.Hour)

	var calls atomic.Int32
	e := echo.New()
	e.Use(middleware.Idempotency(store))
	e.POST("/x", func(c *echo.Context) error {
		n := calls.Add(1)
		if n == 1 {
			return c.String(http.StatusBadRequest, "先失败一次")
		}
		return c.String(http.StatusOK, "ok")
	})

	do := func() int {
		req := httptest.NewRequestWithContext(t.Context(), http.MethodPost, "/x", nil)
		req.Header.Set(middleware.HeaderIdempotencyKey, "same-key")
		rec := httptest.NewRecorder()
		e.ServeHTTP(rec, req)
		return rec.Code
	}

	if code := do(); code != http.StatusBadRequest {
		t.Fatalf("第一次应 400，得到 %d", code)
	}
	if code := do(); code != http.StatusOK {
		t.Fatalf("失败后同 key 重试应能重新执行（200），得到 %d", code)
	}
}
