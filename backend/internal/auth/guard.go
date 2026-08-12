package auth

import (
	"net/netip"
	"strings"
	"sync"
	"time"
)

// 登录失败的内存节流，两个维度（DECISIONS.md §6、MULTI-TENANCY.md §3.2 ⑦）。
//
// 账号维度落库（`users.failed_attempts` + `locked_until`），这里放的是登录**之前**
// 就能判断的两个维度。放内存就够 —— 重启清零可以接受，账号那道锁还在。
//
//	IP       —— 挡「一个人从一台机器上一直试」。也是**公司代码枚举**的主要防线：
//	            枚举意味着换一个代码试一次，per-code 计数永远不会触发，只有 IP 会。
//	公司代码 —— 挡「换一堆 IP 一起打同一家公司」。IP 维度对分布式来源无能为力。
//
// ⚠️ 公司代码这一维是有代价的：攻击者猛打某家公司的登录接口，那家公司的**正常用户
// 也会一起被挡**。所以它的阈值要定得远高于正常用量，而且窗口很短（一分钟一滚），
// 只削峰、不长时间锁死 —— 长时间锁死等于把它变成一件针对客户的可用性武器。
const (
	// ipMaxFailures 是同一个 IP 在窗口内允许的失败次数。
	ipMaxFailures = 20
	// ipWindow 是 IP 维度的统计窗口。
	ipWindow = 10 * time.Minute

	// codeMaxFailures 是同一个公司代码在窗口内允许的失败次数（所有 IP 加起来）。
	// 10 个人的公司一分钟内失败 50 次是不可能的，正常用量碰不到。
	codeMaxFailures = 50
	// codeWindow 是公司代码维度的统计窗口。**故意比 IP 那档短得多**，理由见上。
	codeWindow = time.Minute

	// sweepEvery 决定多久清一次过期记录。
	sweepEvery = time.Minute
)

// 两个维度在同一张 map 里，用前缀区分，省一套锁。
const (
	scopeIP   = "ip:"
	scopeCode = "code:"
)

type failRecord struct {
	failures int
	firstAt  time.Time
	window   time.Duration
	limit    int
}

// loginGuard 记登录失败次数，按 IP 和公司代码两个维度。
type loginGuard struct {
	mu        sync.Mutex
	records   map[string]failRecord
	lastSweep time.Time
}

func newLoginGuard() *loginGuard {
	return &loginGuard{records: map[string]failRecord{}, lastSweep: time.Now()}
}

// blocked 判断这次登录尝试该不该直接拒掉。
//
// 两个维度**任意一个**超了就拒。tenantCode 是用户在登录框里敲的那串，
// 还没查过库 —— 不存在的代码也照样计数，那正是我们要限的东西。
func (g *loginGuard) blocked(ip *netip.Addr, tenantCode string) bool {
	return g.blockedWith(ip, tenantCode, ipMaxFailures)
}

// blockedWith 同上，但 IP 维度用调用方给的阈值。
//
// 平台登录要比租户登录严（§9.2）：拿到一个平台管理员账号 = 能开关所有客户的组织。
// 窗口不用传 —— 每条记录自己记着是按哪个窗口攒起来的。
func (g *loginGuard) blockedWith(ip *netip.Addr, tenantCode string, ipLimit int) bool {
	now := time.Now()

	g.mu.Lock()
	defer g.mu.Unlock()
	g.sweepLocked(now)

	for _, key := range g.keys(ip, tenantCode) {
		rec, ok := g.records[key]
		if !ok || now.Sub(rec.firstAt) > rec.window {
			continue
		}
		limit := rec.limit
		if strings.HasPrefix(key, scopeIP) && ipLimit < limit {
			limit = ipLimit
		}
		if rec.failures >= limit {
			return true
		}
	}
	return false
}

// fail 记一次失败，两个维度各记一笔。
func (g *loginGuard) fail(ip *netip.Addr, tenantCode string) {
	g.failWith(ip, tenantCode, ipMaxFailures, ipWindow)
}

// failWith 同上，IP 维度用调用方给的阈值。
func (g *loginGuard) failWith(ip *netip.Addr, tenantCode string, ipLimit int, ipWin time.Duration) {
	now := time.Now()

	g.mu.Lock()
	defer g.mu.Unlock()
	g.sweepLocked(now)

	for _, key := range g.keys(ip, tenantCode) {
		window, limit := ipWin, ipLimit
		if strings.HasPrefix(key, scopeCode) {
			window, limit = codeWindow, codeMaxFailures
		}
		rec, ok := g.records[key]
		if !ok || now.Sub(rec.firstAt) > window {
			rec = failRecord{firstAt: now}
		}
		rec.failures++
		rec.window, rec.limit = window, limit
		g.records[key] = rec
	}
}

// succeed 登录成功就清掉这个 IP 的失败记录。
//
// ⚠️ **不清公司代码那一笔**：攻击者手上只要有一个能登进去的账号，
// 就能靠反复成功登录把整家公司的计数一直归零，那道防线就废了。
func (g *loginGuard) succeed(ip *netip.Addr) {
	if ip == nil {
		return
	}
	g.mu.Lock()
	delete(g.records, scopeIP+ip.String())
	g.mu.Unlock()
}

// keys 返回这次尝试要记/要查的键。IP 拿不到时只剩公司代码那一维。
func (g *loginGuard) keys(ip *netip.Addr, tenantCode string) []string {
	keys := make([]string, 0, 2)
	if ip != nil {
		keys = append(keys, scopeIP+ip.String())
	}
	// 公司代码大小写不敏感（tenants.code 存的一律是小写），键也要归一化，
	// 否则大小写换着写就能绕过计数。
	if code := strings.ToLower(strings.TrimSpace(tenantCode)); code != "" {
		keys = append(keys, scopeCode+code)
	}
	return keys
}

// sweepLocked 清掉过期记录，别让这个 map 变成内存泄漏。
func (g *loginGuard) sweepLocked(now time.Time) {
	if now.Sub(g.lastSweep) < sweepEvery {
		return
	}
	g.lastSweep = now
	for key, rec := range g.records {
		if now.Sub(rec.firstAt) > rec.window {
			delete(g.records, key)
		}
	}
}
