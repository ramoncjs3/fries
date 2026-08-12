package auth

import (
	"context"
	"errors"
	"net/netip"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/ramoncjs3/fries/internal/authz"
	"github.com/ramoncjs3/fries/internal/config"
	"github.com/ramoncjs3/fries/internal/errs"
	"github.com/ramoncjs3/fries/internal/repo"
)

// 平台登录是**整个系统最高价值的攻击目标**（MULTI-TENANCY.md §9.2）：
// 拿到一个平台管理员账号 = 能开关所有客户的组织。所以它比租户登录严：
//
//	· 独立的会话表和 cookie 名（§10.1）
//	· 更严的失败节流（下面这两个常量）
//	· 强制强密码，**不吃租户级的密码策略** —— 租户管理员能把自己公司的策略调松，
//	  但那影响不到平台
//	· 每一个动作都进审计
//
// ⚠️ 二次验证（TOTP）这一轮没做，这是**明确的欠账**，不是忘了。
const (
	// platformIPMaxFailures 比租户端的 20 严一半。
	platformIPMaxFailures = 10
	// platformAccountMaxFailures 是账号维度的锁定阈值（租户端默认 5，走配置）。
	platformAccountMaxFailures = 5
	// platformLockMinutes 是锁定时长。
	platformLockMinutes = 30
)

// platformPasswordPolicy 是平台管理员的密码策略，**写死不可调**。
//
// 租户级策略存在 settings 表里、租户管理员能自己改（§7.2）；平台的不能 ——
// 能改它的人本来就是平台管理员，让他有机会把自己的门槛调低毫无意义。
func platformPasswordPolicy() config.Security {
	return config.Security{
		PasswordMinLength:  12,
		PasswordRequireMix: true,
		PasswordMaxAgeDays: 0,
	}
}

// PlatformService 是平台管理端的认证服务。
//
// 它和租户端的 Service 是两套东西，只共用密码哈希、token 生成这些底层工具。
// 刻意不合并：合并意味着每个方法里都要判「这是平台还是租户」，而那种判断
// 漏一处就是把两个世界打通。
type PlatformService struct {
	store *repo.Store
	cfg   SessionConfig
	guard *loginGuard
}

// NewPlatformService 造平台认证服务。
func NewPlatformService(store *repo.Store, cfg SessionConfig) *PlatformService {
	return &PlatformService{store: store, cfg: cfg, guard: newLoginGuard()}
}

// Config 暴露平台会话配置，handler 用它造 cookie。
func (s *PlatformService) Config() SessionConfig { return s.cfg }

// PlatformLoginInput 是平台登录入参。**没有公司代码** —— 平台管理员不属于任何组织。
type PlatformLoginInput struct {
	Username  string
	Password  string
	IP        *netip.Addr
	UserAgent string
}

// Login 校验平台管理员的账号密码并建立会话。
func (s *PlatformService) Login(ctx context.Context, in PlatformLoginInput) (*LoginResult, error) {
	// 平台登录的 IP 节流单独一套，阈值比租户端严（§9.2）。
	// 第二个维度传空串：平台登录没有公司代码这一层，只按 IP。
	if s.guard.blockedWith(in.IP, "", platformIPMaxFailures) {
		return nil, errs.RateLimited.Wrap(errors.New("同一 IP 平台登录失败次数过多"))
	}

	q := s.store.Platform()
	admin, err := q.GetPlatformAdminByUsername(ctx, in.Username)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			// 账号不存在也跑一次哈希校验，抹平时间差
			VerifyPassword(in.Password, dummyHash)
			s.guard.failWith(in.IP, "", platformIPMaxFailures, ipWindow)
			return nil, errs.InvalidCredentials
		}
		return nil, errs.Internal.Wrap(err)
	}

	// 先验密码再看状态 —— 理由同租户端：反过来的话「账号已停用」就成了枚举探针。
	if !VerifyPassword(in.Password, admin.PasswordHash) {
		s.guard.failWith(in.IP, "", platformIPMaxFailures, ipWindow)
		if _, err := q.MarkPlatformLoginFailure(ctx, repo.MarkPlatformLoginFailureParams{
			ID:          admin.ID,
			MaxFailures: platformAccountMaxFailures,
			LockMinutes: platformLockMinutes,
		}); err != nil {
			return nil, errs.Internal.Wrap(err)
		}
		return nil, errs.InvalidCredentials
	}
	if admin.Status != statusActive {
		return nil, errs.AccountLocked.Detailf("账号已被停用")
	}
	if admin.LockedUntil != nil && admin.LockedUntil.After(time.Now()) {
		return nil, errs.AccountLocked.Detailf(
			"账号已锁定，请 %d 分钟后再试", minutesUntil(*admin.LockedUntil))
	}

	s.guard.succeed(in.IP)
	if err := q.MarkPlatformLoginSuccess(ctx, admin.ID); err != nil {
		return nil, errs.Internal.Wrap(err)
	}

	token := randomToken(sessionTokenBytes)
	expiresAt := time.Now().UTC().Add(s.cfg.TTL)
	sessionID, err := uuid.NewV7()
	if err != nil {
		return nil, errs.Internal.Wrap(err)
	}
	if _, err := q.CreatePlatformSession(ctx, repo.CreatePlatformSessionParams{
		ID:        sessionID,
		TokenHash: hashToken(token),
		AdminID:   admin.ID,
		IP:        in.IP,
		UserAgent: truncate(in.UserAgent, 512),
		ExpiresAt: expiresAt,
	}); err != nil {
		return nil, errs.Internal.Wrap(err)
	}

	return &LoginResult{
		Principal: platformPrincipalOf(admin, sessionID),
		Token:     token,
		CSRFToken: csrfToken(s.cfg.Secret, sessionID),
		ExpiresAt: expiresAt,
	}, nil
}

// AuthenticateSession 用平台 cookie 里的 token 换出主体。
func (s *PlatformService) AuthenticateSession(ctx context.Context, token string) (*authz.Principal, error) {
	if token == "" {
		return nil, errs.Unauthenticated
	}
	row, err := s.store.Platform().GetLivePlatformSession(ctx, hashToken(token))
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, errs.SessionExpired
		}
		return nil, errs.Internal.Wrap(err)
	}
	if row.PlatformAdmin.Status != statusActive {
		return nil, errs.AccountLocked.Detailf("账号已被停用")
	}

	_ = s.store.Platform().TouchPlatformSession(ctx, row.PlatformSession.ID)
	return platformPrincipalOf(row.PlatformAdmin, row.PlatformSession.ID), nil
}

// Logout 吊销一个平台会话。
func (s *PlatformService) Logout(ctx context.Context, sessionID uuid.UUID) error {
	if err := s.store.Platform().RevokePlatformSession(ctx, sessionID); err != nil {
		return errs.Internal.Wrap(err)
	}
	return nil
}

// ChangePassword 平台管理员改自己的密码。改完把其它会话踢掉。
func (s *PlatformService) ChangePassword(
	ctx context.Context, adminID uuid.UUID, oldPassword, newPassword string, keepSessionID uuid.UUID,
) error {
	q := s.store.Platform()
	admin, err := q.GetPlatformAdminByID(ctx, adminID)
	if err != nil {
		return errs.Internal.Wrap(err)
	}
	if !VerifyPassword(oldPassword, admin.PasswordHash) {
		return errs.InvalidCredentials.Detailf("原密码不正确")
	}
	if oldPassword == newPassword {
		return errs.ValidationFailed.WithField("body.new_password", "新密码不能和原密码相同")
	}
	// 走平台自己的强密码要求，不吃租户级策略（§9.2）
	if err := CheckPasswordStrength(newPassword, platformPasswordPolicy()); err != nil {
		return err
	}

	if err := q.SetPlatformAdminPassword(ctx, repo.SetPlatformAdminPasswordParams{
		ID:           adminID,
		PasswordHash: HashPassword(newPassword),
	}); err != nil {
		return errs.Internal.Wrap(err)
	}
	if err := q.RevokeOtherPlatformSessions(ctx, repo.RevokeOtherPlatformSessionsParams{
		AdminID:       adminID,
		KeepSessionID: keepSessionID,
	}); err != nil {
		return errs.Internal.Wrap(err)
	}
	return nil
}

// platformPrincipalOf 把平台管理员行翻译成授权主体。
//
// ⚠️ **TenantID 是零值，这是有意的** —— 平台管理员不属于任何租户。
// 授权中间件靠 Type 分流：平台主体只走 Realm=platform 的路由，
// 租户主体只走 Realm=tenant 的（§10.4）。
func platformPrincipalOf(admin repo.PlatformAdmin, sessionID uuid.UUID) *authz.Principal {
	return &authz.Principal{
		Type:               authz.PrincipalPlatform,
		ID:                 admin.ID,
		Name:               admin.Username,
		DisplayName:        admin.DisplayName,
		Scope:              authz.ScopeAll,
		SessionID:          sessionID,
		MustChangePassword: admin.MustChangePassword,
	}
}

// BootstrapPlatform 在库里一个平台管理员都没有时创建第一个（§10.10）。
//
// 这条链路是**整个系统权限的源头**：平台管理员开租户，租户管理员管自己的人。
// 和租户端的 bootstrap 一样：随机密码、只显示一次、强制首次登录改密，
// 而且**只在一个都没有时才跑** —— 有了之后再跑一次就是凭空多一个最高权限账号。
func BootstrapPlatform(ctx context.Context, store *repo.Store) (BootstrapResult, error) {
	var result BootstrapResult

	q := store.Platform()
	count, err := q.CountPlatformAdmins(ctx)
	if err != nil {
		return result, err
	}
	if count > 0 {
		return result, nil
	}

	password := RandomPassword(bootstrapPasswordChars)
	id, err := uuid.NewV7()
	if err != nil {
		return result, err
	}
	admin, err := q.CreatePlatformAdmin(ctx, repo.CreatePlatformAdminParams{
		ID:                 id,
		Username:           bootstrapUsername,
		DisplayName:        "平台管理员",
		PasswordHash:       HashPassword(password),
		MustChangePassword: true,
	})
	if err != nil {
		return result, err
	}

	return BootstrapResult{Created: true, Username: admin.Username, Password: password}, nil
}
