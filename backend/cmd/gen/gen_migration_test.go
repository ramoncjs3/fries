package main

import (
	"strings"
	"testing"
)

// TestGenMigrationMultiTenant 是产出器最关键的守门：产出的建表迁移**一开始就是多租户的**。
// 生成器一旦吐出不带租户 / 索引没打头的模板，后面每个模块都从错的地方起步。
func TestGenMigrationMultiTenant(t *testing.T) {
	def := validGenerated()
	sql := genMigration(&def)

	must := []string{
		"CREATE TABLE suppliers (",                                                                      // 复数表名
		"tenant_id  uuid        NOT NULL REFERENCES tenants (id)",                                       // 租户列 + 外键
		"id         uuid        PRIMARY KEY",                                                            // 主键
		"CREATE UNIQUE INDEX uk_suppliers_tenant_id ON suppliers (tenant_id, id)",                       // 复合外键锚点
		"CREATE UNIQUE INDEX uk_suppliers_name ON suppliers (tenant_id, name) WHERE deleted_at IS NULL", // 唯一字段 tenant_id 打头 + 部分索引
		"CHECK (status IN ('active', 'terminated'))",                                                    // enum CHECK
		"numeric(18, 2)", // decimal 精度
		"CREATE INDEX idx_suppliers_status ON suppliers (tenant_id, status)",         // filterable enum
		"CREATE INDEX idx_suppliers_started_at ON suppliers (tenant_id, started_at)", // filterable date
		"gin (name gin_trgm_ops)",                 // searchable trgm
		"CREATE TRIGGER trg_suppliers_updated_at", // updated_at 触发器
		"DO NOT EDIT",                             // 托管文件标记
		"DROP TABLE suppliers;",                   // Down 段
	}
	for _, m := range must {
		if !strings.Contains(sql, m) {
			t.Errorf("产出的迁移里应包含：%s\n\n完整产出：\n%s", m, sql)
		}
	}

	// 硬约束：**每一条 CREATE UNIQUE INDEX 都必须以 (tenant_id 打头**。
	for _, line := range strings.Split(sql, "\n") {
		l := strings.ToLower(strings.TrimSpace(line))
		if strings.HasPrefix(l, "create unique index") && !strings.Contains(l, "(tenant_id") {
			t.Errorf("唯一索引没以 tenant_id 打头（跨租户会串味）：%s", line)
		}
	}
}

func TestPluralize(t *testing.T) {
	cases := map[string]string{
		"supplier": "suppliers",
		"product":  "products",
		"category": "categories",
		"box":      "boxes",
		"day":      "days", // y 前是元音，不变 ies
		"class":    "classes",
	}
	for key, want := range cases {
		if got := pluralize(key); got != want {
			t.Errorf("pluralize(%q) = %q，期望 %q", key, got, want)
		}
	}
}
