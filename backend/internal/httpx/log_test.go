package httpx_test

import (
	"bytes"
	"context"
	"encoding/json"
	"log/slog"
	"strings"
	"testing"

	"github.com/google/uuid"

	"github.com/ramoncjs3/fries/internal/httpx"
)

// 客户报障时给的是「我们公司昨天下午打不开」—— 没有 tenant_id 就只能翻全量日志（§12.1）。
func TestLogHandlerAddsRequestContext(t *testing.T) {
	tenant := uuid.New()

	cases := []struct {
		name       string
		ctx        func() context.Context
		wantTenant string
		wantReq    string
	}{
		{
			name: "请求上下文齐全",
			ctx: func() context.Context {
				return httpx.WithTenant(httpx.WithRequestID(t.Context(), "req_abc"), tenant)
			},
			wantTenant: tenant.String(),
			wantReq:    "req_abc",
		},
		{
			// 平台管理员不属于任何组织（§2.3），后台任务也没有请求上下文 ——
			// 这时**不该**塞两个空字段进去，只会让每行日志变长
			name:       "什么都没有就什么都不加",
			ctx:        func() context.Context { return t.Context() },
			wantTenant: "",
			wantReq:    "",
		},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			var buf bytes.Buffer
			logger := slog.New(httpx.NewLogHandler(slog.NewJSONHandler(&buf, nil)))
			logger.InfoContext(c.ctx(), "干了点什么")

			var got map[string]any
			if err := json.Unmarshal(buf.Bytes(), &got); err != nil {
				t.Fatalf("日志不是合法 JSON：%v，%s", err, buf.String())
			}
			if tenantID, _ := got["tenant_id"].(string); tenantID != c.wantTenant {
				t.Errorf("tenant_id 应该是 %q，得到 %q", c.wantTenant, tenantID)
			}
			if reqID, _ := got["request_id"].(string); reqID != c.wantReq {
				t.Errorf("request_id 应该是 %q，得到 %q", c.wantReq, reqID)
			}
		})
	}
}

// `logger.With(...)` 是长期运行的组件（任务、审计）最爱用的写法。
// WithAttrs 不重新包一层的话，那些组件打出来的日志会**全部**丢掉租户。
func TestLogHandlerSurvivesWith(t *testing.T) {
	tenant := uuid.New()
	var buf bytes.Buffer

	logger := slog.New(httpx.NewLogHandler(slog.NewJSONHandler(&buf, nil))).
		With(slog.String("module", "audit")).
		WithGroup("detail")
	logger.InfoContext(httpx.WithTenant(t.Context(), tenant), "干了点什么")

	out := buf.String()
	if !strings.Contains(out, tenant.String()) {
		t.Errorf("With/WithGroup 之后应该还带着租户，得到 %s", out)
	}
	if !strings.Contains(out, `"module":"audit"`) {
		t.Errorf("内层 handler 自己的属性也该在，得到 %s", out)
	}
}
