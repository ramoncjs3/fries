package httpx

import (
	"errors"
	"log/slog"
	"net/http"

	"github.com/danielgtaylor/huma/v2"

	"github.com/ramoncjs3/fries/internal/errs"
)

// ProblemItem 是 RFC 9457 响应里 errors[] 的一项：哪个字段、错在哪。
type ProblemItem struct {
	Location string `json:"location,omitempty" doc:"出错位置，如 body.username / query.page"`
	Message  string `json:"message" doc:"这一项的中文提示"`
	Value    any    `json:"value,omitempty" doc:"原样回显的入参值，便于排查"`
}

// Problem 是全站唯一的错误响应体：RFC 9457 Problem Details + `code` / `request_id`
// 两个扩展成员（RFC 明确允许扩展，见 DECISIONS.md §4.3）。
//
// 前端只读 code（机器判断）和 detail（中文文案）。
type Problem struct {
	Type      string        `json:"type" example:"about:blank" doc:"错误类型的文档地址，当前统一 about:blank"`
	Title     string        `json:"title" example:"Conflict" doc:"错误类型的英文简述"`
	Status    int           `json:"status" example:"409" doc:"HTTP 状态码"`
	Detail    string        `json:"detail,omitempty" example:"用户名已存在" doc:"给用户看的中文文案"`
	Code      string        `json:"code" example:"user.duplicate_username" doc:"机器判断用的错误码，前端 switch 它"`
	RequestID string        `json:"request_id,omitempty" doc:"本次请求的 ID，报障时提供它就能定位日志"`
	Errors    []ProblemItem `json:"errors,omitempty" doc:"字段级错误，前端可自动映射到表单字段"`
}

// Error 实现 error。
func (p *Problem) Error() string { return p.Code + ": " + p.Detail }

// GetStatus 实现 huma.StatusError，让 huma 用它作为响应状态码。
func (p *Problem) GetStatus() int { return p.Status }

// SetRequestID 实现 request_id 注入接口（Transformer 调用）。
func (p *Problem) SetRequestID(id string) { p.RequestID = id }

// ContentType 实现 huma.ContentTypeFilter：错误响应用 application/problem+json。
func (p *Problem) ContentType(ct string) string {
	switch ct {
	case "application/json":
		return "application/problem+json"
	case "application/cbor":
		return "application/problem+cbor"
	}
	return ct
}

// 编译期确认实现了 huma 的两个接口。
var (
	_ huma.StatusError       = (*Problem)(nil)
	_ huma.ContentTypeFilter = (*Problem)(nil)
)

func init() {
	// 覆盖 huma 的错误构造：全站错误响应都变成我们的 Problem（DECISIONS.md §4.3）。
	// huma 生成 OpenAPI 时也会调 NewError(0, "") 来推导错误 schema，所以放在 init 里最稳。
	huma.NewError = func(status int, msg string, causes ...error) huma.StatusError {
		return NewProblem(status, msg, causes...)
	}
	// NewErrorWithContext 多了 ctx，用来把 5xx 的真实原因写进日志（红线 #5）。
	huma.NewErrorWithContext = func(ctx huma.Context, status int, msg string, causes ...error) huma.StatusError {
		p := NewProblem(status, msg, causes...)
		if p.Status >= http.StatusInternalServerError {
			// Operation 在少数早期错误路径上可能是 nil，别在记日志的时候再 panic 一次。
			method, path := "", ""
			if op := ctx.Operation(); op != nil {
				method, path = op.Method, op.Path
			}
			logInternal(RequestID(ctx.Context()), method, path, msg, causes)
		}
		return p
	}
}

// NewProblem 把「状态码 + 消息 + 原始 error」翻译成 Problem。
//
// 规则：
//   - 只要 error 链里有注册过的错误码，就用它的 code / status / 文案；
//   - 否则按状态码兜底选一个通用错误码（errs.ForStatus）；
//   - **5xx 一律只给通用文案，不带任何内部细节**（红线 #5）。
func NewProblem(status int, msg string, causes ...error) *Problem {
	code := resolveCode(status, causes)
	p := &Problem{
		Type:   "about:blank",
		Status: code.Status,
		Title:  http.StatusText(code.Status),
		Code:   code.Code,
		Detail: code.Message,
	}

	if p.Status >= http.StatusInternalServerError {
		// 5xx 到此为止：不回显 detail、不回显字段错误，避免泄露内部实现。
		return p
	}

	for _, cause := range causes {
		if cause == nil {
			continue
		}
		// 业务错误自带的自定义文案与字段错误
		if e, ok := errs.From(cause); ok {
			if e.Detail != "" {
				p.Detail = e.Detail
			}
			for _, f := range e.Fields {
				p.Errors = append(p.Errors, ProblemItem{Location: f.Location, Message: f.Message})
			}
			continue
		}
		// huma 的入参校验错误
		var detailer huma.ErrorDetailer
		if errors.As(cause, &detailer) {
			d := detailer.ErrorDetail()
			p.Errors = append(p.Errors, ProblemItem{
				Location: d.Location,
				Message:  d.Message,
				Value:    d.Value,
			})
			continue
		}
		// 其它 4xx error（多半是框架产生的），用它的文本当字段错误的兜底说明
		if text := cause.Error(); text != "" {
			p.Errors = append(p.Errors, ProblemItem{Message: text})
		}
	}

	if len(p.Errors) == 0 && msg != "" && p.Code == errs.ValidationFailed.Code {
		// huma 自己的解析失败（例如 body 不是合法 JSON）只有一句英文 msg，
		// 放进 errors[] 供排查，用户看到的仍是 detail 里的中文。
		p.Errors = []ProblemItem{{Message: msg}}
	}
	return p
}

// resolveCode 决定这次响应用哪个错误码。
func resolveCode(status int, causes []error) *errs.Code {
	for _, cause := range causes {
		if cause == nil {
			continue
		}
		if e, ok := errs.From(cause); ok {
			return e.Code
		}
	}
	return errs.ForStatus(status)
}

// logInternal 把 5xx 的真实原因写日志：客户端只拿到通用文案 + request_id，
// 堆栈和 SQL 只留在这里（红线 #5）。
func logInternal(requestID, method, path, msg string, causes []error) {
	attrs := []any{
		slog.String("request_id", requestID),
		slog.String("method", method),
		slog.String("path", path),
	}
	if msg != "" {
		attrs = append(attrs, slog.String("msg", msg))
	}
	for i, cause := range causes {
		if cause == nil {
			continue
		}
		attrs = append(attrs, slog.String("cause", cause.Error()))
		if i >= 4 {
			break
		}
	}
	slog.Error("请求处理失败", attrs...)
}
