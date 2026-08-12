package main

import (
	"strings"
	"testing"
)

// ⚠️ 页面产出没法在这里编译验（要 tsc + eslint + 后端接进路由后的 OpenAPI 类型），
// 这些只是「该出现的片段在不在、import 按字段裁没裁」的属性测试。真验在 4e。

func TestGenNewPageStructure(t *testing.T) {
	def := validGenerated()
	src := genNewPageTSX(&def)
	for _, want := range []string{
		"Safe to edit",
		"export default function SupplierNewPage()",
		"const { create } = useSupplierMutations()",
		"resolver: zodResolver(supplierSchema), defaultValues: emptySupplier",
		"const guard = useUnsavedGuard(form.formState.isDirty)",
		"await create.mutateAsync({",
		"name: values.name.trim(),",                   // string trim
		"credit: values.credit,",                      // decimal 原样
		"started_at: values.started_at || undefined,", // 可选 date 空了发 undefined（huma format 拒空串）
		"const LIST_PATH = '/suppliers'",
		"const FORM_ID = 'supplier-new-form'",
		"<ConfirmDialog state={guard} />",
	} {
		if !strings.Contains(src, want) {
			t.Errorf("NewPage 应包含：%s", want)
		}
	}
}

// TestGenNewPageControls 各类字段渲染对应控件；enum 用 SelectField + labels，无 enum/bool 不 import Controller。
func TestGenNewPageControls(t *testing.T) {
	def := validGenerated() // 有 enum(status) 无 bool
	src := genNewPageTSX(&def)
	for _, want := range []string{
		"import { Controller, useForm } from 'react-hook-form'", // 有 enum → 要 Controller
		"import { SelectField } from '@/components/ui/select'",
		"import { statusLabels } from '@/features/supplier/types'",
		"options={Object.entries(statusLabels).map(([value, label]) => ({ value, label }))}",
		`{...form.register("name")}`,                             // string
		`<Input type="date" {...form.register("started_at")} />`, // date
	} {
		if !strings.Contains(src, want) {
			t.Errorf("NewPage 控件产出应包含：%s", want)
		}
	}
	// 没有 bool 字段 → 不 import Checkbox
	if strings.Contains(src, "import { Checkbox }") {
		t.Error("没有 bool 字段不该 import Checkbox（未用会被 eslint 拦）")
	}
}

// TestGenNewPageNoEnumNoController 无 enum/bool 的模块不 import Controller/SelectField/Checkbox。
func TestGenNewPageNoEnumNoController(t *testing.T) {
	def := ModuleDef{
		Key: "note", Name: "便签", Generated: true, Scoped: true,
		Menu:    Menu{Path: "/notes", Icon: "box"},
		Fields:  []Field{{Name: "title", Type: typeString, Label: "标题", Required: true, Max: 100}},
		Actions: []string{actList, actCreate},
	}
	src := genNewPageTSX(&def)
	if strings.Contains(src, "Controller") {
		t.Error("无 enum/bool 不该 import/用 Controller")
	}
	if strings.Contains(src, "SelectField") || strings.Contains(src, "Checkbox") {
		t.Error("无 enum/bool 不该 import SelectField/Checkbox")
	}
	if !strings.Contains(src, "import { useForm } from 'react-hook-form'") {
		t.Error("应只 import useForm")
	}
}

func TestGenListPageStructure(t *testing.T) {
	def := validGenerated()
	src := genListPageTSX(&def)
	for _, want := range []string{
		"export default function SupplierListPage()",
		"const query = useSuppliers({",
		"keyword: params.search || undefined,",
		`status: params.filter("status") || undefined,`,        // filter：Query 用 camel key、filter 传 snake
		`startedAt: params.filter("started_at") || undefined,`, // date filter
		"const columns: Array<Column<Supplier>> = [",
		"rowLink={(row) => `/suppliers/${row.id}`}", // 有 read → 行链接到详情
		`emptyMessage="还没有供应商"`,
		"<ConfirmDialog state={confirm} />",
	} {
		if !strings.Contains(src, want) {
			t.Errorf("ListPage 应包含：%s", want)
		}
	}
}

// TestGenListPageColumns 各类型列的 cell 渲染 + 可空兜 '—' + date 用 DateTime + enum 查表。
func TestGenListPageColumns(t *testing.T) {
	def := validGenerated()
	src := genListPageTSX(&def)
	for _, want := range []string{
		"cell: (row) => row.name },",                                  // required string 直接取
		"row.status ? (statusLabels[row.status] ?? row.status) : '—'", // 可空 enum 查表 + 兜底
		"cell: (row) => row.credit ?? '—' },",                         // 可空 decimal 兜 '—'
		"cell: (row) => <DateTime value={row.started_at} /> },",       // date 用 DateTime
		"key: 'actions'",
		"can('supplier', 'update')",
		"can('supplier', 'delete')",
		"remove.mutateAsync({ id: row.id, version: row.version })",
	} {
		if !strings.Contains(src, want) {
			t.Errorf("ListPage 列产出应包含：%s", want)
		}
	}
}

// TestGenListPageReadOnly 只读列表（list+read）：不产操作列、不 import 删除/编辑相关。
func TestGenListPageReadOnly(t *testing.T) {
	def := ModuleDef{
		Key: "ledger", Name: "台账", Generated: true, Scoped: true,
		Menu:    Menu{Path: "/ledgers", Icon: "box"},
		Fields:  []Field{{Name: "title", Type: typeString, Label: "标题", Required: true, Searchable: true, Max: 100}},
		Actions: []string{actList, actRead},
	}
	src := genListPageTSX(&def)
	for _, bad := range []string{"RowActions", "ConfirmDialog", "useConfirm", "Trash2", "Pencil", "useSupplierMutations", "FROM_LIST_STATE"} {
		if strings.Contains(src, bad) {
			t.Errorf("只读列表不该出现：%s", bad)
		}
	}
	if !strings.Contains(src, "rowLink={(row) => `/ledgers/${row.id}`}") {
		t.Error("有 read 动作应有 rowLink 到详情")
	}
}

func TestGenDetailPageStructure(t *testing.T) {
	def := validGenerated()
	src := genDetailPageTSX(&def)
	for _, want := range []string{
		"export default function SupplierDetailPage()",
		"const detail = useSupplier(id)",
		"const editing = searchParams.get('edit') === '1' && editable",
		"function SupplierDetailBody({",
		"key={editing ? `edit-${editSeq}` : 'read'}", // 切编辑态重置表单
		"resolver: zodResolver(supplierSchema),",
		"name: entity.name ?? '',",                                       // 必填 string 映射
		"status: (entity.status ?? '') as SupplierFormValues['status'],", // enum cast
		"credit: entity.credit ?? '',",                                   // 可空 decimal 兜零值
		"await update.mutateAsync({",
		"id: entity.id,\n        version: entity.version,", // 乐观锁
		"err.code === 'common.version_conflict'",           // 版本冲突翻译
		"function cancelEdit()",
		"title={entity.name}", // 标题取第一个 string 字段
		"<ConfirmDialog state={confirm} />",
		"<ConfirmDialog state={guard} />",
	} {
		if !strings.Contains(src, want) {
			t.Errorf("DetailPage 应包含：%s", want)
		}
	}
}

// TestGenDetailPageViewEdit 字段双态：编辑给控件、只读给展示值；required 星号只在必填字段。
func TestGenDetailPageViewEdit(t *testing.T) {
	def := validGenerated()
	src := genDetailPageTSX(&def)
	for _, want := range []string{
		`<DetailItem label="供应商名称" required={editing} error={errors.name?.message}>`, // 必填有 required
		"{editing ? (",
		`{...form.register("name")}`, // 编辑控件
		"entity.status ? (statusLabels[entity.status] ?? entity.status) : '—'", // 只读 enum 查表
		"<DateTime value={entity.started_at} />",                               // 只读 date
	} {
		if !strings.Contains(src, want) {
			t.Errorf("DetailPage 双态产出应包含：%s", want)
		}
	}
	// 可选字段（remark）不该有 required 星号
	if strings.Contains(src, `label="备注" required=`) {
		t.Error("可选字段不该有 required 星号")
	}
}

// TestGenDetailPageReadOnly 无 update 动作 → 只读详情页（无表单/保存，仅展示 + 可选删除）。
func TestGenDetailPageReadOnly(t *testing.T) {
	def := ModuleDef{
		Key: "ledger", Name: "台账", Generated: true, Scoped: true,
		Menu:    Menu{Path: "/ledgers", Icon: "box"},
		Fields:  []Field{{Name: "title", Type: typeString, Label: "标题", Required: true, Max: 100}},
		Actions: []string{actList, actRead},
	}
	src := genDetailPageTSX(&def)
	for _, bad := range []string{"useForm", "zodResolver", "update.mutateAsync", "FORM_ID", "editing", "SelectField"} {
		if strings.Contains(src, bad) {
			t.Errorf("只读详情页不该出现：%s", bad)
		}
	}
	if !strings.Contains(src, "function LedgerDetailBody({") {
		t.Error("应有内层 body 组件")
	}
}

// TestGenNewPageBoolControl bool 字段用 Checkbox + Controller。
func TestGenNewPageBoolControl(t *testing.T) {
	def := ModuleDef{
		Key: "flagged", Name: "开关项", Generated: true, Scoped: true,
		Menu: Menu{Path: "/flaggeds", Icon: "box"},
		Fields: []Field{
			{Name: "name", Type: typeString, Label: "名称", Required: true, Max: 100},
			{Name: "active", Type: typeBool, Label: "启用", Default: "true"},
		},
		Actions: []string{actList, actCreate},
	}
	src := genNewPageTSX(&def)
	for _, want := range []string{
		"import { Checkbox } from '@/components/ui/checkbox'",
		"import { Controller, useForm } from 'react-hook-form'",
		"onCheckedChange={(v) => field.onChange(v === true)}",
		"active: values.active,", // bool 原样
	} {
		if !strings.Contains(src, want) {
			t.Errorf("bool 字段 NewPage 应包含：%s", want)
		}
	}
}
