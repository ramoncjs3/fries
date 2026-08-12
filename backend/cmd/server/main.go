// Command server 是 fries 的后端主程序。
//
//	server                      # 正常启动
//	server -config path.yaml    # 指定配置文件
//	server --selfcheck          # 启动自检：纯内存、不连库、秒级（DECISIONS.md §3.7）
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/labstack/echo/v5"

	"github.com/ramoncjs3/fries/internal/auth"
	"github.com/ramoncjs3/fries/internal/config"
	"github.com/ramoncjs3/fries/internal/httpx"
	"github.com/ramoncjs3/fries/internal/repo"
)

// version 由编译期注入：go build -ldflags "-X main.version=$(git describe)"
var version = "dev"

func main() {
	// **整个进程按 UTC 跑**（DECISIONS.md §2.5）。
	//
	// 不这么做的话：pgx 把 timestamptz 解码成的 time.Time 带的是本机时区，
	// JSON 序列化出来就是 `+08:00` 而不是约定的带 Z 的 RFC3339。
	// 靠「每处记得写 .UTC()」是守不住的 —— 少写一处就漂一处。
	time.Local = time.UTC

	configPath := flag.String("config", "", "配置文件路径，留空则依次找 config/config.yaml、../config/config.yaml")
	selfcheck := flag.Bool("selfcheck", false, "只做启动自检（纯内存、不连库），检查完即退出")
	dumpOpenAPI := flag.Bool("openapi", false, "把 OpenAPI 文档打到 stdout 后退出（前端生成 TS 类型用）")
	showVersion := flag.Bool("version", false, "打印版本号后退出")
	flag.Parse()

	if *showVersion {
		fmt.Println(version) //nolint:forbidigo // 就是要打到 stdout
		return
	}

	cfg, err := config.Load(*configPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "配置有问题，服务没启动：\n%v\n", err)
		os.Exit(1)
	}

	logger := newLogger(cfg.Log)
	slog.SetDefault(logger)

	if *selfcheck {
		os.Exit(runSelfcheck(cfg, logger))
	}

	if *dumpOpenAPI {
		os.Exit(dumpOpenAPISpec(cfg, logger))
	}

	if err := run(cfg, logger); err != nil {
		logger.Error("服务异常退出", slog.String("error", err.Error()))
		os.Exit(1)
	}
}

// run 起服务，并在收到 SIGINT / SIGTERM 时优雅关闭。
func run(cfg *config.Config, logger *slog.Logger) error {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	pool, err := repo.NewPool(ctx, cfg.DSN(), repo.PoolOptions{
		MaxConns:        cfg.Database.MaxConns,
		MinConns:        cfg.Database.MinConns,
		ConnMaxLifetime: cfg.Database.ConnMaxLifetime,
		ConnectTimeout:  cfg.Database.ConnectTimeout,
	})
	if err != nil {
		return err
	}
	defer pool.Close()

	app, err := newApp(ctx, cfg, logger, pool, version)
	if err != nil {
		return err
	}

	// 首次启动时建管理员账号；已经有用户就什么都不做。
	// 平台管理员**先于**租户管理员引导：它是整个系统权限的源头（§10.10）。
	platformAdmin, err := auth.BootstrapPlatform(ctx, app.store)
	if err != nil {
		return fmt.Errorf("引导首个平台管理员: %w", err)
	}
	if platformAdmin.Created {
		printInitialPlatformAdmin(platformAdmin, logger)
	}

	admin, err := auth.Bootstrap(ctx, app.store)
	if err != nil {
		return err
	}
	if admin.Created {
		printInitialAdmin(admin, logger)
	}
	// 引导可能刚建了用户和角色绑定，让授权策略立刻跟上。
	if err := app.checker.Reload(ctx); err != nil {
		return err
	}

	if cfg.Session.Secret == config.PlaceholderSecret {
		logger.Warn("还在用样例里的占位会话密钥，任何人都能伪造 CSRF token",
			slog.String("怎么办", "config.yaml 里换成 openssl rand -base64 32 生成的值"))
	}

	// 跨租户运行期兜底（MULTI-TENANCY.md §12.2）。**开着这件事必须说出来** ——
	// 它会把「查回了别人的行」变成 panic，运维看到进程挂掉时得知道是谁干的。
	if cfg.Server.TenantAssertions {
		repo.EnableTenantAssertions()
		logger.Warn("跨租户运行期兜底已开启：查回属于别的组织的行会直接 panic",
			slog.String("适用", "staging；生产该关掉，每行都要过一遍反射"))
	}

	app.warnIfAuditTamperable(ctx)
	app.warnIfOrphanBounds(ctx)
	app.watchChanges(ctx)

	tasks := app.newTaskRunner()
	tasks.Start(ctx)
	defer tasks.Wait()

	logger.Info("服务启动", slog.String("version", version), slog.Any("config", cfg.Redacted()))

	start := echo.StartConfig{
		Address:         cfg.Server.Addr,
		HideBanner:      true,
		HidePort:        true,
		GracefulTimeout: cfg.Server.ShutdownTimeout,
		OnShutdownError: func(err error) {
			logger.Error("优雅关闭超时，仍有请求没收尾", slog.String("error", err.Error()))
		},
		BeforeServeFunc: func(s *http.Server) error {
			s.ReadTimeout = cfg.Server.ReadTimeout
			s.WriteTimeout = cfg.Server.WriteTimeout
			return nil
		},
	}

	// ctx 被取消时 StartConfig 会自己走优雅关闭流程，等存量请求收尾。
	if err := start.Start(ctx, app.echo); err != nil && !errors.Is(err, http.ErrServerClosed) {
		return err
	}

	// HTTP 存量请求都收尾了（不会再有人入队），把异步邮件队列排空再退。
	if app.emailDrainer != nil {
		drainCtx, cancel := context.WithTimeout(context.Background(), cfg.Server.ShutdownTimeout)
		if err := app.emailDrainer.Shutdown(drainCtx); err != nil {
			logger.Warn("异步邮件队列没排空就超时了，可能有信没发出去", slog.String("error", err.Error()))
		}
		cancel()
	}

	logger.Info("服务已退出")
	return nil
}

// printInitialAdmin 把首个管理员的账号密码打给人看。
//
// 日志可能被采集走，所以再往 stdout 明文打一份 —— 本机部署的人直接就能看到。
func printInitialAdmin(admin auth.BootstrapResult, logger *slog.Logger) {
	logger.Warn("已创建首个管理员账号，密码只显示这一次",
		slog.String("tenant_code", admin.TenantCode),
		slog.String("username", admin.Username))

	// 多租户之后登录框有三格，**公司代码不给出来就登不进去** —— 它是随机生成的
	// （MULTI-TENANCY.md §10.9：叫 default 的话谁都猜得到）。
	line := strings.Repeat("=", 60)
	fmt.Printf("%s\n公司代码：  %s\n初始管理员：%s\n初始密码：  %s\n"+
		"（密码只显示这一次，登录后会强制改密）\n%s\n",
		line, admin.TenantCode, admin.Username, admin.Password, line) //nolint:forbidigo // 就是要打给人看
}

// printInitialPlatformAdmin 把首个平台管理员的凭据打给人看。
//
// 和租户端那份分开打，因为**登录入口不一样**：平台端在 /platform，
// 而且不用填公司代码（平台管理员不属于任何组织）。
func printInitialPlatformAdmin(admin auth.BootstrapResult, logger *slog.Logger) {
	logger.Warn("已创建首个平台管理员，密码只显示这一次",
		slog.String("username", admin.Username))

	line := strings.Repeat("=", 60)
	fmt.Printf("%s\n【平台管理端】登录入口：/platform\n"+
		"平台管理员：%s\n初始密码：  %s\n"+
		"（密码只显示这一次，登录后会强制改密；平台端不填公司代码）\n%s\n",
		line, admin.Username, admin.Password, line) //nolint:forbidigo // 就是要打给人看
}

// newLogger 按配置造 slog logger。
func newLogger(cfg config.Log) *slog.Logger {
	var level slog.Level
	switch cfg.Level {
	case "debug":
		level = slog.LevelDebug
	case "warn":
		level = slog.LevelWarn
	case "error":
		level = slog.LevelError
	default:
		level = slog.LevelInfo
	}

	// 外面统一包一层：每条日志自动带 request_id 和 tenant_id（MULTI-TENANCY.md §12.1）
	opts := &slog.HandlerOptions{Level: level}
	if cfg.Format == "text" {
		return slog.New(httpx.NewLogHandler(slog.NewTextHandler(os.Stdout, opts)))
	}
	return slog.New(httpx.NewLogHandler(slog.NewJSONHandler(os.Stdout, opts)))
}
