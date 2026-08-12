package repo_test

import (
	"strings"
	"testing"

	"github.com/google/uuid"

	"github.com/ramoncjs3/fries/internal/repo"
)

// 这一组守的是**运行期兜底的第二层**（MULTI-TENANCY.md §12.2）：核真正发出去的 SQL。
//
// 为什么需要它：assert.go 那层核的是查回来的行，所以它对**写操作**天生是瞎的
// （execrows 只回一个数字，没有行可核）。而「批量按 id 写」恰恰是审计里发现的
// 最危险的一类（§10.8）。这一层从另一个方向看同一件事 —— 不看结果，看语句。
//
// ⚠️ 这里的用例分两半，两半都要有：
//
//	该拦的拦住   —— 少了它这层就是装饰
//	该放过的放过 —— 少了它整个测试套件和 staging 都跑不起来，而且人会直接把开关关掉，
//	                那比没装更糟（误报会让人开始忽略兜底）
//
// 判据本身在 internal/tenantsql，那边有自己的一套用例。这里只测「tracer 怎么用它」。

// withAssertions 在这条用例期间打开运行期兜底，结束后恢复。
//
// 开关是进程级的，所以**不能 t.Parallel()** —— 关掉的那一刻别的用例可能正指望它开着。
func withAssertions(t *testing.T, on bool) {
	t.Helper()
	before := repo.TenantAssertionsEnabled()
	if on {
		repo.EnableTenantAssertions()
	} else {
		repo.DisableTenantAssertions()
	}
	t.Cleanup(func() {
		if before {
			repo.EnableTenantAssertions()
		} else {
			repo.DisableTenantAssertions()
		}
	})
}

// TestTracerCatchesMissingTenantCondition 是这一层的**核心用例**。
//
// 这几条 SQL 都是「构建期查不到」的形状 —— 动态拼出来的，不在 db/queries/*.sql 里，
// 也不是一整条 Go 字符串字面量。它们是 §1.2 说的「手写代码」缺口，
// 在这层网装上之前会安安静静地跑成功。
func TestTracerCatchesMissingTenantCondition(t *testing.T) {
	withAssertions(t, true)

	cases := []struct {
		name string
		sql  string
	}{
		{
			name: "整条都没带租户条件",
			// tenant-exempt: 这是**故意写坏的样本** —— 这条用例测的就是「兜底认不认得出坏 SQL」。
			sql: "SELECT id, username FROM users WHERE status = 'active'",
		},
		{
			name: "按 id 查一行也不例外（BOLA，§11.1）",
			// tenant-exempt: 同上，故意写坏的样本。
			sql: "SELECT * FROM users WHERE id = $1",
		},
		{
			name: "批量按 id 写，条件挂在子查询的表上（§10.8）",
			// tenant-exempt: 同上，故意写坏的样本。
			sql: `UPDATE users SET status = 'disabled'
			      WHERE id = ANY($1::uuid[])
			        AND department_id IN (SELECT id FROM departments WHERE tenant_id = $2)`,
		},
		{
			name: "递归 CTE 只给种子加了条件（§10.7）",
			// tenant-exempt: 同上，故意写坏的样本。
			sql: `WITH RECURSIVE subtree AS (
			          SELECT d.id AS node_id FROM departments d WHERE d.tenant_id = $1 AND d.id = $2
			          UNION ALL
			          SELECT d.id FROM departments d JOIN subtree s ON d.parent_id = s.node_id
			      ) SELECT node_id FROM subtree`,
		},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			msg := repo.TraceForTest(t.Context(), c.sql)
			if msg == "" {
				t.Fatal("这条 SQL 没绑住租户，运行期兜底必须炸")
			}
			// 消息要指向该看的地方，否则半年后没人知道这是什么
			if !strings.Contains(msg, "§12.2") {
				t.Errorf("panic 消息应该指向 MULTI-TENANCY.md §12.2，实际是：%s", msg)
			}
		})
	}
}

// TestTracerLetsLegitimateStatementsThrough 是另一半，同样重要。
//
// 误报会让人直接把开关关掉，那比没装更糟。
func TestTracerLetsLegitimateStatementsThrough(t *testing.T) {
	withAssertions(t, true)

	cases := []struct {
		name string
		sql  string
	}{
		{
			name: "带了租户条件",
			sql:  "SELECT * FROM users WHERE tenant_id = $1 AND id = $2",
		},
		{
			// 测试基建每个用例前都靠它清场，那是**故意跨租户**的。
			// 拦住它等于所有集成测试都跑不起来。
			name: "TRUNCATE 清场",
			// tenant-exempt: 测试清场语句，故意跨租户。
			sql: "TRUNCATE users, roles, departments RESTART IDENTITY CASCADE",
		},
		{
			name: "迁移的 DDL",
			// tenant-exempt: DDL 不读写行。
			sql: "ALTER TABLE users ADD COLUMN nickname varchar(64)",
		},
		{
			// ⚠️ 这两条才是真正在考 IsDML 的：它们在 FROM/UPDATE 后面提到了租户表，
			// 所以过不了「一张租户表都不碰」那道门，只能靠动词判断放过。
			//
			// 拿掉 IsDML 之后**整套集成测试都起不来** —— testdb 是把整个迁移文件
			// 一次性发给数据库的，而 00007 里就有一句
			// `UPDATE users SET tenant_id = '...' WHERE tenant_id IS NULL` 的回填。
			name: "迁移整批执行（第一个词是 SET，里面有回填语句）",
			// tenant-exempt: 这是迁移的回填语句，那一刻租户列刚建出来、全是 NULL。
			sql: `SET LOCAL lock_timeout = '5s';
			      ALTER TABLE users ADD COLUMN tenant_id uuid;
			      UPDATE users SET tenant_id = '019...ff' WHERE tenant_id IS NULL;`,
		},
		{
			name: "CREATE VIEW ... AS SELECT",
			// tenant-exempt: DDL，建视图本身不取行。
			sql: "CREATE VIEW active_users AS SELECT * FROM users WHERE status = 'active'",
		},
		{
			name: "审过的豁免查询（认证链路，§3.2 ③）",
			// tenant-exempt: 这是从 sqlcgen 里照抄的一条**已豁免**查询，它本身就不该带租户条件 ——
			//   这条用例验的正是「豁免清单认得出它」。
			sql: `-- name: DeleteDeadSessions :execrows
			      DELETE FROM sessions WHERE expires_at < now() - interval '7 days'`,
		},
		{
			name: "一张租户表都不碰",
			sql:  "SELECT * FROM tenants WHERE lower(code) = lower($1)",
		},
		{
			name: "探活",
			sql:  "SELECT 1",
		},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if msg := repo.TraceForTest(t.Context(), c.sql); msg != "" {
				t.Fatalf("这条语句是正当的，不该炸：%s", msg)
			}
		})
	}
}

// TestTracerRejectsUnknownQueryNames 守「认不出名字不等于豁免」。
//
// 豁免清单是按 sqlc 的查询名对的。手写的裸 SQL 没有那一行 `-- name:`，
// 于是取不到名字 —— 这时必须按**默认拒绝**处理。反过来（认不出就放过）
// 等于给所有手写 SQL 开了一张免票，而手写 SQL 正是这层网要覆盖的东西。
func TestTracerRejectsUnknownQueryNames(t *testing.T) {
	withAssertions(t, true)

	// tenant-exempt: 故意写坏的样本，而且故意没有 `-- name:` 那一行。
	const handWritten = "DELETE FROM sessions WHERE user_id = $1"
	if msg := repo.TraceForTest(t.Context(), handWritten); msg == "" {
		t.Fatal("认不出查询名的手写 SQL 不该被当成豁免")
	}

	// 编造一个不在清单里的查询名，同样不该放过
	// tenant-exempt: 故意写坏的样本，而且故意用了一个不存在的查询名。
	const fakeName = `-- name: TotallyMadeUp :many
	                  SELECT * FROM users WHERE status = 'active'`
	if msg := repo.TraceForTest(t.Context(), fakeName); msg == "" {
		t.Fatal("查询名不在豁免清单里，不该放过")
	}
}

// TestTracerComparesTenantValue 守的是**别的几层都做不到的那一条**：
// 条件在、语法对、静态检查也过，但绑的是**别人的**租户。
//
// 它守 §4.2 那条红线：租户的唯一来源是会话，不许从请求参数取。
// 那条规矩到这一层之前没有任何机械强制，全靠 review。
func TestTracerComparesTenantValue(t *testing.T) {
	withAssertions(t, true)

	session := uuid.New()
	other := uuid.New()
	const sql = "SELECT * FROM users WHERE tenant_id = $1 AND id = $2"

	t.Run("绑的是别人的租户，必须炸", func(t *testing.T) {
		ctx := repo.RequestTenantForTest(t.Context(), session)
		msg := repo.TraceForTest(ctx, sql, other, uuid.New())
		if msg == "" {
			t.Fatal("这次请求属于 session 那个组织，查的却是 other 的数据，必须炸")
		}
		if !strings.Contains(msg, "§4.2") {
			t.Errorf("panic 消息应该指向 §4.2（租户只能来自会话），实际是：%s", msg)
		}
	})

	t.Run("绑的就是本次请求的租户，放过", func(t *testing.T) {
		ctx := repo.RequestTenantForTest(t.Context(), session)
		if msg := repo.TraceForTest(ctx, sql, session, uuid.New()); msg != "" {
			t.Fatalf("租户对得上，不该炸：%s", msg)
		}
	})

	t.Run("这次请求没有租户就不比对", func(t *testing.T) {
		// 平台管理员和未认证请求都没有租户；后台任务、启动加载也没有。
		// 这些路径会按别的租户去查（比如平台端开组织时往新组织里写东西），
		// 比对就是误报。
		if msg := repo.TraceForTest(t.Context(), sql, other, uuid.New()); msg != "" {
			t.Fatalf("没有请求租户时不该比对：%s", msg)
		}
	})
}

// TestTracerIsOffByDefault 守「生产不背这个成本」。
//
// 每条 SQL 过一遍正则，热路径上不该默认开着。三档：集成测试始终开、
// staging 用 server.tenant_assertions 打开、生产关掉。
func TestTracerIsOffByDefault(t *testing.T) {
	withAssertions(t, false)

	// tenant-exempt: 故意写坏的样本 —— 这条用例要验的正是「关着的时候它不管」。
	const bad = "SELECT * FROM users WHERE id = $1"
	if msg := repo.TraceForTest(t.Context(), bad); msg != "" {
		t.Fatalf("兜底关着的时候不该检查，却炸了：%s", msg)
	}
}
