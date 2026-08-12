// Package task 是后台定时任务：goroutine + ticker + PG advisory lock。
//
// 不引 cron 框架、不引消息队列（DECISIONS.md §1、§9）。
// 多实例部署时靠 advisory lock 保证同一时刻只有一个实例在跑同一个任务。
package task

import (
	"context"
	"hash/fnv"
	"log/slog"
	"sync"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

// Job 是一个定时任务。
type Job struct {
	// Name 进日志，也用来算 advisory lock 的键。
	Name string
	// Every 是执行间隔。
	Every time.Duration
	// RunAtStart 为 true 时启动后立刻跑一次，不等第一个间隔。
	RunAtStart bool
	// Fn 是任务本体。返回 error 只记日志，不影响后续调度。
	Fn func(ctx context.Context) error
}

// Runner 跑一组定时任务。
type Runner struct {
	pool   *pgxpool.Pool
	logger *slog.Logger
	jobs   []Job
	wg     sync.WaitGroup
}

// NewRunner 造一个任务调度器。
func NewRunner(pool *pgxpool.Pool, logger *slog.Logger) *Runner {
	return &Runner{pool: pool, logger: logger}
}

// Add 登记一个任务。必须在 Start 之前调。
func (r *Runner) Add(job Job) { r.jobs = append(r.jobs, job) }

// Start 启动所有任务，ctx 取消后各自退出。
func (r *Runner) Start(ctx context.Context) {
	for _, job := range r.jobs {
		r.wg.Add(1)
		go func() {
			defer r.wg.Done()
			r.loop(ctx, job)
		}()
	}
}

// Wait 等所有任务退出，优雅关闭时用。
func (r *Runner) Wait() { r.wg.Wait() }

func (r *Runner) loop(ctx context.Context, job Job) {
	if job.RunAtStart {
		r.runOnce(ctx, job)
	}

	ticker := time.NewTicker(job.Every)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			r.runOnce(ctx, job)
		}
	}
}

// runOnce 抢到锁才执行 —— 多实例时只有一个实例真的干活。
func (r *Runner) runOnce(ctx context.Context, job Job) {
	conn, err := r.pool.Acquire(ctx)
	if err != nil {
		r.logger.WarnContext(ctx, "定时任务取连接失败",
			slog.String("job", job.Name), slog.String("error", err.Error()))
		return
	}
	defer conn.Release()

	key := lockKey(job.Name)
	var locked bool
	if err := conn.QueryRow(ctx, "SELECT pg_try_advisory_lock($1)", key).Scan(&locked); err != nil {
		r.logger.WarnContext(ctx, "定时任务抢锁失败",
			slog.String("job", job.Name), slog.String("error", err.Error()))
		return
	}
	if !locked {
		// 别的实例正在跑，这次跳过是正常的
		return
	}
	defer func() {
		if _, err := conn.Exec(context.WithoutCancel(ctx), "SELECT pg_advisory_unlock($1)", key); err != nil {
			r.logger.WarnContext(ctx, "定时任务释放锁失败",
				slog.String("job", job.Name), slog.String("error", err.Error()))
		}
	}()

	start := time.Now()
	if err := job.Fn(ctx); err != nil {
		r.logger.ErrorContext(ctx, "定时任务失败",
			slog.String("job", job.Name),
			slog.Duration("took", time.Since(start)),
			slog.String("error", err.Error()))
		return
	}
	r.logger.DebugContext(ctx, "定时任务完成",
		slog.String("job", job.Name), slog.Duration("took", time.Since(start)))
}

// lockKey 把任务名散列成 advisory lock 用的 int64。
func lockKey(name string) int64 {
	h := fnv.New64a()
	h.Write([]byte("fries.task." + name))
	return int64(h.Sum64() >> 1) // 去掉符号位，避免负数带来的可读性问题
}
