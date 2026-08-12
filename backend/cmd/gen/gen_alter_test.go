package main

import (
	"os"
	"path/filepath"
	"testing"
)

// TestExistingColumns 从建表 + ALTER 迁移里正确扫出现有列，且不被 default 值里的 `);` 截断。
func TestExistingColumns(t *testing.T) {
	dir := t.TempDir()
	write := func(name, content string) {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	// 建表：注意 note 的 default 里含 `);` —— 用来验块尾判据用的是 `\n);` 而不是裸 `);`。
	write("00001_create_widgets.sql", `-- +goose Up
CREATE TABLE widgets (
    id         uuid        PRIMARY KEY,
    tenant_id  uuid        NOT NULL REFERENCES tenants (id),
    name       varchar(100) NOT NULL,
    note       text        DEFAULT 'a);b',
    qty        integer,

    created_at timestamptz NOT NULL DEFAULT now(),
    version    integer     NOT NULL DEFAULT 0
);
`)
	// 后续 ALTER 加的列。
	write("00005_add_to_widgets.sql", `-- +goose Up
ALTER TABLE widgets ADD COLUMN color varchar(20);
`)
	// 无关的别的表，别混进来。
	write("00002_create_others.sql", "CREATE TABLE others (\n    id uuid,\n    ghost text\n);\n")

	cols, err := existingColumns(dir, "widgets")
	if err != nil {
		t.Fatal(err)
	}
	// 应有：name/note/qty（建表业务列，标准列也会被扫进来但无所谓）+ color（ALTER）。
	for _, want := range []string{"name", "note", "qty", "color"} {
		if !cols[want] {
			t.Errorf("应扫到列 %q，实际：%v", want, cols)
		}
	}
	// **关键**：note 的 default 含 `);`，其后的 qty 不能被漏读（否则会被判成新字段 → 重复 ALTER）。
	if !cols["qty"] {
		t.Error("qty 被 default 里的 `);` 截断漏读了 —— 会导致重复 ADD COLUMN")
	}
	// 别的表的列不该混进来。
	if cols["ghost"] {
		t.Error("扫到了别的表 others 的列 ghost")
	}
}
