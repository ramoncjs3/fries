package handler

import (
	"context"
	"net/http"

	"github.com/danielgtaylor/huma/v2"

	"github.com/ramoncjs3/fries/internal/audit"
	"github.com/ramoncjs3/fries/internal/httpx"
	"github.com/ramoncjs3/fries/internal/perm"
	"github.com/ramoncjs3/fries/internal/perm/modules"
	settingssvc "github.com/ramoncjs3/fries/internal/service/settings"
)

// Settings 是配置管理接口，租户端和平台端共用一个 handler。
//
// 共用是因为两边的形状完全一样（都是「列出分组 → 改若干项」），
// 差别只在**走哪个 Realm 的路由和权限点** —— 而那是注册路由时定的，
// 不是 handler 里 if 出来的。
type Settings struct {
	svc *settingssvc.Service
}

// NewSettings 造配置管理 handler。
func NewSettings(svc *settingssvc.Service) *Settings { return &Settings{svc: svc} }

// SettingItem 是页面上的一个配置项。
type SettingItem = settingssvc.Item

// SettingGroup 是一组配置项。
type SettingGroup = settingssvc.Group

// SettingsResult 是配置列表的响应体。
type SettingsResult struct {
	Groups []SettingGroup `json:"groups"`
}

// SaveSettingsInput 是保存配置的入参。
type SaveSettingsInput struct {
	Body struct {
		Items []struct {
			Key string `json:"key" minLength:"1" maxLength:"100" doc:"配置项 key，从列表接口拿"`
			// Value 的类型由服务端按注册表判 —— 前端原样把控件的值传回来。
			//
			// ⚠️ 这里**不能**用 huma 的 minimum/maximum 约束：允许范围存在
			// platform_settings 里，是平台管理员随时能调的运行时值，
			// 而 huma 的 tag 是编译期常量（MULTI-TENANCY.md §10.5）。
			Value any `json:"value" doc:"新值。整数项传数字，开关传 true/false，文本传字符串"`
		} `json:"items" minItems:"1" maxItems:"50" doc:"要改的配置项"`
	}
}

// RegisterSettings 注册租户端的配置管理路由。
func RegisterSettings(api huma.API, h *Settings) {
	perm.Guard(api, modules.SettingsSecurity.Point(perm.ActionList), huma.Operation{
		OperationID: "list-settings",
		Method:      http.MethodGet,
		Path:        "/settings",
		Summary:     "查看本组织的设置",
		Description: "返回分组 → 配置项，每项带当前值和**平台允许的取值范围**。" +
			"范围由服务端给，前端不要硬编码 —— 它是平台随时能调的。",
		Tags: []string{modules.SettingsSecurity.Key},
	}, h.list)

	perm.Guard(api, modules.SettingsSecurity.Point(perm.ActionUpdate), huma.Operation{
		OperationID: "save-settings",
		Method:      http.MethodPut,
		Path:        "/settings",
		Summary:     "修改本组织的设置",
		Description: "改完立即生效（写库 → NOTIFY → 各实例刷新缓存）。" +
			"密码策略只影响下次改密，**不影响已有密码**。",
		Tags: []string{modules.SettingsSecurity.Key},
	}, h.save)
}

// RegisterPlatformSettings 注册平台端的配置管理路由。
//
// 路径挂在 PlatformPrefix 下面，所以认证中间件会按路径认平台会话
// （§15.5：按路径选会话套，租户的凭据到了这边就是「没带」→ 401）。
func RegisterPlatformSettings(api huma.API, h *Settings) {
	perm.Guard(api, modules.PlatformSetting.Point(perm.ActionList), huma.Operation{
		OperationID: "list-platform-settings",
		Method:      http.MethodGet,
		Path:        PlatformPrefix + "/settings",
		Summary:     "查看平台设置",
		Description: "平台自己的配置，外加**给各组织划的可调范围**（MULTI-TENANCY.md §10.5）。",
		Tags:        []string{platformTag},
	}, h.listPlatform)

	perm.Guard(api, modules.PlatformSetting.Point(perm.ActionUpdate), huma.Operation{
		OperationID: "save-platform-settings",
		Method:      http.MethodPut,
		Path:        PlatformPrefix + "/settings",
		Summary:     "修改平台设置",
		Description: "改可调范围会立刻约束所有组织的下一次配置修改，但**不会回改**" +
			"它们已经设好的值 —— 那些值下次被改动时才会受新范围约束。",
		Tags: []string{platformTag},
	}, h.savePlatform)
}

func (h *Settings) list(ctx context.Context, _ *struct{}) (*httpx.Response[SettingsResult], error) {
	groups, err := h.svc.List(ctx)
	if err != nil {
		return nil, err
	}
	return httpx.OK(SettingsResult{Groups: groups}), nil
}

func (h *Settings) save(ctx context.Context, in *SaveSettingsInput) (*httpx.Response[SettingsResult], error) {
	if err := h.svc.Save(ctx, updatesOf(ctx, in)); err != nil {
		return nil, err
	}
	// 保存完把最新的一份带回去：值可能被服务端规整过，前端直接换掉本地状态
	groups, err := h.svc.List(ctx)
	if err != nil {
		return nil, err
	}
	return httpx.OK(SettingsResult{Groups: groups}), nil
}

func (h *Settings) listPlatform(ctx context.Context, _ *struct{}) (*httpx.Response[SettingsResult], error) {
	return httpx.OK(SettingsResult{Groups: h.svc.ListPlatform(ctx)}), nil
}

func (h *Settings) savePlatform(ctx context.Context, in *SaveSettingsInput) (*httpx.Response[SettingsResult], error) {
	if err := h.svc.SavePlatform(ctx, updatesOf(ctx, in)); err != nil {
		return nil, err
	}
	return httpx.OK(SettingsResult{Groups: h.svc.ListPlatform(ctx)}), nil
}

// updatesOf 把入参翻成 service 要的形状，顺带把改了哪些 key 记进审计。
//
// ⚠️ **只记 key，不记 value。** 配置值本身不敏感，但这条规矩要保持一致 ——
// 审计的 detail 有字段数和长度上限，而且下一个配置项可能就是敏感的
// （比如将来的 SMTP 密码）。要看改成了什么，去看配置的历史值，不是审计。
func updatesOf(ctx context.Context, in *SaveSettingsInput) []settingssvc.Update {
	out := make([]settingssvc.Update, 0, len(in.Body.Items))
	keys := make([]string, 0, len(in.Body.Items))

	for _, it := range in.Body.Items {
		out = append(out, settingssvc.Update{Key: it.Key, Value: it.Value})
		keys = append(keys, it.Key)
	}
	audit.Detail(ctx, "keys", keys)
	return out
}
