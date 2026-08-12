package httpx

import (
	"context"
	"log/slog"

	"github.com/google/uuid"
)

// LogHandler 给每条日志自动补上 request_id 和 tenant_id。
//
// **包一层 handler，而不是让每个调用点自己写**（MULTI-TENANCY.md §12.1）：
// 客户报障时给你的是「我们公司昨天下午打不开」，没有 tenant_id 就只能在全量日志里翻。
// 靠自觉写的话，漏掉的恰好会是出事那条 —— 平时没人会想起来给一条 warn 加租户。
//
// 只补**非零**的值：后台任务、启动阶段的日志本来就没有请求上下文，
// 硬塞两个空字段只会让每行日志变长。
type LogHandler struct{ slog.Handler }

// NewLogHandler 包住一个 handler。传 nil 会 panic —— 那是装配错误，越早炸越好。
func NewLogHandler(inner slog.Handler) *LogHandler {
	if inner == nil {
		panic("httpx: LogHandler 的内层 handler 不能为 nil")
	}
	return &LogHandler{Handler: inner}
}

// Handle 补上 request_id 和 tenant_id 之后交给内层 handler。
func (h *LogHandler) Handle(ctx context.Context, r slog.Record) error {
	if id := RequestID(ctx); id != "" {
		r.AddAttrs(slog.String("request_id", id))
	}
	if tenant := Tenant(ctx); tenant != uuid.Nil {
		r.AddAttrs(slog.String("tenant_id", tenant.String()))
	}
	return h.Handler.Handle(ctx, r)
}

// WithAttrs 重新包一层再返回。
//
// ⚠️ 这个方法和下面的 WithGroup **不能省**。少写的话，内嵌的 slog.Handler
// 会直接返回没包装的自己 —— 于是 `logger.With("module", "audit")` 之后
// 打的日志全都不带 request_id 和 tenant_id 了。
// 而 `With` 恰恰是长期运行的组件（任务、审计）最爱用的写法。
func (h *LogHandler) WithAttrs(attrs []slog.Attr) slog.Handler {
	return &LogHandler{Handler: h.Handler.WithAttrs(attrs)}
}

// WithGroup 同 WithAttrs：重新包一层再返回。
func (h *LogHandler) WithGroup(name string) slog.Handler {
	return &LogHandler{Handler: h.Handler.WithGroup(name)}
}
