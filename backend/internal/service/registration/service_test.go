package registration_test

import (
	"context"
	"errors"
	"fmt"
	"net/netip"
	"strings"
	"sync"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/ramoncjs3/fries/internal/config"
	"github.com/ramoncjs3/fries/internal/errs"
	"github.com/ramoncjs3/fries/internal/httpx"
	"github.com/ramoncjs3/fries/internal/notify"
	"github.com/ramoncjs3/fries/internal/repo"
	"github.com/ramoncjs3/fries/internal/repo/testdb"
	"github.com/ramoncjs3/fries/internal/service/registration"
)

type captureMailer struct {
	mu   sync.Mutex
	msgs []notify.Message
}

func (m *captureMailer) SendEmail(_ context.Context, msg notify.Message) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.msgs = append(m.msgs, msg)
	return nil
}

func (m *captureMailer) count() int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return len(m.msgs)
}

func (m *captureMailer) tokenFromLastEmail(t *testing.T) string {
	t.Helper()
	m.mu.Lock()
	defer m.mu.Unlock()
	if len(m.msgs) == 0 {
		t.Fatal("没有邮件可抠 token")
	}
	body := m.msgs[len(m.msgs)-1].Body
	i := strings.Index(body, "token=")
	if i < 0 {
		t.Fatalf("邮件里没有验证链接：%s", body)
	}
	tok := body[i+len("token="):]
	if j := strings.IndexAny(tok, "\n \t"); j >= 0 {
		tok = tok[:j]
	}
	return tok
}

// regService 起一个自助注册服务。enable 决定平台设置里「允许自助注册」开不开。
func regService(t *testing.T, enable bool) (*registration.Service, *captureMailer, *repo.Store, *pgxpool.Pool, *config.Settings) {
	t.Helper()
	pool := testdb.Start(t)
	store := repo.New(pool)
	if enable {
		// platform_settings 是快照+还原的，本次插入只影响这个用例。
		if _, err := pool.Exec(t.Context(), `
			INSERT INTO platform_settings (key, value) VALUES ('registration.self_service', 'true'::jsonb)
			ON CONFLICT (key) DO UPDATE SET value = 'true'::jsonb`); err != nil {
			t.Fatal(err)
		}
	}
	settings, err := config.NewSettings(t.Context(), store)
	if err != nil {
		t.Fatal(err)
	}
	mailer := &captureMailer{}
	svc := registration.New(store, settings, mailer, "https://app.test")
	return svc, mailer, store, pool, settings
}

// TestRegisterDisabledByDefault 默认关：注册直接被拒，不发信、不建任何东西。
func TestRegisterDisabledByDefault(t *testing.T) {
	svc, mailer, _, _, _ := regService(t, false)
	err := svc.Register(t.Context(), registration.RegisterInput{
		Email: "founder@acme.io", CompanyName: "Acme", Code: "acmenew", Password: "S3lfReg!pass",
	})
	if !errors.Is(err, registration.ErrDisabled) {
		t.Fatalf("自助注册没开时应返回 ErrDisabled，得到：%v", err)
	}
	if mailer.count() != 0 {
		t.Fatal("被拒时一封信都不该发")
	}
}

// TestRegisterVerifyHappyPath 开启后走完整流程：注册 → 收验证信 → 验证 → 组织和 admin 都建好。
func TestRegisterVerifyHappyPath(t *testing.T) {
	svc, mailer, store, _, _ := regService(t, true)
	ctx := t.Context()

	if err := svc.Register(ctx, registration.RegisterInput{
		Email: "founder@acme.io", CompanyName: "Acme 公司", Code: "acmenew", Password: "S3lfReg!pass",
	}); err != nil {
		t.Fatalf("注册申请不该报错：%v", err)
	}
	if mailer.count() != 1 {
		t.Fatalf("应发 1 封验证邮件，实际 %d", mailer.count())
	}
	token := mailer.tokenFromLastEmail(t)

	result, err := svc.Verify(ctx, token)
	if err != nil {
		t.Fatalf("验证不该报错：%v", err)
	}
	if result.TenantCode != "acmenew" {
		t.Fatalf("返回的公司代码应是 acmenew，得到 %q", result.TenantCode)
	}

	// 组织真建出来了，admin 也在，邮箱对得上。
	tenant, err := store.Platform().GetTenantByCode(ctx, "acmenew")
	if err != nil {
		t.Fatalf("组织应已创建：%v", err)
	}
	admin, err := store.ForTenant(tenant.ID).GetUserByIdentifier(ctx, "founder@acme.io")
	if err != nil {
		t.Fatalf("首个管理员应已创建：%v", err)
	}
	if admin.Username != "admin" {
		t.Errorf("管理员用户名应是 admin，得到 %q", admin.Username)
	}

	// token 一次性：再验一次必须失败（且不会建出第二个组织）。
	if _, err := svc.Verify(ctx, token); err == nil {
		t.Fatal("同一个验证 token 第二次必须被拒")
	}
}

// TestRegisterThrottledPerIP 同一个 IP 注册申请超过每小时上限就被拒（挡「群发验证信放大器」）。
// 全局 IP 限流（20/s）拦不住这种低频高价值发信，注册专用节流补这一层。
func TestRegisterThrottledPerIP(t *testing.T) {
	svc, mailer, _, _, _ := regService(t, true)
	ip := netip.MustParseAddr("203.0.113.9")
	ctx := httpx.WithClientIP(t.Context(), &ip)

	// 头几次（到上限）都放行、都发信；输入各不相同且合法，排除别的约束干扰。
	sent := 0
	for i := range 20 {
		err := svc.Register(ctx, registration.RegisterInput{
			Email:       fmt.Sprintf("founder%d@acme.io", i),
			CompanyName: "Acme",
			Code:        fmt.Sprintf("acme%02d", i),
			Password:    "S3lfReg!pass",
		})
		if errors.Is(err, errs.RateLimited) {
			break // 命中节流
		}
		if err != nil {
			t.Fatalf("第 %d 次注册意外报错：%v", i+1, err)
		}
		sent++
	}

	if sent == 0 || sent >= 20 {
		t.Fatalf("应在若干次后触发节流，实际放行 %d 次", sent)
	}
	if mailer.count() != sent { // 被节流的那次不发信
		t.Fatalf("发信数应等于放行数 %d，实际 %d", sent, mailer.count())
	}

	// 换个 IP 立刻能注册（节流是按 IP 的，不误伤别人）。
	other := netip.MustParseAddr("203.0.113.10")
	if err := svc.Register(httpx.WithClientIP(t.Context(), &other),
		registration.RegisterInput{Email: "fresh@acme.io", CompanyName: "Acme", Code: "freshco", Password: "S3lfReg!pass"}); err != nil {
		t.Fatalf("另一个 IP 不该被节流：%v", err)
	}
}

// TestVerifyRejectsWhenDisabledAfterRegister 注册时开着、验证前被管理员关掉 → 验证必须拒，
// 不能兑现建租户。关掉自助注册要能立刻止血，而不是留一个到 token 过期（24h）的兑现窗口。
func TestVerifyRejectsWhenDisabledAfterRegister(t *testing.T) {
	svc, mailer, store, pool, settings := regService(t, true)
	ctx := t.Context()

	if err := svc.Register(ctx, registration.RegisterInput{
		Email: "founder@acme.io", CompanyName: "Acme", Code: "acmenew", Password: "S3lfReg!pass",
	}); err != nil {
		t.Fatalf("注册申请不该报错：%v", err)
	}
	token := mailer.tokenFromLastEmail(t)

	// 管理员关掉自助注册（改库 + 重载平台设置，模拟运行期翻开关）。
	if _, err := pool.Exec(ctx, `UPDATE platform_settings SET value = 'false'::jsonb WHERE key = 'registration.self_service'`); err != nil {
		t.Fatal(err)
	}
	if err := settings.ReloadPlatform(ctx); err != nil {
		t.Fatal(err)
	}

	if _, err := svc.Verify(ctx, token); !errors.Is(err, registration.ErrDisabled) {
		t.Fatalf("关掉后验证应返回 ErrDisabled，得到：%v", err)
	}
	// 没建出任何组织。
	if _, err := store.Platform().GetTenantByCode(ctx, "acmenew"); err == nil {
		t.Fatal("自助注册已关，不该建出组织")
	}
}

// TestVerifyRejectsTakenCode 验证时公司代码已被占用 → 报可读错误（不 500、不建半残组织）。
func TestVerifyRejectsTakenCode(t *testing.T) {
	svc, mailer, _, pool, _ := regService(t, true)
	ctx := t.Context()
	// 先占掉 acmenew 这个代码。
	testdb.NewTenant(t, pool, 0) // acme
	if _, err := pool.Exec(ctx, `INSERT INTO tenants (id, code, name) VALUES (gen_random_uuid(), 'acmenew', '别人家')`); err != nil {
		t.Fatal(err)
	}

	if err := svc.Register(ctx, registration.RegisterInput{
		Email: "founder@acme.io", CompanyName: "Acme", Code: "acmenew", Password: "S3lfReg!pass",
	}); err != nil {
		t.Fatal(err)
	}
	token := mailer.tokenFromLastEmail(t)
	if _, err := svc.Verify(ctx, token); err == nil {
		t.Fatal("代码已被占用，验证应报错而不是建重复组织")
	}
}
