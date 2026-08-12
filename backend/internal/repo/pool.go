package repo

import (
	"context"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

// PoolOptions 是连接池参数。
//
// 这里刻意不 import internal/config：config 要读 settings 表（也就是要 import repo），
// 反过来 repo 再依赖 config 就成环了。参数由 cmd/server 组装时传进来。
type PoolOptions struct {
	MaxConns        int32
	MinConns        int32
	ConnMaxLifetime time.Duration
	// ConnectTimeout 是启动时等数据库就绪的总时长。
	ConnectTimeout time.Duration
}

// NewPool 按配置建连接池，并在启动时探活一次。
//
// 连不上就直接失败 —— 起来了却连不上库，只会把问题推迟到第一个请求，更难查。
func NewPool(ctx context.Context, dsn string, opts PoolOptions) (*pgxpool.Pool, error) {
	poolCfg, err := pgxpool.ParseConfig(dsn)
	if err != nil {
		return nil, fmt.Errorf("解析数据库连接串: %w", err)
	}
	poolCfg.MaxConns = opts.MaxConns
	poolCfg.MinConns = opts.MinConns
	poolCfg.MaxConnLifetime = opts.ConnMaxLifetime

	// 会话时区固定 UTC。否则 PG 会按服务器时区把 timestamptz 返回成
	// `+08:00` 偏移，接口输出就不是 §2.5 要求的带 Z 的 RFC3339 了。
	if poolCfg.ConnConfig.RuntimeParams == nil {
		poolCfg.ConnConfig.RuntimeParams = map[string]string{}
	}
	poolCfg.ConnConfig.RuntimeParams["timezone"] = "UTC"

	// 运行期兜底：核每一条真正发出去的 SQL（MULTI-TENANCY.md §12.2，见 trace.go）。
	//
	// **无条件挂上，由 tenantAssertions 那个开关决定要不要真检查** ——
	// 装配时判开关的话，「先建池、后打开断言」这个顺序就成了隐式依赖，
	// 而 testdb.Start 和 cmd/server 恰好是两种不同的顺序。挂着不开销可忽略
	// （一次 atomic.Load）。
	poolCfg.ConnConfig.Tracer = &tenantTracer{}

	pool, err := pgxpool.NewWithConfig(ctx, poolCfg)
	if err != nil {
		return nil, fmt.Errorf("创建数据库连接池: %w", err)
	}

	pingCtx, cancel := context.WithTimeout(ctx, opts.ConnectTimeout)
	defer cancel()
	if err := waitReady(pingCtx, pool); err != nil {
		pool.Close()
		return nil, fmt.Errorf("连接数据库 %s: %w", poolCfg.ConnConfig.Host, err)
	}
	return pool, nil
}

// retryInterval 是启动探活的重试间隔。容器编排里 PG 可能比后端晚几秒就绪。
const retryInterval = 500 * time.Millisecond

// waitReady 在超时窗口内反复探活，直到连上或超时。
func waitReady(ctx context.Context, pool *pgxpool.Pool) error {
	var lastErr error
	for {
		lastErr = pool.Ping(ctx)
		if lastErr == nil {
			return nil
		}
		select {
		case <-ctx.Done():
			return fmt.Errorf("%w（最后一次错误：%w）", ctx.Err(), lastErr)
		case <-time.After(retryInterval):
		}
	}
}
