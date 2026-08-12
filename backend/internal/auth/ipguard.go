package auth

import (
	"net/netip"
	"sync"
	"time"
)

// ipGuardSweepEvery 决定多久清一次过期记录，别让 map 变成内存泄漏。
const ipGuardSweepEvery = 10 * time.Minute

// IPGuard 是按 IP 的固定窗口计数节流（内存）。给某个接口加「单个 IP 在窗口内最多 N 次」的护栏，
// 补全局限流够不着的地方 —— 全局 IP 限流是「20/s 挡手滑脚本」，管不住低频但高价值的滥用
// （如把自助注册当群发验证信放大器：20/s 一小时能放几万封）。
//
// 放内存就够：重启清零可接受（不是要长期锁死谁），单副本零依赖。多副本时每副本一份、阈值
// 放大 N 倍，但这类护栏针对的动作本就低频，放大后仍远严于全局限流；真要跨副本精确共享再搬 PG
// （和限流/幂等一个套路）。和登录的 loginGuard 是同一类内存节流，只是单维、通用。
type IPGuard struct {
	mu        sync.Mutex
	seen      map[string]ipGuardRecord
	lastSweep time.Time
	now       func() time.Time // 测试可替换（默认 time.Now）
	limit     int
	window    time.Duration
}

type ipGuardRecord struct {
	count   int
	firstAt time.Time
}

// NewIPGuard 造一个按 IP 的固定窗口节流：单个 IP 在 window 内最多放行 limit 次。
func NewIPGuard(limit int, window time.Duration) *IPGuard {
	return &IPGuard{
		seen:   map[string]ipGuardRecord{},
		now:    time.Now,
		limit:  limit,
		window: window,
	}
}

// Allow 记一次尝试，返回是否放行（窗口内未超上限）。
//
// IP 为 nil（反代后头解析不出来源）时**不在这里限** —— 交给全局 IP 限流兜，别把所有解析不出
// 来源的流量挤成一个共用桶、互相误伤。
func (g *IPGuard) Allow(ip *netip.Addr) bool {
	if ip == nil {
		return true
	}
	key := guardKey(*ip)
	now := g.now()

	g.mu.Lock()
	defer g.mu.Unlock()
	g.sweepLocked(now)

	rec, ok := g.seen[key]
	if !ok || now.Sub(rec.firstAt) > g.window {
		g.seen[key] = ipGuardRecord{count: 1, firstAt: now}
		return true
	}
	if rec.count >= g.limit {
		return false
	}
	rec.count++
	g.seen[key] = rec
	return true
}

// guardKey 把 IP 归一成计数用的 key。
//
// **IPv6 按 /64 前缀归并**：一个 /64 分配（家宽、云主机默认都是整段 /64）能轮换 2^64 个
// 地址，逐个 /128 计数等于没限 —— 攻击者每次换个地址就重置额度。按 /64 归并让「同一个分配」
// 共用一份额度。IPv4 用完整地址（/32 就是一台）。
//
// ⚠️ **残留**：换**不同 /64 段**（或不同 IPv4）仍能各得一份额度 —— 这是 per-IP 护栏的通病，
// 挡不住「攻击者手握大量互不相邻的出口」。真要压死放大得再加一道与 IP 无关的全局注册配额；
// 本层的定位是「抬高单点滥用成本」，不是「绝对配额」。
func guardKey(ip netip.Addr) string {
	if ip.Is6() && !ip.Is4In6() {
		if p, err := ip.Prefix(64); err == nil {
			return p.String()
		}
	}
	return ip.String()
}

func (g *IPGuard) sweepLocked(now time.Time) {
	if now.Sub(g.lastSweep) < ipGuardSweepEvery {
		return
	}
	g.lastSweep = now
	for k, rec := range g.seen {
		if now.Sub(rec.firstAt) > g.window {
			delete(g.seen, k)
		}
	}
}
