package middleware

import (
	"context"
	"io"
	"log/slog"
	"testing"
	"time"

	"github.com/ramoncjs3/fries/internal/repo"
	"github.com/ramoncjs3/fries/internal/repo/testdb"
)

// TestPgRateStore 验 PG 限流器的固定窗口计数（SCALING.md §1）：同一窗口内加到上限就拒，
// 不同 key 各算各的，DB 出错时 fail-open 放行。
//
// 白盒测（package middleware）：直接构造 pgRateStore、调 allow，不绕 HTTP。
func TestPgRateStore(t *testing.T) {
	pool := testdb.Start(t)
	ctx := t.Context()
	q := repo.New(pool).Unscoped()
	log := slog.New(slog.NewTextHandler(io.Discard, nil))

	// 把窗口调大到一小时，让本测试的几次调用稳落在同一个窗口里，绕开秒边界抖动。
	old := rateWindow
	rateWindow = time.Hour
	t.Cleanup(func() { rateWindow = old })

	t.Run("同一窗口加到上限就拒", func(t *testing.T) {
		s := newPgRateStore(q, log, 3)
		key := "ip:1.2.3.4"
		for i := 1; i <= 3; i++ {
			if !s.allow(ctx, key) {
				t.Fatalf("第 %d 次应放行（上限 3）", i)
			}
		}
		if s.allow(ctx, key) {
			t.Fatal("第 4 次应被限住（超过上限 3）")
		}
	})

	t.Run("不同 key 各算各的", func(t *testing.T) {
		s := newPgRateStore(q, log, 1)
		if !s.allow(ctx, "ip:a") {
			t.Fatal("a 的第一次应放行")
		}
		// b 是另一个维度键，不该被 a 的计数波及
		if !s.allow(ctx, "ip:b") {
			t.Fatal("b 的第一次应放行 —— 不同 key 互不影响")
		}
	})

	t.Run("DB 出错 fail-open 放行", func(t *testing.T) {
		s := newPgRateStore(q, log, 1)
		canceled, cancel := context.WithCancel(ctx)
		cancel() // 取消的 ctx 会让查询失败
		if !s.allow(canceled, "ip:fail") {
			t.Fatal("查库失败时应 fail-open 放行，别把 DB 抖动放大成全站 429")
		}
	})
}
