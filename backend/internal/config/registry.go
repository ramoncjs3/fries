package config

import (
	"fmt"
	"net/http"
	"slices"
	"sort"

	"github.com/ramoncjs3/fries/internal/errs"
	"github.com/ramoncjs3/fries/internal/perm"
)

// 配置项注册表 —— 一处声明，产出接口校验、前端表单、以及**白名单**。
//
// # 为什么要有这一层
//
// 直接写一个 5 字段的强类型接口（`PUT /settings/security`）更省事，但在这里有死结：
// 平台给租户划的上下界（MULTI-TENANCY.md §10.5）是**平台管理员随时能调的运行时值**，
// 而 huma 的 `minimum:"10"` 是编译期常量。写死就等于绕开那套护栏。
//
// 既然「服务端能描述有哪些项、什么类型、区间多少」这件事必然要有，
// 就把它做成和 `perm` 一样的一等公民。
//
// # 🔴 它同时是白名单，这一条比元数据要紧
//
// `Set` / `SetPlatform` 原来接受**任意 key 字符串**。配置页一接上来，
// 它们就成了「往配置表里写任意键」的入口。最要紧的后果在平台端：
//
// §10.5 的护栏靠「`platform_settings` 里有没有 `limits.<key>.min` 这一行」，
// 而 bounds.go 里明写着**没有对应行的 key 不受限**。于是键名敲错一个字母 ——
//
//	limits.security.password_min_length.mn   ← 少了个 i
//
// —— 那条下界就**静默消失**了，没有任何报错，页面上看着还像保存成功。
// 租户第二天就能把密码策略调到 1 位。
//
// 所以：**未声明的 key 一律拒绝**，而 `limits.*` 的键名由 BoundKey 拼出来，不由人敲。
//
// # Realm 和 perm 是同一个道理
//
// 租户端的写入口拒绝平台级 key。这和 §3.2 ② 那个「平台权限点混进租户角色页」
// 是完全同一个 bug 形状 —— 一处全局注册表 + 两个世界，就必须标清楚每一项属于谁。

// 分组标识。前端按它把配置项分段显示。
const (
	groupSecurity = "security"
	groupPlatform = "platform"
)

// groupNames 是分组的中文名。
//
// 名字放这里而不是每个 Item 上：**组名属于组，不属于组里的每一项**。
// 挂在 Item 上的话同一个名字要重复写 N 遍，改的时候漏一处就出现两个组名。
var groupNames = map[string]string{
	groupSecurity: "安全策略",
	groupPlatform: "平台",
}

// GroupName 取分组的中文名，没登记就退回分组标识（总比空白强）。
func GroupName(group string) string {
	if name, ok := groupNames[group]; ok {
		return name
	}
	return group
}

// Kind 是配置值的类型。
//
// 只有三种：数字、开关、文本。够用，而且每多一种前端就要多一个控件。
type Kind string

const (
	// KindInt 是整数，受 limits.* 上下界约束。
	KindInt Kind = "int"
	// KindBool 是开关。卡不出区间来，所以不受上下界约束。
	KindBool Kind = "bool"
	// KindString 是文本。同上。
	KindString Kind = "string"
)

// Item 是一个配置项的声明。
type Item struct {
	// Key 是配置键，和 settings / platform_settings 表里存的一致。
	Key string
	// Realm 决定它归谁改：租户级进 settings 表，平台级进 platform_settings。
	Realm perm.Realm
	// Kind 是值的类型。
	Kind Kind
	// Group 是分组标识，前端按它分段。同一组的项显示在一起。
	// 组的中文名在 groupNames 里，不在这里 —— 那是组的属性，不是项的。
	Group string
	// Name 是这一项的中文名，显示在输入框旁边。
	Name string
	// Desc 是说明，显示在输入框下面。**写清楚「改了会怎样」**，
	// 而不是复述字段名 —— 用户看不懂 password_max_age_days，但看得懂「0 表示永不过期」。
	Desc string
	// Unit 是单位（位 / 天 / 次 / 分钟），空表示没有单位。
	Unit string
	// Default 是库里没有这一行时的兜底值，必须和 defaultSecurity() 对齐。
	Default any
}

// Bounded 判断这一项受不受平台上下界约束。
//
// 只有整数卡得出区间。开关和文本要限制的话那是另一件事（比如「这个开关必须开着」），
// 到时候单独做，别硬塞进区间模型。
func (i Item) Bounded() bool { return i.Kind == KindInt }

// items 是全部配置项的声明。
//
// ⚠️ **只声明真正有代码读的项。** DECISIONS.md §5 当年列了一长串（上传大小、
// 分页默认值、导出行数上限、AI 模型与 prompt……），但那些功能本身还不存在。
// 声明出来就是给每个租户一个改了也不生效的旋钮 —— 而这个项目自己写过：
// 「留在 settings 里等于给每个租户一个改了也不生效的配置项，那比不给改更糟」。
//
// 加一项的时候一起做三件事：这里声明、config 里加 Key 常量、代码里真的读它。
// 启动自检会核对前两件（checkRegistry），第三件只能靠 review。
var items = []Item{
	{
		Key: KeyPasswordMinLength, Realm: perm.RealmTenant, Kind: KindInt,
		Group: groupSecurity,
		Name:  "密码最短长度", Unit: "位", Default: 10,
		Desc: "新设置的密码不能短于这个长度。**不影响已有密码** —— 它们存的是哈希，" +
			"系统不知道原文多长，只在下次改密时按新规则校验。",
	},
	{
		Key: KeyPasswordRequireMix, Realm: perm.RealmTenant, Kind: KindBool,
		Group: groupSecurity,
		Name:  "密码必须混合大小写和数字", Default: true,
		Desc: "开启后，新密码必须同时包含大写字母、小写字母和数字。同样只影响下次改密。",
	},
	{
		Key: KeyPasswordMaxAgeDays, Realm: perm.RealmTenant, Kind: KindInt,
		Group: groupSecurity,
		Name:  "密码有效期", Unit: "天", Default: 0,
		Desc: "超过这个天数没改密码，登录后会被要求先改密。**填 0 表示永不过期。**",
	},
	{
		Key: KeyLoginMaxFailures, Realm: perm.RealmTenant, Kind: KindInt,
		Group: groupSecurity,
		Name:  "连续登录失败几次锁定", Unit: "次", Default: 5,
		Desc: "达到次数后账号被临时锁定。锁定时长见下一项。",
	},
	{
		Key: KeyLoginLockMinutes, Realm: perm.RealmTenant, Kind: KindInt,
		Group: groupSecurity,
		Name:  "锁定时长", Unit: "分钟", Default: 15,
		Desc: "账号被锁定后要等多久才能再试。管理员重置密码会立刻解锁。",
	},

	// —— 平台级 ——
	//
	// 这两项为什么不是租户级（MULTI-TENANCY.md §15.1）：审计按月分区、过期整分区 DROP，
	// 分区本来就跨租户，没法「A 公司留 180 天、B 公司留 30 天」；产品名同理，
	// 客户自己的名字在 tenants.name 里。
	{
		Key: KeyAuditRetentionDays, Realm: perm.RealmPlatform, Kind: KindInt,
		Group: groupPlatform,
		Name:  "审计日志保留期", Unit: "天", Default: 180,
		Desc: "超过保留期的审计日志会被整分区删除。**对所有组织生效** —— " +
			"审计按月分区，分区是跨组织的，没法按组织分别设置。",
	},
	{
		Key: KeySystemName, Realm: perm.RealmPlatform, Kind: KindString,
		Group: groupPlatform,
		Name:  "产品名称", Default: "fries",
		Desc: "显示在浏览器标题和登录页上。**不是客户的组织名** —— 那个在组织管理里改。",
	},
	{
		Key: KeyAllowSelfRegistration, Realm: perm.RealmPlatform, Kind: KindBool,
		Group: groupPlatform,
		Name:  "允许自助注册", Default: false,
		Desc: "打开后，陌生人可以自己注册开一个组织（需邮箱验证）。**默认关**：" +
			"开着意味着任何人都能建租户，先想清楚滥用防护（MULTI-TENANCY.md §9.2）。",
	},
}

// byKey 是 items 的索引，进程启动时建好。
var byKey = func() map[string]Item {
	m := make(map[string]Item, len(items))
	for _, it := range items {
		m[it.Key] = it
	}
	return m
}()

// ItemsIn 按 Realm 列出配置项，顺序和声明顺序一致（前端直接按这个顺序渲染）。
func ItemsIn(realm perm.Realm) []Item {
	out := make([]Item, 0, len(items))
	for _, it := range items {
		if it.Realm == realm {
			out = append(out, it)
		}
	}
	return out
}

// defaultOf 取注册表里声明的兜底值。
//
// 类型对不上时返回 Go 零值 —— 但那种情况过不了 checkRegistry，服务根本起不来。
func defaultOf[T any](key string) T {
	var zero T
	it, ok := byKey[key]
	if !ok {
		return zero
	}
	v, ok := it.Default.(T)
	if !ok {
		return zero
	}
	return v
}

// 上下界设置的 key 由这两个后缀拼出来（迁移 00011 里那 6 行就是这个格式）。
const (
	boundPrefix    = "limits."
	boundSuffixMin = ".min"
	boundSuffixMax = ".max"
)

// BoundKey 拼出某个配置项的上界/下界设置键。
//
// ⚠️ **平台端改上下界时必须走它，不许手敲字符串。** 敲错一个字母那条界就静默消失
// （见本文件开头）—— 而这正是这个注册表要堵的洞。
func BoundKey(key string, upper bool) string {
	if upper {
		return boundPrefix + key + boundSuffixMax
	}
	return boundPrefix + key + boundSuffixMin
}

// BoundKeys 列出全部合法的上下界键，给平台端的写入口做白名单。
func BoundKeys() []string {
	out := make([]string, 0, len(items)*2)
	for _, it := range items {
		if it.Realm != perm.RealmTenant || !it.Bounded() {
			continue
		}
		out = append(out, BoundKey(it.Key, false), BoundKey(it.Key, true))
	}
	sort.Strings(out)
	return out
}

// ErrUnknownKey 是「这个配置项不存在」。
//
// ⚠️ **key 不存在和 key 属于另一个世界，返回的是同一个错误。**
// 分开报的话，租户管理员就能拿它当探针问出「平台有哪些配置项」——
// 和 MULTI-TENANCY.md §11.2 那条「跨租户访问要表现成不存在，而不是无权限」同一个道理。
var ErrUnknownKey = errs.Define("settings.unknown_key", http.StatusBadRequest,
	"没有这个配置项")

// checkKind 核对值的类型和声明的 Kind 对得上。
//
// 不查的话会**静默丢值**：往 int 项写 10.5，`Set` 照样存进去，
// 而读的时候 `Int()` 解不出 int、退回默认值 —— 页面上显示保存成功，
// 实际生效的还是老值，而且没有任何报错。这类「写了不生效」最难查。
//
// ⚠️ JSON 里没有整数类型，走 HTTP 进来的 10 是 float64 —— 这是
// MULTI-TENANCY.md §15.7 那个教训的同一处：只认 int 的话，
// **恰恰是页面那条路会绕过校验**。
func checkKind(key string, value any) error {
	kind := KindInt // limits.* 都是整数
	if it, ok := byKey[key]; ok {
		kind = it.Kind
	}

	switch kind {
	case KindInt:
		if _, ok := asInt(value); !ok {
			return errs.ValidationFailed.WithField("body.value", "这一项要填整数")
		}
	case KindBool:
		if _, ok := value.(bool); !ok {
			return errs.ValidationFailed.WithField("body.value", "这一项只能是开或关")
		}
	case KindString:
		if _, ok := value.(string); !ok {
			return errs.ValidationFailed.WithField("body.value", "这一项要填文本")
		}
	}
	return nil
}

// writable 判断某个 key 能不能通过对应 Realm 的写入口写进去。
//
// 平台级入口额外接受 limits.*（它们是平台给租户划的界，本身就是平台级配置）。
func writable(key string, realm perm.Realm) bool {
	if it, ok := byKey[key]; ok {
		return it.Realm == realm
	}
	return realm == perm.RealmPlatform && slices.Contains(BoundKeys(), key)
}

// CheckRegistry 核对注册表内部一致性，`server --selfcheck` 会跑它。
//
// 查的是「声明里有没有自相矛盾的地方」，纯内存、不碰数据库 —— 所以它能进自检
// （DECISIONS.md §3.7：自检不连库）。
//
// **「这一项真的有代码在读吗」查不了**，只能靠 review 和「加项时三件事一起做」
// 那条约定（见 items 上面的注释）。
func CheckRegistry() error {
	seen := make(map[string]bool, len(items))
	for _, it := range items {
		switch {
		case it.Key == "":
			return fmt.Errorf("有配置项没写 Key")
		case seen[it.Key]:
			return fmt.Errorf("配置项 %s 声明了两次", it.Key)
		case !it.Realm.Valid():
			return fmt.Errorf("配置项 %s 的 Realm 不合法：%q", it.Key, it.Realm)
		case it.Name == "" || it.Desc == "":
			return fmt.Errorf("配置项 %s 缺中文名或说明 —— 它们会直接显示给用户", it.Key)
		case it.Group == "":
			return fmt.Errorf("配置项 %s 缺分组", it.Key)
		case groupNames[it.Group] == "":
			return fmt.Errorf("配置项 %s 的分组 %q 没有中文名（groupNames 里加一条）", it.Key, it.Group)
		case it.Default == nil:
			return fmt.Errorf("配置项 %s 没有默认值 —— 库里读不到时就没有兜底了", it.Key)
		}
		if err := checkDefaultKind(it); err != nil {
			return err
		}
		seen[it.Key] = true
	}
	return nil
}

// checkDefaultKind 核对默认值的 Go 类型和声明的 Kind 对得上。
//
// 对不上的话，前端会按 Kind 渲染控件、而拿到一个类型不符的值，
// 表现是「输入框里空的，但保存时说格式不对」。
func checkDefaultKind(it Item) error {
	var ok bool
	switch it.Kind {
	case KindInt:
		_, ok = it.Default.(int)
	case KindBool:
		_, ok = it.Default.(bool)
	case KindString:
		_, ok = it.Default.(string)
	default:
		return fmt.Errorf("配置项 %s 的 Kind 不认识：%q", it.Key, it.Kind)
	}
	if !ok {
		return fmt.Errorf("配置项 %s 声明成 %s，默认值却是 %T", it.Key, it.Kind, it.Default)
	}
	return nil
}
