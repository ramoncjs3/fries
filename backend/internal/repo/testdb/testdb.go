// Package testdb 给集成测试起一个真 PostgreSQL 并跑完迁移。
//
// **不 mock 数据库**（DECISIONS.md §1）：软删除 + 部分唯一索引、分区表、触发器算的
// 哈希链，这些东西 mock 出来的假库全都验不到。
//
// 需要 Docker。`go test -short`（make dev-check）会跳过，`make check` 会真跑。
package testdb

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/testcontainers/testcontainers-go"
	"github.com/testcontainers/testcontainers-go/modules/postgres"
	"github.com/testcontainers/testcontainers-go/wait"

	"github.com/ramoncjs3/fries/internal/repo"
)

// containerImage 和 deploy/docker-compose.yml 里的保持一致，测的才是同一个 PG。
const containerImage = "postgres:17-alpine"

// startTimeout 是等容器就绪的时间。第一次要拉镜像，给足。
const startTimeout = 3 * time.Minute

// 每张会被测试写脏的表。roles / settings 里的种子数据要留着，不能一起清。
// 每个测试开始前要清空的表。
//
// ⚠️ **加了新表就要加到这里**。漏了的话上一个测试留下的数据会撞下一个测试的
// 唯一约束（部门编号、角色标识都是全局唯一的），表现成「单独跑过、一起跑就挂」。
//
// roles 清空之后要把内置 admin 角色重新插回去 —— 它是迁移里塞的种子数据，
// TRUNCATE 会一起带走（见 reset）。
// ⚠️ tenants 必须在列表里（MULTI-TENANCY.md §7.3）。它是所有租户表的父表，
// CASCADE 会把挂在下面的数据一起带走 —— 这正是想要的。
var truncatedTables = []string{
	"audit_logs", "sessions", "service_accounts", "user_roles", "users",
	"role_permissions", "roles", "departments", "settings", "tenants",
	"idempotency_keys", "rate_limits", "password_reset_tokens", "pending_registrations",
	// 平台端也要清 —— 漏了的话「引导首个平台管理员」在第二个用例里就不干活了
	// （它只在一个都没有时才跑），表现成「测试单独跑过、一起跑就挂」。
	"platform_sessions", "platform_admins",
}

var (
	once     sync.Once
	shared   *pgxpool.Pool
	startErr error

	// platformSeed 是迁移刚跑完时 platform_settings 的样子。
	//
	// 这张表**不能 TRUNCATE**：它装的是迁移种下的平台配置（审计保留期、产品名、
	// 以及租户设置的上下界）。清空了就再也回不来，后面每个用例都会看到
	// 「一条界都没有」，而那正是「没有对应行 = 不受限」的语义。
	//
	// 但也**不能不清**：有用例会改上下界（比如「平台收紧一档之后租户就调不动了」），
	// 不还原的话它会泄漏给后面每一个用例 —— 这一条实实在在栽过：
	// 配置管理的用例单独跑全绿、和整套一起跑就挂在「不能小于 20（平台的下限）」。
	//
	// 所以是**快照 + 还原**：启动时照一张，每个用例前恢复成那张。
	// 好处是种子数据只有迁移一处定义，这里不抄第二份，改迁移不用改测试。
	platformSeed []platformSetting
)

// platformSetting 是 platform_settings 的一行。
type platformSetting struct {
	key         string
	value       []byte
	description string
}

// TenantFixture 是一个测试租户，附带它自己的内置 admin 角色。
//
// 多租户之后**每个租户各有一个** admin 角色（MULTI-TENANCY.md §3.2 ①），
// 角色 key 只在租户内唯一，所以两个租户可以都叫 admin。
type TenantFixture struct {
	ID          uuid.UUID
	Code        string
	AdminRoleID uuid.UUID
}

// tenantIDs 是两个测试租户的固定 id。固定值让失败日志好读 ——
// 一眼能看出「这行属于 A 还是 B」。
var tenantIDs = [2]uuid.UUID{
	uuid.MustParse("01920000-0000-7000-8000-0000000000a1"),
	uuid.MustParse("01920000-0000-7000-8000-0000000000b1"),
}

// tenantCodes 是两个测试租户的公司代码。登录要用。
var tenantCodes = [2]string{"acme", "globex"}

// TwoTenants 建两个互不相干的测试租户。
//
// ⚠️ **跨租户测试必须两个租户都有数据**（MULTI-TENANCY.md §3.2 ⑧）。
// 只给 A 造数据、断言 B 查不到的话，B 查不到是因为**库里本来就没有 B 的东西**，
// 隔离有没有生效根本没测到 —— 那种断言把 `WHERE tenant_id = ?` 整个删掉也照样绿。
//
// 每个租户各带一个 builtin admin 角色（`*:*`），和迁移里的种子数据一致。
func TwoTenants(t *testing.T, pool *pgxpool.Pool) (TenantFixture, TenantFixture) {
	t.Helper()
	return NewTenant(t, pool, 0), NewTenant(t, pool, 1)
}

// NewTenant 建第 idx 个测试租户（0 或 1）。同一个 idx 反复调会撞唯一约束 ——
// 那是有意的：测试里「不小心建了两次同一个租户」应该当场炸掉。
func NewTenant(t *testing.T, pool *pgxpool.Pool, idx int) TenantFixture {
	t.Helper()
	ctx := t.Context()

	id := tenantIDs[idx]
	code := tenantCodes[idx]
	roleID := uuid.New()

	if _, err := pool.Exec(ctx,
		`INSERT INTO tenants (id, code, name) VALUES ($1, $2, $3)`,
		id, code, code+" 公司"); err != nil {
		t.Fatalf("建测试租户 %s 失败：%v", code, err)
	}
	if _, err := pool.Exec(ctx, `
		INSERT INTO roles (tenant_id, id, key, name, description, data_scope, builtin)
		VALUES ($1, $2, 'admin', '超级管理员', '拥有全部权限，内置角色不可删除', 'all', true)`,
		id, roleID); err != nil {
		t.Fatalf("建租户 %s 的内置角色失败：%v", code, err)
	}
	if _, err := pool.Exec(ctx,
		`INSERT INTO role_permissions (tenant_id, role_id, resource, action) VALUES ($1, $2, '*', '*')`,
		id, roleID); err != nil {
		t.Fatalf("给租户 %s 的内置角色赋权失败：%v", code, err)
	}
	return TenantFixture{ID: id, Code: code, AdminRoleID: roleID}
}

// Start 返回一个跑完迁移的库连接。
//
// 整个测试进程共用一个容器（起容器很贵），每个测试开始前清一次数据。
//
// 顺带**把运行期兜底打开**（MULTI-TENANCY.md §12.2）：从这里拿库的测试全程开着，
// 任何一条漏了租户条件的查询在返回结果那一刻就会 panic。
// 生产默认关（每行都过一遍反射，热路径上不该背这个成本）。
func Start(t *testing.T) *pgxpool.Pool {
	t.Helper()
	if testing.Short() {
		t.Skip("集成测试需要 Docker，-short 模式跳过")
	}
	repo.EnableTenantAssertions()

	once.Do(func() {
		shared, startErr = boot()
		if startErr == nil {
			startErr = snapshotPlatformSettings(context.Background(), shared)
		}
	})
	if startErr != nil {
		t.Fatalf("起测试数据库失败：%v", startErr)
	}

	reset(t, shared)
	return shared
}

// StartWithAppRole 起一个**独立**容器，在跑迁移**之前**先建好受限角色 fries_app，
// 于是迁移里那些 `IF EXISTS fries_app` 的 GRANT/REVOKE 会真的执行。
//
// 为什么单独一个容器：共用的测试库（Start）用 owner 连，superuser 绕过一切权限检查，
// 测不到「DB 层拒绝应用账号改删审计」这类约束 —— H1 漏洞就是这么漏过去的。
// 返回 owner 连接和 fries_app 连接，都是**不带租户 tracer** 的裸池：这个 helper 专测
// DB 层权限，查询会故意直接寻址分区、不带租户条件，不该被运行期兜底拦下。
// 容器随测试结束销毁。整个 make check 里只有审计防篡改一处用它，多起一个容器可以接受。
func StartWithAppRole(t *testing.T) (owner, app *pgxpool.Pool) {
	t.Helper()
	if testing.Short() {
		t.Skip("集成测试需要 Docker，-short 模式跳过")
	}
	ctx := t.Context()

	container, err := postgres.Run(ctx, containerImage,
		postgres.WithDatabase("fries_test"),
		postgres.WithUsername("fries"),
		postgres.WithPassword("fries"),
		postgres.BasicWaitStrategies(),
		testcontainers.WithWaitStrategy(
			wait.ForLog("database system is ready to accept connections").
				WithOccurrence(2).WithStartupTimeout(startTimeout)),
	)
	if err != nil {
		t.Fatalf("起 PostgreSQL 容器失败：%v", err)
	}
	t.Cleanup(func() {
		stopCtx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		_ = container.Terminate(stopCtx)
	})

	ownerDSN, err := container.ConnectionString(ctx, "sslmode=disable")
	if err != nil {
		t.Fatalf("取连接串失败：%v", err)
	}

	// 建受限角色，**必须在迁移之前**：迁移里的授权语句只在角色已存在时才执行。
	if err := createAppRole(ctx, ownerDSN); err != nil {
		t.Fatalf("建 fries_app 角色失败：%v", err)
	}
	if err := migrate(ctx, ownerDSN); err != nil {
		t.Fatalf("跑迁移失败：%v", err)
	}

	owner = plainPool(ctx, t, ownerDSN, "", "")
	app = plainPool(ctx, t, ownerDSN, "fries_app", "fries_app")
	return owner, app
}

// createAppRole 建受限角色。口令随便设，测试容器不对外。
func createAppRole(ctx context.Context, ownerDSN string) error {
	conn, err := pgx.Connect(ctx, ownerDSN)
	if err != nil {
		return fmt.Errorf("连接测试库: %w", err)
	}
	defer func() { _ = conn.Close(ctx) }()
	if _, err := conn.Exec(ctx, `CREATE ROLE fries_app LOGIN PASSWORD 'fries_app'`); err != nil {
		return fmt.Errorf("CREATE ROLE fries_app: %w", err)
	}
	return nil
}

// plainPool 建一个不带租户 tracer 的裸连接池，可选换成别的身份连。
// 不走 repo.NewPool 是有意的：那个会装运行期兜底 tracer，而这里的权限测试要发的
// 正是「不带租户条件、直接寻址分区」的查询，被 tracer 拦下就测不成了。
func plainPool(ctx context.Context, t *testing.T, dsn, user, pass string) *pgxpool.Pool {
	t.Helper()
	cfg, err := pgxpool.ParseConfig(dsn)
	if err != nil {
		t.Fatalf("解析连接串失败：%v", err)
	}
	if user != "" {
		cfg.ConnConfig.User = user
		cfg.ConnConfig.Password = pass
	}
	pool, err := pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		t.Fatalf("建连接池（%s）失败：%v", user, err)
	}
	t.Cleanup(pool.Close)
	return pool
}

func boot() (*pgxpool.Pool, error) {
	// 和 cmd/server 的 main 保持一致：整个进程按 UTC 跑，
	// 否则测试里看到的时间格式和线上不一样，等于没测（DECISIONS.md §2.5）。
	time.Local = time.UTC

	ctx, cancel := context.WithTimeout(context.Background(), startTimeout)
	defer cancel()

	container, err := postgres.Run(ctx, containerImage,
		postgres.WithDatabase("fries_test"),
		postgres.WithUsername("fries"),
		postgres.WithPassword("fries"),
		postgres.BasicWaitStrategies(),
		testcontainers.WithWaitStrategy(
			wait.ForLog("database system is ready to accept connections").
				WithOccurrence(2).WithStartupTimeout(startTimeout)),
	)
	if err != nil {
		return nil, fmt.Errorf("起 PostgreSQL 容器: %w", err)
	}

	dsn, err := container.ConnectionString(ctx, "sslmode=disable")
	if err != nil {
		return nil, fmt.Errorf("取连接串: %w", err)
	}

	if err := migrate(ctx, dsn); err != nil {
		return nil, err
	}

	// 走和生产**同一个**建池函数：连接参数（会话时区 UTC 等）一致，测试才测得到真行为。
	// 简单协议只在跑迁移时用（见 migrate）—— 两者的参数编码方式不一样，混用会让
	// jsonb 参数被当成 bytea 发过去，冒出「invalid input syntax for type json」。
	pool, err := repo.NewPool(ctx, dsn, repo.PoolOptions{
		MaxConns:        8,
		MinConns:        1,
		ConnMaxLifetime: time.Hour,
		ConnectTimeout:  30 * time.Second,
	})
	if err != nil {
		return nil, fmt.Errorf("建连接池: %w", err)
	}
	return pool, nil
}

// migrate 按文件名顺序执行迁移的 Up 段。
//
// 这里不引 goose 库：迁移文件的 Up 段本来就是一段合法 SQL，用简单协议一次发过去就行，
// 少一个依赖。真正的迁移执行仍然是 `make migrate`（goose），格式必须保持兼容。
func migrate(ctx context.Context, dsn string) error {
	cfg, err := pgx.ParseConfig(dsn)
	if err != nil {
		return fmt.Errorf("解析连接串: %w", err)
	}
	// 一个迁移文件里有多条语句，只有简单协议能一次执行完。
	cfg.DefaultQueryExecMode = pgx.QueryExecModeSimpleProtocol

	conn, err := pgx.ConnectConfig(ctx, cfg)
	if err != nil {
		return fmt.Errorf("连接测试库: %w", err)
	}
	defer func() { _ = conn.Close(ctx) }()

	dir, err := migrationsDir()
	if err != nil {
		return err
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		return fmt.Errorf("读迁移目录: %w", err)
	}

	files := make([]string, 0, len(entries))
	for _, e := range entries {
		if !e.IsDir() && strings.HasSuffix(e.Name(), ".sql") {
			files = append(files, e.Name())
		}
	}
	sort.Strings(files)

	for _, name := range files {
		raw, err := os.ReadFile(filepath.Join(dir, name))
		if err != nil {
			return fmt.Errorf("读 %s: %w", name, err)
		}
		up := raw
		if i := strings.Index(string(raw), "-- +goose Down"); i >= 0 {
			up = raw[:i]
		}
		if _, err := conn.Exec(ctx, string(up)); err != nil {
			return fmt.Errorf("执行迁移 %s: %w", name, err)
		}
	}
	return nil
}

// migrationsDir 从当前测试所在目录往上找 backend/db/migrations。
func migrationsDir() (string, error) {
	dir, err := os.Getwd()
	if err != nil {
		return "", err
	}
	for {
		candidate := filepath.Join(dir, "db", "migrations")
		if st, err := os.Stat(candidate); err == nil && st.IsDir() {
			return candidate, nil
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return "", fmt.Errorf("找不到 db/migrations 目录")
		}
		dir = parent
	}
}

// reset 清掉上一个测试留下的数据。
//
// 连租户一起清：多租户之后「内置 admin 角色」是**每个租户各一个**，没有全局那一份了，
// 所以这里不再回插种子角色 —— 需要租户的用例自己调 TwoTenants 造。
func reset(t *testing.T, pool *pgxpool.Pool) {
	t.Helper()
	ctx := t.Context()

	if _, err := pool.Exec(ctx,
		"TRUNCATE "+strings.Join(truncatedTables, ", ")+" RESTART IDENTITY CASCADE"); err != nil {
		t.Fatalf("清理测试数据失败：%v", err)
	}
	// 平台配置还原成迁移刚跑完的样子（理由见 platformSeed）。
	if err := restorePlatformSettings(ctx, pool); err != nil {
		t.Fatalf("还原平台配置失败：%v", err)
	}
	// 哈希链的链头也要清，否则新一轮的第一条会接在旧链后面。
	// 链头行由触发器按需重建（每个租户一行 + 一行平台级哨兵），这里删光就行。
	if _, err := pool.Exec(ctx, "DELETE FROM audit_chain_head"); err != nil {
		t.Fatalf("重置审计链失败：%v", err)
	}
}

// snapshotPlatformSettings 把迁移刚种下的平台配置照一份下来。
//
// tenant-exempt: platform_settings 是平台级表，本来就没有 tenant_id。
func snapshotPlatformSettings(ctx context.Context, pool *pgxpool.Pool) error {
	rows, err := pool.Query(ctx,
		`-- tenant-exempt: platform_settings 是平台级表，没有 tenant_id 这一列
		 SELECT key, value, description FROM platform_settings ORDER BY key`)
	if err != nil {
		return fmt.Errorf("读平台配置快照: %w", err)
	}
	defer rows.Close()

	platformSeed = platformSeed[:0]
	for rows.Next() {
		var it platformSetting
		if err := rows.Scan(&it.key, &it.value, &it.description); err != nil {
			return fmt.Errorf("扫平台配置快照: %w", err)
		}
		platformSeed = append(platformSeed, it)
	}
	return rows.Err()
}

// restorePlatformSettings 把平台配置恢复成快照的样子。
//
// 先删后插而不是 upsert：用例可能**新增**了一行（比如给某个 key 补一条上界），
// upsert 留不掉它。
func restorePlatformSettings(ctx context.Context, pool *pgxpool.Pool) error {
	// tenant-exempt: platform_settings 是平台级表，没有 tenant_id 这一列
	if _, err := pool.Exec(ctx, `DELETE FROM platform_settings`); err != nil {
		return fmt.Errorf("清平台配置: %w", err)
	}
	for _, it := range platformSeed {
		if _, err := pool.Exec(ctx,
			`-- tenant-exempt: platform_settings 是平台级表，没有 tenant_id 这一列
			 INSERT INTO platform_settings (key, value, description) VALUES ($1, $2, $3)`,
			it.key, it.value, it.description); err != nil {
			return fmt.Errorf("还原平台配置 %s: %w", it.key, err)
		}
	}
	return nil
}
