package repo

import (
	"context"
	"errors"
	"log/slog"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

// listenRetryDelay 是监听连接断了之后的重连间隔。
const listenRetryDelay = 3 * time.Second

// Listen 订阅一个 PG 通知频道，收到通知就调 onNotify。
//
// 这是「改配置立即生效」和「改权限立即生效」的底座：写库 → NOTIFY → 各实例刷新缓存。
// 多实例自动同步，不引 Redis（DECISIONS.md §5）。
//
// onNotify 收到的是通知的**负载**。settings_changed 的负载是那一行的 tenant_id
// （MULTI-TENANCY.md §9.4）—— 不带租户的话，一个租户改配置会让所有实例把
// 所有租户的缓存全刷一遍。
//
// ⚠️ **负载为空串表示「刷全部」**：重连之后主动补的那一次就是空的（断开期间的
// 通知已经丢了，不知道漏了谁）。收到空负载一定要走全量刷新，不能当成 no-op。
//
// 阻塞运行，通常放 goroutine 里；ctx 取消就退出。连接断了会自己重连 ——
// 期间的通知会丢，所以重连成功后**主动刷一次**，不然缓存可能一直是旧的。
func Listen(ctx context.Context, pool *pgxpool.Pool, channel string, onNotify func(payload string), logger *slog.Logger) {
	for ctx.Err() == nil {
		if err := listenOnce(ctx, pool, channel, onNotify); err != nil && ctx.Err() == nil {
			logger.Warn("监听数据库通知中断，稍后重连",
				slog.String("channel", channel),
				slog.String("error", err.Error()),
				slog.Duration("retry_in", listenRetryDelay))

			select {
			case <-ctx.Done():
				return
			case <-time.After(listenRetryDelay):
			}
		}
	}
}

func listenOnce(ctx context.Context, pool *pgxpool.Pool, channel string, onNotify func(payload string)) error {
	conn, err := pool.Acquire(ctx)
	if err != nil {
		return err
	}
	defer conn.Release()

	// 频道名不能参数化，只能拼进 SQL —— 所以它必须是代码里的常量，
	// 绝不能来自外部输入（红线 #2）。
	if _, err := conn.Exec(ctx, "LISTEN "+quoteIdent(channel)); err != nil {
		return err
	}

	// 断线重连后先刷一次：断开期间的通知已经丢了，空负载 = 全量刷新。
	onNotify("")

	for {
		notification, err := conn.Conn().WaitForNotification(ctx)
		if err != nil {
			if errors.Is(err, context.Canceled) {
				return nil
			}
			return err
		}
		onNotify(notification.Payload)
	}
}

// quoteIdent 把标识符包成双引号形式，内部的双引号翻倍。
func quoteIdent(s string) string {
	out := make([]byte, 0, len(s)+2)
	out = append(out, '"')
	for i := range len(s) {
		if s[i] == '"' {
			out = append(out, '"')
		}
		out = append(out, s[i])
	}
	return string(append(out, '"'))
}
