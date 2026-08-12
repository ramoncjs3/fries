package repo_test

import (
	"strings"
	"testing"

	"github.com/google/uuid"

	"github.com/ramoncjs3/fries/internal/repo"
	"github.com/ramoncjs3/fries/internal/repo/testdb"
)

// 运行期兜底是这套无-RLS 方案里**最接近兜底网的东西**（MULTI-TENANCY.md §12.2）。
//
// 它守的场景是：某天有人写了一条漏掉租户条件的查询，于是 ForTenant(A) 查回了 B 的行。
// 没有这层网的话，那次查询会安安静静地成功 —— 测试也照样绿，
// 因为断言通常只看「我要的那条在不在」，不看「多出来的是谁的」。
//
// 这里直接把「查回了别人的行」这件事造出来：拿 Unscoped()（声明过要绕过隔离的那条路）
// 查到 B 的用户，再送进 A 的句柄去核 —— 真实的漏条件查询等价于这个动作。
func TestTenantAssertionCatchesForeignRow(t *testing.T) {
	pool := testdb.Start(t)
	if !repo.TenantAssertionsEnabled() {
		t.Fatal("测试里这层网必须是开着的，否则漏条件的查询不会有任何动静")
	}

	tenantA := testdb.NewTenant(t, pool, 0)
	tenantB := testdb.NewTenant(t, pool, 1)
	store := repo.New(pool)

	userB := mustCreateUser(t, store.ForTenant(tenantB.ID), "b-user")

	// A 的句柄按 id 查 B 的人：查询本身带了租户条件，所以查不到（这是正常的隔离）
	if _, err := store.ForTenant(tenantA.ID).GetUserByID(t.Context(), userB.ID); err == nil {
		t.Fatal("A 按 id 查得到 B 的人，隔离本身就破了")
	}

	// 现在造出「查询漏了租户条件」的那一刻：B 的行落到 A 的句柄手里
	got := repo.AssertTenantForTest(tenantA.ID, userB)
	if got == "" {
		t.Fatal("B 的行进了 A 的句柄，这层网必须炸 —— 没炸就等于没装")
	}
	for _, want := range []string{"跨租户", tenantA.ID.String(), tenantB.ID.String()} {
		if !strings.Contains(got, want) {
			t.Errorf("panic 消息里应该有 %q，好让人知道是谁串到了谁：%s", want, got)
		}
	}

	// sqlc.embed 出来的投影行（形如 {Entity, xLabel}）：TenantID 藏在嵌套的 Entity 里。
	// 生成模块带 ref JOIN 的 List/Get、user 模块的 List/Get 都是这形状 —— 这层网也得能钻进去核。
	embedRow := struct {
		User  repo.User
		Label *string
	}{User: userB}
	if got := repo.AssertTenantForTest(tenantA.ID, embedRow); got == "" {
		t.Fatal("embed 形状的行里嵌着 B 的数据，这层网必须钻进嵌套结构体核出来 —— 没炸就是漏了")
	}
}

// 自己的行、以及核不了的行（count、没有 TenantID 字段的投影）都必须放过。
//
// **误报比漏报更该防**：这层网一旦开始瞎炸，人会先去关掉它，那就白装了。
func TestTenantAssertionStaysQuietOnOwnData(t *testing.T) {
	pool := testdb.Start(t)
	tenant := testdb.NewTenant(t, pool, 0)
	store := repo.New(pool)
	q := store.ForTenant(tenant.ID)

	user := mustCreateUser(t, q, "own-user")

	cases := map[string]any{
		"自己的行":        user,
		"自己的行组成的切片":   []repo.User{user, user},
		"空切片":         []repo.User{},
		"没有 TenantID": struct{ Name string }{"投影出来的行"},
		"计数":          int64(3),
		"空指针":         (*repo.User)(nil),
		// embed 形状但嵌的是自己的行 —— 钻进去核也应当安静（不误报）。
		"自己的 embed 投影行": struct {
			User  repo.User
			Label *string
		}{User: user},
	}
	for name, v := range cases {
		t.Run(name, func(t *testing.T) {
			if got := repo.AssertTenantForTest(tenant.ID, v); got != "" {
				t.Errorf("这个不该炸，却炸了：%s", got)
			}
		})
	}

	// 真查一遍也必须安静 —— 上面都是造出来的值，这条走的是真实链路
	if _, err := q.ListUsers(t.Context(), repo.ListUsersArgs{Limit: 10}); err != nil {
		t.Fatalf("正常查询不该有事：%v", err)
	}
}

func mustCreateUser(t *testing.T, q *repo.TenantQueries, username string) repo.User {
	t.Helper()
	id, err := uuid.NewV7()
	if err != nil {
		t.Fatal(err)
	}
	user, err := q.CreateUser(t.Context(), repo.CreateUserArgs{
		ID:           id,
		Username:     username,
		DisplayName:  username,
		PasswordHash: "x",
	})
	if err != nil {
		t.Fatalf("建用户失败：%v", err)
	}
	return user
}
