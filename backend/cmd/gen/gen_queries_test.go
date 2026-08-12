package main

import (
	"strings"
	"testing"
)

// TestGenQueriesTenantScoped 守 queries 产出器最关键的事：**每条查询都带租户条件**，
// CRUD 齐全，软删除 + 乐观锁到位。漏一条 tenant_id 就是 BOLA（§11.1）。
func TestGenQueriesTenantScoped(t *testing.T) {
	def := validGenerated()
	sql := genQueries(&def)

	// 六条 CRUD 查询都在。
	for _, name := range []string{
		"-- name: ListSuppliers :many",
		"-- name: CountSuppliers :one",
		"-- name: GetSupplier :one",
		"-- name: CreateSupplier :one",
		"-- name: UpdateSupplier :one",
		"-- name: SoftDeleteSupplier :execrows",
	} {
		if !strings.Contains(sql, name) {
			t.Errorf("缺查询：%s\n\n%s", name, sql)
		}
	}

	// **每一条查询正文都必须出现 tenant_id**（按 `-- name:` 切段逐条查）。
	segs := strings.Split(sql, "-- name:")
	checked := 0
	for _, seg := range segs[1:] {
		// SELECT/UPDATE/DELETE 是 WHERE tenant_id = sqlc.arg('tenant_id')；
		// INSERT 是 VALUES(sqlc.arg('tenant_id'), …)。都引用了这个参数。
		if !strings.Contains(seg, "sqlc.arg('tenant_id')") {
			t.Errorf("有查询没带 tenant_id 参数（BOLA）：\n%s", strings.TrimSpace(seg))
		}
		checked++
	}
	if checked != 6 {
		t.Fatalf("应有 6 条查询，实际切出 %d 条", checked)
	}

	// 关键属性：Get 按 id 也带 tenant_id；改删带乐观锁；软删除不是物理删。
	must := []string{
		"AND id = sqlc.arg('id') AND deleted_at IS NULL", // Get 带租户 + id + 软删除过滤
		"AND version = sqlc.arg('version')",              // 乐观锁
		"SET deleted_at = now(), version = version + 1",  // 软删除
		"OR name ILIKE sqlc.narg('keyword')",             // searchable 做 ILIKE
		"OR status = sqlc.narg('status')",                // filterable 做等值
		"sqlc.arg('name')",                               // 必填字段用 arg
		"sqlc.narg('remark')",                            // 可空字段用 narg
	}
	for _, m := range must {
		if !strings.Contains(sql, m) {
			t.Errorf("产出的 queries 里应包含：%s", m)
		}
	}
}

func TestPascal(t *testing.T) {
	cases := map[string]string{
		"supplier":        "Supplier",
		"service_account": "ServiceAccount",
		"order_line":      "OrderLine",
	}
	for in, want := range cases {
		if got := pascal(in); got != want {
			t.Errorf("pascal(%q) = %q，期望 %q", in, got, want)
		}
	}
}
