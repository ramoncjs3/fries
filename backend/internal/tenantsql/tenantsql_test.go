package tenantsql_test

import (
	"strings"
	"testing"

	"github.com/ramoncjs3/fries/internal/tenantsql"
)

// 这个文件守的是**分析器自己**（MULTI-TENANCY.md §1.2 ③）。
//
// 它是四层机械强制里的第三层，而不用 RLS 就意味着没有兜底网 ——
// 分析器漏判等于那一层不存在，而且**静默**：所有查询照样绿。
// 下面每条用例都对应一个真实漏过去的形状，改坏了当场就能看到。
//
// ⚠️ 同一份实现被两个地方调：构建期的 `gen lint-sql` 和运行期的 pgx tracer。
// 所以这里的每一条也同时是在守运行期那道网。

// analyze 跑一条查询正文，返回分析器报出来的问题。
func analyze(t *testing.T, sql string) []string {
	t.Helper()
	return tenantsql.Analyze(sql)
}

// TestBareBindMustNotCoverOtherTables 守的是「裸绑定不能连带放过别的表」。
//
// 曾经的判定是「一段 SQL 里有裸的 tenant_id 条件，就把所有不带别名的租户表
// 全算成绑好了」。于是下面这两条通过了检查，而它们都是真的跨租户写。
//
// 这一类特别危险，因为**别的几层都看不见它**：
//   - ForTenant 包装：查询有 tenant_id 参数，照样注入，看着完全正常
//   - 查回来的行核对（repo/assert.go）：核的是返回行，写操作没有返回行可核
//
// 只剩「有人记得写跨租户测试」这一条，那不是机制。
func TestBareBindMustNotCoverOtherTables(t *testing.T) {
	cases := []struct {
		name string
		sql  string
	}{
		{
			// 条件绑的是 departments，users 其实光着 ——
			// 传一串别家公司的 user_id 就能把人一起停掉（§10.8）。
			name: "批量按 id 写，条件挂在子查询的表上",
			sql: `UPDATE users SET status = 'disabled'
WHERE id = ANY(sqlc.arg('user_ids')::uuid[])
  AND department_id IN (SELECT id FROM departments WHERE tenant_id = sqlc.arg('tenant_id'))`,
		},
		{
			// 条件绑的是 roles，user_roles 光着。
			name: "条件挂在 EXISTS 里的表上",
			sql: `DELETE FROM user_roles
WHERE role_id = sqlc.arg('role_id')
  AND EXISTS (SELECT 1 FROM roles WHERE tenant_id = sqlc.arg('tenant_id'))`,
		},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := analyze(t, c.sql)
			if len(got) == 0 {
				t.Fatal("这条查询是跨租户的，分析器必须报出来")
			}
			// 报错要说清歧义在哪 —— 作者明明写了 tenant_id，
			// 只给一句「每处都要带条件」他会以为是检查器坏了。
			if !strings.Contains(got[0], "没写限定名") {
				t.Errorf("提示应该点出「条件没写限定名」，实际是：%s", got[0])
			}
		})
	}
}

// TestQualifiedBindPasses 确认没把正常写法一起拦掉。
func TestQualifiedBindPasses(t *testing.T) {
	cases := []struct {
		name string
		sql  string
	}{
		{
			name: "只有一张表，裸写没有歧义",
			sql: `UPDATE users SET status = 'disabled'
WHERE tenant_id = sqlc.arg('tenant_id') AND id = sqlc.arg('id')`,
		},
		{
			name: "多张表，每张都写了限定名",
			sql: `UPDATE users u SET status = 'disabled'
WHERE u.tenant_id = sqlc.arg('tenant_id')
  AND u.id = ANY(sqlc.arg('user_ids')::uuid[])
  AND u.department_id IN (
      SELECT d.id FROM departments d WHERE d.tenant_id = sqlc.arg('tenant_id'))`,
		},
		{
			name: "JOIN 上的等值传递也算绑上了",
			sql: `SELECT r.* FROM roles r
JOIN user_roles ur ON ur.role_id = r.id AND ur.tenant_id = r.tenant_id
WHERE r.tenant_id = sqlc.arg('tenant_id')`,
		},
		{
			name: "一张租户表都不碰",
			sql:  `SELECT * FROM tenants WHERE lower(code) = lower(sqlc.arg('code')::text)`,
		},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := analyze(t, c.sql); len(got) != 0 {
				t.Fatalf("这条查询是对的，不该报：%v", got)
			}
		})
	}
}

// TestRecursiveCTEStillChecked 守 §10.7：递归 CTE 的两半要分开查。
func TestRecursiveCTEStillChecked(t *testing.T) {
	got := analyze(t, `WITH RECURSIVE subtree AS (
    SELECT d.id AS node_id FROM departments d
    WHERE d.tenant_id = sqlc.arg('tenant_id') AND d.id = sqlc.arg('id')
    UNION ALL
    SELECT d.id FROM departments d JOIN subtree s ON d.parent_id = s.node_id
)
SELECT node_id FROM subtree`)
	if len(got) == 0 {
		t.Fatal("递归那一半没带租户条件，分析器必须报出来（§10.7）")
	}
}

// TestInsertNeedsTenantColumn 守 INSERT 的列名清单。
//
// 只看整条 SQL 里有没有 tenant_id 这个词是不够的 —— 见下面第二条。
func TestInsertNeedsTenantColumn(t *testing.T) {
	// tenant-exempt: 下面几条是**故意写坏的样本** —— 这个文件测的就是「分析器认不认得出坏 SQL」，
	//   样本必须真的坏。lint-sql 抓到它们说明它在正常工作。
	if got := analyze(t, `INSERT INTO users (id, username) VALUES ($1, $2)`); len(got) == 0 {
		t.Fatal("INSERT 的列里没有 tenant_id，必须报")
	}
	if got := analyze(t,
		`INSERT INTO users (username) SELECT username FROM users WHERE tenant_id = $1`); len(got) == 0 {
		t.Fatal("列里没有 tenant_id，光靠 WHERE 里出现过这个词不能算过")
	}
	if got := analyze(t, `INSERT INTO users (tenant_id, id) VALUES ($1, $2)`); len(got) != 0 {
		t.Fatalf("这条是对的，不该报：%v", got)
	}
}

// TestCommentsCannotSilenceTheCheck 守「注释里的 tenant_id 不算数」。
//
// 不去掉注释的话，一句「这里不用带 tenant_id」的注释就能让检查通过。
// 块注释同样要去 —— 只处理 `--` 的话，`/* tenant_id */` 就是个后门。
func TestCommentsCannotSilenceTheCheck(t *testing.T) {
	cases := []string{
		"SELECT * FROM users WHERE id = $1 -- 这里不用带 tenant_id",
		"SELECT * FROM users /* tenant_id */ WHERE id = $1",
		"/* tenant_id = $1 */\nSELECT * FROM users WHERE id = $1",
	}
	for _, sql := range cases {
		t.Run(sql, func(t *testing.T) {
			if got := analyze(t, sql); len(got) == 0 {
				t.Fatal("注释里的 tenant_id 不能让检查通过")
			}
		})
	}
}

// TestQueryNameIsReadableFromGeneratedSQL 守运行期兜底的前提。
//
// pgx 的 tracer 只拿得到 SQL 文本，认不出「这是哪条查询」就没法对豁免清单。
// 好在 sqlc 会把 `-- name: X :kind` 原样留在生成的 SQL 字符串里 ——
// **这条测试就是在钉住那个前提**：哪天 sqlc 不再留这一行，运行期兜底会
// 把所有豁免查询都当成违规，这里会先炸。
func TestQueryNameIsReadableFromGeneratedSQL(t *testing.T) {
	// tenant-exempt: 这是从 sqlcgen 里照抄的一条**已豁免**查询（DeleteDeadSessions），
	//   用来验证运行期能从 SQL 里读出查询名。它本身就不该带租户条件。
	// 和 sqlcgen 里的格式一字不差
	const generated = `-- name: DeleteDeadSessions :execrows
DELETE FROM sessions
WHERE expires_at < now() - interval '7 days'
`
	if got := tenantsql.QueryName(generated); got != "DeleteDeadSessions" {
		t.Fatalf("取查询名应该得到 DeleteDeadSessions，得到 %q", got)
	}
	// 手写的裸 SQL 没有这一行 —— 取不到名字要返回空串，由调用方按「没有豁免」处理
	if got := tenantsql.QueryName("SELECT 1"); got != "" {
		t.Fatalf("认不出名字时应该返回空串，得到 %q", got)
	}
}

// TestBoundTenantArgIndex 守值比对的取参逻辑。
func TestBoundTenantArgIndex(t *testing.T) {
	cases := []struct {
		name   string
		sql    string
		want   int
		wantOK bool
	}{
		{"裸写", "SELECT * FROM users WHERE tenant_id = $1 AND id = $2", 0, true},
		{"带限定名", "SELECT * FROM users u WHERE u.id = $1 AND u.tenant_id = $2", 1, true},
		{"带类型转换", "SELECT * FROM audit_logs WHERE tenant_id = $3::uuid", 2, true},
		{"等值传递不算绑到参数", "SELECT * FROM users u JOIN roles r ON r.tenant_id = u.tenant_id", 0, false},
		{"IS NULL 不算", "SELECT * FROM audit_logs WHERE tenant_id IS NULL", 0, false},
		{"注释里的不算", "SELECT * FROM users WHERE id = $1 -- tenant_id = $9", 0, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, ok := tenantsql.BoundTenantArgIndex(c.sql)
			if ok != c.wantOK {
				t.Fatalf("找到与否应该是 %v，得到 %v", c.wantOK, ok)
			}
			if ok && got != c.want {
				t.Errorf("参数下标应该是 %d，得到 %d", c.want, got)
			}
		})
	}
}

// TestIsDML 守「哪些语句要检查」。
//
// TRUNCATE 尤其要放过：测试基建每个用例前都会 `TRUNCATE users, ...` 清场，
// 那是**故意跨租户**的。拦住它等于所有集成测试都跑不起来。
func TestIsDML(t *testing.T) {
	// tenant-exempt: 这里测的是「哪些语句算 DML」，样本只要语法像就行，
	//   带不带租户条件跟这条测试无关。
	dml := []string{
		"SELECT * FROM users WHERE tenant_id = $1",
		"  insert into users (tenant_id) values ($1)",
		"-- name: X :many\nWITH RECURSIVE t AS (SELECT 1) SELECT * FROM t",
		// tenant-exempt: 同上，语法样本。
		"UPDATE users SET x = 1",
		"DELETE FROM users",
	}
	notDML := []string{
		"TRUNCATE users, roles RESTART IDENTITY CASCADE",
		"CREATE TABLE users (id uuid)",
		"ALTER TABLE users ADD COLUMN x int",
		"SET LOCAL lock_timeout = '5s'",
		"BEGIN",
		"LISTEN \"settings_changed\"",
	}
	for _, sql := range dml {
		if !tenantsql.IsDML(sql) {
			t.Errorf("应该算 DML：%s", sql)
		}
	}
	for _, sql := range notDML {
		if tenantsql.IsDML(sql) {
			t.Errorf("不该算 DML：%s", sql)
		}
	}
}

// TestTouchesGuardedTable 守「哪些表要管」。
func TestTouchesGuardedTable(t *testing.T) {
	// tenant-exempt: 这里测的是「哪些表要管」，样本是表名清单，不是要执行的查询。
	if !tenantsql.TouchesGuardedTable("SELECT * FROM users") {
		t.Error("users 是租户表，要管")
	}
	// 三张认证表 + 四张平台表结构上就没有 tenant_id（§3.2 ③、§6）
	for _, sql := range []string{
		"SELECT * FROM tenants",
		"SELECT * FROM platform_admins",
		"SELECT * FROM platform_sessions",
		"SELECT * FROM platform_settings",
		"SELECT 1",
	} {
		if tenantsql.TouchesGuardedTable(sql) {
			t.Errorf("不该管：%s", sql)
		}
	}
}

// TestCommaJoinIsCaught 守逗号 JOIN 这个盲点（§8.4）。
//
// rxTableWithAlias 只认 from/join 后紧跟的那一张表，`FROM a, b` 里逗号后面的 b 看不见 ——
// 它漏了租户条件也不会被报，而运行期兜底用的是同一个正则，两层一起瞎。
// 这里断言：逗号 JOIN 既被 Analyze 直接报问题，也被 TouchesGuardedTable 当成「要管」
// （否则运行期直接跳过它）。
func TestCommaJoinIsCaught(t *testing.T) {
	// tenant-exempt: 这些是喂给分析器的「逗号 JOIN 样本」，不是要执行的查询 —— 故意不带租户条件
	commaJoins := []string{
		"SELECT * FROM users u, departments d WHERE u.tenant_id = $1",
		"SELECT * FROM some_view v, users u WHERE v.id = u.id",
		"UPDATE roles SET name = $1 FROM users u, user_roles ur WHERE ur.role_id = roles.id",
	}
	for _, sql := range commaJoins {
		if len(analyze(t, sql)) == 0 {
			t.Errorf("逗号 JOIN 应被 Analyze 报出来：%s", sql)
		}
		if !tenantsql.TouchesGuardedTable(sql) {
			t.Errorf("逗号 JOIN 应被当成要管，否则运行期会跳过：%s", sql)
		}
	}

	// 显式 JOIN 不该被这条规则误伤。
	// tenant-exempt: 样本是查询形状，不是要执行的语句
	ok := "SELECT * FROM users u JOIN departments d ON d.tenant_id = u.tenant_id WHERE u.tenant_id = $1"
	for _, p := range analyze(t, ok) {
		if strings.Contains(p, "逗号 JOIN") {
			t.Errorf("显式 JOIN 被误判成逗号 JOIN：%s", ok)
		}
	}
}
