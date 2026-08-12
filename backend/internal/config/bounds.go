package config

import (
	"encoding/json"
	"fmt"

	"github.com/ramoncjs3/fries/internal/errs"
)

// 平台给租户级设置划的区间（MULTI-TENANCY.md §10.5）。
//
// # 为什么要有这个
//
// §7.2 把密码策略、登录锁定改成了租户级。于是**租户管理员可以把密码策略调到 1 位、
// 把锁定时间调到 0** —— 他给自己公司挖坑，但出了事是平台的品牌在承担。
// 平台给个区间，租户在区间内自便，这是 SaaS 的常规做法。
//
// # 为什么拦在 Settings.Set 而不是接口层
//
// Set 是租户级配置**唯一的**写入口。拦在这里，将来配置管理页面接上来时自动就是受保护的；
// 拦在 handler 里的话，那个页面得记得自己校验一遍 —— 而「记得」不是机制。
//
// # 区间从哪来
//
// 平台设置里的 `limits.<key>.min` / `.max`（见迁移 00011），不是代码里的常量：
// 接了一个合规要求更高的客户，平台整体收紧一档，不用发版。
// **没有对应 limits 行的 key 不受限** —— 想限就往 platform_settings 加一行。

// checkTenantBound 核对一个租户级配置值是否落在平台划的区间内。
//
// 只管数字：布尔和字符串卡不出区间来（`password_require_mix` 是个开关）。
// 想强制某个开关必须开着，那是另一件事，到时候单独做。
func (s *Settings) checkTenantBound(key string, value any) error {
	got, ok := asInt(value)
	if !ok {
		return nil
	}

	if minimum, ok := s.Bound(key, false); ok && got < minimum {
		return errs.ValidationFailed.WithField("body.value",
			fmt.Sprintf("这一项不能小于 %d（平台的下限）", minimum))
	}
	if maximum, ok := s.Bound(key, true); ok && got > maximum {
		return errs.ValidationFailed.WithField("body.value",
			fmt.Sprintf("这一项不能大于 %d（平台的上限）", maximum))
	}
	return nil
}

// Bound 取某个租户级配置项的下界（upper=false）或上界（upper=true）。
//
// 没配就返回 false —— **那就是不限**。所以键名敲错等于把界关掉，
// 这也是为什么键名必须由 BoundKey 拼、写入口必须走白名单（见 registry.go）。
//
// 导出是给接口用的：前端表单要显示「允许范围」，那个范围只能从服务端拿
// （它是平台管理员随时能调的运行时值，不是编译期常量）。
func (s *Settings) Bound(key string, upper bool) (int, bool) {
	var v int
	if s.decodePlatform(BoundKey(key, upper), &v) {
		return v, true
	}
	return 0, false
}

// asInt 把配置值转成整数。
//
// ⚠️ 得同时认 int 和 float64：Set 的入参是 any，
// 代码里传进来的是 int，而从 JSON 解出来再传进来的是 float64。
// 只认 int 的话，走 HTTP 那条路进来的值会**静默跳过校验** —— 而那恰恰是租户改配置的路。
func asInt(value any) (int, bool) {
	switch v := value.(type) {
	case int:
		return v, true
	case int32:
		return int(v), true
	case int64:
		return int(v), true
	case float64:
		// JSON 里没有整数类型，10 解出来是 10.0。带小数的不是我们要管的整数配置
		if v != float64(int(v)) {
			return 0, false
		}
		return int(v), true
	case json.Number:
		n, err := v.Int64()
		if err != nil {
			return 0, false
		}
		return int(n), true
	default:
		return 0, false
	}
}
