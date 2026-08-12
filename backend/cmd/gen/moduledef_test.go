package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// validGenerated 是一份合法的可生成模块定义（对齐 DECISIONS.md §10.1 的例子）。
func validGenerated() ModuleDef {
	return ModuleDef{
		Key:       "supplier",
		Name:      "供应商",
		Generated: true,
		Scoped:    true,
		Menu:      Menu{Path: "/suppliers", Icon: "truck"},
		Fields: []Field{
			{Name: "name", Type: "string", Label: "供应商名称", Required: true, Unique: true, Searchable: true, Max: 100},
			{Name: "status", Type: "enum", Label: "状态", Filterable: true, Default: "active",
				Values: map[string]string{"active": "合作中", "terminated": "已终止"}},
			{Name: "credit", Type: "decimal", Label: "授信额度", Precision: []int{18, 2}},
			{Name: "started_at", Type: "date", Label: "合作起始日", Filterable: true},
			{Name: "remark", Type: "text", Label: "备注"},
		},
		Sortable: []string{"created_at", "name", "started_at"},
		Actions:  []string{"list", "read", "create", "update", "delete", "export"},
	}
}

// TestValidateModuleDef 是校验逻辑的变异测试：合法定义无错，每种坏法都被抓到。
func TestValidateModuleDef(t *testing.T) {
	base := validGenerated()
	if errs := base.Validate(); len(errs) != 0 {
		t.Fatalf("合法定义不该报错：%v", errs)
	}

	cases := []struct {
		name    string
		mutate  func(*ModuleDef)
		wantHit string
	}{
		{"key 非法", func(m *ModuleDef) { m.Key = "Supplier" }, "key"},
		{"name 空", func(m *ModuleDef) { m.Name = "" }, "name 不能为空"},
		{"generated 但没字段", func(m *ModuleDef) { m.Fields = nil }, "必须有 fields"},
		{"字段名占用标准列", func(m *ModuleDef) { m.Fields[0].Name = "tenant_id" }, "标准列"},
		{"字段名重复", func(m *ModuleDef) { m.Fields[1].Name = "name" }, "重复"},
		{"字段缺 label", func(m *ModuleDef) { m.Fields[0].Label = "" }, "缺 label"},
		{"未知类型", func(m *ModuleDef) { m.Fields[0].Type = "money" }, "不认识"},
		{"enum 没 values", func(m *ModuleDef) { m.Fields[1].Values = nil }, "必须给 values"},
		{"enum default 不在 values", func(m *ModuleDef) { m.Fields[1].Default = "gone" }, "不在 values"},
		{"decimal 没 precision", func(m *ModuleDef) { m.Fields[2].Precision = nil }, "precision"},
		{"string 没 max", func(m *ModuleDef) { m.Fields[0].Max = 0 }, "必须给 max"},
		{"非文本字段 searchable", func(m *ModuleDef) { m.Fields[2].Searchable = true }, "只对 string/text"},
		{"未知 action", func(m *ModuleDef) { m.Actions = append(m.Actions, "frobnicate") }, "不认识"},
		{"menu.path 非法", func(m *ModuleDef) { m.Menu.Path = "suppliers" }, "menu.path"},
		{"menu.icon 空", func(m *ModuleDef) { m.Menu.Icon = "" }, "menu.icon"},
		{"sortable 引用不存在字段", func(m *ModuleDef) { m.Sortable = []string{"nope"} }, "sortable"},
		{"字段名撞分页/搜索内建", func(m *ModuleDef) { m.Fields[0].Name = "keyword" }, "撞"},
		{"字段名撞 sqlc rename", func(m *ModuleDef) { m.Fields[0].Name = "ip" }, "sqlc 重命名"},
		{"enum code 非法", func(m *ModuleDef) { m.Fields[1].Values = map[string]string{"a'b": "x"} }, "code"},
		{"int default 非整数", func(m *ModuleDef) {
			m.Fields = append(m.Fields, Field{Name: "n", Type: typeInt, Label: "数", Default: "abc"})
		}, "不是整数"},
		{"bool default 非布尔", func(m *ModuleDef) {
			m.Fields = append(m.Fields, Field{Name: "b", Type: typeBool, Label: "开", Default: "yes"})
		}, "只能是 true 或 false"},
		{"actions 缺 list", func(m *ModuleDef) { m.Actions = []string{actRead, actCreate} }, "必须包含 list"},
		{"decimal 配 default 静默失效", func(m *ModuleDef) { m.Fields[2].Default = "0" }, "静默失效"},
		{"date 配 default 静默失效", func(m *ModuleDef) { m.Fields[3].Default = "2020-01-01" }, "静默失效"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			m := validGenerated()
			c.mutate(&m)
			errs := m.Validate()
			if !containsSub(errs, c.wantHit) {
				t.Fatalf("期望报告里含 %q，实际：%v", c.wantHit, errs)
			}
		})
	}
}

// TestLoadModuleDefKnownFields 敲错字段名（filterble）必须报错，不能静默失效。
func TestLoadModuleDefKnownFields(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "supplier.yaml")
	write(t, path, `
key: supplier
name: 供应商
generated: true
menu: { path: /suppliers, icon: truck }
fields:
  - { name: nm, type: string, label: 名称, max: 100, filterble: true }
actions: [list]
`)
	if _, err := LoadModuleDef(path); err == nil || !strings.Contains(err.Error(), "filterble") {
		t.Fatalf("敲错的字段名 filterble 应报错，得到：%v", err)
	}
}

// TestLoadModuleDefKeyMustMatchFilename key 和文件名不一致要报错。
func TestLoadModuleDefKeyMustMatchFilename(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "supplier.yaml")
	write(t, path, "key: vendor\nname: 供应商\ngenerated: false\n")
	if _, err := LoadModuleDef(path); err == nil || !strings.Contains(err.Error(), "不一致") {
		t.Fatalf("key 和文件名不一致应报错，得到：%v", err)
	}
}

// TestLoadModuleDefValid 一份合法 YAML 能加载出正确结构。
func TestLoadModuleDefValid(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "supplier.yaml")
	write(t, path, `
key: supplier
name: 供应商
generated: true
scoped: true
menu: { path: /suppliers, icon: truck }
fields:
  - { name: name, type: string, label: 供应商名称, required: true, unique: true, max: 100 }
  - { name: status, type: enum, label: 状态, values: { active: 合作中, terminated: 已终止 } }
actions: [list, create, update, delete]
`)
	def, err := LoadModuleDef(path)
	if err != nil {
		t.Fatal(err)
	}
	if def.Key != "supplier" || len(def.Fields) != 2 || !def.Scoped {
		t.Fatalf("解析结果不对：%+v", def)
	}
	if def.Fields[1].Values["active"] != "合作中" {
		t.Fatalf("enum values 没解析对：%+v", def.Fields[1])
	}
}

func containsSub(errs []string, sub string) bool {
	for _, e := range errs {
		if strings.Contains(e, sub) {
			return true
		}
	}
	return false
}

func write(t *testing.T, path, content string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
}
