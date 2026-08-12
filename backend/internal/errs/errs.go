// Package errs 是全站错误码注册表。
//
// 这个包**纯 Go，不 import 任何 HTTP 框架** —— service 层只 import 它，
// 把错误码翻译成 HTTP 响应是 internal/httpx 的事（见 DECISIONS.md §1.1、§4.5）。
//
// 用法：
//
//	// 模块的 errors.go 里声明
//	var ErrNotFound = errs.Define("user.not_found", 404, "用户不存在")
//
//	// service 里返回
//	return nil, ErrNotFound                       // 最常见
//	return nil, ErrNotFound.Wrap(dbErr)           // 带内部原因，只进日志
//	return nil, ErrDuplicate.Detailf("用户名 %q 已存在", name)
package errs

import (
	"errors"
	"fmt"
	"regexp"
	"sort"
	"strings"
	"sync"
)

// codePattern 约束错误码格式：`<domain>.<reason>`，全小写下划线分词（DECISIONS.md §4.5）。
var codePattern = regexp.MustCompile(`^[a-z][a-z0-9_]*(\.[a-z][a-z0-9_]*)+$`)

// Code 是一个注册过的错误码。它自己实现 error，可以直接 return。
//
// 同一个 code 在进程内只有一个 *Code 实例，因此可以用 == 比较，
// 也可以用 errors.Is 比较（包装过之后依然成立）。
type Code struct {
	// Code 是机器判断用的稳定标识，如 "user.not_found"。前端 switch 它。
	Code string
	// Status 是对应的 HTTP 状态码。
	Status int
	// Message 是给终端用户看的中文文案。
	Message string
}

// Error 让 *Code 直接当 error 用。
func (c *Code) Error() string { return c.Code + ": " + c.Message }

// StatusCode 让框架层不用认识 errs 包也能拿到状态码（Echo 的 HTTPStatusCoder）。
//
// 注意方法名故意不叫 GetStatus —— 那是 huma.StatusError 的签名，一旦实现，
// huma 就会绕开我们覆盖的 NewError，把错误对象原样当响应体写出去，
// RFC 9457 格式和错误码就都没了。
func (c *Code) StatusCode() int { return c.Status }

// Domain 返回错误码的域，即第一段（common / auth / perm / <module>）。
func (c *Code) Domain() string {
	if i := strings.Index(c.Code, "."); i > 0 {
		return c.Code[:i]
	}
	return c.Code
}

var (
	mu       sync.RWMutex
	registry = map[string]*Code{}
)

// Define 注册一个错误码并返回它。
//
// 重复注册直接 panic —— 宁可启动就失败，也不要两个地方抢同一个 code
// 而前端拿到的文案取决于哪个包先 init（DECISIONS.md §4.5、§4.7）。
func Define(code string, status int, message string) *Code {
	if !codePattern.MatchString(code) {
		panic(fmt.Sprintf("errs: 错误码 %q 格式不合法，要求 <domain>.<reason>，全小写下划线分词", code))
	}
	if status < 400 || status > 599 {
		panic(fmt.Sprintf("errs: 错误码 %q 的 HTTP 状态码 %d 不在 4xx/5xx 范围内", code, status))
	}
	if strings.TrimSpace(message) == "" {
		panic(fmt.Sprintf("errs: 错误码 %q 缺少中文文案", code))
	}

	mu.Lock()
	defer mu.Unlock()
	if old, dup := registry[code]; dup {
		panic(fmt.Sprintf("errs: 错误码 %q 重复注册（已注册为 status=%d message=%q）", code, old.Status, old.Message))
	}
	c := &Code{Code: code, Status: status, Message: message}
	registry[code] = c
	return c
}

// Lookup 按 code 字符串取回注册过的错误码。
func Lookup(code string) (*Code, bool) {
	mu.RLock()
	defer mu.RUnlock()
	c, ok := registry[code]
	return c, ok
}

// All 返回全部已注册错误码，按 code 字典序排序（make errdoc 用它生成文档）。
func All() []*Code {
	mu.RLock()
	out := make([]*Code, 0, len(registry))
	for _, c := range registry {
		out = append(out, c)
	}
	mu.RUnlock()
	sort.Slice(out, func(i, j int) bool { return out[i].Code < out[j].Code })
	return out
}

// Field 是一条字段级错误，对应 RFC 9457 响应里 errors[] 的一项（DECISIONS.md §4.3）。
type Field struct {
	// Location 形如 "body.username" / "query.page"，前端用它定位到表单字段。
	Location string
	// Message 是这个字段的中文提示。
	Message string
}

// Error 是「一个错误码 + 这次具体发生了什么」。
//
// 只在需要携带额外信息时才用，普通场景直接返回 *Code 就够了。
type Error struct {
	// Code 是注册过的错误码，决定 HTTP 状态和默认文案。
	Code *Code
	// Detail 覆盖默认文案，给用户看。留空则用 Code.Message。
	// 注意：5xx 的 Detail 不会返回前端（红线 #5），只进日志。
	Detail string
	// Fields 是字段级错误。
	Fields []Field
	// cause 是内部原因（SQL 报错、上游返回等），**只进日志，永不返回前端**。
	cause error
}

// Error 实现 error 接口。
func (e *Error) Error() string {
	var b strings.Builder
	b.WriteString(e.Code.Code)
	if e.Detail != "" {
		b.WriteString(": ")
		b.WriteString(e.Detail)
	} else {
		b.WriteString(": ")
		b.WriteString(e.Code.Message)
	}
	if e.cause != nil {
		b.WriteString(" <- ")
		b.WriteString(e.cause.Error())
	}
	return b.String()
}

// Unwrap 让 errors.Is / errors.As 能穿到内部原因和错误码。
func (e *Error) Unwrap() []error {
	if e.cause == nil {
		return []error{e.Code}
	}
	return []error{e.Code, e.cause}
}

// Cause 返回内部原因，供日志层使用。
func (e *Error) Cause() error { return e.cause }

// StatusCode 同 (*Code).StatusCode。
func (e *Error) StatusCode() int { return e.Code.Status }

// Wrap 给错误码附上内部原因。内部原因只写日志，不会返回给前端。
func (c *Code) Wrap(cause error) *Error {
	return &Error{Code: c, cause: cause}
}

// Detailf 用自定义文案覆盖默认文案（4xx 才会返回前端）。
func (c *Code) Detailf(format string, a ...any) *Error {
	return &Error{Code: c, Detail: fmt.Sprintf(format, a...)}
}

// WithField 附加一条字段级错误。
func (c *Code) WithField(location, message string) *Error {
	return &Error{Code: c, Fields: []Field{{Location: location, Message: message}}}
}

// Wrap 给已有 *Error 补上内部原因。
func (e *Error) Wrap(cause error) *Error {
	e.cause = cause
	return e
}

// Detailf 覆盖文案。
func (e *Error) Detailf(format string, a ...any) *Error {
	e.Detail = fmt.Sprintf(format, a...)
	return e
}

// WithField 追加一条字段级错误。
func (e *Error) WithField(location, message string) *Error {
	e.Fields = append(e.Fields, Field{Location: location, Message: message})
	return e
}

// From 把任意 error 归一成 *Error。
//
// 传入 *Code、*Error，或包装过它们的 error 都能识别；
// 完全无关的 error 返回 false —— 调用方（httpx）应把它当内部错误处理。
func From(err error) (*Error, bool) {
	if err == nil {
		return nil, false
	}
	var e *Error
	if errors.As(err, &e) {
		return e, true
	}
	var c *Code
	if errors.As(err, &c) {
		return &Error{Code: c}, true
	}
	return nil, false
}
