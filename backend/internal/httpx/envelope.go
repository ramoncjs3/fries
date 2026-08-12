// Package httpx 是 huma / Echo 的胶水层：响应封套、RFC 9457 错误输出、
// request_id 注入。业务代码（service）不 import 这个包，只 import internal/errs。
package httpx

// Pagination 是分页信息。**禁止塞进 data 或响应头**（DECISIONS.md §4.2）。
type Pagination struct {
	Page     int   `json:"page" doc:"当前页码，从 1 开始"`
	PageSize int   `json:"page_size" doc:"每页条数"`
	Total    int64 `json:"total" doc:"符合条件的总条数"`
}

// Data 是单对象成功响应的封套。
type Data[T any] struct {
	Data      T      `json:"data"`
	RequestID string `json:"request_id" doc:"本次请求的 ID，报障时提供它就能定位日志"`
}

// SetRequestID 实现指针版注入接口（Transformer 用）。
func (d *Data[T]) SetRequestID(id string) { d.RequestID = id }

// WithRequestID 实现值版注入接口：返回填好 request_id 的副本。
// huma 的 Transformer 拿到的 body 是值而不是指针，所以两个都得有。
func (d Data[T]) WithRequestID(id string) any { d.RequestID = id; return d }

// Page 是列表成功响应的封套。
type Page[T any] struct {
	Data       []T        `json:"data"`
	Pagination Pagination `json:"pagination"`
	RequestID  string     `json:"request_id" doc:"本次请求的 ID，报障时提供它就能定位日志"`
}

// SetRequestID 实现指针版注入接口。
func (p *Page[T]) SetRequestID(id string) { p.RequestID = id }

// WithRequestID 实现值版注入接口。
func (p Page[T]) WithRequestID(id string) any { p.RequestID = id; return p }

// Response 是 huma handler 的单对象输出类型。
//
//	func getUser(ctx context.Context, in *Input) (*httpx.Response[User], error) {
//	    return httpx.OK(user), nil
//	}
type Response[T any] struct {
	Body Data[T]
}

// PageResponse 是 huma handler 的列表输出类型。
type PageResponse[T any] struct {
	Body Page[T]
}

// OK 包一个单对象响应。request_id 由 Transformer 自动填，handler 不用管。
func OK[T any](v T) *Response[T] {
	return &Response[T]{Body: Data[T]{Data: v}}
}

// Paged 包一个分页响应。items 为 nil 时输出 `[]` 而不是 `null`。
func Paged[T any](items []T, page, pageSize int, total int64) *PageResponse[T] {
	if items == nil {
		items = []T{}
	}
	return &PageResponse[T]{Body: Page[T]{
		Data:       items,
		Pagination: Pagination{Page: page, PageSize: pageSize, Total: total},
	}}
}
