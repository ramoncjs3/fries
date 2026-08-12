package httpx_test

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/danielgtaylor/huma/v2"

	"github.com/ramoncjs3/fries/internal/errs"
	"github.com/ramoncjs3/fries/internal/httpx"
)

func TestHumaNewErrorIsOverridden(t *testing.T) {
	got := huma.NewError(http.StatusNotFound, "没找到")
	if _, ok := got.(*httpx.Problem); !ok {
		t.Fatalf("huma.NewError 应返回 *httpx.Problem，得到 %T", got)
	}
}

func TestProblemUsesRegisteredCode(t *testing.T) {
	dup := errs.Define("testproblem.duplicate", http.StatusConflict, "已存在")

	p := httpx.NewProblem(http.StatusInternalServerError, "whatever", dup.Detailf("用户名 %q 已存在", "bob"))

	if p.Code != dup.Code {
		t.Errorf("code 应取自错误码，得到 %q", p.Code)
	}
	if p.Status != http.StatusConflict {
		t.Errorf("状态码应取自错误码而不是入参，得到 %d", p.Status)
	}
	if p.Detail != `用户名 "bob" 已存在` {
		t.Errorf("detail 应取自 Detailf，得到 %q", p.Detail)
	}
	if p.Type != "about:blank" {
		t.Errorf("type 应为 about:blank，得到 %q", p.Type)
	}
}

func TestProblemHidesInternalDetail(t *testing.T) {
	secret := "pq: password authentication failed for user \"fries\""
	p := httpx.NewProblem(http.StatusInternalServerError, secret, errs.Internal.Detailf("查库炸了：%s", secret))

	blob, err := json.Marshal(p)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(blob), "password") {
		t.Fatalf("5xx 响应泄露了内部细节：%s", blob)
	}
	if p.Detail != errs.Internal.Message {
		t.Errorf("5xx 只能给通用文案，得到 %q", p.Detail)
	}
	if len(p.Errors) != 0 {
		t.Errorf("5xx 不该带字段级错误，得到 %d 条", len(p.Errors))
	}
}

func TestProblemCarriesValidationDetails(t *testing.T) {
	p := httpx.NewProblem(http.StatusUnprocessableEntity, "validation failed",
		&huma.ErrorDetail{Location: "query.times", Message: "expected number <= 5", Value: 99})

	if p.Code != errs.ValidationFailed.Code {
		t.Errorf("huma 的 422 应归到 %s，得到 %s", errs.ValidationFailed.Code, p.Code)
	}
	if p.Status != http.StatusBadRequest {
		t.Errorf("§4.6 把校验失败定为 400，得到 %d", p.Status)
	}
	if len(p.Errors) != 1 || p.Errors[0].Location != "query.times" {
		t.Fatalf("字段级错误没带上：%+v", p.Errors)
	}
}

func TestProblemFallsBackByStatus(t *testing.T) {
	p := httpx.NewProblem(http.StatusNotFound, "")
	if p.Code != errs.NotFound.Code {
		t.Errorf("没有错误码时应按状态码兜底，得到 %s", p.Code)
	}
	if p.Detail != errs.NotFound.Message {
		t.Errorf("兜底文案应来自错误码，得到 %q", p.Detail)
	}
}

func TestProblemContentType(t *testing.T) {
	p := httpx.NewProblem(http.StatusNotFound, "")
	if got := p.ContentType("application/json"); got != "application/problem+json" {
		t.Errorf("错误响应要用 RFC 9457 的 content-type，得到 %s", got)
	}
}

func TestEnvelopeRequestID(t *testing.T) {
	data := httpx.Data[string]{Data: "x"}
	filled, ok := data.WithRequestID("req_1").(httpx.Data[string])
	if !ok {
		t.Fatal("WithRequestID 应返回同类型的副本")
	}
	if filled.RequestID != "req_1" {
		t.Errorf("request_id 没填上：%+v", filled)
	}
	if data.RequestID != "" {
		t.Error("WithRequestID 不该改原值")
	}

	page := httpx.Paged([]int(nil), 1, 20, 0)
	blob, err := json.Marshal(page.Body)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(blob), `"data":[]`) {
		t.Errorf("空列表要序列化成 []，得到 %s", blob)
	}
}
