package auth

import (
	"context"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/ramoncjs3/fries/internal/config"
	"github.com/ramoncjs3/fries/internal/notify"
	"github.com/ramoncjs3/fries/internal/repo"
	"github.com/ramoncjs3/fries/internal/repo/testdb"
)

// captureMailer 把发出去的邮件记下来，测试从里面把重置链接里的 token 抠出来。
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

// tokenFromLastEmail 从最后一封邮件正文里抠出 ?token=... 的值。
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
		t.Fatalf("邮件里没有重置链接：%s", body)
	}
	tok := body[i+len("token="):]
	if j := strings.IndexAny(tok, "\n \t"); j >= 0 {
		tok = tok[:j]
	}
	return tok
}

// makeUser 在某租户下插一个带邮箱和密码的用户，返回 id。
func makeUser(t *testing.T, pool *pgxpool.Pool, tenantID uuid.UUID, username, email, password string) uuid.UUID {
	t.Helper()
	id := uuid.New()
	if _, err := pool.Exec(t.Context(), `
		INSERT INTO users (id, tenant_id, username, display_name, email, password_hash, status)
		VALUES ($1, $2, $3, $3, $4, $5, 'active')`,
		id, tenantID, username, email, HashPassword(password)); err != nil {
		t.Fatalf("建测试用户失败：%v", err)
	}
	return id
}

// resetService 拿 testdb 起一个能测忘记密码的 auth.Service（mailer 用 captureMailer）。
func resetService(t *testing.T) (*Service, *captureMailer, *repo.Store, testdb.TenantFixture, *pgxpool.Pool) {
	t.Helper()
	pool := testdb.Start(t)
	store := repo.New(pool)
	tenant := testdb.NewTenant(t, pool, 0)
	mailer := &captureMailer{}
	cfg := SessionConfig{Secret: strings.Repeat("a", 32), TTL: time.Hour, CookieName: "s"}
	svc := NewService(store, config.NewDefaultSettings(), nil, cfg, mailer, "https://app.test")
	return svc, mailer, store, tenant, pool
}

// TestForgotPasswordHappyPath 走一遍完整流程：申请 → 收信 → 用 token 改密 → 新密码能用、token 作废。
func TestForgotPasswordHappyPath(t *testing.T) {
	svc, mailer, store, tenant, pool := resetService(t)
	ctx := t.Context()
	makeUser(t, pool, tenant.ID, "alice", "alice@example.com", "OldPassw0rd!")

	if err := svc.RequestPasswordReset(ctx, ResetRequestInput{
		TenantCode: tenant.Code, Identifier: "alice@example.com",
	}); err != nil {
		t.Fatalf("申请重置不该报错：%v", err)
	}
	if mailer.count() != 1 {
		t.Fatalf("应发出 1 封重置邮件，实际 %d", mailer.count())
	}
	token := mailer.tokenFromLastEmail(t)

	newPass := "BrandNewP@ss1"
	if err := svc.ResetPassword(ctx, ResetPasswordInput{Token: token, NewPassword: newPass}); err != nil {
		t.Fatalf("用 token 改密不该报错：%v", err)
	}

	// 新密码能用了。
	user, err := store.ForTenant(tenant.ID).GetUserByIdentifier(ctx, "alice")
	if err != nil {
		t.Fatal(err)
	}
	if !VerifyPassword(newPass, user.PasswordHash) {
		t.Fatal("改密后新密码应该能验证通过")
	}

	// token 一次性：再用一次必须失败。
	if err := svc.ResetPassword(ctx, ResetPasswordInput{Token: token, NewPassword: "Another0ne!"}); err == nil {
		t.Fatal("同一个 token 第二次必须被拒（一次性）")
	}
}

// TestForgotPasswordNoEnumeration 防枚举：不存在的用户/租户，不报错、也不发信。
func TestForgotPasswordNoEnumeration(t *testing.T) {
	svc, mailer, _, tenant, pool := resetService(t)
	ctx := t.Context()
	makeUser(t, pool, tenant.ID, "bob", "bob@example.com", "OldPassw0rd!")

	// 用户不存在
	if err := svc.RequestPasswordReset(ctx, ResetRequestInput{
		TenantCode: tenant.Code, Identifier: "nobody@example.com",
	}); err != nil {
		t.Fatalf("不存在的用户不该报错（防枚举）：%v", err)
	}
	// 公司代码不存在
	if err := svc.RequestPasswordReset(ctx, ResetRequestInput{
		TenantCode: "no-such-company", Identifier: "bob@example.com",
	}); err != nil {
		t.Fatalf("不存在的公司代码不该报错（防枚举）：%v", err)
	}
	if mailer.count() != 0 {
		t.Fatalf("给不存在的账号申请，一封信都不该发，实际发了 %d", mailer.count())
	}
}

// TestResetPasswordRejectsExpiredToken 过期 token 必须被拒。
func TestResetPasswordRejectsExpiredToken(t *testing.T) {
	svc, _, _, tenant, pool := resetService(t)
	ctx := t.Context()
	userID := makeUser(t, pool, tenant.ID, "carol", "carol@example.com", "OldPassw0rd!")

	// 直接插一条**已过期**的 token（白盒能算 hashToken）。
	token := randomToken(resetTokenBytes)
	if _, err := pool.Exec(ctx, `
		INSERT INTO password_reset_tokens (id, tenant_id, user_id, token_hash, expires_at)
		VALUES ($1, $2, $3, $4, now() - interval '1 hour')`,
		uuid.New(), tenant.ID, userID, hashToken(token)); err != nil {
		t.Fatal(err)
	}
	if err := svc.ResetPassword(ctx, ResetPasswordInput{Token: token, NewPassword: "BrandNewP@ss1"}); err == nil {
		t.Fatal("过期 token 必须被拒")
	}
}

// TestResetPasswordIsolatedByTenant 两租户都有同名用户，重置 A 的不影响 B 的（token 带 tenant_id）。
func TestResetPasswordIsolatedByTenant(t *testing.T) {
	pool := testdb.Start(t)
	store := repo.New(pool)
	a := testdb.NewTenant(t, pool, 0)
	b := testdb.NewTenant(t, pool, 1)
	mailer := &captureMailer{}
	cfg := SessionConfig{Secret: strings.Repeat("a", 32), TTL: time.Hour, CookieName: "s"}
	svc := NewService(store, config.NewDefaultSettings(), nil, cfg, mailer, "")
	ctx := t.Context()

	makeUser(t, pool, a.ID, "dave", "dave@example.com", "AoldPassw0rd!")
	makeUser(t, pool, b.ID, "dave", "dave@example.com", "BoldPassw0rd!")

	// 给 A 租户的 dave 申请并重置。
	if err := svc.RequestPasswordReset(ctx, ResetRequestInput{TenantCode: a.Code, Identifier: "dave@example.com"}); err != nil {
		t.Fatal(err)
	}
	token := mailer.tokenFromLastEmail(t)
	if err := svc.ResetPassword(ctx, ResetPasswordInput{Token: token, NewPassword: "NewForAonly1!"}); err != nil {
		t.Fatal(err)
	}

	// B 租户的 dave 密码不该被动过。
	bUser, err := store.ForTenant(b.ID).GetUserByIdentifier(ctx, "dave")
	if err != nil {
		t.Fatal(err)
	}
	if !VerifyPassword("BoldPassw0rd!", bUser.PasswordHash) {
		t.Fatal("重置 A 租户的用户，竟然改到了 B 租户同名用户的密码 —— 跨租户串了")
	}
}
