// Package config 加载 config/config.yaml。
//
// 只放**启动必需**的配置（DB、端口、密钥、日志级别）。业务可调项一律进 DB settings 表，
// 后台页面改完立即生效（DECISIONS.md §5）。**禁止 .env。**
package config

import (
	"errors"
	"fmt"
	"net/url"
	"os"
	"strings"
	"time"

	"github.com/go-viper/mapstructure/v2"
	"github.com/knadh/koanf/parsers/yaml"
	"github.com/knadh/koanf/providers/confmap"
	"github.com/knadh/koanf/providers/env"
	"github.com/knadh/koanf/providers/file"
	"github.com/knadh/koanf/v2"
)

// EnvPrefix 是环境变量覆盖前缀，`__` 表示层级：
// `FRIES_DATABASE__HOST=postgres` 覆盖 `database.host`。
//
// 只为容器化部署留这一条口子（compose 里 DB 主机名和本地不同），
// **配置的真相仍在 config.yaml**，不引入 .env。
const EnvPrefix = "FRIES_"

// PlaceholderSecret 是 config.example.yaml 里的占位密钥。
//
// 它长度够、能让本地开发和自检跑起来，但**绝不能上生产** ——
// 服务启动时发现是它就会大声警告。`make dev` 生成 config.yaml 时会自动换成随机值。
const PlaceholderSecret = "PLACEHOLDER-CHANGE-ME-openssl-rand-base64-32"

// minSessionSecretLen 是会话密钥的最小长度。
const minSessionSecretLen = 32

// 共享状态（限流器 / 幂等键）的存储后端取值。
const (
	SharedStateMemory   = "memory"
	SharedStatePostgres = "postgres"
)

// Config 是整个后端的启动配置。
type Config struct {
	Server   Server   `koanf:"server"`
	Database Database `koanf:"database"`
	Log      Log      `koanf:"log"`
	Session  Session  `koanf:"session"`
	Email    Email    `koanf:"email"`
}

// 发信通道取值。
const (
	EmailProviderNone = "none" // 不真发，只记日志（LogMailer）
	EmailProviderSES  = "ses"  // AWS SES
)

// Email 是发信配置。密钥这类敏感项走 config.yaml（不进库、不上页面），
// 页面可配的部分（是否启用、显示名）在 DB settings 里（DECISIONS.md §5）。
type Email struct {
	// Provider：none（默认，走 LogMailer 只记日志）/ ses。
	Provider string `koanf:"provider"`
	// BaseURL 是前端 app 的公开地址（如 https://app.example.com），拼「重置密码」这类
	// 邮件里的链接用。为空时链接里只带 token 路径（本地开发够用）。
	BaseURL string `koanf:"base_url"`
	SES     SES    `koanf:"ses"`
}

// SES 是 AWS SES 的连接配置。凭据是敏感项，只从 config.yaml 读。
type SES struct {
	Region          string `koanf:"region"`
	AccessKeyID     string `koanf:"access_key_id"`
	SecretAccessKey string `koanf:"secret_access_key"`
	// From 是发件人地址，必须是 SES 里验证过的地址/域名。
	From string `koanf:"from"`
}

// Server 是 HTTP 服务相关配置。
type Server struct {
	// Addr 是监听地址，形如 ":8080"。
	Addr string `koanf:"addr"`
	// APIPrefix 是接口前缀，固定 /api/v1（DECISIONS.md §4.8）。
	APIPrefix string `koanf:"api_prefix"`
	// ReadTimeout / WriteTimeout 是 net/http 层的读写超时。
	ReadTimeout  time.Duration `koanf:"read_timeout"`
	WriteTimeout time.Duration `koanf:"write_timeout"`
	// RequestTimeout 是单个请求的处理超时，超时返回 common.service_unavailable。
	RequestTimeout time.Duration `koanf:"request_timeout"`
	// ShutdownTimeout 是收到 SIGTERM 后等待存量请求收尾的时间。
	ShutdownTimeout time.Duration `koanf:"shutdown_timeout"`
	// MaxBodyBytes 是请求体大小上限，超过返回 400。
	MaxBodyBytes int64 `koanf:"max_body_bytes"`
	// IdempotencyTTL 是幂等键的记忆时长（DECISIONS.md §8.1）。
	IdempotencyTTL time.Duration `koanf:"idempotency_ttl"`
	// TrustedProxy 为 true 时才信任 X-Forwarded-For 里的客户端 IP。
	// 只有前面确实挂了自家 nginx 才打开，否则日志里的 IP 可被伪造。
	TrustedProxy bool `koanf:"trusted_proxy"`
	// TenantAssertions 打开跨租户的运行期兜底：ForTenant 查回来的每一行都核一遍
	// tenant_id，不等就 panic（MULTI-TENANCY.md §12.2）。
	//
	// **staging 打开，生产关掉。** 集成测试里始终是开的，不看这个配置。
	//
	// 为什么值得在 staging 开：那里有接近真实的数据量和并发，
	// 而漏了租户条件的查询在只有一份数据的测试库里**根本看不出来**
	// （§3.2 ⑧ 那个陷阱）。生产关掉是因为每行都要过一遍反射。
	TenantAssertions bool `koanf:"tenant_assertions"`
	// ExposeDocs 打开 `/api/v1/docs`、`/openapi`、`/schemas` 这三个交互文档端点。
	//
	// **默认关（生产安全）。** 它们不经过授权中间件，开着等于把整份接口清单
	// （含 `/platform/*` 平台管理端）摊给未登录的人做侦察 —— 平台端是最高价值目标，
	// 方案里明说尽量不暴露（MULTI-TENANCY.md §9.2）。本地开发在 config.example.yaml 里设 true。
	// ⚠️ 关掉不影响 `make gen-api`：它走 `-openapi` 离线导出，不碰这几个 HTTP 端点。
	ExposeDocs bool `koanf:"expose_docs"`
	// SharedStateStore 决定限流器和幂等键把「跨副本共享状态」放哪：
	//   memory   —— 每副本一份内存（默认）。单副本部署够用、零延迟。
	//   postgres —— 存进库，多副本共享。多副本部署必须用它，否则限流阈值放大 N 倍、
	//               幂等重放能溜过去（SCALING.md §1）。
	// ⚠️ postgres 版限流器每请求打一次库，是有代价的 —— 只有真多副本才值得开。
	SharedStateStore string `koanf:"shared_state_store"`
}

// Database 是 PostgreSQL 连接配置。
type Database struct {
	Host     string `koanf:"host"`
	Port     int    `koanf:"port"`
	User     string `koanf:"user"`
	Password string `koanf:"password"`
	Name     string `koanf:"name"`
	// SSLMode 对应 libpq 的 sslmode：disable / require / verify-full …
	SSLMode string `koanf:"sslmode"`
	// MaxConns / MinConns 是连接池上下限。
	MaxConns int32 `koanf:"max_conns"`
	MinConns int32 `koanf:"min_conns"`
	// ConnMaxLifetime 是单个连接的最长存活时间。
	ConnMaxLifetime time.Duration `koanf:"conn_max_lifetime"`
	// ConnectTimeout 是启动时探活数据库的超时。
	ConnectTimeout time.Duration `koanf:"connect_timeout"`
}

// Log 是日志配置。
type Log struct {
	// Level：debug / info / warn / error
	Level string `koanf:"level"`
	// Format：json（生产）/ text（本地看着舒服）
	Format string `koanf:"format"`
}

// Session 是会话配置。第 ② 步（认证与权限）开始用。
type Session struct {
	// Secret 是签发会话 cookie 的密钥，至少 32 字节。
	// 生成：openssl rand -base64 32
	Secret string `koanf:"secret"`
	// TTL 是会话有效期。
	TTL time.Duration `koanf:"ttl"`
	// CookieName 是会话 cookie 名。
	CookieName string `koanf:"cookie_name"`
	// Secure 为 true 时 cookie 只走 HTTPS。生产必须 true。
	Secure bool `koanf:"secure"`
}

// defaults 是所有配置项的默认值。config.yaml 只需要写要改的部分。
func defaults() map[string]any {
	return map[string]any{
		"server.addr":               ":8080",
		"server.api_prefix":         "/api/v1",
		"server.read_timeout":       "15s",
		"server.write_timeout":      "30s",
		"server.request_timeout":    "30s",
		"server.shutdown_timeout":   "15s",
		"server.max_body_bytes":     int64(10 << 20), // 10 MiB
		"server.idempotency_ttl":    "24h",
		"server.trusted_proxy":      false,
		"server.expose_docs":        false,
		"server.shared_state_store": "memory",

		"database.host":              "localhost",
		"database.port":              5432,
		"database.user":              "fries",
		"database.name":              "fries",
		"database.sslmode":           "disable",
		"database.max_conns":         20,
		"database.min_conns":         2,
		"database.conn_max_lifetime": "1h",
		"database.connect_timeout":   "10s",

		"email.provider": "none",

		"log.level":  "info",
		"log.format": "json",

		"session.ttl":         "12h",
		"session.cookie_name": "fries_session",
		"session.secure":      false,
	}
}

// Load 读取配置文件并叠加环境变量覆盖，然后校验。
//
// path 为空时按顺序找 config/config.yaml、../config/config.yaml
// （分别对应「仓库根目录运行」和「backend 目录运行」）。
func Load(path string) (*Config, error) {
	k := koanf.New(".")

	if err := k.Load(confmap.Provider(defaults(), "."), nil); err != nil {
		return nil, fmt.Errorf("加载默认配置: %w", err)
	}

	if path == "" {
		path = discover()
	}
	if path != "" {
		if err := k.Load(file.Provider(path), yaml.Parser()); err != nil {
			return nil, fmt.Errorf("读取配置文件 %s: %w", path, err)
		}
	}

	if err := k.Load(env.Provider(EnvPrefix, ".", envKeyMapper), nil); err != nil {
		return nil, fmt.Errorf("读取环境变量覆盖: %w", err)
	}

	var cfg Config
	if err := k.UnmarshalWithConf("", &cfg, koanf.UnmarshalConf{
		Tag: "koanf",
		DecoderConfig: &mapstructure.DecoderConfig{
			Result:           &cfg,
			WeaklyTypedInput: true,
			DecodeHook:       mapstructure.StringToTimeDurationHookFunc(),
		},
	}); err != nil {
		return nil, fmt.Errorf("解析配置: %w", err)
	}

	if err := cfg.Validate(); err != nil {
		return nil, err
	}
	return &cfg, nil
}

// envKeyMapper 把 FRIES_DATABASE__HOST 映射成 database.host。
func envKeyMapper(s string) string {
	s = strings.TrimPrefix(s, EnvPrefix)
	return strings.ReplaceAll(strings.ToLower(s), "__", ".")
}

// candidatePaths 是自动查找配置文件的位置，按优先级排列。
var candidatePaths = []string{"config/config.yaml", "../config/config.yaml"}

// discover 找一个存在的配置文件；都不存在则返回空串（全部走默认值）。
func discover() string {
	for _, p := range candidatePaths {
		if st, err := os.Stat(p); err == nil && !st.IsDir() {
			return p
		}
	}
	return ""
}

// Validate 校验配置，把所有问题一次报全。启动即失败好过跑起来才报错。
func (c *Config) Validate() error {
	var problems []error
	fail := func(format string, a ...any) {
		problems = append(problems, fmt.Errorf(format, a...))
	}

	if c.Server.Addr == "" {
		fail("server.addr 不能为空")
	}
	if !strings.HasPrefix(c.Server.APIPrefix, "/") {
		fail("server.api_prefix 必须以 / 开头，当前是 %q", c.Server.APIPrefix)
	}
	if c.Server.MaxBodyBytes <= 0 {
		fail("server.max_body_bytes 必须大于 0")
	}
	switch c.Server.SharedStateStore {
	case SharedStateMemory, SharedStatePostgres:
	default:
		fail("server.shared_state_store 只能是 %q 或 %q，当前是 %q",
			SharedStateMemory, SharedStatePostgres, c.Server.SharedStateStore)
	}
	switch c.Email.Provider {
	case EmailProviderNone:
	case EmailProviderSES:
		// 选了 SES 就必须把连接信息填齐 —— 缺一项到发信那一刻才炸，不如启动就拦。
		if c.Email.SES.Region == "" {
			fail("email.provider=ses 时 email.ses.region 必填")
		}
		if c.Email.SES.AccessKeyID == "" || c.Email.SES.SecretAccessKey == "" {
			fail("email.provider=ses 时 email.ses.access_key_id / secret_access_key 必填")
		}
		if c.Email.SES.From == "" {
			fail("email.provider=ses 时 email.ses.from 必填（且须是 SES 验证过的地址）")
		}
	default:
		fail("email.provider 只能是 %q 或 %q，当前是 %q",
			EmailProviderNone, EmailProviderSES, c.Email.Provider)
	}
	for name, d := range map[string]time.Duration{
		"server.read_timeout":     c.Server.ReadTimeout,
		"server.write_timeout":    c.Server.WriteTimeout,
		"server.request_timeout":  c.Server.RequestTimeout,
		"server.shutdown_timeout": c.Server.ShutdownTimeout,
		"server.idempotency_ttl":  c.Server.IdempotencyTTL,
	} {
		if d <= 0 {
			fail("%s 必须大于 0，当前是 %s", name, d)
		}
	}

	if c.Database.Host == "" {
		fail("database.host 不能为空")
	}
	if c.Database.Port <= 0 || c.Database.Port > 65535 {
		fail("database.port 不合法：%d", c.Database.Port)
	}
	if c.Database.User == "" {
		fail("database.user 不能为空")
	}
	if c.Database.Name == "" {
		fail("database.name 不能为空")
	}
	switch c.Database.SSLMode {
	case "disable", "allow", "prefer", "require", "verify-ca", "verify-full":
	default:
		fail("database.sslmode 不合法：%q", c.Database.SSLMode)
	}
	if c.Database.MaxConns < 1 {
		fail("database.max_conns 至少为 1")
	}
	if c.Database.MinConns < 0 || c.Database.MinConns > c.Database.MaxConns {
		fail("database.min_conns 必须在 0 和 max_conns(%d) 之间", c.Database.MaxConns)
	}

	switch c.Log.Level {
	case "debug", "info", "warn", "error":
	default:
		fail("log.level 只能是 debug / info / warn / error，当前是 %q", c.Log.Level)
	}
	switch c.Log.Format {
	case "json", "text":
	default:
		fail("log.format 只能是 json / text，当前是 %q", c.Log.Format)
	}

	// 会话 cookie 和 CSRF token 都靠它签名，短了就是可爆破的。
	if len(c.Session.Secret) < minSessionSecretLen {
		fail("session.secret 至少 %d 字节，用 openssl rand -base64 32 生成", minSessionSecretLen)
	}
	if c.Session.TTL <= 0 {
		fail("session.ttl 必须大于 0")
	}

	return errors.Join(problems...)
}

// DSN 拼出 PostgreSQL 连接串。密码做 URL 转义，含特殊字符也不会拼坏。
func (c *Config) DSN() string {
	u := &url.URL{
		Scheme: "postgres",
		User:   url.UserPassword(c.Database.User, c.Database.Password),
		Host:   fmt.Sprintf("%s:%d", c.Database.Host, c.Database.Port),
		Path:   "/" + c.Database.Name,
	}
	q := url.Values{}
	q.Set("sslmode", c.Database.SSLMode)
	u.RawQuery = q.Encode()
	return u.String()
}

// Redacted 返回可以安全打进日志的配置摘要（密码和密钥都不出现）。
func (c *Config) Redacted() map[string]any {
	return map[string]any{
		"server.addr":            c.Server.Addr,
		"server.api_prefix":      c.Server.APIPrefix,
		"server.request_timeout": c.Server.RequestTimeout.String(),
		"database.host":          c.Database.Host,
		"database.port":          c.Database.Port,
		"database.name":          c.Database.Name,
		"database.sslmode":       c.Database.SSLMode,
		"log.level":              c.Log.Level,
		"log.format":             c.Log.Format,
	}
}
