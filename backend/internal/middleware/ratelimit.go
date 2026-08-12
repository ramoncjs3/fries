package middleware

import (
	"context"
	"log/slog"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/labstack/echo/v5"
	"golang.org/x/time/rate"

	"github.com/ramoncjs3/fries/internal/authz"
	"github.com/ramoncjs3/fries/internal/errs"
	"github.com/ramoncjs3/fries/internal/repo"
)

// 限流是「防手滑脚本把服务打挂」的护栏，不是精细的配额管理
// —— 登录失败节流在 internal/auth，接口配额等有 Service Account 用量再说。
//
// 分两个维度，缺一不可（MULTI-TENANCY.md §3.2 ⑦、§12.3）：
//
//	IP     —— 跑在**认证之前**，那时还不知道租户是谁。它是登录接口唯一的护栏
//	          （公司代码枚举也靠它，见 auth/guard.go）
//	租户   —— 跑在**认证之后**。没有这一维的话，一个租户跑个批量导入就能占掉
//	          共享的服务能力，**所有客户一起变慢** —— 那是 SaaS 最典型的噪声邻居问题
//
// ⚠️ 光有 IP 那一维是不够的：一家公司十个人十个 IP，每人都在自己的额度内，
// 加起来照样能把服务压住，而别家客户没有任何办法。
const (
	// ipRatePerSecond 是单个 IP 的稳态速率。
	ipRatePerSecond = 20
	// ipRateBurst 是单个 IP 允许的瞬时突发。列表页一次点开好几个请求，留够余量。
	ipRateBurst = 60

	// tenantRatePerSecond 是单个组织的稳态速率。
	//
	// 定得比 IP 那档宽：一个组织十来个人同时在用是正常的，而这一维要挡的是
	// 「一个组织的量把别家挤掉」，不是「组织内部谁用得多」。
	// ⚠️ 别定得太紧 —— 组织内部共用一个桶，太紧就变成「同事之间互相挤」，
	// 那和 auth/guard.go 里公司代码那一维一样，会成为针对客户的可用性问题。
	tenantRatePerSecond = 60
	// tenantRateBurst 是单个组织允许的瞬时突发。
	tenantRateBurst = 200

	// rateLimitIdle 超过这么久没访问的桶会被清掉。
	rateLimitIdle = 10 * time.Minute
)

// RateLimiter 是按某一个维度的限流器。
//
// 维度由 keyOf 决定。**keyOf 返回空串表示这次请求不属于这个维度，直接放过** ——
// 未认证的请求没有租户，它归 IP 那一维管。
//
// 「放不放行」这个决策抽在 rateStore 后面，和幂等键（IdempotencyStore）对称：
// 多副本部署要把每副本一份的内存桶换成 PG/Redis 共享状态时，只换 rateStore 实现，
// keyOf 和 Middleware 一行都不用动（SCALING.md §1）。
type RateLimiter struct {
	keyOf func(*echo.Context) string
	store rateStore
}

// rateStore 决定某个键这次请求放不放行。换共享实现（PG）只动这里。
//
// 拿 ctx 是给 PG 版发查询用；内存版忽略它。返回值只有一个 bool：DB 出错这类情况
// 由实现自己消化（PG 版 fail-open，见 pgRateStore.allow），不往上抛 —— 限流是护栏，
// 不该因为它自己的存储抖一下就把请求挡了。
type rateStore interface {
	allow(ctx context.Context, key string) bool
}

type visitor struct {
	limiter  *rate.Limiter
	lastSeen time.Time
}

// ipKeyOf 按客户端 IP 取维度键。
func ipKeyOf(c *echo.Context) string { return c.RealIP() }

// tenantKeyOf 按主体的租户取维度键；没有租户（未认证、平台管理员）返回空串 = 不归这一维管。
func tenantKeyOf(c *echo.Context) string {
	p, ok := authz.PrincipalFrom(c.Request().Context())
	if !ok || p.TenantID == uuid.Nil {
		return ""
	}
	return p.TenantID.String()
}

// NewRateLimiter 造一个按 IP 的**内存**限流器。**挂在认证之前** —— 登录接口靠它。
func NewRateLimiter() *RateLimiter {
	return &RateLimiter{keyOf: ipKeyOf, store: newMemoryRateStore(rate.Limit(ipRatePerSecond), ipRateBurst)}
}

// NewTenantRateLimiter 造一个按租户的**内存**限流器。**必须挂在认证之后**，
// 否则拿不到主体，每次请求的键都是空串，等于没装。
//
// 平台管理员没有租户（§2.3），落不进任何桶 —— 他们人数极少，
// 由 IP 那一维加平台登录自己那套更严的节流（auth/platform.go）管着。
func NewTenantRateLimiter() *RateLimiter {
	return &RateLimiter{keyOf: tenantKeyOf, store: newMemoryRateStore(rate.Limit(tenantRatePerSecond), tenantRateBurst)}
}

// NewPgRateLimiter / NewPgTenantRateLimiter 是上面两个的 **PostgreSQL 版**（多副本共享计数）。
// 语义从令牌桶变成固定窗口（每秒一个桶），理由和代价见 migrations/00015。
//
// ⚠️ 每窗口上限传的是 **perSecond（稳态速率），不是 burst**。内存令牌桶里 burst 是**瞬时峰值容量**、
// perSecond 才是可持续速率；固定窗口没有这两层，若拿 burst 当每秒上限，稳态吞吐会比内存版宽 3 倍
// 以上（IP 20→60、租户 60→200），切到 PG 后同一套护栏悄悄变松。用 perSecond 对齐内存版的稳态；
// 每秒 20/60 对「列表页点开几个请求」这种正常突发也够。
func NewPgRateLimiter(q *repo.UnscopedQueries, log *slog.Logger) *RateLimiter {
	return &RateLimiter{keyOf: ipKeyOf, store: newPgRateStore(q, log, ipRatePerSecond)}
}

// NewPgTenantRateLimiter 是按租户的 PostgreSQL 版限流器（多副本共享计数）。
func NewPgTenantRateLimiter(q *repo.UnscopedQueries, log *slog.Logger) *RateLimiter {
	return &RateLimiter{keyOf: tenantKeyOf, store: newPgRateStore(q, log, tenantRatePerSecond)}
}

// Middleware 返回限流中间件。超限返回 common.rate_limited，前端会 toast 提示。
func (r *RateLimiter) Middleware() echo.MiddlewareFunc {
	return func(next echo.HandlerFunc) echo.HandlerFunc {
		return func(c *echo.Context) error {
			key := r.keyOf(c)
			if key == "" {
				return next(c)
			}
			if !r.store.allow(c.Request().Context(), key) {
				return errs.RateLimited
			}
			return next(c)
		}
	}
}

// memoryRateStore 是每副本一份的内存令牌桶（DECISIONS.md §6：不引 Redis）。
//
// ⚠️ **多副本时每副本各一份桶，实际阈值放大 N 倍**（SCALING.md §1）。
// 换成 PG/Redis 共享状态的实现塞进 RateLimiter.store 即可，调用方无感。
type memoryRateStore struct {
	perSecond rate.Limit
	burst     int

	mu        sync.Mutex
	visitors  map[string]*visitor
	lastSweep time.Time
}

func newMemoryRateStore(perSecond rate.Limit, burst int) *memoryRateStore {
	return &memoryRateStore{
		perSecond: perSecond,
		burst:     burst,
		visitors:  map[string]*visitor{},
		lastSweep: time.Now(),
	}
}

func (s *memoryRateStore) allow(_ context.Context, key string) bool {
	now := time.Now()

	s.mu.Lock()
	defer s.mu.Unlock()
	s.sweepLocked(now)

	v, ok := s.visitors[key]
	if !ok {
		v = &visitor{limiter: rate.NewLimiter(s.perSecond, s.burst)}
		s.visitors[key] = v
	}
	v.lastSeen = now
	return v.limiter.Allow()
}

func (s *memoryRateStore) sweepLocked(now time.Time) {
	if now.Sub(s.lastSweep) < rateLimitIdle {
		return
	}
	s.lastSweep = now
	for key, v := range s.visitors {
		if now.Sub(v.lastSeen) > rateLimitIdle {
			delete(s.visitors, key)
		}
	}
}
