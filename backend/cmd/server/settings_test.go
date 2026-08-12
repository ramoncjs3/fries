package main

import (
	"encoding/json"
	"net/http"
	"strconv"
	"strings"
	"testing"

	"github.com/ramoncjs3/fries/internal/config"
	"github.com/ramoncjs3/fries/internal/errs"
	"github.com/ramoncjs3/fries/internal/repo/testdb"
)

// 配置管理的整条链路（设计稿 docs/superpowers/specs/2026-08-11-settings-design.md）。
//
// 这一组要证的不只是「接口能用」，还有三件更要紧的事：
//
//  1. 改完**立即生效** —— 写库 → NOTIFY → 各实例刷新缓存，下一次登录就按新策略走
//  2. **跨租户隔离** —— A 改自己的不影响 B，也改不动 B 的
//  3. **白名单真的拦得住** —— 未声明的 key、别人世界的 key，一律拒绝

// settingsBody 解出配置接口返回的分组。
func settingsBody(t *testing.T, raw string) map[string]any {
	t.Helper()
	var out struct {
		Data struct {
			Groups []struct {
				Key   string `json:"key"`
				Items []struct {
					Key   string `json:"key"`
					Value any    `json:"value"`
					Min   *int   `json:"min"`
					Max   *int   `json:"max"`
				} `json:"items"`
			} `json:"groups"`
		} `json:"data"`
	}
	if err := json.Unmarshal([]byte(raw), &out); err != nil {
		t.Fatalf("解配置响应失败：%v\n%s", err, raw)
	}
	values := map[string]any{}
	for _, g := range out.Data.Groups {
		for _, it := range g.Items {
			values[it.Key] = it.Value
		}
	}
	if len(values) == 0 {
		t.Fatalf("一项配置都没返回：%s", raw)
	}
	return values
}

// saveSettings 拼一次保存请求。
func saveSettings(items ...map[string]any) map[string]any {
	return map[string]any{"items": items}
}

func item(key string, value any) map[string]any {
	return map[string]any{"key": key, "value": value}
}

// intPtr 把可空的界打成人话 —— 直接 %v 一个 *int 打出来是指针地址，
// 报错信息里看到 0x1400459f050 完全没用。
func intPtr(v *int) string {
	if v == nil {
		return "（没设）"
	}
	return strconv.Itoa(*v)
}

// TestSettingsTakeEffectImmediately 是这个功能的**验收标准**：改完立即生效。
//
// 不是「接口返回 200」，是「下一次真实登录**按新策略走**」——
// 中间隔着写库、触发器 NOTIFY、缓存刷新三步，任何一步断了这条都会红。
func TestSettingsTakeEffectImmediately(t *testing.T) {
	a := newLiveApp(t)
	session := a.asAdmin(t)

	// 改密接口是真正读密码策略的地方（auth.CheckPasswordStrength），
	// 所以拿它当探针 —— 用「新建用户」验不了，那条路的密码是系统生成的。
	changeTo := func(t *testing.T, oldPw, newPw string) int {
		t.Helper()
		rec := a.call(t, http.MethodPost, "/api/v1/me/password", map[string]string{
			"old_password": oldPw, "new_password": newPw,
		}, session)
		return rec.Code
	}

	// asAdmin 把密码改成了这个（16 位，默认策略下合法）
	const current = "Adm1nPassw0rd2026"
	const twelve = "Passw0rd2026" // 12 位，含大小写和数字

	t.Run("先确认默认策略放行 12 位", func(t *testing.T) {
		if code := changeTo(t, current, twelve); code != http.StatusOK {
			t.Fatalf("默认最短长度是 10，12 位该通过，得到 %d", code)
		}
	})

	// 把最短长度调到 16
	rec := a.call(t, http.MethodPut, "/api/v1/settings",
		saveSettings(item(config.KeyPasswordMinLength, 16)), session)
	if rec.Code != http.StatusOK {
		t.Fatalf("改配置应该 200，得到 %d：%s", rec.Code, rec.Body)
	}
	if got := settingsBody(t, rec.Body.String())[config.KeyPasswordMinLength]; got != float64(16) {
		t.Fatalf("保存后返回的值应该是 16，得到 %v", got)
	}

	t.Run("新策略立刻生效", func(t *testing.T) {
		if code := changeTo(t, twelve, "Passw0rd2027"); code == http.StatusOK {
			t.Fatal("最短长度已经调到 16，12 位的密码不该再被接受 —— " +
				"配置没生效（写库 / NOTIFY / 缓存刷新，三步里断了一步）")
		}
	})

	t.Run("够长的仍然放行", func(t *testing.T) {
		if code := changeTo(t, twelve, "Passw0rd2027Long"); code != http.StatusOK {
			t.Fatalf("16 位应该通过，得到 %d —— 别把策略卡死了", code)
		}
	})
}

// TestSettingsRejectUndeclaredKeys 守白名单 —— 这是整个注册表存在的理由。
//
// 尤其是最后一条：租户拿平台的 `limits.*` 键去写，等于**自己给自己松绑**。
func TestSettingsRejectUndeclaredKeys(t *testing.T) {
	a := newLiveApp(t)
	session := a.asAdmin(t)

	cases := []struct {
		name string
		key  string
	}{
		{"凭空编的 key", "totally.made_up"},
		{"平台级的 key", config.KeyAuditRetentionDays},
		{"平台给租户划的界 —— 租户自己改不得", config.BoundKey(config.KeyPasswordMinLength, false)},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			rec := a.call(t, http.MethodPut, "/api/v1/settings",
				saveSettings(item(c.key, 1)), session)
			if rec.Code == http.StatusOK {
				t.Fatalf("%s 不该写得进去：%s", c.key, rec.Body)
			}
			if code := decodeProblem(t, rec).Code; code != config.ErrUnknownKey.Code {
				t.Errorf("错误码应是 %s，得到 %s", config.ErrUnknownKey.Code, code)
			}
		})
	}
}

// TestSettingsRespectPlatformBounds 守 MULTI-TENANCY.md §10.5 的护栏。
//
// 这条护栏在这个功能接上来之前**没有任何真实调用方** —— 也就是说它写好了
// 但从没被走过。这里是它第一次被真的走一遍。
func TestSettingsRespectPlatformBounds(t *testing.T) {
	a := newLiveApp(t)
	session := a.asAdmin(t)

	// 迁移 00011 给密码最短长度定的是 [10, 64]
	t.Run("低于下限被拒", func(t *testing.T) {
		rec := a.call(t, http.MethodPut, "/api/v1/settings",
			saveSettings(item(config.KeyPasswordMinLength, 1)), session)
		if rec.Code == http.StatusOK {
			t.Fatal("平台下限是 10，不该让租户调到 1 位")
		}
		if !strings.Contains(rec.Body.String(), "下限") {
			t.Errorf("报错应该说清是平台的下限：%s", rec.Body)
		}
	})

	t.Run("区间随响应给出来，前端不用硬编码", func(t *testing.T) {
		rec := a.call(t, http.MethodGet, "/api/v1/settings", nil, session)
		if rec.Code != http.StatusOK {
			t.Fatalf("读配置应该 200，得到 %d：%s", rec.Code, rec.Body)
		}
		var out struct {
			Data struct {
				Groups []struct {
					Items []struct {
						Key string `json:"key"`
						Min *int   `json:"min"`
						Max *int   `json:"max"`
					} `json:"items"`
				} `json:"groups"`
			} `json:"data"`
		}
		if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
			t.Fatal(err)
		}
		for _, g := range out.Data.Groups {
			for _, it := range g.Items {
				if it.Key != config.KeyPasswordMinLength {
					continue
				}
				if it.Min == nil || *it.Min != 10 {
					t.Errorf("密码最短长度的下限应该是 10，得到 %s", intPtr(it.Min))
				}
				if it.Max == nil || *it.Max != 64 {
					t.Errorf("上限应该是 64，得到 %s", intPtr(it.Max))
				}
				return
			}
		}
		t.Fatal("响应里没有密码最短长度这一项")
	})
}

// TestSettingsAreIsolatedPerTenant 是跨租户那条（MULTI-TENANCY.md §3.2 ⑧）。
//
// ⚠️ **两个租户都要有自己的配置**：只给 A 设值的话，「B 没被改到」是因为
// B 本来就没有配置，等于什么都没测。所以这里两边各设一个不同的值，
// 再互相确认对方的没动。
func TestSettingsAreIsolatedPerTenant(t *testing.T) {
	a := newLiveApp(t)
	session := a.asAdmin(t)

	// B 公司：另一个租户，直接从 config 层给它设一份自己的策略
	other := testdb.NewTenant(t, a.pool, 1)
	if err := a.settings.Set(t.Context(), other.ID, config.KeyPasswordMinLength, 20, nil); err != nil {
		t.Fatalf("给 B 公司设配置失败：%v", err)
	}

	// A 公司走接口设成 12
	rec := a.call(t, http.MethodPut, "/api/v1/settings",
		saveSettings(item(config.KeyPasswordMinLength, 12)), session)
	if rec.Code != http.StatusOK {
		t.Fatalf("A 改配置应该 200，得到 %d：%s", rec.Code, rec.Body)
	}

	t.Run("A 看到的是自己的 12", func(t *testing.T) {
		rec := a.call(t, http.MethodGet, "/api/v1/settings", nil, session)
		if got := settingsBody(t, rec.Body.String())[config.KeyPasswordMinLength]; got != float64(12) {
			t.Fatalf("A 应该看到 12，得到 %v", got)
		}
	})

	t.Run("B 的还是 20，没被 A 带跑", func(t *testing.T) {
		if got := a.settings.Security(other.ID).PasswordMinLength; got != 20 {
			t.Fatalf("B 公司的最短长度应该还是 20，得到 %d —— 配置串租户了", got)
		}
	})

	// 没有「A 能不能读到 B 的配置」那条用例：配置接口压根没有按 id 查别人的入口，
	// 租户是从会话取的（MULTI-TENANCY.md §4.2）。写一条 `strings.Contains(body, "20")`
	// 的断言看着像在测隔离，实际永远不会红 —— **测不出东西的测试比没有更糟**。
}

// TestPlatformSettingsNeedPlatformSession 守两边的分界。
//
// 租户会话打平台接口是 **401 而不是 403**（MULTI-TENANCY.md §15.5）：
// 按路径选会话套之后，一边的凭据到了另一边就是「没带」。
// 403 等于告诉对方「你的身份我认出来了」。
func TestPlatformSettingsNeedPlatformSession(t *testing.T) {
	a := newLiveApp(t)
	session := a.asAdmin(t)

	for _, c := range []struct {
		method string
		body   any
	}{
		{http.MethodGet, nil},
		{http.MethodPut, saveSettings(item(config.KeyAuditRetentionDays, 30))},
	} {
		rec := a.call(t, c.method, "/api/v1/platform/settings", c.body, session)
		if rec.Code != http.StatusUnauthorized {
			t.Fatalf("%s 平台设置应该 401（租户凭据到平台就是没带），得到 %d：%s",
				c.method, rec.Code, rec.Body)
		}
	}
}

// TestPlatformCanTightenBounds 是 §10.5 承诺的那句话：
// 「接了合规要求更高的客户，平台整体收紧一档，不用发版」。
//
// 在这个功能接上来之前，那句话只对一半 —— 界只能上数据库改。
func TestPlatformCanTightenBounds(t *testing.T) {
	a := newLiveApp(t)

	username, password := a.bootstrapPlatformAdmin(t)
	platform, rec := a.platformLogin(t, username, password)
	if platform == nil {
		t.Fatalf("平台管理员应该能登录，得到 %d：%s", rec.Code, rec.Body)
	}

	// 平台把密码最短长度的下限从 10 收紧到 14
	minKey := config.BoundKey(config.KeyPasswordMinLength, false)
	rec = a.call(t, http.MethodPut, "/api/v1/platform/settings",
		saveSettings(item(minKey, 14)), platform)
	if rec.Code != http.StatusOK {
		t.Fatalf("平台改上下界应该 200，得到 %d：%s", rec.Code, rec.Body)
	}

	t.Run("租户立刻受新界约束", func(t *testing.T) {
		session := a.asAdmin(t)
		rec := a.call(t, http.MethodPut, "/api/v1/settings",
			saveSettings(item(config.KeyPasswordMinLength, 12)), session)
		if rec.Code == http.StatusOK {
			t.Fatal("平台已经把下限收到 14，租户不该还能设 12")
		}
	})

	t.Run("平台也写不进编错的界", func(t *testing.T) {
		// 少一个字母那条界就静默消失 —— 这正是白名单要堵的洞
		rec := a.call(t, http.MethodPut, "/api/v1/platform/settings",
			saveSettings(item("limits.security.password_min_length.mn", 14)), platform)
		if rec.Code == http.StatusOK {
			t.Fatal("键名敲错了不该写得进去 —— 那条界会静默失效")
		}
		if code := decodeProblem(t, rec).Code; code != config.ErrUnknownKey.Code {
			t.Errorf("错误码应是 %s，得到 %s", config.ErrUnknownKey.Code, code)
		}
	})
}

// TestSettingsRejectWrongTypes 守「写了不生效」那一类静默失败。
//
// 往 int 项写 10.5 的话，`Set` 照样存得进去，而读的时候解不出 int、
// 退回默认值 —— 页面显示保存成功，实际生效的还是老值。
func TestSettingsRejectWrongTypes(t *testing.T) {
	a := newLiveApp(t)
	session := a.asAdmin(t)

	cases := []struct {
		name  string
		key   string
		value any
	}{
		{"整数项写小数", config.KeyPasswordMinLength, 10.5},
		{"整数项写文本", config.KeyPasswordMinLength, "十二"},
		{"开关项写数字", config.KeyPasswordRequireMix, 1},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			rec := a.call(t, http.MethodPut, "/api/v1/settings",
				saveSettings(item(c.key, c.value)), session)
			if rec.Code == http.StatusOK {
				t.Fatalf("类型不对不该写得进去：%s", rec.Body)
			}
			if code := decodeProblem(t, rec).Code; code != errs.ValidationFailed.Code {
				t.Errorf("错误码应是 %s，得到 %s", errs.ValidationFailed.Code, code)
			}
		})
	}
}
