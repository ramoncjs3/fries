package repo_test

import (
	"context"
	"errors"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/ramoncjs3/fries/internal/repo"
	"github.com/ramoncjs3/fries/internal/repo/testdb"
)

// 这一组是整套多租户改造的**验收标准**（MULTI-TENANCY.md 第 ② 步）：
//
//	拿 A 租户的上下文去读、去写 B 租户的数据，必须失败。
//
// 不用 PostgreSQL RLS 就意味着数据库那一层没有兜底网（§1.1）。这组测试
// 加上 ForTenant 包装、SQL 静态检查、MustTenant 中间件，是仅有的防线。
//
// ⚠️ 两个陷阱，写这类测试时最容易踩：
//
//  1. **两个租户都必须有数据**（§3.2 ⑧）。只给 A 造数据、断言 B 查不到的话，
//     B 查不到是因为库里本来就没有 B 的东西 —— 把 `WHERE tenant_id = ?` 整个删掉，
//     那种断言照样绿。所以下面每一处都是两边各造一份，再互相去够对方的。
//
//  2. **写也要测**（§10.8）。读的时候大家都记得加条件，按 id 更新时很容易觉得
//     「id 是唯一的，够了」—— 那正是 OWASP API Top 10 第一名的 BOLA（§11.1）。
//     所以每个写接口都要断言「拿 A 的句柄改 B 的 id，影响行数是 0」，
//     而且改完还要回头确认 B 的数据**原样没动**。

// tenantData 是一个租户里造好的一套数据。
type tenantData struct {
	tenant     testdb.TenantFixture
	q          *repo.TenantQueries
	userID     uuid.UUID
	deptID     uuid.UUID
	roleID     uuid.UUID
	supplierID uuid.UUID // 生成器产出的 supplier 模块，验它的 ForTenant 隔离也到位
}

func seedTenant(t *testing.T, store *repo.Store, tenant testdb.TenantFixture) tenantData {
	t.Helper()
	ctx := t.Context()
	q := store.ForTenant(tenant.ID)

	dept, err := q.CreateDepartment(ctx, repo.CreateDepartmentArgs{
		ID: uuid.New(), Name: "研发部", Code: "RD", Status: "active",
	})
	if err != nil {
		t.Fatalf("[%s] 建部门失败：%v", tenant.Code, err)
	}

	user, err := q.CreateManagedUser(ctx, repo.CreateManagedUserArgs{
		ID: uuid.New(), Username: "zhangsan", DisplayName: "张三",
		PasswordHash: "x", Status: "active", DepartmentID: &dept.ID,
	})
	if err != nil {
		t.Fatalf("[%s] 建用户失败：%v", tenant.Code, err)
	}

	role, err := q.CreateRole(ctx, repo.CreateRoleArgs{
		ID: uuid.New(), Key: "viewer", Name: "只读", Description: "",
		DataScope: "self", Status: "active",
	})
	if err != nil {
		t.Fatalf("[%s] 建角色失败：%v", tenant.Code, err)
	}

	// 内置 admin 角色绑到这个人身上 —— CountActiveAdmins 那条护栏要用（§3.2 ①）
	if err := q.AssignUserRole(ctx, repo.AssignUserRoleArgs{
		UserID: user.ID, RoleID: tenant.AdminRoleID,
	}); err != nil {
		t.Fatalf("[%s] 赋内置角色失败：%v", tenant.Code, err)
	}

	// 生成器产出的 supplier 模块也造一条 —— 验它走的是同一套 ForTenant 隔离。
	supplier, err := q.CreateSupplier(ctx, repo.CreateSupplierArgs{
		ID: uuid.New(), Name: "供应商-" + tenant.Code,
	})
	if err != nil {
		t.Fatalf("[%s] 建供应商失败：%v", tenant.Code, err)
	}

	return tenantData{
		tenant: tenant, q: q, userID: user.ID, deptID: dept.ID, roleID: role.ID,
		supplierID: supplier.ID,
	}
}

// setup 造两个租户，**每个都有一整套数据**。
func setup(t *testing.T) (a, b tenantData) {
	t.Helper()
	pool := testdb.Start(t)
	store := repo.New(pool)
	ta, tb := testdb.TwoTenants(t, pool)
	return seedTenant(t, store, ta), seedTenant(t, store, tb)
}

// ---------------------------------------------------------------- 读

func TestCrossTenantListsSeeOnlyOwnRows(t *testing.T) {
	a, b := setup(t)
	ctx := t.Context()

	users, err := a.q.ListUsers(ctx, repo.ListUsersArgs{Limit: 100, DepartmentIds: []uuid.UUID{}})
	if err != nil {
		t.Fatal(err)
	}
	if len(users) != 1 {
		t.Fatalf("A 的用户列表应该只有 1 个人，得到 %d 个", len(users))
	}
	if users[0].User.ID != a.userID {
		t.Errorf("A 看到的不是自己的人：%s", users[0].User.ID)
	}
	// B 明明也有一个叫 zhangsan 的人 —— 列表里一个都不该出现
	for _, u := range users {
		if u.User.ID == b.userID {
			t.Fatalf("A 的用户列表里出现了 B 的人 %s", b.userID)
		}
	}

	depts, err := a.q.ListDepartments(ctx, repo.ListDepartmentsArgs{})
	if err != nil {
		t.Fatal(err)
	}
	if len(depts) != 1 || depts[0].ID != a.deptID {
		t.Fatalf("A 的部门列表应该只有自己那一个，得到 %d 个", len(depts))
	}

	roles, err := a.q.ListRoles(ctx, repo.ListRolesArgs{Limit: 100})
	if err != nil {
		t.Fatal(err)
	}
	// 内置 admin + viewer，各租户各一套
	if len(roles) != 2 {
		t.Fatalf("A 的角色列表应该有 2 个（内置 admin + viewer），得到 %d 个", len(roles))
	}
	for _, r := range roles {
		if r.ID == b.roleID || r.ID == b.tenant.AdminRoleID {
			t.Fatalf("A 的角色列表里出现了 B 的角色 %s", r.ID)
		}
	}
}

func TestCrossTenantGetByIDFails(t *testing.T) {
	a, b := setup(t)
	ctx := t.Context()

	// 「按 id 查一行」也必须带 tenant_id —— 这是 BOLA 的正面（§11.1）。
	// 拿到的应该是「不存在」，而不是「无权限」：后者等于确认了这个 id 真的存在（§11.2）。
	cases := []struct {
		name string
		run  func() error
	}{
		{"GetUserByID", func() error {
			_, err := a.q.GetUserByID(ctx, b.userID)
			return err
		}},
		{"GetUserWithDepartment", func() error {
			_, err := a.q.GetUserWithDepartment(ctx, b.userID)
			return err
		}},
		{"GetDepartment", func() error {
			_, err := a.q.GetDepartment(ctx, b.deptID)
			return err
		}},
		{"GetRole", func() error {
			_, err := a.q.GetRole(ctx, b.roleID)
			return err
		}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if err := c.run(); !errors.Is(err, pgx.ErrNoRows) {
				t.Fatalf("拿 A 的上下文查 B 的 id 应该查不到，得到 err=%v", err)
			}
		})
	}
}

// TestCrossTenantSuppliersIsolated 验**生成器产出的模块**（supplier）也守跨租户隔离：
// 它走的是和手写模块同一套 ForTenant 句柄，这里把「列表只看自己 / 按别家 id 查不到 /
// 改删别家命中 0 行 / 别家数据原样没动」在生成的 CRUD 上跑一遍。
func TestCrossTenantSuppliersIsolated(t *testing.T) {
	a, b := setup(t)
	ctx := t.Context()

	// 1) List 只看到自己的那一条。
	rows, err := a.q.ListSuppliers(ctx, repo.ListSuppliersArgs{Limit: 100})
	if err != nil {
		t.Fatalf("A 列供应商失败：%v", err)
	}
	if len(rows) != 1 || rows[0].ID != a.supplierID {
		t.Fatalf("A 应只看到自己那一条供应商，得到 %d 条", len(rows))
	}

	// 2) 按 B 的 id Get → 查不到（不是「无权限」，§11.2）。
	if _, err := a.q.GetSupplier(ctx, b.supplierID); !errors.Is(err, pgx.ErrNoRows) {
		t.Fatalf("拿 A 查 B 的供应商 id 应查不到，得到 err=%v", err)
	}

	// 3) 拿 A 的句柄改 B 的供应商 → 命中 0 行（:one 返回 ErrNoRows）。
	if _, err := a.q.UpdateSupplier(ctx, repo.UpdateSupplierArgs{
		ID: b.supplierID, Name: "被篡改", Version: 1,
	}); !errors.Is(err, pgx.ErrNoRows) {
		t.Fatalf("拿 A 改 B 的供应商应命中 0 行，得到 err=%v", err)
	}

	// 4) 拿 A 的句柄删 B 的供应商 → 影响 0 行（:execrows）。
	affected, err := a.q.SoftDeleteSupplier(ctx, repo.SoftDeleteSupplierArgs{
		ID: b.supplierID, Version: 1,
	})
	if err != nil {
		t.Fatalf("A 删 B 的供应商报错：%v", err)
	}
	if affected != 0 {
		t.Fatalf("拿 A 删 B 的供应商应影响 0 行，实际 %d", affected)
	}

	// 5) B 的供应商原样还在。
	if _, err := b.q.GetSupplier(ctx, b.supplierID); err != nil {
		t.Fatalf("B 的供应商不该被 A 的操作动到：%v", err)
	}
}

func TestCrossTenantCountsAreScoped(t *testing.T) {
	a, b := setup(t)
	ctx := t.Context()

	// ⚠️ 这一条是「最后一个管理员」护栏的地基（§3.2 ①）。
	// 原来 CountActiveAdmins 是一条不带租户条件的全表 count，多租户下就变成：
	// A 公司还有 admin，B 公司删掉自己最后一个 admin 时检查照样通过 ——
	// **B 公司被锁在门外，只能上数据库救。**
	//
	// 排除掉自己之后，A 应该数出 0 个（A 只有一个管理员），
	// 而不是把 B 的管理员也数进来。
	n, err := a.q.CountActiveAdmins(ctx, a.userID)
	if err != nil {
		t.Fatal(err)
	}
	if n != 0 {
		t.Fatalf("排除自己之后 A 应该没有别的管理员了，数出 %d 个（把 B 的人数进来了？）", n)
	}
	// B 那边同理 —— 两边都要断言，不然「返回 0」可能只是查询恒空
	if n, err := b.q.CountActiveAdmins(ctx, uuid.New()); err != nil || n != 1 {
		t.Fatalf("B 应该正好有 1 个管理员，得到 %d（err=%v）", n, err)
	}

	if n, err := a.q.CountUsers(ctx); err != nil || n != 1 {
		t.Fatalf("A 应该正好有 1 个用户，得到 %d（err=%v）", n, err)
	}
	if n, err := a.q.CountDepartmentUsers(ctx, &b.deptID); err != nil || n != 0 {
		t.Fatalf("A 数 B 的部门成员应该是 0，得到 %d（err=%v）", n, err)
	}
	if n, err := a.q.CountRoleUsers(ctx, b.tenant.AdminRoleID); err != nil || n != 0 {
		t.Fatalf("A 数 B 的角色成员应该是 0，得到 %d（err=%v）", n, err)
	}
}

func TestCrossTenantSubtreeStopsAtBoundary(t *testing.T) {
	a, b := setup(t)
	ctx := t.Context()

	// 递归 CTE 的**递归那一半**最容易漏租户条件（§10.7）：
	// 种子带了就完事，递归部分只靠 parent_id 关联，看着「跟着种子走自然就在租户里」。
	ids, err := a.q.ListDepartmentSubtreeIDs(ctx, a.deptID)
	if err != nil {
		t.Fatal(err)
	}
	if len(ids) != 1 || ids[0] != a.deptID {
		t.Fatalf("A 的子树应该只有自己一个节点，得到 %v", ids)
	}

	// 拿 A 的上下文去要 B 的子树：连种子都不该匹配上
	ids, err = a.q.ListDepartmentSubtreeIDs(ctx, b.deptID)
	if err != nil {
		t.Fatal(err)
	}
	if len(ids) != 0 {
		t.Fatalf("A 不该取到 B 的部门子树，得到 %v", ids)
	}
}

// ---------------------------------------------------------------- 写

func TestCrossTenantWritesAffectZeroRows(t *testing.T) {
	a, b := setup(t)
	ctx := t.Context()

	// 先记下 B 的原样，最后回头对
	beforeUser := mustGetUser(t, b, b.userID)
	beforeDept := mustGetDept(t, b, b.deptID)

	t.Run("UpdateManagedUser", func(t *testing.T) {
		_, err := a.q.UpdateManagedUser(ctx, repo.UpdateManagedUserArgs{
			ID: b.userID, Version: beforeUser.Version,
			DisplayName: "被别家改了", Status: "disabled",
		})
		if !errors.Is(err, pgx.ErrNoRows) {
			t.Fatalf("拿 A 的上下文改 B 的用户应该影响 0 行，得到 err=%v", err)
		}
	})

	t.Run("SoftDeleteUser", func(t *testing.T) {
		_, err := a.q.SoftDeleteUser(ctx, repo.SoftDeleteUserArgs{
			ID: b.userID, Version: beforeUser.Version,
		})
		if !errors.Is(err, pgx.ErrNoRows) {
			t.Fatalf("拿 A 的上下文删 B 的用户应该影响 0 行，得到 err=%v", err)
		}
	})

	t.Run("ResetUserPassword", func(t *testing.T) {
		rows, err := a.q.ResetUserPassword(ctx, repo.ResetUserPasswordArgs{
			ID: b.userID, PasswordHash: "被别家重置了",
		})
		if err != nil {
			t.Fatal(err)
		}
		if rows != 0 {
			t.Fatalf("拿 A 的上下文重置 B 的密码应该影响 0 行，得到 %d 行", rows)
		}
	})

	t.Run("SetUsersDepartment", func(t *testing.T) {
		// 批量按 id 改是 BOLA 的重灾区：传一串别家公司的 user_id 就能把人
		// 从他们的部门里摘出来，返回的影响行数还成了一个存在性探针。
		rows, err := a.q.SetUsersDepartment(ctx, repo.SetUsersDepartmentArgs{
			UserIds: []uuid.UUID{b.userID}, DepartmentID: nil,
		})
		if err != nil {
			t.Fatal(err)
		}
		if rows != 0 {
			t.Fatalf("拿 A 的上下文批量改 B 的人应该影响 0 行，得到 %d 行", rows)
		}
	})

	t.Run("UpdateDepartment", func(t *testing.T) {
		_, err := a.q.UpdateDepartment(ctx, repo.UpdateDepartmentArgs{
			ID: b.deptID, Version: beforeDept.Version,
			Name: "被别家改了", Code: "HACKED", Status: "disabled",
		})
		if !errors.Is(err, pgx.ErrNoRows) {
			t.Fatalf("拿 A 的上下文改 B 的部门应该影响 0 行，得到 err=%v", err)
		}
	})

	t.Run("SoftDeleteDepartment", func(t *testing.T) {
		_, err := a.q.SoftDeleteDepartment(ctx, repo.SoftDeleteDepartmentArgs{
			ID: b.deptID, Version: beforeDept.Version,
		})
		if !errors.Is(err, pgx.ErrNoRows) {
			t.Fatalf("拿 A 的上下文删 B 的部门应该影响 0 行，得到 err=%v", err)
		}
	})

	t.Run("SoftDeleteRole", func(t *testing.T) {
		_, err := a.q.SoftDeleteRole(ctx, repo.SoftDeleteRoleArgs{ID: b.roleID, Version: 0})
		if !errors.Is(err, pgx.ErrNoRows) {
			t.Fatalf("拿 A 的上下文删 B 的角色应该影响 0 行，得到 err=%v", err)
		}
	})

	t.Run("ClearUserRoles", func(t *testing.T) {
		// :exec 不返回影响行数，所以直接看结果：B 的人还挂着他的角色
		if err := a.q.ClearUserRoles(ctx, b.userID); err != nil {
			t.Fatal(err)
		}
		roleIDs, err := b.q.ListRoleIDsOfUser(ctx, b.userID)
		if err != nil {
			t.Fatal(err)
		}
		if len(roleIDs) != 1 {
			t.Fatalf("B 的人应该还挂着 1 个角色，现在有 %d 个 —— 被 A 摘掉了", len(roleIDs))
		}
	})

	t.Run("ClearRolePermissions", func(t *testing.T) {
		if err := a.q.ClearRolePermissions(ctx, b.tenant.AdminRoleID); err != nil {
			t.Fatal(err)
		}
		points, err := b.q.ListRolePermissions(ctx, b.tenant.AdminRoleID)
		if err != nil {
			t.Fatal(err)
		}
		if len(points) != 1 {
			t.Fatalf("B 的内置 admin 应该还有通配权限，现在有 %d 条 —— 被 A 清空了", len(points))
		}
	})

	// ⚠️ 最要紧的一步：确认上面那些写**真的一行都没改到**。
	// 只断言「返回 0 行」是不够的 —— 万一某条 SQL 改了行却报了 0，就漏过去了。
	afterUser := mustGetUser(t, b, b.userID)
	if afterUser.DisplayName != beforeUser.DisplayName ||
		afterUser.Status != beforeUser.Status ||
		afterUser.PasswordHash != beforeUser.PasswordHash ||
		afterUser.DeletedAt != nil ||
		afterUser.Version != beforeUser.Version {
		t.Fatalf("B 的用户被改动了：改前 %+v，改后 %+v", beforeUser, afterUser)
	}
	if afterUser.DepartmentID == nil || *afterUser.DepartmentID != b.deptID {
		t.Fatalf("B 的用户被移出了部门：%v", afterUser.DepartmentID)
	}

	afterDept := mustGetDept(t, b, b.deptID)
	if afterDept.Name != beforeDept.Name || afterDept.Code != beforeDept.Code ||
		afterDept.Status != beforeDept.Status || afterDept.Version != beforeDept.Version {
		t.Fatalf("B 的部门被改动了：改前 %+v，改后 %+v", beforeDept, afterDept)
	}
}

// ---------------------------------------------------------------- 插入

func TestCrossTenantInsertCannotReferenceOtherTenant(t *testing.T) {
	a, b := setup(t)
	ctx := t.Context()

	// 复合外键在数据库这一层杜绝跨租户引用（§2.2.1）。
	// 这是应用层漏掉时的最后一道 —— 建人的时候塞一个别家的部门 id，写不进去。
	_, err := a.q.CreateManagedUser(ctx, repo.CreateManagedUserArgs{
		ID: uuid.New(), Username: "lisi", DisplayName: "李四",
		PasswordHash: "x", Status: "active", DepartmentID: &b.deptID,
	})
	if err == nil {
		t.Fatal("把人分到别家公司的部门里居然成功了 —— 复合外键 fk_users_department 没生效")
	}

	// 部门挂到别家的部门下面，同样写不进去
	_, err = a.q.CreateDepartment(ctx, repo.CreateDepartmentArgs{
		ID: uuid.New(), ParentID: &b.deptID, Name: "子部门", Code: "SUB", Status: "active",
	})
	if err == nil {
		t.Fatal("把部门挂到别家公司的部门下面居然成功了 —— 复合外键 fk_departments_parent 没生效")
	}
}

// ---------------------------------------------------------------- 事务

func TestTenantBindingSurvivesTransaction(t *testing.T) {
	a, b := setup(t)
	ctx := t.Context()

	// 事务里拿到的句柄必须仍然绑着同一个租户（§9.6）。
	// 退化成裸句柄的话，多步写包（改角色权限、将来的开租户）整段绕过强制机制 ——
	// 而那些恰恰都在事务里。
	err := a.q.InTx(ctx, func(q *repo.TenantQueries) error {
		if q.TenantID() != a.tenant.ID {
			t.Fatalf("事务里的句柄换了租户：%s", q.TenantID())
		}
		_, err := q.GetUserByID(ctx, b.userID)
		if !errors.Is(err, pgx.ErrNoRows) {
			t.Fatalf("事务里拿 A 的句柄查 B 的人应该查不到，得到 err=%v", err)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}

func mustGetUser(t *testing.T, d tenantData, id uuid.UUID) repo.User {
	t.Helper()
	u, err := d.q.GetUserByID(context.WithoutCancel(t.Context()), id)
	if err != nil {
		t.Fatalf("[%s] 读用户 %s 失败：%v", d.tenant.Code, id, err)
	}
	return u
}

func mustGetDept(t *testing.T, d tenantData, id uuid.UUID) repo.Department {
	t.Helper()
	dept, err := d.q.GetDepartment(context.WithoutCancel(t.Context()), id)
	if err != nil {
		t.Fatalf("[%s] 读部门 %s 失败：%v", d.tenant.Code, id, err)
	}
	return dept
}

// ---------------------------------------------------------------- 权限兜底护栏（第 ④ 步）

// TestLastAdminGuardIsPerTenant 是第 ④ 步的验收标准：
//
//	**一个租户删不掉自己最后一个 admin，也锁不死别人。**
//
// ⚠️ 这条护栏原来靠一条**不带任何租户条件**的全表 count（`CountActiveAdmins`）。
// 多租户下那就错得很危险：A 公司还有 admin，B 公司删掉自己最后一个 admin 时
// 检查照样通过 —— **B 公司被锁在门外，只能上数据库救**（§3.2 ①）。
//
// 这里直接测那条 count 的口径：service 层的 ensureNotLastAdmin 完全建立在它上面。
func TestLastAdminGuardIsPerTenant(t *testing.T) {
	a, b := setup(t)
	ctx := t.Context()

	// 两家公司各有一个管理员（setup 里给每个租户的人绑了内置 admin 角色）
	someoneElse := uuid.New()
	for _, d := range []tenantData{a, b} {
		n, err := d.q.CountActiveAdmins(ctx, someoneElse)
		if err != nil {
			t.Fatal(err)
		}
		if n != 1 {
			t.Fatalf("[%s] 应该正好数出 1 个管理员，得到 %d —— "+
				"数多了说明把别家的人算进来了，护栏会误放行", d.tenant.Code, n)
		}
	}

	// A 要删掉自己唯一的管理员：排除他本人之后应该一个都不剩 → 护栏必须拦
	remaining, err := a.q.CountActiveAdmins(ctx, a.userID)
	if err != nil {
		t.Fatal(err)
	}
	if remaining != 0 {
		t.Fatalf("A 排除掉自己之后不该还有管理员，得到 %d —— "+
			"B 公司的管理员被算进了 A 的护栏，A 会被放行删掉自己最后一个 admin", remaining)
	}

	// 反过来：A 再加一个管理员，**不能**让 B 的护栏跟着松掉
	second := uuid.New()
	if _, err := a.q.CreateManagedUser(ctx, repo.CreateManagedUserArgs{
		ID: second, Username: "admin2", DisplayName: "第二个管理员",
		PasswordHash: "x", Status: "active",
	}); err != nil {
		t.Fatal(err)
	}
	if err := a.q.AssignUserRole(ctx, repo.AssignUserRoleArgs{
		UserID: second, RoleID: a.tenant.AdminRoleID,
	}); err != nil {
		t.Fatal(err)
	}

	if n, err := a.q.CountActiveAdmins(ctx, a.userID); err != nil || n != 1 {
		t.Fatalf("A 现在排除自己还剩 1 个管理员，得到 %d（err=%v）", n, err)
	}
	if n, err := b.q.CountActiveAdmins(ctx, b.userID); err != nil || n != 0 {
		t.Fatalf("A 加人不该影响 B 的护栏：B 排除自己应该是 0，得到 %d（err=%v）", n, err)
	}
}

// TestBuiltinAdminRoleIsPerTenant 说明「内置 admin 角色」现在是每租户各一个（§3.2 ①）。
//
// 原来全局只有一个固定 UUID 的 admin 角色。多租户下每家公司各有自己的一个，
// 角色 key 只在租户内唯一 —— 两家都叫 admin 是正常的，而且互相看不见、动不了。
func TestBuiltinAdminRoleIsPerTenant(t *testing.T) {
	a, b := setup(t)
	ctx := t.Context()

	if a.tenant.AdminRoleID == b.tenant.AdminRoleID {
		t.Fatal("两个租户共用了同一个内置角色")
	}

	// 两边各自按 key 都能查到自己的那一个
	for _, d := range []tenantData{a, b} {
		role, err := d.q.GetRoleByKey(ctx, "admin")
		if err != nil {
			t.Fatalf("[%s] 应该有自己的内置 admin 角色：%v", d.tenant.Code, err)
		}
		if role.ID != d.tenant.AdminRoleID {
			t.Errorf("[%s] 查到的是别人的内置角色 %s", d.tenant.Code, role.ID)
		}
		if !role.Builtin {
			t.Errorf("[%s] 内置角色的 builtin 标记丢了 —— 那它就能被改被删了", d.tenant.Code)
		}
	}

	// A 够不到 B 的内置角色：service 层的「内置角色不可改不可删」判断
	// 建立在「先按 id 查得到这一行」上，查不到就根本走不到那一步
	if _, err := a.q.GetRole(ctx, b.tenant.AdminRoleID); !errors.Is(err, pgx.ErrNoRows) {
		t.Fatalf("A 不该查得到 B 的内置角色，得到 err=%v", err)
	}

	// 通配权限也只在自己租户内数得到
	points, err := a.q.ListRolePermissions(ctx, b.tenant.AdminRoleID)
	if err != nil {
		t.Fatal(err)
	}
	if len(points) != 0 {
		t.Fatalf("A 读到了 B 内置角色的权限点 %v", points)
	}
}
