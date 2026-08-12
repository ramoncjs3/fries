// Package registration 是自助注册：陌生人自己注册开一个组织（MULTI-TENANCY.md §5、§9.2）。
//
// ⚠️ **公开输入直接建租户，滥用面不小。** 三道防线：
//   - 平台设置「允许自助注册」默认关，关着时直接拒；
//   - 必须邮箱验证通过才真的建租户（否则垃圾邮箱涌进来）；
//   - 注册/验证两个接口都是公开 POST，走中间件的 IP 限流。
//
// 建租户本身复用 platform.ProvisionTenant —— 和平台管理员开租户是同一段逻辑，绝不长歪。
package registration

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/ramoncjs3/fries/internal/audit"
	"github.com/ramoncjs3/fries/internal/auth"
	"github.com/ramoncjs3/fries/internal/config"
	"github.com/ramoncjs3/fries/internal/errs"
	"github.com/ramoncjs3/fries/internal/httpx"
	"github.com/ramoncjs3/fries/internal/notify"
	"github.com/ramoncjs3/fries/internal/repo"
	"github.com/ramoncjs3/fries/internal/service/platform"
)

const (
	verifyTokenBytes = 32
	// 验证链接比忘记密码宽松些：注册的人可能要过会儿才去邮箱点。
	verifyTokenTTL = 24 * time.Hour
	// adminUsername 是新组织首个管理员的用户名，和平台开租户保持一致。
	adminUsername = "admin"
)

// ErrDisabled 是自助注册没开时的回应。
var ErrDisabled = errs.Define("registration.disabled", http.StatusForbidden,
	"自助注册当前未开放，请联系管理员")

const (
	// ipRegLimit 是单个 IP 每小时允许的注册申请数。注册是「开个组织」的一次性低频动作，正常人
	// 一小时来不了几次；压到这个量，正常用量碰不到、又足以掐断「拿平台当群发验证信放大器」。
	ipRegLimit  = 5
	ipRegWindow = time.Hour
)

// Service 是自助注册服务。
type Service struct {
	store    *repo.Store
	settings *config.Settings
	mailer   notify.Mailer
	baseURL  string
	guard    *auth.IPGuard
}

// New 造自助注册服务。
func New(store *repo.Store, settings *config.Settings, mailer notify.Mailer, baseURL string) *Service {
	return &Service{
		store: store, settings: settings, mailer: mailer, baseURL: baseURL,
		guard: auth.NewIPGuard(ipRegLimit, ipRegWindow),
	}
}

// RegisterInput 是注册申请入参。
type RegisterInput struct {
	Email       string
	CompanyName string
	Code        string // 想要的公司代码
	Password    string // 管理员密码，本人自己设
}

// Register 收下一个注册申请：校验 → 存待验证记录 → 发验证邮件。
//
// ⚠️ **不检查邮箱/公司代码是否已存在**（防枚举）：一律走同样的流程、发同样的信。
// 公司代码被占用这类冲突留到验证后真建租户时才报（那时对方已证明了邮箱所有权）。
// 只有格式/强度这类**输入校验**会当场返回 —— 那不泄露「谁已经是客户」。
func (s *Service) Register(ctx context.Context, in RegisterInput) error {
	if !s.settings.AllowSelfRegistration() {
		return ErrDisabled
	}
	if err := platform.ValidateTenantCode(in.Code); err != nil {
		return err
	}
	// 新组织还没有租户级策略，用平台默认密码策略校验强度。
	if err := auth.CheckPasswordStrength(in.Password, s.settings.Security(uuid.Nil)); err != nil {
		return err
	}
	// 注册专用的按 IP 节流：全局 IP 限流（20/s）挡不住「拿平台当群发验证信放大器」。
	// **放在输入校验之后**：格式/强度不过的请求根本不发信、也就不占额度，正常人手滑改几次
	// 密码不会被锁；只有「够格发信」的请求才计数。超限直接拒（不建记录、不发信）。
	if !s.guard.Allow(httpx.ClientIP(ctx)) {
		return errs.RateLimited
	}

	token := auth.RandomToken(verifyTokenBytes)
	id, err := uuid.NewV7()
	if err != nil {
		return errs.Internal.Wrap(err)
	}

	q := s.store.Unscoped()
	// 同一邮箱重发时先作废旧的待验证记录（一封邮箱一条有效）。
	if err := q.InvalidatePendingRegistrationsByEmail(ctx, in.Email); err != nil {
		return errs.Internal.Wrap(err)
	}
	if err := q.CreatePendingRegistration(ctx, repo.CreatePendingRegistrationParams{
		ID:                id,
		Email:             in.Email,
		CompanyName:       in.CompanyName,
		DesiredCode:       strings.ToLower(strings.TrimSpace(in.Code)),
		AdminPasswordHash: auth.HashPassword(in.Password),
		TokenHash:         auth.HashToken(token),
		ExpiresAt:         time.Now().UTC().Add(verifyTokenTTL),
	}); err != nil {
		return errs.Internal.Wrap(err)
	}

	return s.mailer.SendEmail(ctx, notify.Message{
		To:      []string{in.Email},
		Subject: "验证邮箱，完成组织注册",
		Body: fmt.Sprintf(
			"你正在注册组织「%s」。点下面的链接验证邮箱、完成注册（%d 小时内有效）：\n\n%s\n\n"+
				"如果不是你本人操作，忽略这封邮件即可，不会创建任何东西。",
			in.CompanyName, int(verifyTokenTTL.Hours()), s.verifyLink(token)),
	})
}

func (s *Service) verifyLink(token string) string {
	return strings.TrimRight(s.baseURL, "/") + "/register/verify?token=" + token
}

// VerifiedResult 是验证成功、组织建好后回给用户的东西：告诉他怎么登录。
type VerifiedResult struct {
	TenantCode    string `json:"tenant_code" doc:"登录时填的公司代码"`
	AdminUsername string `json:"admin_username" doc:"管理员用户名"`
}

// Verify 用验证 token 完成注册：原子认领待验证记录 → 建租户 + 首个管理员。
//
// 认领是一条 DELETE ... RETURNING，并发里只有一个能拿到，天然防重复建租户。
// 公司代码此时被占用（别人在这期间注册了同名）→ 返回可读错误，用户换个代码重来。
func (s *Service) Verify(ctx context.Context, token string) (VerifiedResult, error) {
	if token == "" {
		return VerifiedResult{}, errs.ValidationFailed.WithField("body.token", "缺少验证 token")
	}
	// 再查一次开关：Register 时开着、但管理员**因发现滥用而关掉**后，此前 24h 内已占好的 pending
	// 记录不该还能兑现成真租户。关掉自助注册要能立刻止血，而不是留一个 24h 的兑现窗口。
	if !s.settings.AllowSelfRegistration() {
		return VerifiedResult{}, ErrDisabled
	}

	reg, err := s.store.Unscoped().ClaimPendingRegistrationByToken(ctx, auth.HashToken(token))
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return VerifiedResult{}, errs.ValidationFailed.Detailf("验证链接无效或已过期，请重新注册")
		}
		return VerifiedResult{}, errs.Internal.Wrap(err)
	}

	email := reg.Email
	created, err := platform.ProvisionTenant(ctx, s.store, platform.ProvisionParams{
		Code:              reg.DesiredCode,
		Name:              reg.CompanyName,
		AdminUsername:     adminUsername,
		AdminEmail:        &email,
		AdminPasswordHash: reg.AdminPasswordHash,
		// 密码是本人自己设的，不用强制首次改。
		MustChangePassword: false,
		CreatedBy:          nil, // 自助注册没有操作人
	})
	if err != nil {
		// pending 已被认领删掉；code 冲突/非法这类由 ProvisionTenant 给可读错误。
		return VerifiedResult{}, err
	}

	audit.SetTenantID(ctx, created.ID)
	audit.SetResourceID(ctx, created.ID)
	return VerifiedResult{TenantCode: created.Code, AdminUsername: adminUsername}, nil
}

// DeleteExpired 清过期的待验证记录，给后台任务调。
func (s *Service) DeleteExpired(ctx context.Context) (int64, error) {
	return s.store.Unscoped().DeleteExpiredPendingRegistrations(ctx)
}
