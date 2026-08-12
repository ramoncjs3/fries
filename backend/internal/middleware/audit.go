package middleware

import (
	"context"
	"log/slog"
	"time"

	"github.com/google/uuid"
	"github.com/labstack/echo/v5"

	"github.com/ramoncjs3/fries/internal/audit"
	"github.com/ramoncjs3/fries/internal/authz"
	"github.com/ramoncjs3/fries/internal/httpx"
)

// auditWriteTimeout 是写审计的超时。请求本身可能已经超时了，审计还是要落地。
const auditWriteTimeout = 3 * time.Second

// actorTypeOf 把主体类型翻成 audit_logs.actor_type。
//
// ⚠️ **平台管理员必须记成 ActorPlatform**（MULTI-TENANCY.md §9.2）。
// 这里曾经只分「service / 其它」两档，于是开组织、停组织这些**最高权限的动作**
// 全被记成普通 user —— 事后查审计只能靠「tenant_id 是 NULL 而 actor_id 不是」
// 反推。§9.2 的要求是「平台管理员的每一个动作都进审计」，
// 记下来了但标签是错的，等于查不出来。
//
// 写成穷举的 switch 是有意的：以后再加一种主体，漏了这里会退化成「记成 user」
// 这种**看不出来**的错，而不是编译错误 —— 所以 default 也只能是最保守的那档。
func actorTypeOf(t authz.PrincipalType) string {
	switch t {
	case authz.PrincipalPlatform:
		return audit.ActorPlatform
	case authz.PrincipalService:
		return audit.ActorService
	case authz.PrincipalUser:
		return audit.ActorUser
	default:
		return audit.ActorUser
	}
}

// AuditSink 是审计写入器需要提供的能力。
type AuditSink interface {
	Write(ctx context.Context, rec audit.Record) error
}

// Audit 是审计的中间件层：自动记下谁、何时、什么资源、什么动作、IP、UA、结果码、耗时
// （DECISIONS.md §6）。
//
// 「哪条记录」中间件看不到（新增操作的 ID 在响应里），由 handler 调
// audit.SetResourceID 补上。
//
// **读写全记**：查询也是审计对象 —— 谁翻过哪些数据同样重要。
func Audit(sink AuditSink, logger *slog.Logger, skip PathSkipper) echo.MiddlewareFunc {
	return func(next echo.HandlerFunc) echo.HandlerFunc {
		return func(c *echo.Context) error {
			req := c.Request()
			if skip != nil && skip(req.URL.Path) {
				return next(c)
			}

			start := time.Now()
			ctx, recorder := audit.WithRecorder(req.Context())
			c.SetRequest(req.WithContext(ctx))

			err := next(c)

			// 用最新的 request context：认证中间件在内层把主体塞了进去
			finished := c.Request().Context()
			_, status := echo.ResolveResponseStatus(c.Response(), err)

			collected := recorder.Snapshot()
			if collected.Skipped {
				return err
			}
			if collected.Resource == "" {
				// 没被授权中间件或 handler 标记过（公开接口、404 之类）
				collected.Resource, collected.Action = "http", "request"
			}

			rec := audit.Record{
				// 租户可以是 nil：未认证请求本来就不属于任何租户（§7.1）
				TenantID:   collected.TenantID,
				RequestID:  httpx.RequestID(finished),
				ActorType:  audit.ActorAnonymous,
				Resource:   collected.Resource,
				Action:     collected.Action,
				ResourceID: collected.ResourceID,
				Method:     req.Method,
				Path:       req.URL.Path,
				IP:         clientIP(c),
				UserAgent:  req.UserAgent(),
				HTTPStatus: status,
				Duration:   time.Since(start),
				Detail:     collected.Detail,
			}
			switch {
			case collected.Actor != nil:
				// handler 显式指定的主体优先 —— 登录成功那条就是这么来的
				id := collected.Actor.ID
				rec.ActorType, rec.ActorID, rec.ActorName = collected.Actor.Type, &id, collected.Actor.Name
			default:
				if p, ok := authz.PrincipalFrom(finished); ok {
					id := p.ID
					rec.ActorID = &id
					rec.ActorName = p.Name
					// 主体身上的租户优先级最高：认证过的请求一定归属某个租户
					if p.TenantID != uuid.Nil {
						tid := p.TenantID
						rec.TenantID = &tid
					}
					rec.ActorType = actorTypeOf(p.Type)
				}
			}

			// 请求的 context 可能已经因为超时或客户端断开被取消了，审计不能跟着丢。
			writeCtx, cancel := context.WithTimeout(context.WithoutCancel(finished), auditWriteTimeout)
			defer cancel()
			if writeErr := sink.Write(writeCtx, rec); writeErr != nil {
				logger.ErrorContext(writeCtx, "写审计日志失败",
					slog.String("request_id", rec.RequestID),
					slog.String("action", rec.Resource+":"+rec.Action),
					slog.String("error", writeErr.Error()))
			}
			return err
		}
	}
}
