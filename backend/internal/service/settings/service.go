// Package settings 是配置管理的业务层：把注册表和当前值组装成页面要的样子，
// 再把页面提交的值写回去。
//
// ⚠️ **校验不在这里，在 `config.Settings.Set`。** 那是租户级配置唯一的写入口，
// 拦在那里的话，将来多接一个入口（导入、脚本、AI 工具）也自动受保护
// —— 而拦在这一层的话，那些入口得记得自己校验一遍，「记得」不是机制。
//
// 这一层只做三件事：取值、组装视图、把 config 的错误原样往上抛。
package settings

import (
	"context"
	"encoding/json"

	"github.com/google/uuid"

	"github.com/ramoncjs3/fries/internal/authz"
	"github.com/ramoncjs3/fries/internal/config"
	"github.com/ramoncjs3/fries/internal/perm"
)

// Service 是配置管理服务。
type Service struct {
	settings *config.Settings
}

// New 造配置管理服务。
func New(settings *config.Settings) *Service { return &Service{settings: settings} }

// Item 是页面上的一个配置项：声明 + 当前值 + 允许区间。
//
// 类型名会原样进 OpenAPI 的 schema 名，所以叫 Item 而不是更泛的名字。
type Item struct {
	Key   string `json:"key"`
	Kind  string `json:"kind" doc:"int / bool / string，前端按它选控件"`
	Name  string `json:"name" doc:"中文名"`
	Desc  string `json:"desc" doc:"说明，显示在输入框下面"`
	Unit  string `json:"unit" doc:"单位，空表示没有单位"`
	Value any    `json:"value" doc:"当前值。库里没有这一项时是默认值"`
	// Min / Max 是**平台划的区间**，可空表示这一项不受限。
	//
	// ⚠️ 它们必须由服务端给：区间存在 platform_settings 里，是平台管理员
	// 随时能调的**运行时值**，前端硬编码就等于绕开了那套护栏
	// （MULTI-TENANCY.md §10.5）。
	Min *int `json:"min,omitempty"`
	Max *int `json:"max,omitempty"`
}

// Group 是一组配置项，页面上显示成一段。
type Group struct {
	Key   string `json:"key"`
	Name  string `json:"name" doc:"分组中文名"`
	Items []Item `json:"items"`
}

// Update 是一次配置修改的入参。
type Update struct {
	Key   string
	Value any
}

// List 列出当前租户的配置项（含当前值和允许区间）。
func (s *Service) List(ctx context.Context) ([]Group, error) {
	tenantID, err := authz.MustTenant(ctx)
	if err != nil {
		return nil, err
	}
	values := s.settings.TenantValues(tenantID)

	return s.group(perm.RealmTenant, func(it config.Item) any {
		return decodeOr(values[it.Key], it.Default)
	}), nil
}

// Save 改当前租户的配置项。
//
// **逐项写，不包事务**：`config.Settings.Set` 内部会刷新缓存，
// 包在事务里会让缓存刷到未提交的值。配置项之间没有相互约束，部分成功可以接受
// —— 第一个失败就返回，前面成功的那些已经生效了。
func (s *Service) Save(ctx context.Context, updates []Update) error {
	tenantID, err := authz.MustTenant(ctx)
	if err != nil {
		return err
	}
	actor := actorID(ctx)

	for _, u := range updates {
		if err := s.settings.Set(ctx, tenantID, u.Key, u.Value, actor); err != nil {
			return err
		}
	}
	return nil
}

// ListPlatform 列出平台级配置项，外加平台给租户划的上下界。
func (s *Service) ListPlatform(context.Context) []Group {
	groups := s.group(perm.RealmPlatform, func(it config.Item) any {
		return s.settings.PlatformValue(it.Key, it.Default)
	})
	return append(groups, s.boundsGroup())
}

// SavePlatform 改平台级配置项（含上下界）。
func (s *Service) SavePlatform(ctx context.Context, updates []Update) error {
	actor := actorID(ctx)
	for _, u := range updates {
		if err := s.settings.SetPlatform(ctx, u.Key, u.Value, actor); err != nil {
			return err
		}
	}
	return nil
}

// group 把注册表里某个 Realm 的项按声明顺序分组，值由 valueOf 提供。
func (s *Service) group(realm perm.Realm, valueOf func(config.Item) any) []Group {
	var groups []Group
	index := map[string]int{}

	for _, it := range config.ItemsIn(realm) {
		i, ok := index[it.Group]
		if !ok {
			i = len(groups)
			index[it.Group] = i
			groups = append(groups, Group{Key: it.Group, Name: config.GroupName(it.Group)})
		}
		groups[i].Items = append(groups[i].Items, s.itemOf(it, valueOf(it)))
	}
	return groups
}

// itemOf 把一条声明 + 当前值组装成页面要的样子，顺带带上允许区间。
func (s *Service) itemOf(it config.Item, value any) Item {
	out := Item{
		Key: it.Key, Kind: string(it.Kind), Name: it.Name,
		Desc: it.Desc, Unit: it.Unit, Value: value,
	}
	if !it.Bounded() {
		return out
	}
	if v, ok := s.settings.Bound(it.Key, false); ok {
		out.Min = &v
	}
	if v, ok := s.settings.Bound(it.Key, true); ok {
		out.Max = &v
	}
	return out
}

// boundsGroup 把「平台给租户划的界」组装成一段。
//
// ⚠️ **键名从 `config.BoundKey` 拿，不在这里拼字符串。** 敲错一个字母那条界
// 就静默消失（没有对应行 = 不受限），而页面上看着还像保存成功了。
func (s *Service) boundsGroup() Group {
	g := Group{Key: "limits", Name: "租户可调范围"}

	for _, it := range config.ItemsIn(perm.RealmTenant) {
		if !it.Bounded() {
			continue
		}
		for _, b := range []struct {
			upper bool
			name  string
			desc  string
		}{
			{false, it.Name + "（下限）", "各组织不能把这一项设得比它更小"},
			{true, it.Name + "（上限）", "各组织不能把这一项设得比它更大"},
		} {
			key := config.BoundKey(it.Key, b.upper)
			value, set := s.settings.Bound(it.Key, b.upper)
			g.Items = append(g.Items, Item{
				Key: key, Kind: string(config.KindInt), Name: b.name,
				Desc: b.desc + "。留空表示不限制。", Unit: it.Unit,
				Value: boundValue(value, set),
			})
		}
	}
	return g
}

// boundValue 没配过的界返回 nil，让前端显示成空而不是 0 —— 0 是个合法的界。
func boundValue(v int, set bool) any {
	if !set {
		return nil
	}
	return v
}

// decodeOr 把库里存的 JSON 解成值，解不动就用默认值。
//
// 解不动是**正常路径**：租户一条配置行都没有的时候 raw 就是空的。
func decodeOr(raw json.RawMessage, def any) any {
	if len(raw) == 0 {
		return def
	}
	var v any
	if err := json.Unmarshal(raw, &v); err != nil {
		return def
	}
	return v
}

// actorID 取当前操作者的 id，进 settings.updated_by。
func actorID(ctx context.Context) *uuid.UUID {
	p, ok := authz.PrincipalFrom(ctx)
	if !ok {
		return nil
	}
	id := p.ID
	return &id
}
