package middleware_test

import (
	"context"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"

	"github.com/google/uuid"
	"github.com/labstack/echo/v5"

	"github.com/ramoncjs3/fries/internal/audit"
	"github.com/ramoncjs3/fries/internal/authz"
	"github.com/ramoncjs3/fries/internal/middleware"
)

// TestAuditRecordsPlatformActorType 是「平台管理员的动作要认得出来」的守门测试
// （MULTI-TENANCY.md §9.2）。
//
// 中间件曾经只分「service / 其它」两档，于是开组织、停组织这些**最高权限的动作**
// 全被记成普通 user。动作确实进了审计，但标签是错的 —— 事后只能靠
// 「tenant_id 是 NULL 而 actor_id 不是」反推，而 §9.2 要的是能直接查。
//
// 顺带把三种主体一起钉住：加第四种主体时漏了翻译，会退化成「记成 user」这种
// **看不出来**的错，不是编译错误。
func TestAuditRecordsPlatformActorType(t *testing.T) {
	tenantID := uuid.New()

	cases := []struct {
		name      string
		principal *authz.Principal
		wantActor string
		wantOwner *uuid.UUID
	}{
		{
			name: "平台管理员",
			principal: &authz.Principal{
				Type: authz.PrincipalPlatform, ID: uuid.New(), Name: "platform-admin",
			},
			wantActor: audit.ActorPlatform,
			// 平台管理员不属于任何组织，这条记在平台级那批（tenant_id 为 NULL），
			// 客户在自己的审计里看不到 —— 也不该看到。
			wantOwner: nil,
		},
		{
			name: "租户里的人",
			principal: &authz.Principal{
				Type: authz.PrincipalUser, ID: uuid.New(), TenantID: tenantID, Name: "zhangsan",
			},
			wantActor: audit.ActorUser,
			wantOwner: &tenantID,
		},
		{
			name: "Service Account",
			principal: &authz.Principal{
				Type: authz.PrincipalService, ID: uuid.New(), TenantID: tenantID, Name: "erp-sync",
			},
			wantActor: audit.ActorService,
			wantOwner: &tenantID,
		},
		{
			name:      "没认出主体",
			principal: nil,
			wantActor: audit.ActorAnonymous,
			wantOwner: nil,
		},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			sink := &fakeAuditSink{}
			e := echo.New()
			// Audit 在外层，注入主体的在内层 —— 和真实链路一样：
			// 审计中间件先起手，认证中间件在它内侧把主体塞进 context。
			e.Use(middleware.Audit(sink, slog.New(slog.NewTextHandler(io.Discard, nil)), nil))
			e.Use(injectPrincipal(c.principal))
			e.Any("/*", func(c *echo.Context) error { return c.String(http.StatusOK, "ok") })

			req := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/api/v1/ping", nil)
			e.ServeHTTP(httptest.NewRecorder(), req)

			rec, ok := sink.last()
			if !ok {
				t.Fatal("这次请求没有写审计")
			}
			if rec.ActorType != c.wantActor {
				t.Errorf("actor_type 应该是 %q，得到 %q", c.wantActor, rec.ActorType)
			}
			switch {
			case c.wantOwner == nil && rec.TenantID != nil:
				t.Errorf("这条该记成平台级（tenant_id 为空），得到 %s", *rec.TenantID)
			case c.wantOwner != nil && (rec.TenantID == nil || *rec.TenantID != *c.wantOwner):
				t.Errorf("这条该归属组织 %s，得到 %v", *c.wantOwner, rec.TenantID)
			}
		})
	}
}

// injectPrincipal 假装认证中间件：把一个固定的主体塞进 context。
//
// 用它而不是把真的认证链搭起来 —— 这些用例要验的是「拿到主体之后怎么处理」，
// 认证本身有 authenticate_test.go 管。
func injectPrincipal(p *authz.Principal) echo.MiddlewareFunc {
	return injectPrincipalFunc(func(*echo.Context) *authz.Principal { return p })
}

// injectPrincipalFunc 同上，但主体按请求算 —— 一个进程里要出现好几个租户时用它。
func injectPrincipalFunc(of func(*echo.Context) *authz.Principal) echo.MiddlewareFunc {
	return func(next echo.HandlerFunc) echo.HandlerFunc {
		return func(c *echo.Context) error {
			p := of(c)
			if p == nil {
				return next(c)
			}
			req := c.Request()
			c.SetRequest(req.WithContext(authz.WithPrincipal(req.Context(), p)))
			return next(c)
		}
	}
}

type fakeAuditSink struct {
	mu      sync.Mutex
	records []audit.Record
}

func (f *fakeAuditSink) Write(_ context.Context, rec audit.Record) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.records = append(f.records, rec)
	return nil
}

func (f *fakeAuditSink) last() (audit.Record, bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if len(f.records) == 0 {
		return audit.Record{}, false
	}
	return f.records[len(f.records)-1], true
}
