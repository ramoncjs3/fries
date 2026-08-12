//go:build !genonly

package main

import (
	"context"
	"flag"
	"fmt"

	"github.com/jackc/pgx/v5"

	"github.com/ramoncjs3/fries/internal/auth"
	"github.com/ramoncjs3/fries/internal/config"
)

// devAdminPassword 是本地开发用的固定管理员密码。
//
// 它弱到过不了默认策略（至少 10 位 + 大小写数字混合），所以这个子命令
// 会顺手把本地库里的密码策略放宽。**这也正是它只准跑在本机的原因。**
const devAdminPassword = "admin"

// localHosts 是允许执行的数据库主机白名单。
//
// 这个命令会把管理员密码设成 `admin` 并关掉密码强度策略 —— 一旦对着
// 别的机器跑就是把生产打穿，所以宁可写死白名单，也不给 `-force` 之类的后门。
var localHosts = map[string]bool{
	"localhost": true,
	"127.0.0.1": true,
	"::1":       true,
}

// runDevAdmin 把 admin 的密码重置成 `admin`，并放宽本地库的密码策略。
//
// 用途只有一个：本地开发时不想记随机生成的 bootstrap 密码。
// 重建数据库之后重跑一次即可（DECISIONS.md §12）。
func runDevAdmin(_ string, args []string) error {
	fs := flag.NewFlagSet("dev-admin", flag.ContinueOnError)
	path := fs.String("config", "", "配置文件路径")
	if err := fs.Parse(args); err != nil {
		return err
	}

	cfg, err := config.Load(*path)
	if err != nil {
		return err
	}
	if !localHosts[cfg.Database.Host] {
		return fmt.Errorf("拒绝执行：数据库主机是 %q，这个命令只准对本机库跑", cfg.Database.Host)
	}

	ctx := context.Background()
	conn, err := pgx.Connect(ctx, cfg.DSN())
	if err != nil {
		return fmt.Errorf("连数据库: %w", err)
	}
	defer func() { _ = conn.Close(ctx) }()

	tx, err := conn.Begin(ctx)
	if err != nil {
		return fmt.Errorf("开事务: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	// 多租户之后本地库里应该恰好有一个租户（迁移建的那个）。
	// 登录要敲公司代码，所以这里得把它查出来告诉人。
	var tenantID, tenantCode string
	if err := tx.QueryRow(ctx,
		`SELECT id::text, code FROM tenants ORDER BY created_at LIMIT 1`).
		Scan(&tenantID, &tenantCode); err != nil {
		return fmt.Errorf("找本地租户（库是不是没跑迁移？）: %w", err)
	}

	// 先放宽策略。不放宽的话，登录进去之后改密码那一步反而会被自己的策略卡住。
	// 密码策略现在是**租户级**的，要带上 tenant_id（MULTI-TENANCY.md §7.2）。
	for key, value := range map[string]string{
		config.KeyPasswordMinLength:  "1",
		config.KeyPasswordRequireMix: "false",
	} {
		if _, err := tx.Exec(ctx, `
			INSERT INTO settings (tenant_id, key, value)
			VALUES ($1::uuid, $2, $3::jsonb)
			ON CONFLICT (tenant_id, key) DO UPDATE SET value = excluded.value`,
			tenantID, key, value); err != nil {
			return fmt.Errorf("放宽 %s: %w", key, err)
		}
	}

	// 顺手清掉失败计数和锁定 —— 试错试到锁了的话，光改密码还是登不进去。
	tag, err := tx.Exec(ctx, `
		UPDATE users
		   SET password_hash        = $1,
		       password_changed_at  = now(),
		       must_change_password = false,
		       failed_attempts      = 0,
		       locked_until         = NULL
		 WHERE tenant_id = $2::uuid AND username = 'admin' AND deleted_at IS NULL`,
		auth.HashPassword(devAdminPassword), tenantID)
	if err != nil {
		return fmt.Errorf("重置 admin 密码: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return fmt.Errorf("库里没有 admin 用户，先跑一次 make dev 让它 bootstrap 出来")
	}

	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("提交: %w", err)
	}

	fmt.Printf("✓ 本地 admin 密码已重置为 %s（密码策略已放宽，仅限本机）\n", devAdminPassword)
	fmt.Printf("  登录时「公司代码」填：%s\n", tenantCode)
	return nil
}

func init() {
	extraCommands = append(extraCommands,
		command{"dev-admin", "把本地 admin 密码重置为 admin（只准对本机库跑）", runDevAdmin})
}
