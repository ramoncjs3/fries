package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestNextMigrationNum(t *testing.T) {
	dir := t.TempDir()
	for _, n := range []string{"00001_init.sql", "00007_foo.sql", "00012_bar.sql", "notes.txt"} {
		if err := os.WriteFile(filepath.Join(dir, n), []byte("x"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	got, err := nextMigrationNum(dir)
	if err != nil {
		t.Fatal(err)
	}
	if got != 13 {
		t.Errorf("下一个迁移号应为 13，得到 %d", got)
	}
}

// TestWriteModule 写全套文件到临时 root，再验幂等（seed 跳过、迁移跳过）。
func TestWriteModule(t *testing.T) {
	root := t.TempDir()
	// 迁移目录要先有，nextMigrationNum 才读得到。
	if err := os.MkdirAll(filepath.Join(root, "backend", "db", "migrations"), 0o755); err != nil {
		t.Fatal(err)
	}
	def := validGenerated()

	// 第一次：全写。
	res, err := writeModule(root, &def)
	if err != nil {
		t.Fatal(err)
	}
	if len(res) != 13 {
		t.Fatalf("应写/处理 13 个文件，得到 %d", len(res))
	}
	// 关键文件落盘了。
	for _, rel := range []string{
		"backend/db/migrations/00001_create_suppliers.sql",
		"backend/db/queries/supplier.sql",
		"backend/internal/service/supplier/service.go",
		"backend/internal/handler/supplier.go",
		"frontend/src/features/supplier/ListPage.tsx",
	} {
		if _, err := os.Stat(filepath.Join(root, rel)); err != nil {
			t.Errorf("应写出 %s：%v", rel, err)
		}
	}

	// 第二次：seed 跳过、迁移跳过、queries 覆盖。
	res2, err := writeModule(root, &def)
	if err != nil {
		t.Fatal(err)
	}
	var skipped, overwritten int
	for _, r := range res2 {
		switch {
		case strings.HasPrefix(r.status, "skipped"): // 迁移「表已建无新字段」+ 11 个 seed
			skipped++
		case r.status == "overwritten":
			overwritten++
		}
	}
	if overwritten != 1 { // 只有 queries.sql
		t.Errorf("第二次应有 1 个 overwritten（queries），得到 %d", overwritten)
	}
	if skipped != 12 { // 迁移 + 11 个 seed
		t.Errorf("第二次应跳过 12 个（迁移+11 seed），得到 %d", skipped)
	}
}

// TestIncrementalMigration 表已建 + YAML 加了新字段 → 产 ALTER 增量迁移；必填无 default 被拒。
func TestIncrementalMigration(t *testing.T) {
	root := t.TempDir()
	migDir := filepath.Join(root, "backend", "db", "migrations")
	if err := os.MkdirAll(migDir, 0o755); err != nil {
		t.Fatal(err)
	}
	base := validGenerated()

	// 第一次：建表 + 全套。
	if _, err := writeModule(root, &base); err != nil {
		t.Fatal(err)
	}

	// YAML 加两个新字段：可选 text（安全）+ 可选 ref（安全）。
	grown := validGenerated()
	grown.Fields = append(grown.Fields,
		Field{Name: "tag", Type: typeText, Label: "标签"},
		Field{Name: "buyer_id", Type: typeRef, Ref: "buyer", Label: "买家"},
	)
	res, err := writeModule(root, &grown)
	if err != nil {
		t.Fatal(err)
	}
	var alter string
	for _, r := range res {
		if strings.HasPrefix(r.status, "written(ALTER") {
			alter = r.path
		}
	}
	if alter == "" {
		t.Fatal("加了新字段应产 ALTER 增量迁移")
	}
	sql, _ := os.ReadFile(alter)
	for _, want := range []string{
		"ALTER TABLE suppliers ADD COLUMN tag text;",
		"ALTER TABLE suppliers ADD COLUMN buyer_id uuid;",
		"FOREIGN KEY (tenant_id, buyer_id) REFERENCES buyers (tenant_id, id);",
		"DROP COLUMN tag;",
	} {
		if !strings.Contains(string(sql), want) {
			t.Errorf("ALTER 迁移应含：%s\n%s", want, sql)
		}
	}

	// 新增必填字段但没 default → 拒（防给有数据的表加 NOT NULL 列炸）。
	unsafe := []Field{{Name: "must", Type: typeString, Label: "必填", Required: true, Max: 50}}
	if _, err := genAlterMigration(&base, unsafe); err == nil || !strings.Contains(err.Error(), "必填") {
		t.Errorf("新增必填无 default 应被拒，得到：%v", err)
	}
}

func TestPostGenSteps(t *testing.T) {
	def := validGenerated() // 有 decimal 字段
	joined := strings.Join(postGenSteps(&def), "\n")
	for _, want := range []string{
		"shopspring/decimal", // 有 decimal 字段才提示
		"make gen-sqlc",      // 同步流水线
		"MODULES.md",
	} {
		if !strings.Contains(joined, want) {
			t.Errorf("后续步骤应提到：%s", want)
		}
	}
}
