package main

import (
	"go/parser"
	"go/token"
	"strings"
	"testing"
)

// mustParseGo 断言产出的 Go 代码**至少语法上是对的**（能被 go/parser 解析）。
// 真正能不能编译要等 4e（引用的 perm / errs 包在那时才连得上），但语法错这里就能兜住。
func mustParseGo(t *testing.T, src string) {
	t.Helper()
	if _, err := parser.ParseFile(token.NewFileSet(), "gen.go", src, parser.AllErrors); err != nil {
		t.Fatalf("产出的 Go 语法有错：%v\n\n%s", err, src)
	}
}

func TestGenPermModule(t *testing.T) {
	def := validGenerated()
	src := genPermModule(&def)
	mustParseGo(t, src)

	for _, m := range []string{
		"package modules",
		`Key:    "supplier"`,
		"Realm:  perm.RealmTenant,",
		"Scoped: true,",
		`Menu:   perm.Menu{Path: "/suppliers", Icon: "truck", ShowIf: perm.ActionList, Order: 500}`,
		"{Key: perm.ActionList, Name: \"查询\"}",
		"{Key: perm.ActionCreate, Name: \"新增\"}",
		`{Key: "export", Name: "导出"}`, // 非标准动作用字面量
		"Safe to edit",                // 种子文件头
	} {
		if !strings.Contains(src, m) {
			t.Errorf("perm 模块产出里应包含：%s", m)
		}
	}
}

func TestGenErrorsUniqueFields(t *testing.T) {
	def := validGenerated() // name 是唯一字段
	src := genErrors(&def)
	mustParseGo(t, src)

	for _, m := range []string{
		"package supplier",
		"uk_suppliers_name", // 索引名和迁移一致
		`errs.Define("supplier.name_taken", http.StatusConflict`,
		"供应商名称已被占用",
		"ErrNameTaken",
		"Safe to edit",
	} {
		if !strings.Contains(src, m) {
			t.Errorf("errors 产出里应包含：%s", m)
		}
	}
}

// TestGenErrorsNoUniqueFields 没有唯一字段时，errors.go 只有 package + 说明，不带 errs import
// （否则未用 import 编译不过）。
func TestGenErrorsNoUniqueFields(t *testing.T) {
	def := validGenerated()
	for i := range def.Fields {
		def.Fields[i].Unique = false
	}
	src := genErrors(&def)
	mustParseGo(t, src)

	if strings.Contains(src, "= errs.Define(") {
		t.Error("没有唯一字段时不该产出错误码")
	}
	if strings.Contains(src, "import") {
		t.Error("没有错误码就不该 import errs —— 未用 import 编译不过")
	}
}
