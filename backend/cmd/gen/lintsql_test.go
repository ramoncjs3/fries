package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// 这个文件守的是 lint-sql 这层**外壳**：谁被允许拿不带租户的句柄。
//
// 「一条 SQL 有没有带租户条件」那部分已经抽到 internal/tenantsql，
// 用例也跟着搬过去了 —— 那份实现同时被运行期的 pgx tracer 调用，
// 测试放在它旁边才不会有人只改一边。

// TestPlatformHandleIsGuarded 是**这次修复的另一个守门测试**。
//
// `checkUnscopedCallers` 曾经在「文件里没有 .Unscoped()」时就提前返回，
// 于是只调 `.Platform()` 的文件被整个跳过 —— platformCallers 那份白名单
// 整整是死代码。而 `Platform().ListTenants()` 就是整份客户名单（§8.1）。
func TestPlatformHandleIsGuarded(t *testing.T) {
	cases := []struct {
		name    string
		pkg     string
		call    string
		wantErr bool
	}{
		{"业务代码拿平台句柄", "internal/service/user", "store.Platform()", true},
		{"业务代码拿不带租户的句柄", "internal/service/user", "store.Unscoped()", true},
		{"平台服务自己拿平台句柄", "internal/service/platform", "store.Platform()", false},
		{"认证链路拿不带租户的句柄", "internal/auth", "store.Unscoped()", false},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			root := writeFakePackage(t, c.pkg, c.call)

			var p problems
			if err := checkUnscopedCallers(root, &p); err != nil {
				t.Fatal(err)
			}
			switch {
			case c.wantErr && len(p) == 0:
				t.Fatalf("%s 调 %s 必须被拦下来", c.pkg, c.call)
			case !c.wantErr && len(p) != 0:
				t.Fatalf("%s 调 %s 是白名单里的，不该报：%v", c.pkg, c.call, p)
			}
		})
	}
}

// TestAnalyzeTenantSchema 是 B1/B2 两条结构卡点的变异测试。
//
// 拿掉这两条检查、或把判据写松，下面任一用例就会翻 —— 这正是它们该拦的漏写。
func TestAnalyzeTenantSchema(t *testing.T) {
	guarded := uniqueIndexDef{table: "users", firstCol: "tenant_id"}

	cases := []struct {
		name         string
		hasTenant    map[string]bool
		idx          map[string]uniqueIndexDef
		inlineUnique map[string]bool
		wantHit      string // 期望报告里包含的关键字；空 = 不该报
	}{
		{
			name:      "干净：登记过的表 + tenant_id 打头的索引",
			hasTenant: map[string]bool{"users": true},
			idx:       map[string]uniqueIndexDef{"uk_users_email": guarded},
			wantHit:   "",
		},
		{
			name:      "B1：带 tenant_id 的新表没登记",
			hasTenant: map[string]bool{"orders": true},
			wantHit:   "没登记进 tenantsql",
		},
		{
			name:      "B1：豁免表（触发器专属）不报",
			hasTenant: map[string]bool{"audit_chain_head": true},
			wantHit:   "",
		},
		{
			name:      "B2：唯一索引没以 tenant_id 打头",
			hasTenant: map[string]bool{"users": true},
			idx:       map[string]uniqueIndexDef{"uk_users_email": {table: "users", firstCol: "email"}},
			wantHit:   "不是 tenant_id",
		},
		{
			name:      "B2：认证索引明列为全平台唯一，不报",
			hasTenant: map[string]bool{"sessions": true},
			idx:       map[string]uniqueIndexDef{"uk_sessions_token": {table: "sessions", firstCol: "token_hash"}},
			wantHit:   "",
		},
		{
			name:      "B2：非租户表上的唯一索引不管",
			hasTenant: map[string]bool{"users": true},
			idx:       map[string]uniqueIndexDef{"uk_tenants_code": {table: "tenants", firstCol: "code"}},
			wantHit:   "",
		},
		{
			name:         "B2：租户表用内联 UNIQUE 约束（检查器看不出列顺序）",
			hasTenant:    map[string]bool{"users": true},
			inlineUnique: map[string]bool{"users": true},
			wantHit:      "内联 UNIQUE",
		},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := analyzeTenantSchema(tenantSchema{
				hasTenant:    c.hasTenant,
				idx:          c.idx,
				inlineUnique: c.inlineUnique,
			})
			switch {
			case c.wantHit == "" && len(got) != 0:
				t.Fatalf("不该报，却报了：%v", got)
			case c.wantHit != "" && !containsAny(got, c.wantHit):
				t.Fatalf("期望报告里含 %q，实际：%v", c.wantHit, got)
			}
		})
	}
}

// TestReplayTenantSchema 验回放：DROP + 同名重建后，现状要以最后一次为准，
// 不能把被替换掉的历史定义当成现状（多租户那次就是这么改索引的）。
func TestReplayTenantSchema(t *testing.T) {
	dir := t.TempDir()
	write := func(name, sql string) {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(sql), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	// 先建单列唯一索引，再（像 00007 那样）加 tenant_id 列、DROP 重建成带 tenant_id 的版本。
	write("0001_init.sql", `-- +goose Up
CREATE TABLE orders (id uuid PRIMARY KEY, name text NOT NULL);
CREATE UNIQUE INDEX uk_orders_name ON orders (name);
-- +goose Down
DROP TABLE orders;
`)
	write("0002_tenant.sql", `-- +goose Up
ALTER TABLE public.orders ADD COLUMN tenant_id uuid;
DROP INDEX uk_orders_name;
CREATE UNIQUE INDEX uk_orders_name ON public.orders (tenant_id, name);
CREATE TABLE widgets (
	tenant_id uuid NOT NULL,
	id uuid PRIMARY KEY,
	sku text UNIQUE,
	status text CHECK (status IN ('unique', 'used'))
);
-- +goose Down
DROP TABLE widgets;
`)

	s, err := replayTenantSchema(dir)
	if err != nil {
		t.Fatal(err)
	}
	// orders 走 schema 限定名（public.orders）ALTER —— 应归一成纯表名识别。
	if !s.hasTenant["orders"] {
		t.Error("orders 通过 ALTER 加了 tenant_id，应被识别（含 schema 限定名）")
	}
	if !s.hasTenant["widgets"] {
		t.Error("widgets 在 CREATE TABLE 里内联了 tenant_id，应被识别")
	}
	if def := s.idx["uk_orders_name"]; def.firstCol != "tenant_id" || def.table != "orders" {
		t.Errorf("uk_orders_name 现状应是重建后的 orders (tenant_id, name)，得到 %+v", def)
	}
	// widgets 有内联 `sku text UNIQUE`，应被认出；CHECK 里的 'unique' 字符串不算。
	if !s.inlineUnique["widgets"] {
		t.Error("widgets 的内联 UNIQUE 约束应被识别")
	}
	if s.inlineUnique["orders"] {
		t.Error("orders 没有内联 UNIQUE（CHECK 里的字符串不该算），却被识别成有")
	}
}

// containsAny 判断问题列表里有没有哪条包含关键字。
func containsAny(problems []string, sub string) bool {
	for _, p := range problems {
		if strings.Contains(p, sub) {
			return true
		}
	}
	return false
}

// writeFakePackage 在临时目录里造一个「backend/<pkg>/x.go」，返回仓库根。
//
// 必须真的 import repo：检查器用 AST 判 import，不然它自己的源码
// （常量和注释里都有那个路径）会被自己报出来。
func writeFakePackage(t *testing.T, pkg, call string) string {
	t.Helper()

	root := t.TempDir()
	dir := filepath.Join(root, "backend", filepath.FromSlash(pkg))
	if err := os.MkdirAll(dir, 0o750); err != nil {
		t.Fatal(err)
	}

	src := "package fake\n\nimport \"" + repoImportPath + "\"\n\n" +
		"func f(store *repo.Store) any { return " + call + " }\n"
	if err := os.WriteFile(filepath.Join(dir, "x.go"), []byte(src), 0o600); err != nil {
		t.Fatal(err)
	}
	return root
}
