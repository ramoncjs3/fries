package config_test

import (
	"strings"
	"testing"

	"github.com/google/uuid"

	"github.com/ramoncjs3/fries/internal/config"
	"github.com/ramoncjs3/fries/internal/perm"
)

// 这一组守的是注册表**作为白名单**的那一面（设计稿 §3）。
//
// 元数据错了顶多页面难看；白名单破了是安全问题：`Set` / `SetPlatform` 原来接受
// 任意 key 字符串，配置页一接上来就是「往配置表里写任意键」的入口。
// 最要紧的后果在平台端 —— `limits.<key>.min` 敲错一个字母，那条下界
// **静默消失**（没有对应行 = 不受限），租户第二天就能把密码策略调到 1 位。

// TestRegistryIsConsistent 跑注册表自检。
//
// 它也在 `server --selfcheck` 里跑，这里再跑一遍是为了「改坏了当场红」，
// 不用等到起服务。
func TestRegistryIsConsistent(t *testing.T) {
	if err := config.CheckRegistryForTest(); err != nil {
		t.Fatalf("注册表自检没过：%v", err)
	}
}

// TestEveryDeclaredKeyHasAConstant 守「注册表 key 和 config 里那批常量一一对应」。
//
// 两边都写一遍是没办法的事（常量要给代码引用，注册表要给接口用），
// 所以必须机械核对 —— 少一个的表现是「页面上有这一项，但代码永远读不到它」。
func TestEveryDeclaredKeyHasAConstant(t *testing.T) {
	declared := map[string]bool{}
	for _, it := range append(
		config.ItemsIn(perm.RealmTenant), config.ItemsIn(perm.RealmPlatform)...,
	) {
		declared[it.Key] = true
	}

	constants := []string{
		config.KeyPasswordMinLength, config.KeyPasswordRequireMix,
		config.KeyPasswordMaxAgeDays, config.KeyLoginMaxFailures,
		config.KeyLoginLockMinutes,
		config.KeyAuditRetentionDays, config.KeySystemName,
		config.KeyAllowSelfRegistration,
	}
	for _, key := range constants {
		if !declared[key] {
			t.Errorf("常量 %s 没有在注册表里声明 —— 页面上看不见它", key)
		}
		delete(declared, key)
	}
	for key := range declared {
		t.Errorf("注册表里的 %s 没有对应常量 —— 大概率没有代码在读它，"+
			"那就是个改了也不生效的旋钮", key)
	}
}

// TestBoundKeysAreDerivedNotTyped 守「上下界的键名由代码拼，不由人敲」。
//
// 这条是整个白名单存在的理由：敲错一个字母 = 那条界静默消失。
func TestBoundKeysAreDerivedNotTyped(t *testing.T) {
	got := config.BoundKeys()

	// 只有租户级的整数项才有区间 —— 开关和文本卡不出区间来
	if len(got) != 4*2 {
		t.Fatalf("租户级整数项有 4 个，应该产出 8 个界，得到 %d 个：%v", len(got), got)
	}
	for _, k := range got {
		if !strings.HasPrefix(k, "limits.") {
			t.Errorf("界的键名必须以 limits. 打头，得到 %q", k)
		}
	}
	if want := "limits." + config.KeyPasswordMinLength + ".min"; got[0] != want &&
		!strings.Contains(strings.Join(got, ","), want) {
		t.Errorf("没有拼出 %s", want)
	}

	// 开关项不该有界
	for _, k := range got {
		if strings.Contains(k, config.KeyPasswordRequireMix) {
			t.Errorf("%s 是开关，不该有上下界", config.KeyPasswordRequireMix)
		}
	}
}

// TestWritableRejectsUndeclaredKeys 是**这套白名单的核心用例**。
func TestWritableRejectsUndeclaredKeys(t *testing.T) {
	cases := []struct {
		name  string
		key   string
		realm perm.Realm
		want  bool
	}{
		{"租户端写租户级项", config.KeyPasswordMinLength, perm.RealmTenant, true},
		{"平台端写平台级项", config.KeyAuditRetentionDays, perm.RealmPlatform, true},
		{"平台端写上下界", config.BoundKey(config.KeyPasswordMinLength, false), perm.RealmPlatform, true},

		// 下面每一条都是真实会发生的错误
		{
			// 🔴 这条是整个设计的起点：少一个字母，那条下界就没人管了
			name: "上下界键名敲错一个字母", want: false, realm: perm.RealmPlatform,
			key: "limits.security.password_min_length.mn",
		},
		{
			// 租户管理员不该能碰平台的旋钮 —— 尤其是给他自己划界的那些
			name: "租户端写平台级项", want: false, realm: perm.RealmTenant,
			key: config.KeyAuditRetentionDays,
		},
		{
			// 更要紧的：租户端写上下界 = 自己给自己松绑
			name: "租户端写上下界", want: false, realm: perm.RealmTenant,
			key: config.BoundKey(config.KeyPasswordMinLength, false),
		},
		{"平台端写租户级项", config.KeyPasswordMinLength, perm.RealmPlatform, false},
		{"凭空编一个 key", "totally.made_up", perm.RealmTenant, false},
		{"空 key", "", perm.RealmTenant, false},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := config.WritableForTest(c.key, c.realm); got != c.want {
				t.Fatalf("writable(%q, %s) 应该是 %v，得到 %v", c.key, c.realm, c.want, got)
			}
		})
	}
}

// TestDefaultsComeFromRegistry 守「默认值只有一处」。
//
// 原来 defaultSecurity() 和注册表各写一份同样的值，而它们必须永远相等 ——
// 那是这个项目反复踩过的「两份清单会漂」。现在后者从前者派生。
//
// ⚠️ 准确地说这条用例守的是「**没有人把写死的字面量写回来**」：
// 派生关系还在的时候，改注册表两边一起变，这条断言是同义反复、抓不到东西
// （变异验证时确认过）。它真正会红的时刻，是有人图省事把
// `defaultOf[int](KeyX)` 换回一个字面量、而那个字面量和注册表对不上。
func TestDefaultsComeFromRegistry(t *testing.T) {
	s := config.NewDefaultSettings()
	sec := s.Security(uuid.New())

	declared := map[string]config.Item{}
	for _, it := range config.ItemsIn(perm.RealmTenant) {
		declared[it.Key] = it
	}

	for _, c := range []struct {
		key  string
		got  int
		name string
	}{
		{config.KeyPasswordMinLength, sec.PasswordMinLength, "密码最短长度"},
		{config.KeyPasswordMaxAgeDays, sec.PasswordMaxAgeDays, "密码有效期"},
		{config.KeyLoginMaxFailures, sec.LoginMaxFailures, "失败次数"},
		{config.KeyLoginLockMinutes, sec.LoginLockMinutes, "锁定时长"},
	} {
		it, ok := declared[c.key]
		if !ok {
			t.Fatalf("%s 不在注册表里", c.key)
		}
		if want := it.Default.(int); c.got != want {
			t.Errorf("%s 的默认值：注册表说 %d，Security() 给的是 %d —— 两处漂了",
				c.name, want, c.got)
		}
	}
}
