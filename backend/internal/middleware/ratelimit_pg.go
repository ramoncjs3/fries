package middleware

import (
	"context"
	"log/slog"
	"time"

	"github.com/ramoncjs3/fries/internal/repo"
)

// rateWindow 是固定窗口的长度。所有副本按同一个秒边界分桶，计数才对得上。
// 是 var 而非 const 只为测试能把窗口调大、绕开秒边界抖动；生产不改它。
var rateWindow = time.Second

// pgRateStore 是限流器的 PostgreSQL 实现（SCALING.md §1）。
//
// 固定窗口计数：把 now 截到秒作为窗口键，(key, window) 那一格的计数原子 +1，超过 limit 就拒。
// 和内存版的令牌桶不完全等价（见 migrations/00015），但多副本下所有副本共享同一份计数，
// 内存版做不到。
//
// ⚠️ 每请求打一次库。这是有代价的 —— 只有真多副本才值得开（默认仍走内存版）。
type pgRateStore struct {
	q     *repo.UnscopedQueries
	log   *slog.Logger
	limit int
}

func newPgRateStore(q *repo.UnscopedQueries, log *slog.Logger, limit int) *pgRateStore {
	return &pgRateStore{q: q, log: log, limit: limit}
}

// allow 把当前窗口的计数 +1，返回加完之后是否仍在上限内。
//
// **fail-open**：查库失败就放行。限流是护栏不是关卡 —— 把一次 DB 抖动放大成全站 429
// 比「这一下没限住」糟得多。失败记 warn 让运维看见。
func (s *pgRateStore) allow(ctx context.Context, key string) bool {
	window := time.Now().Truncate(rateWindow)
	count, err := s.q.BumpRateLimit(ctx, repo.BumpRateLimitParams{
		Key:         key,
		WindowStart: window,
	})
	if err != nil {
		if s.log != nil {
			s.log.WarnContext(ctx, "限流器查库失败，本次放行（fail-open）",
				slog.String("error", err.Error()))
		}
		return true
	}
	return int(count) <= s.limit
}

// DeleteOld 清掉早于 cutoff 的窗口，给后台清理任务调。
func (s *pgRateStore) DeleteOld(ctx context.Context, cutoff time.Time) (int64, error) {
	return s.q.DeleteOldRateLimits(ctx, cutoff)
}
