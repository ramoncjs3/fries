package middleware

import (
	"context"
	"time"

	"github.com/ramoncjs3/fries/internal/repo"
)

// PgIdempotencyStore 是幂等键的 PostgreSQL 实现（SCALING.md §1）。
//
// 内存版每副本一份，多副本部署时同一个键落到不同副本上认不出重复。这版让所有副本
// 共享库里同一份「见过的键」。默认仍走内存版（单副本够用、零延迟），多副本才切到这版。
//
// 它拿的是不带租户的句柄：idempotency_keys 是跨租户 infra 表，租户已经拼进 key 字符串了
// （见 scopeOf），不靠 ForTenant 隔离。
type PgIdempotencyStore struct {
	q   *repo.UnscopedQueries
	ttl time.Duration
}

// NewPgIdempotencyStore 造一个 PG 幂等键存储，ttl 是键的记忆时长。
func NewPgIdempotencyStore(q *repo.UnscopedQueries, ttl time.Duration) *PgIdempotencyStore {
	return &PgIdempotencyStore{q: q, ttl: ttl}
}

// Remember 原子地认领 key：新键、或旧键已过期能认领，返回 true；键还有效返回 false（重放）。
//
// 「认领」由一条 INSERT ... ON CONFLICT ... WHERE 完成，不是先查后写 —— 多副本并发下
// 先查后写会有两个副本同时认为「没见过」的竞态，一条原子语句没有这个窗口。
func (s *PgIdempotencyStore) Remember(ctx context.Context, key string) (bool, error) {
	n, err := s.q.ClaimIdempotencyKey(ctx, repo.ClaimIdempotencyKeyParams{
		Key:       key,
		ExpiresAt: time.Now().Add(s.ttl),
	})
	if err != nil {
		return false, err
	}
	return n == 1, nil
}

// Forget 释放 key。释放失败不致命：键会在 ttl 后自然过期，最坏是这段时间内同键重试拿到
// 409（前端静默忽略，DECISIONS.md §4.6），所以这里吞掉错误不阻断请求收尾。
func (s *PgIdempotencyStore) Forget(ctx context.Context, key string) {
	_ = s.q.ForgetIdempotencyKey(ctx, key)
}

// DeleteExpired 清掉所有过期键，给后台清理任务调。
func (s *PgIdempotencyStore) DeleteExpired(ctx context.Context) (int64, error) {
	return s.q.DeleteExpiredIdempotencyKeys(ctx)
}
