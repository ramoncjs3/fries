package middleware

import (
	"context"
	"net/http"
	"sync"
	"time"

	"github.com/labstack/echo/v5"

	"github.com/ramoncjs3/fries/internal/authz"
	"github.com/ramoncjs3/fries/internal/errs"
)

// scopeOf 取「这次请求属于谁」，作为幂等键的前缀。
//
// 没有主体就返回空串（未认证请求共用一个域）—— 不是安全问题：
// 那类接口没有幂等语义，真要防重放靠的是别的机制。
func scopeOf(ctx context.Context) string {
	p, ok := authz.PrincipalFrom(ctx)
	if !ok {
		return "-"
	}
	return p.TenantID.String() + "/" + p.ID.String()
}

// HeaderIdempotencyKey 是幂等键请求头。所有写接口都支持（DECISIONS.md §4.8、§8.1）。
const HeaderIdempotencyKey = "Idempotency-Key"

// maxIdempotencyKeyLen 限制键长度，防止被拿来当内存写入口。
const maxIdempotencyKeyLen = 128

// forgetTimeout 是释放幂等键（失败/panic 时）那次 DELETE 的独立超时 —— 不复用已可能取消的请求 ctx。
const forgetTimeout = 5 * time.Second

// IdempotencyStore 记住已经处理过的幂等键。
//
// 第 ① 步是内存实现（单实例足够）；多副本部署要换成 PG 实现，接口不动即可搬。
//
// ⚠️ 当前语义是「只记键、不记响应」：第一次成功但响应在网络上丢了、客户端拿同一个 key
// 重试，会拿到 409 idempotency_conflict（前端对它静默忽略，DECISIONS.md §4.6），
// **不是重放第一次的成功响应**。对本项目够用。真要做 Stripe 式的响应重放，
// 得给这个接口加「存/取响应体」的方法 —— 那时接口会变，不再是「实现不动接口」。
type IdempotencyStore interface {
	// Remember 尝试记下 key。返回 false 表示这个键已经见过 —— 重复请求。
	Remember(ctx context.Context, key string) (bool, error)
	// Forget 释放 key，让客户端可以安全重试。请求失败时调用。
	Forget(ctx context.Context, key string)
}

// MemoryIdempotencyStore 是基于内存的幂等键存储。
type MemoryIdempotencyStore struct {
	ttl time.Duration

	mu        sync.Mutex
	seen      map[string]time.Time
	lastSweep time.Time
}

// NewMemoryIdempotencyStore 造一个内存幂等键存储，ttl 是键的记忆时长。
func NewMemoryIdempotencyStore(ttl time.Duration) *MemoryIdempotencyStore {
	return &MemoryIdempotencyStore{
		ttl:       ttl,
		seen:      make(map[string]time.Time),
		lastSweep: time.Now(),
	}
}

// Remember 实现 IdempotencyStore。
func (s *MemoryIdempotencyStore) Remember(_ context.Context, key string) (bool, error) {
	now := time.Now()

	s.mu.Lock()
	defer s.mu.Unlock()

	s.sweepLocked(now)
	if exp, ok := s.seen[key]; ok && exp.After(now) {
		return false, nil
	}
	s.seen[key] = now.Add(s.ttl)
	return true, nil
}

// Forget 实现 IdempotencyStore。
func (s *MemoryIdempotencyStore) Forget(_ context.Context, key string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.seen, key)
}

// sweepLocked 清掉过期键。每隔 ttl/4 扫一次，不额外起 goroutine。
func (s *MemoryIdempotencyStore) sweepLocked(now time.Time) {
	if now.Sub(s.lastSweep) < s.ttl/4 {
		return
	}
	s.lastSweep = now
	for k, exp := range s.seen {
		if !exp.After(now) {
			delete(s.seen, k)
		}
	}
}

// Idempotency 让写接口幂等：同一个 Idempotency-Key 只处理一次，
// 重复的直接返回 common.idempotency_conflict（前端静默忽略，DECISIONS.md §4.6）。
//
// 没带这个头的请求原样放过 —— 头是「支持」而不是「必填」。
func Idempotency(store IdempotencyStore) echo.MiddlewareFunc {
	return func(next echo.HandlerFunc) echo.HandlerFunc {
		return func(c *echo.Context) error {
			if !isWrite(c.Request().Method) {
				return next(c)
			}

			raw := c.Request().Header.Get(HeaderIdempotencyKey)
			if raw == "" {
				return next(c)
			}
			if len(raw) > maxIdempotencyKeyLen {
				return errs.ValidationFailed.
					WithField("header."+HeaderIdempotencyKey, "幂等键太长")
			}

			ctx := c.Request().Context()
			// 键按「方法 + 路由 + 客户端给的键」组合，避免不同接口撞键。
			// 路由没匹配上时 c.Path() 是空的，退回用真实路径，否则所有 404
			// 会共用一个键，第二个不同路径的请求会被误判成重放。
			route := c.Path()
			if route == "" {
				route = c.Request().URL.Path
			}
			// ⚠️ 还要带上**租户和主体**（MULTI-TENANCY.md §10.6）。
			// 客户端给的键是一个它自己随便取的字符串：
			//   · 不带租户 —— A 公司用过的键，B 公司再用就被当成重放直接 409，
			//     一次跨租户的互相干扰（虽然拿不到对方的结果，但能搅黄对方的请求）
			//   · 只带租户还不够 —— 同一家公司里张三和李四撞上同一个键，后来的那个
			//     一样会被误判成重放
			// 未认证的请求没有主体，用空前缀 —— 那类接口本来也不该用幂等键。
			key := scopeOf(ctx) + " " + c.Request().Method + " " + route + " " + raw

			fresh, err := store.Remember(ctx, key)
			if err != nil {
				return errs.Internal.Wrap(err)
			}
			if !fresh {
				return errs.IdempotencyConflict
			}

			// 只记住成功的请求。失败（含参数校验失败）就放掉键 —— 操作本来也没做成，
			// 客户端改完重试还得能用同一个键。
			//
			// ⚠️ 用 defer 释放，且**用独立 ctx**：
			//   · 内存版进程重启即清空 map、崩了能自愈；PG 版键跨副本、跨重启存活，一旦占了不放
			//     就把这个操作的重试锁死到 TTL（默认 24h）。多副本部署进程更替频繁，命中概率更高。
			//   · handler panic 时非 defer 的释放会被跳过 → 孤儿键；所以必须 defer。
			//   · 请求超时后 c.Request().Context() 已取消，拿它去 DELETE 会静默失败 → 同样留孤儿；
			//     所以释放走一个新的、带短超时的 background ctx，不受请求生命周期影响。
			released := false
			release := func() {
				if released {
					return
				}
				released = true
				rctx, cancel := context.WithTimeout(context.Background(), forgetTimeout)
				defer cancel()
				store.Forget(rctx, key)
			}
			defer func() {
				// panic 也要放键（否则孤儿锁到 TTL）；放完把 panic 继续往上抛给外层 Recover。
				if r := recover(); r != nil {
					release()
					panic(r)
				}
			}()

			err = next(c)
			if _, status := echo.ResolveResponseStatus(c.Response(), err); status >= http.StatusBadRequest {
				release()
			}
			return err
		}
	}
}

// isWrite 判断是否写请求。
func isWrite(method string) bool {
	switch method {
	case http.MethodPost, http.MethodPut, http.MethodPatch, http.MethodDelete:
		return true
	}
	return false
}
