package auth

import (
	"context"
	"errors"
	"net/netip"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/ramoncjs3/fries/internal/audit"
	"github.com/ramoncjs3/fries/internal/authz"
	"github.com/ramoncjs3/fries/internal/config"
	"github.com/ramoncjs3/fries/internal/errs"
	"github.com/ramoncjs3/fries/internal/notify"
	"github.com/ramoncjs3/fries/internal/repo"
)

// statusActive 是 users / service_accounts 的可用状态。
const statusActive = "active"

// tenantActive 是 tenants 的可用状态。另一档是 suspended（租户只停用不删除，§9.3）。
const tenantActive = "active"

// Service 是认证服务。**不 import echo/huma**（红线 #6）—— cookie 怎么设、
// 请求头怎么读，是 handler 层的事。
type Service struct {
	store        *repo.Store
	settings     *config.Settings
	checker      authz.Checker
	cfg          SessionConfig
	guard        *loginGuard
	mailer       notify.Mailer
	resetBaseURL string
}

// NewService 造认证服务。mailer 发忘记密码这类邮件；resetBaseURL 是前端 app 的公开地址，拼链接用。
func NewService(store *repo.Store, settings *config.Settings, checker authz.Checker, cfg SessionConfig, mailer notify.Mailer, resetBaseURL string) *Service {
	return &Service{
		store:        store,
		settings:     settings,
		checker:      checker,
		cfg:          cfg,
		guard:        newLoginGuard(),
		mailer:       mailer,
		resetBaseURL: resetBaseURL,
	}
}

// Config 暴露会话配置，handler 用它造 cookie。
func (s *Service) Config() SessionConfig { return s.cfg }

// LoginInput 是登录入参。
type LoginInput struct {
	// TenantCode 是「公司代码」，用户在登录框里敲的那个（MULTI-TENANCY.md §4.1）。
	//
	// 为什么必须要它：账号只在**租户内**唯一，两家公司都可以有一个叫 admin 的人。
	// 不先说清楚是哪家公司，就没法确定是谁在登录。
	TenantCode string
	// Account 是登录标识符：用户名 / 邮箱 / 手机号都行。
	Account   string
	Password  string
	IP        *netip.Addr
	UserAgent string
}

// LoginResult 是登录结果。token 交给 handler 放进 cookie，不进响应体。
type LoginResult struct {
	Principal *authz.Principal
	Token     string
	CSRFToken string
	ExpiresAt time.Time
}

// Login 校验账号密码并建立会话。
//
// 失败一律返回同一个 auth.invalid_credentials，不告诉对方「用户不存在」还是
// 「密码错了」—— 那等于送一个用户名枚举接口。
func (s *Service) Login(ctx context.Context, in LoginInput) (*LoginResult, error) {
	// IP 维度的失败节流：账号维度落库，IP 维度放内存就够（DECISIONS.md §6）。
	if s.guard.blocked(in.IP, in.TenantCode) {
		return nil, errs.RateLimited.Wrap(errors.New("同一 IP 登录失败次数过多"))
	}

	// 先按公司代码找租户 —— 这一步在租户上下文建立之前，只能走不带租户的句柄（§3.2 ③）。
	tenant, err := s.store.Platform().GetTenantByCode(ctx, in.TenantCode)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			// ⚠️ 「公司代码不存在」**不能**告诉前端（§7.6）：那等于送一个
			// 「这家公司是不是你们客户」的探测接口。和密码错误给一样的回应。
			// 也跑一次哈希校验抹平时间差 —— 否则「代码不存在」会明显更快。
			VerifyPassword(in.Password, dummyHash)
			s.guard.fail(in.IP, in.TenantCode)
			return nil, errs.InvalidCredentials
		}
		return nil, errs.Internal.Wrap(err)
	}
	// 公司代码有效，从这里开始失败的审计都记在这个租户名下 ——
	// 客户才能看到「有人在爆破我们的账号」（§7.1）。
	audit.SetTenantID(ctx, tenant.ID)

	if tenant.Status != tenantActive {
		// 同上：租户被停用也不能直接说，只进审计和日志。
		VerifyPassword(in.Password, dummyHash)
		s.guard.fail(in.IP, in.TenantCode)
		return nil, errs.InvalidCredentials
	}

	q := s.store.ForTenant(tenant.ID)
	// 密码策略是**租户级**的，每家公司自己定（§7.2）
	policy := s.settings.Security(tenant.ID)

	user, err := q.GetUserByIdentifier(ctx, in.Account)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			// 用户不存在也跑一次哈希校验，抹平时间差
			VerifyPassword(in.Password, dummyHash)
			s.guard.fail(in.IP, in.TenantCode)
			return nil, errs.InvalidCredentials
		}
		return nil, errs.Internal.Wrap(err)
	}

	// **先验密码，再看账号状态。** 反过来的话，「账号已停用」「账号已锁定」这两个
	// 回应就成了用户名枚举的探针：不知道密码的人也能问出哪些账号存在、状态如何。
	if !VerifyPassword(in.Password, user.PasswordHash) {
		s.guard.fail(in.IP, in.TenantCode)
		if _, err := q.MarkLoginFailure(ctx, repo.MarkLoginFailureArgs{
			ID:          user.ID,
			MaxFailures: int32(policy.LoginMaxFailures),
			LockMinutes: int32(policy.LoginLockMinutes),
		}); err != nil {
			return nil, errs.Internal.Wrap(err)
		}
		return nil, errs.InvalidCredentials
	}

	// 密码对了才告诉他账号本身的状态。
	if user.Status != statusActive {
		return nil, errs.AccountLocked.Detailf("账号已被停用")
	}
	if user.LockedUntil != nil && user.LockedUntil.After(time.Now()) {
		return nil, errs.AccountLocked.Detailf(
			"账号已锁定，请 %d 分钟后再试", minutesUntil(*user.LockedUntil))
	}

	s.guard.succeed(in.IP)
	if err := q.MarkLoginSuccess(ctx, user.ID); err != nil {
		return nil, errs.Internal.Wrap(err)
	}

	token := randomToken(sessionTokenBytes)
	// 一律 UTC：接口输出的时间要是带 Z 的 RFC3339（DECISIONS.md §2.5）。
	expiresAt := time.Now().UTC().Add(s.cfg.TTL)
	sessionID, err := uuid.NewV7()
	if err != nil {
		return nil, errs.Internal.Wrap(err)
	}

	if _, err := q.CreateSession(ctx, repo.CreateSessionArgs{
		ID:        sessionID,
		TokenHash: hashToken(token),
		UserID:    user.ID,
		IP:        in.IP,
		UserAgent: truncate(in.UserAgent, 512),
		ExpiresAt: expiresAt,
	}); err != nil {
		return nil, errs.Internal.Wrap(err)
	}

	principal := s.principalOf(user, sessionID, policy)
	return &LoginResult{
		Principal: principal,
		Token:     token,
		CSRFToken: csrfToken(s.cfg.Secret, sessionID),
		ExpiresAt: expiresAt,
	}, nil
}

// Logout 吊销一个会话。服务端立即失效，不等 cookie 过期。
func (s *Service) Logout(ctx context.Context, sessionID uuid.UUID) error {
	q, err := s.tenant(ctx)
	if err != nil {
		return err
	}
	if err := q.RevokeSession(ctx, sessionID); err != nil {
		return errs.Internal.Wrap(err)
	}
	return nil
}

// tenant 取当前请求的租户句柄。已经认证过的路径用它。
func (s *Service) tenant(ctx context.Context) (*repo.TenantQueries, error) {
	id, err := authz.MustTenant(ctx)
	if err != nil {
		return nil, err
	}
	return s.store.ForTenant(id), nil
}

// AuthenticateSession 用 cookie 里的 token 换出主体。
func (s *Service) AuthenticateSession(ctx context.Context, token string) (*authz.Principal, error) {
	if token == "" {
		return nil, errs.Unauthenticated
	}

	// 会话查询是全平台的：拿 token 查这是谁，那一刻还不知道租户（§3.2 ③）。
	// 查出来的这一行才告诉我们 tenant_id —— 后续所有隔离都以它为唯一来源。
	row, err := s.store.Unscoped().GetLiveSession(ctx, hashToken(token))
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			// 会话不存在 / 已吊销 / 已过期，对用户都是「重新登录」
			return nil, errs.SessionExpired
		}
		return nil, errs.Internal.Wrap(err)
	}
	if row.User.Status != statusActive {
		return nil, errs.AccountLocked.Detailf("账号已被停用")
	}
	// 组织被停用，已经发出去的会话立刻失效（§8.2）。
	// 只挡新登录是不够的 —— 那样客户的人拿着旧 cookie 能一直用到过期，
	// 而平台端显示的是「已停用」。这里每个请求都会过一遍，改库也绕不过去。
	if row.TenantStatus != tenantActive {
		// 归属到那个组织再拒（§7.1 同一个道理）：这是那家公司的人在被挡，
		// 记成平台级事件的话，客户自己看不到「我们停用期间还有人在用」。
		audit.SetTenantID(ctx, row.Session.TenantID)
		return nil, errs.TenantSuspended
	}

	// 顺手记录活跃时间，失败不影响本次请求。
	_ = s.store.ForTenant(row.Session.TenantID).TouchSession(ctx, row.Session.ID)

	p := s.principalOf(row.User, row.Session.ID, s.settings.Security(row.Session.TenantID))
	// ⚠️ 租户取自**会话行**，不是从 user 推的（§10.2）。两者由复合外键保证一致。
	p.TenantID = row.Session.TenantID
	return p, nil
}

// AuthenticateAPIKey 用 Service Account 的 API Key 换出主体（DECISIONS.md §8.1）。
func (s *Service) AuthenticateAPIKey(ctx context.Context, key string) (*authz.Principal, error) {
	prefix, secret, ok := splitAPIKey(key)
	if !ok {
		return nil, errs.Unauthenticated
	}

	// 和会话同理：先按 prefix 定位到这一行，它才告诉我们租户是谁。
	row, err := s.store.Unscoped().GetServiceAccountByPrefix(ctx, prefix)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, errs.Unauthenticated
		}
		return nil, errs.Internal.Wrap(err)
	}
	account := row.ServiceAccount

	// prefix 命中了就归属到那个组织，**后面所有失败路径都算它的**（§9.7）。
	//
	// 口径和 §7.1 的登录失败一样：「公司代码有效、密码错误」记在那个租户名下。
	// 这里 prefix 有效、secret 错误是完全同构的一件事 —— 而且它恰恰是最该被记的：
	// 有人拿着这个组织的 key 前缀在猜后半段。记成平台级事件的话，客户在自己的
	// 审计里什么都看不到，而被爆破的是**他的**对接凭据。
	//
	// prefix 根本不存在的那条路在上面就返回了，那种记 NULL —— 猜不出是谁的，也不该硬安给谁。
	audit.SetTenantID(ctx, account.TenantID)

	if row.TenantStatus != tenantActive {
		// 机器身份同理：组织停了，对接系统也该停（§8.2）
		return nil, errs.TenantSuspended
	}
	if account.ExpiresAt != nil && account.ExpiresAt.Before(time.Now()) {
		return nil, errs.Unauthenticated
	}
	if !VerifyPassword(secret, string(account.KeyHash)) {
		return nil, errs.Unauthenticated
	}

	_ = s.store.ForTenant(account.TenantID).TouchServiceAccount(ctx, account.ID)

	roles, scope, _ := s.checker.Identity(account.ID)
	return &authz.Principal{
		Type:        authz.PrincipalService,
		ID:          account.ID,
		TenantID:    account.TenantID,
		Name:        account.Name,
		DisplayName: account.Name,
		Roles:       roles,
		Scope:       scope,
	}, nil
}

// ChangePassword 改自己的密码。改完吊销该用户的其它会话。
func (s *Service) ChangePassword(ctx context.Context, userID uuid.UUID, oldPassword, newPassword string, keepSessionID uuid.UUID) error {
	q, err := s.tenant(ctx)
	if err != nil {
		return err
	}
	tenantID := q.TenantID()

	user, err := q.GetUserByID(ctx, userID)
	if err != nil {
		return errs.Internal.Wrap(err)
	}
	if !VerifyPassword(oldPassword, user.PasswordHash) {
		return errs.InvalidCredentials.Detailf("原密码不正确")
	}
	if oldPassword == newPassword {
		return errs.ValidationFailed.WithField("body.new_password", "新密码不能和原密码相同")
	}
	if err := CheckPasswordStrength(newPassword, s.settings.Security(tenantID)); err != nil {
		return err
	}

	if err := q.SetUserPassword(ctx, repo.SetUserPasswordArgs{
		ID:           userID,
		PasswordHash: HashPassword(newPassword),
	}); err != nil {
		return errs.Internal.Wrap(err)
	}

	// 改密码等于「怀疑之前的会话不安全」，除了当前这条全踢掉。
	if err := q.RevokeOtherUserSessions(ctx, repo.RevokeOtherUserSessionsArgs{
		UserID:        userID,
		KeepSessionID: keepSessionID,
	}); err != nil {
		return errs.Internal.Wrap(err)
	}
	return nil
}

// CurrentTenant 是「当前登录的是哪家公司」，给顶栏显示用。
//
// 名字里带 Current 不是啰嗦：类型名会原样进 OpenAPI 的 schema 名，
// 而平台管理端有一个完整的 `Tenant`（组织记录）。两个都叫 Tenant 的话
// huma 起不来 —— 实测会 panic「duplicate name」。
type CurrentTenant struct {
	Code string `json:"code" doc:"公司代码，登录时要填的那个"`
	Name string `json:"name" doc:"组织名"`
}

// CurrentTenant 取当前请求所属的组织。
//
// 只在 /me 里调一次（一次页面加载一次），**没有塞进 Principal**：
// 塞进去意味着每个请求都要多查一次 tenants 表，而这个信息只有顶栏要显示。
// 真到了要按租户名做别的事的时候，再考虑像 settings 那样按租户缓存 + NOTIFY 刷新。
func (s *Service) CurrentTenant(ctx context.Context) (CurrentTenant, error) {
	tenantID, err := authz.MustTenant(ctx)
	if err != nil {
		return CurrentTenant{}, err
	}
	row, err := s.store.Platform().GetTenantByID(ctx, tenantID)
	if err != nil {
		return CurrentTenant{}, errs.Internal.Wrap(err)
	}
	return CurrentTenant{Code: row.Code, Name: row.Name}, nil
}

// principalOf 把用户行翻译成授权主体。角色和数据范围从 Checker 拿 ——
// 它已经把全量策略加载在内存里，不用再查库。
func (s *Service) principalOf(user repo.User, sessionID uuid.UUID, policy config.Security) *authz.Principal {
	roles, scope, _ := s.checker.Identity(user.ID)

	expired := false
	if policy.PasswordMaxAgeDays > 0 {
		age := time.Since(user.PasswordChangedAt)
		expired = age > time.Duration(policy.PasswordMaxAgeDays)*24*time.Hour
	}

	return &authz.Principal{
		Type:               authz.PrincipalUser,
		ID:                 user.ID,
		TenantID:           user.TenantID,
		Name:               user.Username,
		DisplayName:        user.DisplayName,
		Roles:              roles,
		Scope:              scope,
		SessionID:          sessionID,
		MustChangePassword: user.MustChangePassword,
		PasswordExpired:    expired,
	}
}

// splitAPIKey 拆 API Key：fsa_<prefix>_<secret>。
//
// 用 SplitN 切三段：secret 是 base64url，里面可能带 `_`，切多了就拼不回来。
func splitAPIKey(key string) (prefix, secret string, ok bool) {
	parts := strings.SplitN(key, "_", 3)
	if len(parts) != 3 || parts[0] != apiKeyScheme {
		return "", "", false
	}
	if parts[1] == "" || parts[2] == "" {
		return "", "", false
	}
	return parts[1], parts[2], true
}

func minutesUntil(t time.Time) int {
	d := time.Until(t)
	if d < time.Minute {
		return 1
	}
	return int(d.Minutes()) + 1
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n]
}
