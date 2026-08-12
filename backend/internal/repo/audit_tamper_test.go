package repo_test

import (
	"errors"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgconn"

	"github.com/ramoncjs3/fries/internal/repo/testdb"
)

// TestAuditTamperProtectionOnPartitions 守住 H1：审计防篡改必须在**分区**上生效，
// 不能只在父表上。
//
// audit_logs 是分区表，数据落在子分区 audit_logs_YYYYMM 里。PostgreSQL 直接寻址分区
// （DELETE FROM audit_logs_202608 ...）校验的是分区自己的 ACL，父表的 REVOKE 不下沉。
// 修复前应用账号 fries_app 能直接删改分区里的审计行；这个测试以真的受限角色连库，
// 断言那条路被 DB 拒绝（错误码 42501 insufficient_privilege）。
//
// 共用测试库用 owner 连（superuser 绕过权限检查），测不到这个 —— 所以专门起一个
// 带受限角色的库（testdb.StartWithAppRole）。
func TestAuditTamperProtectionOnPartitions(t *testing.T) {
	owner, app := testdb.StartWithAppRole(t)
	ctx := t.Context()

	// owner 先写一条审计（走父表，哈希链触发器会算 hash）。
	// tenant-exempt: 测的是 DB 层表权限，故意不带租户条件；审计写入本就允许 tenant_id 为空
	if _, err := owner.Exec(ctx, `
		INSERT INTO audit_logs (id, actor_type, resource, action)
		VALUES ($1, 'system', 'test', 'test')`, uuid.New()); err != nil {
		t.Fatalf("owner 写审计失败：%v", err)
	}

	// 取一个真实存在的分区名，不靠拼时间，免得月末边界抖动。
	var part string
	if err := owner.QueryRow(ctx, `
		SELECT c.relname
		FROM pg_inherits i
		JOIN pg_class p ON p.oid = i.inhparent
		JOIN pg_class c ON c.oid = i.inhrelid
		WHERE p.relname = 'audit_logs'
		ORDER BY c.relname
		LIMIT 1`).Scan(&part); err != nil {
		t.Fatalf("找审计分区失败：%v", err)
	}

	// fries_app 能写审计（走父表），这是正常路径，必须成功。
	// tenant-exempt: 测的是 DB 层表权限，故意不带租户条件
	if _, err := app.Exec(ctx, `
		INSERT INTO audit_logs (id, actor_type, resource, action)
		VALUES ($1, 'system', 'test', 'test')`, uuid.New()); err != nil {
		t.Fatalf("fries_app 应当能写审计，却失败了：%v", err)
	}

	// 下面每一条都必须被 DB 以「权限不足」拒绝。
	cases := []struct {
		name string
		sql  string
	}{
		// tenant-exempt: 测的是 DB 层表/分区权限，这些改删语句故意不带租户条件
		{"改父表", `UPDATE audit_logs SET action = 'tampered'`},
		{"删父表", `DELETE FROM audit_logs`},
		{"直接改分区", `UPDATE ` + part + ` SET action = 'tampered'`},
		{"直接删分区", `DELETE FROM ` + part}, // ← H1 修复前这一条会成功
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			_, err := app.Exec(ctx, c.sql)
			if err == nil {
				t.Fatalf("fries_app 竟然能 %s，审计防篡改没生效（H1）", c.name)
			}
			var pgErr *pgconn.PgError
			if !errors.As(err, &pgErr) || pgErr.Code != "42501" {
				t.Fatalf("期望权限不足（42501），得到：%v", err)
			}
		})
	}
}

// TestAuditRolloverPartitionLocked 守住修复的第二半：由 ensure_audit_partition
// 滚动出来的**未来**分区也必须是收了权的，不能只有迁移时预建的那两个安全。
func TestAuditRolloverPartitionLocked(t *testing.T) {
	owner, app := testdb.StartWithAppRole(t)
	ctx := t.Context()

	// 造一个远期分区（现有预建覆盖不到），模拟后台任务滚动。
	var part string
	if err := owner.QueryRow(ctx,
		`SELECT ensure_audit_partition(now() + interval '6 months')`).Scan(&part); err != nil {
		t.Fatalf("滚动审计分区失败：%v", err)
	}

	_, err := app.Exec(ctx, `DELETE FROM `+part)
	if err == nil {
		t.Fatalf("fries_app 竟然能删滚动出来的分区 %s，ensure_audit_partition 没收权", part)
	}
	var pgErr *pgconn.PgError
	if !errors.As(err, &pgErr) || pgErr.Code != "42501" {
		t.Fatalf("期望权限不足（42501），得到：%v", err)
	}
}
