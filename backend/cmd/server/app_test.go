package main

import (
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/labstack/echo/v5"

	"github.com/ramoncjs3/fries/internal/config"
	"github.com/ramoncjs3/fries/internal/errs"
	"github.com/ramoncjs3/fries/internal/httpx"
	"github.com/ramoncjs3/fries/internal/middleware"
)

// newTestApp 装配一个不连库的应用 —— 这一步能跑，就说明 --selfcheck 那条路也通。
func newTestApp(t *testing.T) *app {
	t.Helper()
	cfg, err := config.Load("../../../config/config.example.yaml")
	if err != nil {
		t.Fatalf("加载样例配置失败：%v", err)
	}
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	a, err := newApp(t.Context(), cfg, logger, nil, "test")
	if err != nil {
		t.Fatalf("装配应用失败：%v", err)
	}
	return a
}

func do(t *testing.T, a *app, method, path string, headers map[string]string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequestWithContext(t.Context(), method, path, nil)
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	rec := httptest.NewRecorder()
	a.echo.ServeHTTP(rec, req)
	return rec
}

func TestPingReturnsEnvelopeWithRequestID(t *testing.T) {
	a := newTestApp(t)
	rec := do(t, a, http.MethodGet, "/api/v1/ping?echo=hi&times=2", nil)

	if rec.Code != http.StatusOK {
		t.Fatalf("期望 200，得到 %d：%s", rec.Code, rec.Body)
	}

	var body struct {
		Data struct {
			Message string `json:"message"`
			Times   int    `json:"times"`
		} `json:"data"`
		RequestID string `json:"request_id"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("响应不是合法 JSON：%v，body=%s", err, rec.Body)
	}
	if body.Data.Message != "hi" || body.Data.Times != 2 {
		t.Errorf("data 不对：%+v", body.Data)
	}
	if body.RequestID == "" {
		t.Error("响应体里没有 request_id —— Transformer 没生效")
	}
	// 封套的字段必须和 §4.2 完全一致：huma 默认会多塞一个 $schema，我们关掉了。
	if strings.Contains(rec.Body.String(), "$schema") {
		t.Errorf("响应体里多了 $schema，和 §4.2 的封套对不上：%s", rec.Body)
	}
	if got := rec.Header().Get(httpx.HeaderRequestID); got == "" {
		t.Error("响应头里没有 X-Request-Id")
	} else if got != body.RequestID {
		t.Errorf("响应头和响应体的 request_id 对不上：%s vs %s", got, body.RequestID)
	}
}

func TestValidationErrorIsRFC9457(t *testing.T) {
	a := newTestApp(t)
	rec := do(t, a, http.MethodGet, "/api/v1/ping?times=99", nil)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("校验失败应是 400（§4.6），得到 %d：%s", rec.Code, rec.Body)
	}
	if ct := rec.Header().Get("Content-Type"); !strings.Contains(ct, "application/problem+json") {
		t.Errorf("错误响应的 content-type 应是 problem+json，得到 %q", ct)
	}

	var p httpx.Problem
	if err := json.Unmarshal(rec.Body.Bytes(), &p); err != nil {
		t.Fatalf("错误响应不是合法 JSON：%v，body=%s", err, rec.Body)
	}
	if p.Code != errs.ValidationFailed.Code {
		t.Errorf("错误码应是 %s，得到 %s", errs.ValidationFailed.Code, p.Code)
	}
	if p.Status != http.StatusBadRequest || p.Type != "about:blank" || p.Title == "" {
		t.Errorf("RFC 9457 的必备字段不全：%+v", p)
	}
	if p.RequestID == "" {
		t.Error("错误响应里没有 request_id")
	}
	if len(p.Errors) == 0 {
		t.Error("校验失败应该告诉前端是哪个字段错了")
	}
}

func TestUnknownRouteAlsoRFC9457(t *testing.T) {
	a := newTestApp(t)
	rec := do(t, a, http.MethodGet, "/api/v1/nope", nil)

	if rec.Code != http.StatusNotFound {
		t.Fatalf("期望 404，得到 %d", rec.Code)
	}
	var p httpx.Problem
	if err := json.Unmarshal(rec.Body.Bytes(), &p); err != nil {
		t.Fatalf("Echo 的 404 也必须是同一套错误格式：%v，body=%s", err, rec.Body)
	}
	if p.Code != errs.NotFound.Code {
		t.Errorf("错误码应是 %s，得到 %s", errs.NotFound.Code, p.Code)
	}
	if p.RequestID == "" {
		t.Error("404 也要带 request_id")
	}
}

func TestIncomingRequestIDIsReused(t *testing.T) {
	a := newTestApp(t)
	rec := do(t, a, http.MethodGet, "/api/v1/ping", map[string]string{
		httpx.HeaderRequestID: "req_from_nginx",
	})
	if got := rec.Header().Get(httpx.HeaderRequestID); got != "req_from_nginx" {
		t.Errorf("上游传进来的 request_id 应沿用，得到 %q", got)
	}
}

func TestIncomingRequestIDIsSanitized(t *testing.T) {
	a := newTestApp(t)
	rec := do(t, a, http.MethodGet, "/api/v1/ping", map[string]string{
		httpx.HeaderRequestID: "req_<script>",
	})
	if got := rec.Header().Get(httpx.HeaderRequestID); strings.ContainsAny(got, "<>") {
		t.Errorf("外部传进来的 request_id 必须清洗，得到 %q", got)
	}
}

func TestProtectedRoutesRequireLogin(t *testing.T) {
	a := newTestApp(t)

	cases := []struct {
		name   string
		method string
		path   string
	}{
		{"当前用户", http.MethodGet, "/api/v1/me"},
		{"审计查询", http.MethodGet, "/api/v1/audit-logs"},
		{"登出", http.MethodPost, "/api/v1/auth/logout"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			rec := do(t, a, c.method, c.path, nil)
			if rec.Code != http.StatusUnauthorized {
				t.Fatalf("没登录访问 %s 应该 401，得到 %d：%s", c.path, rec.Code, rec.Body)
			}
			var p httpx.Problem
			if err := json.Unmarshal(rec.Body.Bytes(), &p); err != nil {
				t.Fatalf("响应不是合法 JSON：%v", err)
			}
			if p.Code != errs.Unauthenticated.Code {
				t.Errorf("错误码应是 %s，得到 %s", errs.Unauthenticated.Code, p.Code)
			}
		})
	}
}

func TestLoginRouteIsPublic(t *testing.T) {
	a := newTestApp(t)
	// 自检用的应用不连库，登录必然失败；这里只验「没被 401 挡在门外」。
	rec := do(t, a, http.MethodPost, "/api/v1/auth/login", nil)
	if rec.Code == http.StatusUnauthorized {
		t.Fatalf("登录接口不该要求先登录：%s", rec.Body)
	}
}

func TestBodyLimitRejectsOversizedRequest(t *testing.T) {
	a := newTestApp(t)
	a.echo.POST("/test-body", func(c *echo.Context) error {
		return c.NoContent(http.StatusNoContent)
	})

	oversized := strings.Repeat("x", int(a.cfg.Server.MaxBodyBytes)+1)
	req := httptest.NewRequestWithContext(t.Context(), http.MethodPost, "/test-body", strings.NewReader(oversized))
	rec := httptest.NewRecorder()
	a.echo.ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("超过大小上限的请求应被拒，得到 %d", rec.Code)
	}
	var p httpx.Problem
	if err := json.Unmarshal(rec.Body.Bytes(), &p); err != nil {
		t.Fatalf("响应不是合法 JSON：%v", err)
	}
	if p.Code != errs.ValidationFailed.Code {
		t.Errorf("错误码应是 %s，得到 %s", errs.ValidationFailed.Code, p.Code)
	}
}

func TestRequestContextCarriesDeadline(t *testing.T) {
	a := newTestApp(t)
	var deadlineOK bool
	a.echo.GET("/test-deadline", func(c *echo.Context) error {
		_, deadlineOK = c.Request().Context().Deadline()
		return c.NoContent(http.StatusNoContent)
	})

	if rec := do(t, a, http.MethodGet, "/test-deadline", nil); rec.Code != http.StatusNoContent {
		t.Fatalf("期望 204，得到 %d", rec.Code)
	}
	if !deadlineOK {
		t.Error("handler 拿到的 context 没有 deadline —— 超时中间件没生效，慢查询会一直挂着")
	}
}

func TestPanicIsContained(t *testing.T) {
	a := newTestApp(t)
	a.echo.GET("/test-panic", func(_ *echo.Context) error {
		panic("故意炸的，密码是 hunter2")
	})

	rec := do(t, a, http.MethodGet, "/test-panic", nil)
	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("panic 应变成 500，得到 %d", rec.Code)
	}
	if strings.Contains(rec.Body.String(), "hunter2") {
		t.Fatalf("panic 的内容泄露到了响应里：%s", rec.Body)
	}
	var p httpx.Problem
	if err := json.Unmarshal(rec.Body.Bytes(), &p); err != nil {
		t.Fatalf("响应不是合法 JSON：%v", err)
	}
	if p.Code != errs.Internal.Code || p.Detail != errs.Internal.Message {
		t.Errorf("500 只能给通用文案，得到 code=%s detail=%q", p.Code, p.Detail)
	}
	if p.RequestID == "" {
		t.Error("500 也要带 request_id，否则没法定位日志")
	}

	// panic 的那次请求也得进指标 —— Recover 必须在 Metrics 里侧。
	metrics := do(t, a, http.MethodGet, opsPaths.Metrics, nil)
	if !strings.Contains(metrics.Body.String(), `route="/test-panic",status="500"`) {
		t.Error("panic 的请求没被记进指标")
	}
}

func TestHealthzDoesNotNeedDatabase(t *testing.T) {
	a := newTestApp(t)
	rec := do(t, a, http.MethodGet, opsPaths.Health, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("存活探针不该依赖数据库，得到 %d：%s", rec.Code, rec.Body)
	}
}

func TestReadyzFailsWithoutDatabase(t *testing.T) {
	a := newTestApp(t)
	rec := do(t, a, http.MethodGet, opsPaths.Ready, nil)
	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("没有数据库时就绪探针应返回 503，得到 %d：%s", rec.Code, rec.Body)
	}
}

func TestMetricsExposed(t *testing.T) {
	a := newTestApp(t)
	do(t, a, http.MethodGet, "/api/v1/ping", nil)

	rec := do(t, a, http.MethodGet, opsPaths.Metrics, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("/metrics 应返回 200，得到 %d", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), "http_requests_total") {
		t.Error("/metrics 里没有 http_requests_total")
	}
}

func TestIdempotencyKeyRejectsReplay(t *testing.T) {
	a := newTestApp(t)
	// 挂一条只在测试里存在的写接口，专门验中间件行为。
	a.echo.POST("/test-write", func(c *echo.Context) error {
		return c.JSON(http.StatusOK, map[string]string{"ok": "1"})
	})
	a.echo.POST("/test-fail", func(_ *echo.Context) error {
		return errs.ValidationFailed
	})

	headers := map[string]string{middleware.HeaderIdempotencyKey: "same-key"}

	if rec := do(t, a, http.MethodPost, "/test-write", headers); rec.Code != http.StatusOK {
		t.Fatalf("第一次写请求应成功，得到 %d：%s", rec.Code, rec.Body)
	}

	replay := do(t, a, http.MethodPost, "/test-write", headers)
	if replay.Code != http.StatusConflict {
		t.Fatalf("重放同一个幂等键应返回 409，得到 %d：%s", replay.Code, replay.Body)
	}
	var p httpx.Problem
	if err := json.Unmarshal(replay.Body.Bytes(), &p); err != nil {
		t.Fatalf("响应不是合法 JSON：%v", err)
	}
	if p.Code != errs.IdempotencyConflict.Code {
		t.Errorf("错误码应是 %s，得到 %s", errs.IdempotencyConflict.Code, p.Code)
	}

	// 失败的请求要放掉键：改对了参数还得能用同一个键重试。
	failHeaders := map[string]string{middleware.HeaderIdempotencyKey: "retry-key"}
	if rec := do(t, a, http.MethodPost, "/test-fail", failHeaders); rec.Code != http.StatusBadRequest {
		t.Fatalf("期望 400，得到 %d：%s", rec.Code, rec.Body)
	}
	if rec := do(t, a, http.MethodPost, "/test-fail", failHeaders); rec.Code != http.StatusBadRequest {
		t.Fatalf("失败后同一个键应还能用，得到 %d：%s", rec.Code, rec.Body)
	}
}

func TestNoIdempotencyKeyIsFine(t *testing.T) {
	a := newTestApp(t)
	a.echo.POST("/test-open", func(c *echo.Context) error {
		return c.NoContent(http.StatusNoContent)
	})

	for i := range 2 {
		if rec := do(t, a, http.MethodPost, "/test-open", nil); rec.Code != http.StatusNoContent {
			t.Fatalf("第 %d 次请求：不带幂等键不该被拦，得到 %d", i+1, rec.Code)
		}
	}
}
