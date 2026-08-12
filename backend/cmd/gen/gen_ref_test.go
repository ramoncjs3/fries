package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// refModule 一个带 ref 字段的模块：必填 ref（supplier_id → supplier）+ 可选 ref（category_id → category）。
func refModule() ModuleDef {
	return ModuleDef{
		Key: "product", Name: "产品", Generated: true, Scoped: true,
		Menu: Menu{Path: "/products", Icon: "box"},
		Fields: []Field{
			{Name: "name", Type: typeString, Label: "名称", Required: true, Unique: true, Searchable: true, Max: 100},
			{Name: "supplier_id", Type: typeRef, Ref: "supplier", Label: "供应商", Required: true},
			{Name: "category_id", Type: typeRef, Ref: "category", Label: "分类"},
		},
		Actions: []string{actList, actRead, actCreate, actUpdate, actDelete},
	}
}

func TestGenRefMigration(t *testing.T) {
	def := refModule()
	src := squash(genMigration(&def))
	for _, want := range []string{
		"supplier_id uuid NOT NULL", // 必填 ref → NOT NULL uuid
		"category_id uuid",          // 可选 ref → 可空 uuid
		// 复合外键防跨租户引用（tenant_id 一起锚）—— 约束名和 FOREIGN KEY 分两行
		"ALTER TABLE products ADD CONSTRAINT fk_products_supplier_id",
		"FOREIGN KEY (tenant_id, supplier_id) REFERENCES suppliers (tenant_id, id);",
		"FOREIGN KEY (tenant_id, category_id) REFERENCES categories (tenant_id, id);",
		"CREATE INDEX idx_products_supplier_id ON products (tenant_id, supplier_id);", // 查询索引
	} {
		if !strings.Contains(src, want) {
			t.Errorf("ref 迁移应包含：%s", want)
		}
	}
}

func TestGenRefService(t *testing.T) {
	def := refModule()
	src := squash(genService(&def))
	for _, want := range []string{
		"SupplierID uuid.UUID",  // 必填 ref → 值
		"CategoryID *uuid.UUID", // 可选 ref → 指针
	} {
		if !strings.Contains(src, want) {
			t.Errorf("ref service 应包含：%s", want)
		}
	}
}

func TestGenRefHandler(t *testing.T) {
	def := refModule()
	src := squash(genHandler(&def))
	for _, want := range []string{
		`"github.com/google/uuid"`,    // 用到 uuid.Parse 要 import
		`format:"uuid"`,               // Body tag
		"uuid.Parse(body.SupplierID)", // 必填 ref 解析（id→ID 缩略词，对齐 sqlc）
		`if body.CategoryID != "" {`,  // 可选 ref 判空
		"errInvalidField",             // 解析失败字段级错误
	} {
		if !strings.Contains(src, want) {
			t.Errorf("ref handler 应包含：%s", want)
		}
	}
}

func TestGenRefFrontend(t *testing.T) {
	def := refModule()
	schema := genSchemaTS(&def)
	for _, want := range []string{
		"supplier_id: z.string().uuid('请选择供应商'),", // 必填 ref → uuid 校验
		"category_id: z.string(),",                // 可选 ref
	} {
		if !strings.Contains(schema, want) {
			t.Errorf("ref schema.ts 应包含：%s", want)
		}
	}
	// 模拟 resolveRefTargets 的产物（真实流程里读目标 YAML 得到）：
	// supplier 有 read + 可搜；category 两者都没有 —— 覆盖选择器的两个分支。
	def.refResolved = map[string]resolvedRef{
		"supplier": {Key: "supplier", Entity: "Supplier", Entities: "Suppliers", DisplayField: "name", HasRead: true, HasSearch: true},
		"category": {Key: "category", Entity: "Category", Entities: "Categories", DisplayField: "title", HasRead: false, HasSearch: false},
	}
	page := genNewPageTSX(&def)
	for _, want := range []string{
		"import { RefSelect } from '@/components/RefSelect'",
		"import { listSuppliers, getSupplier } from '@/features/supplier/api'",
		"import { listCategories } from '@/features/category/api'", // 无 read → 不 import getCategory
		"<RefSelect",
		`entity="supplier"`,
		"await listSuppliers({ page: 1, pageSize: 20, keyword: keyword || undefined })", // 可搜 → 带 keyword
		"label: it.name ?? ''",
		"resolveLabel={async (id) => (await getSupplier(id)).name ?? ''}", // 有 read → 反查标签
		"await listCategories({ page: 1, pageSize: 20 })",                 // 不可搜 → 无 keyword
		"label: it.title ?? ''",
	} {
		if !strings.Contains(page, want) {
			t.Errorf("ref NewPage 应包含：%s", want)
		}
	}
	// category 无 read 动作，不该出现它的 resolveLabel / getCategory。
	if strings.Contains(page, "getCategory") {
		t.Error("category 无 read 动作，不该 import/调用 getCategory")
	}
	if strings.Contains(page, `{...form.register("supplier_id")}`) {
		t.Error("ref 不该再是 uuid 文本输入（已换成 RefSelect）")
	}

	// DetailPage：读态显示 JOIN 出的名字（<字段>_label），不再逐条反查；编辑态仍 import 选择器 api。
	detail := genDetailPageTSX(&def)
	for _, want := range []string{
		"import { listSuppliers, getSupplier } from '@/features/supplier/api'", // 编辑态：搜索 + 反查当前项
		"import { listCategories } from '@/features/category/api'",             // category 无 read → 只 list
		"entity.supplier_id_label || '—'",                                      // 读态直接用 JOIN 出的名字
		"entity.category_id_label || '—'",
	} {
		if !strings.Contains(detail, want) {
			t.Errorf("ref DetailPage 应包含：%s", want)
		}
	}
	if strings.Contains(detail, "RefLabel") {
		t.Error("读态改用 JOIN 出的 _label 列，不该再 import/用 RefLabel")
	}
	if strings.Contains(detail, "getCategory") {
		t.Error("category 无 read，DetailPage 不该 import/调用 getCategory")
	}

	// 列表 cell 也显示名字（JOIN 出的 _label）。
	list := genListPageTSX(&def)
	if !strings.Contains(list, "row.supplier_id_label || '—'") {
		t.Error("ref 列表 cell 应显示 JOIN 出的名字（row.supplier_id_label）")
	}

	// 后端：List/Get 用 sqlc.embed(t) + LEFT JOIN 把目标名字作为 <字段>_label 查出。
	q := genQueries(&def)
	for _, want := range []string{
		"SELECT sqlc.embed(t)",
		"r0.name AS supplier_id_label",
		"LEFT JOIN suppliers r0 ON r0.tenant_id = t.tenant_id AND r0.id = t.supplier_id",
		"r1.title AS category_id_label",
		"LEFT JOIN categories r1 ON r1.tenant_id = t.tenant_id AND r1.id = t.category_id",
		"WHERE t.tenant_id = sqlc.arg('tenant_id')", // 有 JOIN → where 列带别名
	} {
		if !strings.Contains(q, want) {
			t.Errorf("ref 查询应包含：%s", want)
		}
	}
	// service：View 有 label 字段，List/Get 映射 fromRow(row.Entity) + textOr。
	svc := genService(&def)
	for _, want := range []string{
		"SupplierIDLabel string", // View 只读 label 字段（gofmt 会对齐，不断言精确空格）
		`json:"supplier_id_label"`,
		"item := fromRow(row.Product)",
		"item.SupplierIDLabel = textOr(row.SupplierIDLabel)",
		"func textOr(v *string) string {",
	} {
		if !strings.Contains(svc, want) {
			t.Errorf("ref service 应包含：%s", want)
		}
	}
}

// TestPascalInitialisms id 要整段大写（对齐 sqlc 的 ParentID/UserID），否则字段名对不上编译炸。
func TestPascalInitialisms(t *testing.T) {
	cases := map[string]string{
		"supplier_id":     "SupplierID",
		"id":              "ID",
		"service_account": "ServiceAccount",
		"name":            "Name",
		"category_id":     "CategoryID",
	}
	for in, want := range cases {
		if got := pascal(in); got != want {
			t.Errorf("pascal(%q) = %q，应为 %q", in, got, want)
		}
	}
	// camel 从 pascal 派生，跟着对：supplier_id → supplierID
	if got := camel("supplier_id"); got != "supplierID" {
		t.Errorf("camel(supplier_id) = %q，应为 supplierID", got)
	}
}

// TestCheckRefTargets ref 目标不存在（无 modules/<目标>.yaml）时校验期就报，别等迁移跑起来。
func TestCheckRefTargets(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "modules"), 0o755); err != nil {
		t.Fatal(err)
	}
	// 只建 supplier.yaml，不建 category.yaml。
	if err := os.WriteFile(filepath.Join(root, "modules", "supplier.yaml"), []byte("key: supplier\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	// refModule 引用 supplier（在）+ category（不在）→ 应报 category 缺失。
	def := refModule()
	err := checkRefTargets(root, &def)
	if err == nil || !strings.Contains(err.Error(), "category") {
		t.Fatalf("category 目标缺失应报错，得到：%v", err)
	}

	// 只引用 supplier（在）→ 通过。
	def2 := ModuleDef{
		Key: "product", Fields: []Field{
			{Name: "supplier_id", Type: typeRef, Ref: "supplier"},
		},
	}
	if err := checkRefTargets(root, &def2); err != nil {
		t.Errorf("supplier 目标在，不该报错：%v", err)
	}

	// 自引用（目标 == 自己）→ 通过（自己的 yaml 一定在）。
	def3 := ModuleDef{
		Key: "node", Fields: []Field{
			{Name: "parent_id", Type: typeRef, Ref: "node"},
		},
	}
	if err := checkRefTargets(root, &def3); err != nil {
		t.Errorf("自引用不该报错：%v", err)
	}
}

// TestValidateRefRequiresTarget ref 字段必须给 ref（目标模块 key）。
func TestValidateRefRequiresTarget(t *testing.T) {
	def := refModule()
	def.Fields[1].Ref = "" // 去掉目标
	errs := def.Validate()
	if !containsSub(errs, "必须给 ref") {
		t.Errorf("ref 缺目标应报错，实际：%v", errs)
	}
}
