package httpx

import (
	"github.com/danielgtaylor/huma/v2"
)

// 注入 request_id 的两个接口。**用接口断言，不用反射** —— 反射跑在每个响应上，
// 既慢又脆（DECISIONS.md §4.4）。
type (
	// requestIDSetter 由指针类型实现（*Problem）。
	requestIDSetter interface{ SetRequestID(string) }
	// requestIDWither 由值类型实现（Data[T] / Page[T]）：返回填好的副本。
	requestIDWither interface{ WithRequestID(string) any }
)

// RequestIDTransformer 在序列化之前给响应体填上 request_id。
//
// 注册一次，所有响应（成功和失败）自动带 request_id，handler 一行不用写。
func RequestIDTransformer(ctx huma.Context, _ string, v any) (any, error) {
	id := RequestID(ctx.Context())
	if id == "" || v == nil {
		return v, nil
	}
	switch t := v.(type) {
	case requestIDSetter:
		t.SetRequestID(id)
		return v, nil
	case requestIDWither:
		return t.WithRequestID(id), nil
	}
	return v, nil
}

// NewConfig 造一个带项目约定的 huma 配置。
//
// apiPrefix 只写进 OpenAPI 的 servers —— 实际挂载靠 Echo group（humaecho.NewWithGroup），
// 所以这里的 DocsPath / OpenAPIPath 都是**相对 group 的路径**，别再自己拼前缀。
//
// exposeDocs 决定挂不挂 /docs、/openapi、/schemas 这三个交互文档端点。它们**不经过授权中间件**，
// 开着等于把整份接口清单（含平台管理端）摊给未登录的人（MULTI-TENANCY.md §9.2），所以生产默认关。
// 路径留空 huma 就不注册对应端点。关掉不影响 OpenAPI 的离线导出（api.OpenAPI() 不看这几个路径）。
func NewConfig(title, version, apiPrefix string, exposeDocs bool) huma.Config {
	// 数组默认不可为空。huma 默认把 Go 切片标成 `["array","null"]`，
	// 生成的 TS 类型就是 `T[] | null`，每个用它的地方都要写一次 `?? []`。
	// 我们的约定是**列表一律返回 []，不返回 null**（httpx.Paged 就是这么做的），
	// schema 得跟约定一致。
	huma.DefaultArrayNullable = false

	cfg := huma.DefaultConfig(title, version)
	if exposeDocs {
		cfg.DocsPath = "/docs"
		cfg.OpenAPIPath = "/openapi"
		cfg.SchemasPath = "/schemas"
	}
	cfg.Servers = []*huma.Server{{URL: apiPrefix, Description: "当前服务"}}

	// huma 默认会往每个响应体里塞一个 `$schema` 字段、并打 Link 响应头。
	// 那和 §4.2 定死的封套对不上，生成的 TS 类型也会多出一个字段，所以关掉。
	// 注意**两处都要清**：CreateHooks 在 NewAPI 里才跑，只清 Transformers 会被它加回来。
	cfg.CreateHooks = nil
	cfg.Transformers = []huma.Transformer{RequestIDTransformer}
	return cfg
}
