package httpx

import (
	"context"
	"net/netip"
	"strings"

	"github.com/google/uuid"
)

// HeaderRequestID 是贯穿全站的请求 ID 响应头（DECISIONS.md §4.8）。
const HeaderRequestID = "X-Request-Id"

// requestIDPrefix 让日志里一眼能认出这是请求 ID。
const requestIDPrefix = "req_"

// maxRequestIDLen 限制外部传入的 ID 长度，防止日志被灌爆。
const maxRequestIDLen = 64

type requestIDKey struct{}

// WithRequestID 把请求 ID 放进 context。中间件调用一次，之后全链路可读。
func WithRequestID(ctx context.Context, id string) context.Context {
	return context.WithValue(ctx, requestIDKey{}, id)
}

// RequestID 从 context 取请求 ID，没有则返回空串。
func RequestID(ctx context.Context) string {
	if ctx == nil {
		return ""
	}
	id, _ := ctx.Value(requestIDKey{}).(string)
	return id
}

type clientIPKey struct{}

// WithClientIP 把客户端 IP 放进 context。
//
// 中间件解析一次，service 层（登录记 IP、限流）和审计都从 context 取 ——
// 免得为了拿一个 IP 就把 echo.Context 一路传下去（红线 #6）。
func WithClientIP(ctx context.Context, addr *netip.Addr) context.Context {
	return context.WithValue(ctx, clientIPKey{}, addr)
}

// ClientIP 取客户端 IP，解析不出来时为 nil。
func ClientIP(ctx context.Context) *netip.Addr {
	if ctx == nil {
		return nil
	}
	addr, _ := ctx.Value(clientIPKey{}).(*netip.Addr)
	return addr
}

type tenantKey struct{}

// WithTenant 把当前租户放进 context，**只为日志服务**（MULTI-TENANCY.md §12.1）。
//
// ⚠️ 别拿它当权限依据 —— 真正的租户绑定在 `authz.Principal.TenantID` 上，
// 数据访问一律走 `Store.ForTenant()`。这里存一份是因为日志的 handler
// 不该反过来依赖 authz（httpx 是最底下那层，authz 在它上面）。
//
// 平台管理员没有租户（§2.3），这时是零值，日志里不出现这个字段。
func WithTenant(ctx context.Context, id uuid.UUID) context.Context {
	return context.WithValue(ctx, tenantKey{}, id)
}

// Tenant 取当前租户，没有则返回零值。
func Tenant(ctx context.Context) uuid.UUID {
	if ctx == nil {
		return uuid.Nil
	}
	id, _ := ctx.Value(tenantKey{}).(uuid.UUID)
	return id
}

// NewRequestID 生成一个新的请求 ID。用 UUIDv7：带时间前缀，日志里天然有序。
func NewRequestID() string {
	id, err := uuid.NewV7()
	if err != nil {
		// NewV7 只在读随机源失败时报错，这种情况退回 v4。
		id = uuid.New()
	}
	return requestIDPrefix + strings.ReplaceAll(id.String(), "-", "")
}

// SanitizeRequestID 清洗调用方传进来的 X-Request-Id。
//
// 外部值不可信：截断长度、只保留安全字符，全被过滤掉就返回空串（交给调用方重新生成）。
func SanitizeRequestID(raw string) string {
	if raw == "" {
		return ""
	}
	if len(raw) > maxRequestIDLen {
		raw = raw[:maxRequestIDLen]
	}
	var b strings.Builder
	for _, r := range raw {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9',
			r == '-', r == '_', r == '.', r == ':':
			b.WriteRune(r)
		}
	}
	return b.String()
}
