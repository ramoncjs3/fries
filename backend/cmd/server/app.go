package main

import (
	"context"
	"log/slog"
	"strings"
	"time"

	"github.com/danielgtaylor/huma/v2"
	"github.com/danielgtaylor/huma/v2/adapters/humaecho"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/labstack/echo/v5"

	"github.com/ramoncjs3/fries/internal/audit"
	"github.com/ramoncjs3/fries/internal/auth"
	"github.com/ramoncjs3/fries/internal/authz"
	"github.com/ramoncjs3/fries/internal/config"
	"github.com/ramoncjs3/fries/internal/handler"
	"github.com/ramoncjs3/fries/internal/httpx"
	"github.com/ramoncjs3/fries/internal/middleware"
	"github.com/ramoncjs3/fries/internal/notify"
	"github.com/ramoncjs3/fries/internal/perm"
	_ "github.com/ramoncjs3/fries/internal/perm/modules" // 权限模块声明，靠 init 注册
	"github.com/ramoncjs3/fries/internal/repo"
	auditsvc "github.com/ramoncjs3/fries/internal/service/audit"
	deptsvc "github.com/ramoncjs3/fries/internal/service/department"
	platformsvc "github.com/ramoncjs3/fries/internal/service/platform"
	registrationsvc "github.com/ramoncjs3/fries/internal/service/registration"
	rolesvc "github.com/ramoncjs3/fries/internal/service/role"
	sasvc "github.com/ramoncjs3/fries/internal/service/service_account"
	settingssvc "github.com/ramoncjs3/fries/internal/service/settings"
	suppliersvc "github.com/ramoncjs3/fries/internal/service/supplier"
	usersvc "github.com/ramoncjs3/fries/internal/service/user"
)

// app 把一次运行需要的东西攒在一起。
//
// pool 可以为 nil —— `--selfcheck` 就是不连库把整个应用装配起来跑检查的，
// 那时 settings / checker / audit 都用不连库的替身。
type app struct {
	cfg      *config.Config
	log      *slog.Logger
	echo     *echo.Echo
	api      huma.API
	metrics  *middleware.Metrics
	pool     *pgxpool.Pool
	store    *repo.Store
	settings *config.Settings
	// platformAuth 是平台管理端的认证服务。和租户端的 auth 是**两套**，
	// 刻意不合并 —— 合并意味着每个方法里都要判「这是平台还是租户」，漏一处就打通了。
	platformAuth *auth.PlatformService
	checker      authz.Checker
	auth         *auth.Service
	// mailer 发信（忘记密码、邮箱验证）。未配发信通道时是 LogMailer，只记日志不真发。
	// 它是包在 AsyncMailer 里的异步发信器 —— 入队即返回，投递在后台（防枚举时序侧信道）。
	mailer notify.Mailer
	// emailDrainer 是上面 mailer 的异步层，优雅关闭时用来把队列排空（见 main.go）。
	emailDrainer *notify.AsyncMailer
	version      string
}

// newApp 装配整个应用：中间件、API 路由、健康检查。
//
// 中间件顺序是有讲究的，改之前先想清楚：
//  1. RequestID 放 Pre —— 路由没匹配上的 404 也要有 request_id
//  2. AccessLog / Metrics 在最外侧，才量得到真实耗时
//  3. **Recover 放在它们里面**：panic 被就地转成 500 错误再往外传，
//     日志和指标才记得到这次请求；Recover 放最外面的话，panic 会直接
//     穿过 AccessLog/Metrics，那条请求在日志和指标里就凭空消失了
//  4. 限流 → BodyLimit → Timeout 是「请求准入」
//  5. **Audit 在 Authenticate 外面**：认证中间件往请求 context 里塞主体，
//     外层的审计中间件在 next() 返回后就能读到「这是谁干的」
//  6. CSRF 在认证之后 —— 它要知道这个会话的 CSRF token 是什么
//  7. 授权跑在 huma 那一层（api.UseMiddleware），因为只有那里拿得到
//     「这个接口要什么权限点」
func newApp(ctx context.Context, cfg *config.Config, logger *slog.Logger, pool *pgxpool.Pool, version string) (*app, error) {
	a := &app{cfg: cfg, log: logger, pool: pool, version: version}

	if err := a.buildServices(ctx); err != nil {
		return nil, err
	}

	e := echo.New()
	e.Logger = logger
	e.HTTPErrorHandler = httpx.EchoErrorHandler
	if cfg.Server.TrustedProxy {
		e.IPExtractor = echo.ExtractIPFromXFFHeader()
	} else {
		// 默认不信任任何代理头，否则日志、审计和限流里的 IP 可以被随便伪造。
		e.IPExtractor = echo.ExtractIPDirect()
	}

	metrics := middleware.NewMetrics()
	// 两个维度：IP 那个必须在认证之前（登录接口靠它），租户那个必须在认证之后
	// （那时才知道是哪家公司）。少了后者，一个租户跑批量导入会让所有客户变慢
	// （MULTI-TENANCY.md §3.2 ⑦、§12.3）。
	// 多副本部署把限流状态放 PG（共享计数），否则每副本一份内存桶（默认、零延迟）。
	var limiter, tenantLimiter *middleware.RateLimiter
	if cfg.Server.SharedStateStore == config.SharedStatePostgres && a.store != nil {
		q := a.store.Unscoped()
		limiter = middleware.NewPgRateLimiter(q, logger)
		tenantLimiter = middleware.NewPgTenantRateLimiter(q, logger)
	} else {
		limiter = middleware.NewRateLimiter()
		tenantLimiter = middleware.NewTenantRateLimiter()
	}

	e.Pre(middleware.RequestID())
	e.Use(
		middleware.AccessLog(logger, skipAccessLog),
		metrics.Middleware(),
		middleware.Recover(logger),
		limiter.Middleware(),
		middleware.BodyLimit(cfg.Server.MaxBodyBytes),
		middleware.Timeout(cfg.Server.RequestTimeout),
		middleware.Audit(a.auditSink(), logger, newAuditSkipper(cfg.Server.APIPrefix)),
		middleware.Authenticate(a.auth, a.platformAuth,
			cfg.Server.APIPrefix+handler.PlatformPrefix),
		// 紧跟着 Authenticate：它要读主体身上的租户，早一格就什么都读不到。
		tenantLimiter.Middleware(),
		middleware.CSRF(a.sessionConfig(), a.platformSessionConfig(),
			cfg.Server.APIPrefix+handler.PlatformPrefix),
		middleware.Idempotency(a.idempotencyStore()),
	)

	group := e.Group(cfg.Server.APIPrefix)
	api := humaecho.NewWithGroup(e, group, httpx.NewConfig("fries API", version, cfg.Server.APIPrefix, cfg.Server.ExposeDocs))
	api.UseMiddleware(middleware.Authorize(api, a.checker))

	a.echo = e
	a.api = api
	a.metrics = metrics

	// 路由登记表是包级全局、注册有副作用（每调一次 Register* 就 append）。一个进程里
	// 建两次 app（测试就是这样）时先清一次，否则会累积成真实路由数的两倍。
	// 只清 routes，不碰 modules（那些是包级 var 一次性注册的，清了不回来）。
	perm.ResetRoutes()

	handler.RegisterSystem(api, version)
	handler.RegisterAuth(api, handler.NewAuth(a.auth, a.checker))
	handler.RegisterRegistration(api, handler.NewRegistration(
		registrationsvc.New(a.store, a.settings, a.mailer, a.cfg.Email.BaseURL)))
	handler.RegisterAudit(api, handler.NewAudit(auditsvc.New(a.store)))
	users := usersvc.New(a.store, a.settings)
	handler.RegisterDepartment(api, handler.NewDepartment(deptsvc.New(a.store), users))
	handler.RegisterRole(api, handler.NewRole(rolesvc.New(a.store)))
	handler.RegisterSupplier(api, handler.NewSupplier(suppliersvc.New(a.store)))
	handler.RegisterPlatform(api, handler.NewPlatform(a.platformAuth, platformsvc.New(a.store)))
	// 租户端和平台端共用一个 handler，差别只在走哪个 Realm 的路由和权限点
	settingsHandler := handler.NewSettings(settingssvc.New(a.settings))
	handler.RegisterSettings(api, settingsHandler)
	handler.RegisterPlatformSettings(api, settingsHandler)
	handler.RegisterUser(api, handler.NewUser(users))
	handler.RegisterServiceAccount(api, handler.NewServiceAccount(sasvc.New(a.store)))
	a.registerOps()

	return a, nil
}

// buildMailer 按配置造发信器，并包一层 AsyncMailer 让发信走后台（防枚举时序侧信道，见 async.go）。
// 底层：配了 SES 就用 SES，否则 LogMailer（只记日志不真发）。只依赖 config，不依赖库 ——
// selfcheck 也能安全构造，且不会真连 AWS。返回的异步层存到 a.emailDrainer，关停时排空队列。
func (a *app) buildMailer() notify.Mailer {
	var base notify.Mailer
	if a.cfg.Email.Provider == config.EmailProviderSES {
		base = notify.NewSESMailer(notify.SESConfig{
			Region:          a.cfg.Email.SES.Region,
			AccessKeyID:     a.cfg.Email.SES.AccessKeyID,
			SecretAccessKey: a.cfg.Email.SES.SecretAccessKey,
			From:            a.cfg.Email.SES.From,
		})
	} else {
		base = notify.NewLogMailer(a.log)
	}
	a.emailDrainer = notify.NewAsyncMailer(base, a.log, notify.AsyncOptions{})
	return a.emailDrainer
}

// buildServices 装配依赖数据库的那几层。pool 为 nil 时用不连库的替身。
func (a *app) buildServices(ctx context.Context) error {
	a.mailer = a.buildMailer()
	if a.pool == nil {
		a.store = nil
		a.settings = config.NewDefaultSettings()
		checker, err := authz.NewCasbinChecker(ctx, authz.EmptySource{})
		if err != nil {
			return err
		}
		a.checker = checker
		a.auth = auth.NewService(nil, a.settings, checker, a.sessionConfig(), a.mailer, a.cfg.Email.BaseURL)
		a.platformAuth = auth.NewPlatformService(nil, a.platformSessionConfig())
		return nil
	}

	a.store = repo.New(a.pool)

	settings, err := config.NewSettings(ctx, a.store)
	if err != nil {
		return err
	}
	a.settings = settings

	checker, err := authz.NewCasbinChecker(ctx, authz.NewDBPolicySource(a.store))
	if err != nil {
		return err
	}
	a.checker = checker

	a.auth = auth.NewService(a.store, settings, checker, a.sessionConfig(), a.mailer, a.cfg.Email.BaseURL)
	a.platformAuth = auth.NewPlatformService(a.store, a.platformSessionConfig())
	return nil
}

// sessionConfig 把 yaml 里的会话配置翻译成 auth 包要的形式。
func (a *app) sessionConfig() auth.SessionConfig {
	return auth.SessionConfig{
		Secret:     a.cfg.Session.Secret,
		TTL:        a.cfg.Session.TTL,
		CookieName: a.cfg.Session.CookieName,
		Secure:     a.cfg.Session.Secure,
	}
}

// platformCookieSuffix 拼在租户 cookie 名后面，得到平台端的 cookie 名。
//
// ⚠️ **两套 cookie 名必须不同**（MULTI-TENANCY.md §10.1）：同名的话，
// 一个人在同一浏览器里登了平台又登租户，后登的会把先登的顶掉，两边都莫名其妙掉线。
// 运维时也分不清哪张 cookie 是哪套身份。
const platformCookieSuffix = "_platform"

// platformSessionConfig 是平台端的会话配置。
//
// TTL 和 Secure 跟着租户端走（同一套部署），只有 cookie 名分开。
// 密钥共用没问题：CSRF token 是拿会话 id 签的，两套会话的 id 不会撞。
func (a *app) platformSessionConfig() auth.SessionConfig {
	cfg := a.sessionConfig()
	cfg.CookieName += platformCookieSuffix
	return cfg
}

func (a *app) auditSink() middleware.AuditSink {
	if a.store == nil {
		return audit.Discard{}
	}
	return audit.NewWriter(a.store)
}

// idempotencyStore 按配置选幂等键的后端：多副本用 postgres（共享一份见过的键），
// 否则内存版（单副本够用、零延迟）。selfcheck 没有库（store 为 nil），退回内存版。
func (a *app) idempotencyStore() middleware.IdempotencyStore {
	if a.cfg.Server.SharedStateStore == config.SharedStatePostgres && a.store != nil {
		return middleware.NewPgIdempotencyStore(a.store.Unscoped(), a.cfg.Server.IdempotencyTTL)
	}
	return middleware.NewMemoryIdempotencyStore(a.cfg.Server.IdempotencyTTL)
}

// watchChanges 订阅配置和授权变更：改完立即生效，多实例自动同步（DECISIONS.md §5）。
func (a *app) watchChanges(ctx context.Context) {
	if a.pool == nil {
		return
	}
	// 租户级配置：负载是那一行的 tenant_id，只刷那一个租户（MULTI-TENANCY.md §9.4）。
	// 负载为空（重连后补的那一次）才全量刷。
	go repo.Listen(ctx, a.pool, config.SettingsChannel, func(payload string) {
		if tenantID, err := uuid.Parse(payload); err == nil {
			a.reloadWithRetry(ctx, "租户配置", func(ctx context.Context) error {
				return a.settings.ReloadTenant(ctx, tenantID)
			}, slog.String("tenant_id", tenantID.String()))
			return
		}
		a.reloadWithRetry(ctx, "配置", a.settings.Reload)
	}, a.log)

	go repo.Listen(ctx, a.pool, config.PlatformSettingsChannel, func(string) {
		a.reloadWithRetry(ctx, "平台配置", a.settings.ReloadPlatform)
	}, a.log)

	go repo.Listen(ctx, a.pool, authz.PolicyChannel, func(string) {
		a.reloadWithRetry(ctx, "授权策略", a.checker.Reload)
	}, a.log)
}

// reloadAttempts / reloadBackoff 是刷新缓存失败后的重试次数和首次退避间隔（每次翻倍）。
// 是 var 而不是 const，只为了测试能把退避调小 —— 运行时别改。
var (
	reloadAttempts = 4
	reloadBackoff  = 200 * time.Millisecond
)

// reloadWithRetry 跑一次缓存刷新，失败就有界退避重试。
//
// **为什么需要**：`repo.Listen` 的回调没有返回值，Listen 那层不知道刷新失败了。
// 失败只打一条日志就放过的话，这次变更就**静默丢了** —— 内存里一直是旧的，
// 直到下次有人改动、或者监听连接断开重连补的那次全量刷才会跟上。
// 撞上一次数据库抖动，表现就是「改了权限没生效」，而除了日志没有任何信号。
//
// ⚠️ **重试用尽仍失败时保留旧缓存，这是有意的 fail-safe，别改成放行**：
// `authz.CasbinChecker.Reload` 会拒绝脏授权（比如有人绕过应用直接往
// role_permissions 里插了平台权限点），那种数据重试多少次都该失败，
// 而旧策略继续生效正是我们要的结果（见 checker.go 里那道兜底）。
func (a *app) reloadWithRetry(
	ctx context.Context, what string, reload func(context.Context) error, attrs ...slog.Attr,
) {
	args := make([]any, 0, len(attrs)+3)
	args = append(args, slog.String("what", what))
	for _, at := range attrs {
		args = append(args, at)
	}

	delay := reloadBackoff
	for attempt := 1; ; attempt++ {
		err := reload(ctx)
		if err == nil {
			done := args
			if attempt > 1 {
				done = append(append([]any{}, args...), slog.Int("attempt", attempt))
			}
			a.log.InfoContext(ctx, "缓存已刷新", done...)
			return
		}
		if attempt >= reloadAttempts || ctx.Err() != nil {
			a.log.ErrorContext(ctx, "刷新缓存失败，已用尽重试，保留旧缓存",
				append(append([]any{}, args...),
					slog.Int("attempts", attempt), slog.String("error", err.Error()))...)
			return
		}
		a.log.WarnContext(ctx, "刷新缓存失败，稍后重试",
			append(append([]any{}, args...),
				slog.Int("attempt", attempt), slog.Duration("retry_in", delay),
				slog.String("error", err.Error()))...)
		select {
		case <-ctx.Done():
			return
		case <-time.After(delay):
		}
		delay *= 2
	}
}

// opsPaths 是运维用的裸路由：不走 /api/v1，也不进 OpenAPI。
var opsPaths = struct{ Health, Ready, Metrics string }{
	Health:  "/healthz",
	Ready:   "/readyz",
	Metrics: "/metrics",
}

// skipAccessLog 过滤掉运维探针的访问日志 —— 每几秒一次，记了全是噪音。
func skipAccessLog(path string) bool {
	switch path {
	case opsPaths.Health, opsPaths.Ready, opsPaths.Metrics:
		return true
	}
	return strings.HasPrefix(path, "/debug/")
}

// newAuditSkipper 决定哪些请求不进审计表。
//
// 探针和文档页每分钟几十次，记进审计只会淹没真正要看的东西；
// **业务接口一律要记，读写都记**（DECISIONS.md §6）。
//
// 这里按**精确路径**匹配，不用 strings.Contains —— 将来真出现一个叫 docs 的业务模块，
// 模糊匹配会让它悄无声息地不再被审计。
func newAuditSkipper(apiPrefix string) middleware.PathSkipper {
	docPaths := map[string]bool{
		apiPrefix + "/docs":             true,
		apiPrefix + "/openapi.json":     true,
		apiPrefix + "/openapi.yaml":     true,
		apiPrefix + "/openapi-3.0.json": true,
		apiPrefix + "/openapi-3.0.yaml": true,
	}
	schemasPrefix := apiPrefix + "/schemas/"

	return func(path string) bool {
		if skipAccessLog(path) {
			return true
		}
		return docPaths[path] || strings.HasPrefix(path, schemasPrefix)
	}
}
