package config_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ramoncjs3/fries/internal/config"
)

func TestLoadExample(t *testing.T) {
	cfg, err := config.Load(filepath.Join("..", "..", "..", "config", "config.example.yaml"))
	if err != nil {
		t.Fatalf("样例配置必须能直接加载（新人 clone 下来第一件事就是复制它）：%v", err)
	}
	if cfg.Server.APIPrefix != "/api/v1" {
		t.Errorf("接口前缀应是 /api/v1，得到 %q", cfg.Server.APIPrefix)
	}
	if cfg.Server.RequestTimeout <= 0 {
		t.Error("时长类配置没解析成 time.Duration")
	}

	// 样例是给人复制去当生产配置的，两个「开了会出事」的开关必须默认关着
	if cfg.Server.TrustedProxy {
		t.Error("样例里 trusted_proxy 必须是 false，否则日志里的 IP 可被伪造")
	}
	if cfg.Server.TenantAssertions {
		t.Error("样例里 tenant_assertions 必须是 false —— 那是 staging 的开关，" +
			"生产开着等于给每一行数据加一次反射（MULTI-TENANCY.md §12.2）")
	}
}

func TestDSNEscapesPassword(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")
	write(t, path, `
session:
  secret: "test-session-secret-must-be-32-bytes-long"
database:
  host: db.internal
  port: 5433
  user: fries
  password: "p@ss:w/rd?"
  name: friesdb
  sslmode: require
`)

	cfg, err := config.Load(path)
	if err != nil {
		t.Fatal(err)
	}
	dsn := cfg.DSN()
	if strings.Contains(dsn, "p@ss:w/rd?") {
		t.Errorf("密码里的特殊字符必须转义，否则连接串会被拼坏：%s", dsn)
	}
	for _, want := range []string{"db.internal:5433", "/friesdb", "sslmode=require"} {
		if !strings.Contains(dsn, want) {
			t.Errorf("连接串里缺 %q：%s", want, dsn)
		}
	}
}

func TestEnvOverride(t *testing.T) {
	t.Setenv("FRIES_DATABASE__HOST", "postgres")
	t.Setenv("FRIES_LOG__LEVEL", "debug")

	cfg, err := config.Load(filepath.Join("..", "..", "..", "config", "config.example.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Database.Host != "postgres" {
		t.Errorf("环境变量没覆盖成功：%q", cfg.Database.Host)
	}
	if cfg.Log.Level != "debug" {
		t.Errorf("环境变量没覆盖成功：%q", cfg.Log.Level)
	}
}

func TestValidateReportsAllProblems(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")
	write(t, path, `
server:
  api_prefix: "api/v1"
database:
  port: 99999
  sslmode: whatever
log:
  level: chatty
session:
  secret: "太短了"
`)

	_, err := config.Load(path)
	if err == nil {
		t.Fatal("这份配置有 5 处错，必须加载失败")
	}
	for _, want := range []string{"api_prefix", "port", "sslmode", "level", "secret"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("错误信息里应该一次报全，缺 %q：\n%v", want, err)
		}
	}
}

func TestEmailSESRequiresFields(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")
	// provider=ses 但连接信息一项没填 —— 应在启动时就报，而不是发第一封信才炸。
	write(t, path, `
session:
  secret: "a-very-long-session-secret-32-bytes!!"
email:
  provider: ses
`)

	_, err := config.Load(path)
	if err == nil {
		t.Fatal("provider=ses 却没填 region/keys/from，必须加载失败")
	}
	for _, want := range []string{"region", "access_key_id", "from"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("错误信息里应提到缺失的 %q：\n%v", want, err)
		}
	}
}

// TestEmailDefaultsToNone 确认不写 email 段时默认 none（走 LogMailer），加载不报错。
func TestEmailDefaultsToNone(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")
	write(t, path, `
session:
  secret: "a-very-long-session-secret-32-bytes!!"
`)
	cfg, err := config.Load(path)
	if err != nil {
		t.Fatalf("默认配置应加载成功：%v", err)
	}
	if cfg.Email.Provider != config.EmailProviderNone {
		t.Errorf("email.provider 默认应是 %q，得到 %q", config.EmailProviderNone, cfg.Email.Provider)
	}
}

func write(t *testing.T, path, content string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
}
