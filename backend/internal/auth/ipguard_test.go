package auth

import (
	"net/netip"
	"testing"
	"time"
)

func TestIPGuard(t *testing.T) {
	base := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	now := base
	g := NewIPGuard(5, time.Hour)
	g.now = func() time.Time { return now }

	ip := netip.MustParseAddr("203.0.113.7")

	// 头 5 次放行，之后拒。
	for i := range 5 {
		if !g.Allow(&ip) {
			t.Fatalf("第 %d 次应放行", i+1)
		}
	}
	if g.Allow(&ip) {
		t.Fatal("超过上限后应被拒")
	}

	// 换个 IP 不受前一个牵连。
	other := netip.MustParseAddr("203.0.113.8")
	if !g.Allow(&other) {
		t.Fatal("另一个 IP 不该被牵连")
	}

	// nil IP（解析不出来源）不在这一层限。
	for range 8 {
		if !g.Allow(nil) {
			t.Fatal("IP 为 nil 时不该在这一层限流")
		}
	}

	// 窗口过后重置：时间推过 window，同一个 IP 又能来。
	now = base.Add(time.Hour + time.Second)
	if !g.Allow(&ip) {
		t.Fatal("窗口过后同一个 IP 应重新放行")
	}
}

// TestIPGuardIPv6GroupsBy64 同一个 /64 段里换地址不能重置额度（否则 IPv6 轮换直接绕过）。
func TestIPGuardIPv6GroupsBy64(t *testing.T) {
	now := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	g := NewIPGuard(5, time.Hour)
	g.now = func() time.Time { return now }

	// 同一 /64（2001:db8:1:1::/64）里的不同 /128 地址，应共享同一份额度。
	for i := range 5 {
		ip := netip.MustParseAddr("2001:db8:1:1::" + string(rune('1'+i)))
		if !g.Allow(&ip) {
			t.Fatalf("第 %d 个同 /64 地址应放行", i+1)
		}
	}
	sixth := netip.MustParseAddr("2001:db8:1:1::ffff")
	if g.Allow(&sixth) {
		t.Fatal("同 /64 段第 6 个地址应被拒 —— IPv6 该按 /64 归并，不能换地址就重置")
	}
	// 另一个 /64 段不受影响。
	other64 := netip.MustParseAddr("2001:db8:1:2::1")
	if !g.Allow(&other64) {
		t.Fatal("不同 /64 段不该被牵连")
	}
}
