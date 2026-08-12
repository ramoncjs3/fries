package main

import (
	"context"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/ramoncjs3/fries/internal/config"
	"github.com/ramoncjs3/fries/internal/task"
)

// 定时任务的间隔。都不是掐着点的活，粗粒度就够。
const (
	partitionInterval = 12 * time.Hour
	retentionInterval = 24 * time.Hour
	sessionInterval   = time.Hour
)

// newTaskRunner 登记后台定时任务（DECISIONS.md §1：goroutine + ticker + advisory lock）。
func (a *app) newTaskRunner() *task.Runner {
	runner := task.NewRunner(a.pool, a.log)
	// 这三个任务都是**跨租户**的：审计分区按月切、过期会话按时间清，
	// 本来就不属于任何一个租户，所以走不带租户的句柄（MULTI-TENANCY.md §3.2 ③）。
	q := a.store.Unscoped()

	// 审计按月分区，分区得提前建好，不然月初第一条审计就插不进去。
	runner.Add(task.Job{
		Name:       "audit-partitions",
		Every:      partitionInterval,
		RunAtStart: true,
		Fn: func(ctx context.Context) error {
			// 多备两个月：审计是同步写、写失败只记日志不阻断请求（internal/middleware/audit.go），
			// 所以「分区缺失」不会立刻暴露，表现成审计**静默丢失**。这个任务要是连续几天不干活
			// （宿主机故障、advisory lock 卡住），只预建当月+次月的话月初就可能撞空。
			// 预建到 +2 月，把可容忍的停摆窗口拉到一个多月，成本只是多两张空分区。
			now := time.Now()
			for _, at := range []time.Time{now, now.AddDate(0, 1, 0), now.AddDate(0, 2, 0)} {
				if _, err := q.EnsureAuditPartition(ctx, at); err != nil {
					return fmt.Errorf("建审计分区 %s: %w", at.Format("2006-01"), err)
				}
			}
			return nil
		},
	})

	// 保留期到了整分区 DROP，不逐行删（DECISIONS.md §2.6）。
	runner.Add(task.Job{
		Name:       "audit-retention",
		Every:      retentionInterval,
		RunAtStart: true,
		Fn: func(ctx context.Context) error {
			cutoff := time.Now().Add(-a.settings.AuditRetention())
			dropped, err := q.DropOldAuditPartitions(ctx, cutoff)
			if err != nil {
				return fmt.Errorf("清理过期审计分区: %w", err)
			}
			if dropped > 0 {
				a.log.InfoContext(ctx, "已删除过期审计分区",
					slog.Int("count", int(dropped)),
					slog.String("cutoff", cutoff.Format(time.DateOnly)))
			}
			return nil
		},
	})

	// 过期/已用的忘记密码 token 清掉 —— 它们是高价值凭据，不留隔夜。
	runner.Add(task.Job{
		Name:       "reset-token-cleanup",
		Every:      retentionInterval,
		RunAtStart: true,
		Fn: func(ctx context.Context) error {
			n, err := q.DeleteExpiredPasswordResetTokens(ctx)
			if err != nil {
				return fmt.Errorf("清理过期重置 token: %w", err)
			}
			if n > 0 {
				a.log.InfoContext(ctx, "已清理过期重置 token", slog.Int64("count", n))
			}
			return nil
		},
	})

	// 过期的自助注册待验证记录清掉（里面有密码哈希，别留隔夜）。
	runner.Add(task.Job{
		Name:       "pending-registration-cleanup",
		Every:      retentionInterval,
		RunAtStart: true,
		Fn: func(ctx context.Context) error {
			n, err := q.DeleteExpiredPendingRegistrations(ctx)
			if err != nil {
				return fmt.Errorf("清理过期注册记录: %w", err)
			}
			if n > 0 {
				a.log.InfoContext(ctx, "已清理过期注册记录", slog.Int64("count", n))
			}
			return nil
		},
	})

	// 过期会话留 7 天备查，之后清掉。
	runner.Add(task.Job{
		Name:  "session-cleanup",
		Every: sessionInterval,
		Fn: func(ctx context.Context) error {
			n, err := q.DeleteDeadSessions(ctx)
			if err != nil {
				return fmt.Errorf("清理过期会话: %w", err)
			}
			// 平台会话是另一张表（§10.1），别忘了它 —— 忘了的话那张表只涨不减
			p, err := a.store.Platform().DeleteDeadPlatformSessions(ctx)
			if err != nil {
				return fmt.Errorf("清理过期平台会话: %w", err)
			}
			if n+p > 0 {
				a.log.InfoContext(ctx, "已清理过期会话",
					slog.Int64("tenant", n), slog.Int64("platform", p))
			}
			return nil
		},
	})

	// 幂等键的过期清理 —— 只有把共享状态放 PG 时才有这张表要清；内存版自己会扫。
	if a.cfg.Server.SharedStateStore == config.SharedStatePostgres {
		runner.Add(task.Job{
			Name:       "idempotency-cleanup",
			Every:      retentionInterval,
			RunAtStart: true,
			Fn: func(ctx context.Context) error {
				n, err := q.DeleteExpiredIdempotencyKeys(ctx)
				if err != nil {
					return fmt.Errorf("清理过期幂等键: %w", err)
				}
				if n > 0 {
					a.log.InfoContext(ctx, "已清理过期幂等键", slog.Int64("count", n))
				}
				return nil
			},
		})

		// 限流窗口每秒一个桶，累积很快 —— 清得比幂等键勤些。只留最近一分钟，
		// 更早的窗口对当下的限流判断已经没意义。
		runner.Add(task.Job{
			Name:       "rate-limit-cleanup",
			Every:      time.Hour,
			RunAtStart: true,
			Fn: func(ctx context.Context) error {
				n, err := q.DeleteOldRateLimits(ctx, time.Now().Add(-time.Minute))
				if err != nil {
					return fmt.Errorf("清理旧限流窗口: %w", err)
				}
				if n > 0 {
					a.log.InfoContext(ctx, "已清理旧限流窗口", slog.Int64("count", n))
				}
				return nil
			},
		})
	}

	return runner
}

// warnIfAuditTamperable 检查应用连库的身份能不能改审计表。
//
// DECISIONS.md §6 要求「DB 层撤销应用账号的 UPDATE/DELETE 权限」。用 owner 或超级用户
// 连库时那条 REVOKE 是无效的（superuser 绕过一切权限检查），这时必须说出来 ——
// 否则会以为防篡改生效了，其实没有。
//
// ⚠️ 查的是**分区**不是父表：audit_logs 是分区表，数据在子分区 audit_logs_YYYYMM 里，
// 而 PostgreSQL 直接寻址分区校验的是分区自己的 ACL。只看父表会漏掉「父表已收权、分区仍可写」
// 这种情况（H1），给出虚假的安全感。这里对所有分区取「有没有任一个还能改删」。
func (a *app) warnIfAuditTamperable(ctx context.Context) {
	var canUpdate, canDelete, isSuper bool
	// bool_or 在没有分区时返回 NULL，coalesce 成 false（正常情况下迁移至少建了两个分区）。
	err := a.pool.QueryRow(ctx, `
		SELECT
			coalesce(bool_or(has_table_privilege(current_user, c.oid, 'UPDATE')), false),
			coalesce(bool_or(has_table_privilege(current_user, c.oid, 'DELETE')), false),
			coalesce((SELECT rolsuper FROM pg_roles WHERE rolname = current_user), false)
		FROM pg_inherits i
		JOIN pg_class p ON p.oid = i.inhparent
		JOIN pg_class c ON c.oid = i.inhrelid
		WHERE p.relname = 'audit_logs'
	`).Scan(&canUpdate, &canDelete, &isSuper)
	if err != nil {
		a.log.WarnContext(ctx, "检查审计表权限失败", slog.String("error", err.Error()))
		return
	}
	if !canUpdate && !canDelete && !isSuper {
		return
	}

	a.log.WarnContext(ctx, "当前数据库账号能改审计表，防篡改没有生效",
		slog.Bool("can_update", canUpdate),
		slog.Bool("can_delete", canDelete),
		slog.Bool("superuser", isSuper),
		slog.String("怎么办", "生产部署时用受限角色 fries_app 连库，见 backend/db/migrations/00004_audit.sql"))
}

// warnIfOrphanBounds 找出 platform_settings 里没人认识的 `limits.*` 行。
//
// 这类行**看着像在生效，其实没有**：租户级配置的上下界是按
// `limits.<租户级 key>.min|max` 精确匹配的，键名错一个字母就永远匹配不上，
// 而 `bounds.go` 里「没有对应行 = 不受限」的语义会让它安安静静地什么都不做
// （MULTI-TENANCY.md §10.5）。表现是「平台明明设了下限，租户还是能调到 1 位」。
//
// 走到这里的多半是手工 INSERT 或者旧迁移留下的 —— 平台端的写入口已经按
// config.BoundKeys() 收口了，正常路径造不出孤儿行。
//
// **只 WARN 不拦启动**：一行脏数据不该让服务起不来，而且它多半是历史遗留。
func (a *app) warnIfOrphanBounds(ctx context.Context) {
	valid := make(map[string]bool)
	for _, k := range config.BoundKeys() {
		valid[k] = true
	}

	var orphans []string
	for _, key := range a.settings.PlatformKeys() {
		if strings.HasPrefix(key, "limits.") && !valid[key] {
			orphans = append(orphans, key)
		}
	}
	if len(orphans) == 0 {
		return
	}

	a.log.WarnContext(ctx, "platform_settings 里有认不出来的上下界配置，它们不会生效",
		slog.Any("keys", orphans),
		slog.String("怎么办", "键名必须是 limits.<租户级配置 key>.min 或 .max，"+
			"合法的那几个见 config.BoundKeys()；多半是键名敲错了或者旧数据"))
}
