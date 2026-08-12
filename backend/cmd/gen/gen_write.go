package main

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
)

// gen_write.go 把产出器的结果真写到盘上（4d）。
//
// 三种写盘语义：
//   - **managed**（头带「DO NOT EDIT」）：每次重写。queries.sql 属于这类 —— 它是模块定义的
//     纯函数，改 YAML 重跑就该更新。
//   - **seed**（头带「Safe to edit」）：只在文件不存在时写，之后归人/AI 所有，重跑不覆盖。
//     service/handler/errors/perm + 全部前端文件属于这类。
//   - **迁移**：write-once。迁移是 append-only 的历史，一旦生成（可能已 apply）就不能重写；
//     已存在同表的建表迁移就跳过。
//
// 写盘是纯增量、安全。改现有文件的**登记**（app.go 装配、tenantsql 租户表、前端路由）在
// gen_register.go 做（幂等、锚点插入）；MODULES.md 那句业务说明仍留人手加。

// 写盘 / 登记的结果状态（抽常量，goconst 会数重复字面量）。
const (
	statusWritten           = "written"
	statusSkippedRegistered = "skipped(已登记)"
)

// writeResult 记录一个文件的写盘结果。
type writeResult struct {
	path   string
	status string // written / skipped(seed exists) / skipped(migration exists) / overwritten
}

// writeModule 把模块的全部产出写到盘上，返回每个文件的结果。
func writeModule(root string, def *ModuleDef) ([]writeResult, error) {
	table := pluralize(def.Key)
	be := filepath.Join(root, "backend")
	fe := filepath.Join(root, "frontend", "src", "features", def.Key)
	var results []writeResult

	// 1) 迁移：建表 write-once；表已建过则给新增字段产 ALTER 增量迁移（§10.4）。
	migDir := filepath.Join(be, "db", "migrations")
	existing, _ := filepath.Glob(filepath.Join(migDir, "*_create_"+table+".sql"))
	if len(existing) > 0 {
		cols, err := existingColumns(migDir, table)
		if err != nil {
			return nil, err
		}
		added := newFields(def, cols)
		if len(added) == 0 {
			results = append(results, writeResult{filepath.Base(existing[0]), "skipped(表已建，无新字段)"})
		} else {
			alter, err := genAlterMigration(def, added)
			if err != nil {
				return nil, err
			}
			num, err := nextMigrationNum(migDir)
			if err != nil {
				return nil, err
			}
			p := filepath.Join(migDir, fmt.Sprintf("%05d_add_to_%s.sql", num, table))
			if err := writeFile(p, alter); err != nil {
				return nil, err
			}
			results = append(results, writeResult{p, "written(ALTER 增量：" + fieldNames(added) + ")"})
		}
	} else {
		num, err := nextMigrationNum(migDir)
		if err != nil {
			return nil, err
		}
		p := filepath.Join(migDir, fmt.Sprintf("%05d_create_%s.sql", num, table))
		if err := writeFile(p, genMigration(def)); err != nil {
			return nil, err
		}
		results = append(results, writeResult{p, statusWritten})
	}

	// 2) managed：queries.sql 每次重写。
	qp := filepath.Join(be, "db", "queries", def.Key+".sql")
	overwritten := fileExists(qp)
	if err := writeFile(qp, genQueries(def)); err != nil {
		return nil, err
	}
	results = append(results, writeResult{qp, ifStr(overwritten, "overwritten", statusWritten)})

	// 3) seed：只在不存在时写。
	seeds := []struct {
		path, content string
	}{
		{filepath.Join(be, "internal", "service", def.Key, "service.go"), genService(def)},
		{filepath.Join(be, "internal", "service", def.Key, "errors.go"), formatGo(genErrors(def))},
		{filepath.Join(be, "internal", "perm", "modules", def.Key+".go"), formatGo(genPermModule(def))},
		{filepath.Join(be, "internal", "handler", def.Key+".go"), genHandler(def)},
		{filepath.Join(fe, "types.ts"), genTypesTS(def)},
		{filepath.Join(fe, "schema.ts"), genSchemaTS(def)},
		{filepath.Join(fe, "api.ts"), genApiTS(def)},
		{filepath.Join(fe, "queries.ts"), genQueriesTS(def)},
		{filepath.Join(fe, "NewPage.tsx"), genNewPageTSX(def)},
		{filepath.Join(fe, "ListPage.tsx"), genListPageTSX(def)},
		{filepath.Join(fe, "DetailPage.tsx"), genDetailPageTSX(def)},
	}
	for _, s := range seeds {
		if fileExists(s.path) {
			results = append(results, writeResult{s.path, "skipped(seed exists)"})
			continue
		}
		if err := writeFile(s.path, s.content); err != nil {
			return nil, err
		}
		results = append(results, writeResult{s.path, statusWritten})
	}
	return results, nil
}

// postGenSteps 是写盘 + 自动登记之后，还要人手跑的（把 SQL/类型/文档同步出来 —— 这些是命令，不改代码）。
func postGenSteps(def *ModuleDef) []string {
	steps := []string{}
	for _, f := range def.Fields {
		if f.Type == typeDecimal {
			steps = append(steps, "有 decimal 字段：cd backend && go get github.com/shopspring/decimal")
			break
		}
	}
	steps = append(steps, "make gen-sqlc gen-tenant-queries gen-api schemadoc")
	steps = append(steps, "docs/MODULES.md 手动加一行说明（生成器不动它）")
	return steps
}

var rxMigrationNum = regexp.MustCompile(`^(\d+)_`)

// nextMigrationNum 扫迁移目录取最大编号 + 1。
func nextMigrationNum(dir string) (int, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return 0, fmt.Errorf("读迁移目录 %s: %w", dir, err)
	}
	max := 0
	for _, e := range entries {
		m := rxMigrationNum.FindStringSubmatch(e.Name())
		if m == nil {
			continue
		}
		if n, err := strconv.Atoi(m[1]); err == nil && n > max {
			max = n
		}
	}
	return max + 1, nil
}

func writeFile(path, content string) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		return fmt.Errorf("写 %s: %w", path, err)
	}
	return nil
}

func fileExists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}

func ifStr(cond bool, a, b string) string {
	if cond {
		return a
	}
	return b
}
