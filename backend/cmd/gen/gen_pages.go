package main

import (
	"fmt"
	"strings"
)

// gen_pages.go 产出前端页面（种子文件）：NewPage / ListPage / DetailPage。
//
// 页面用共享布局组件（<DetailPage>/<DetailItem>/<ListPage>），业务页面只填「有什么字段、
// 每个字段怎么渲染」。守设计系统 lint：不写行内 style、不写死颜色/px/字号、时间用 <DateTime>、
// 请求走 queries（@/api/client）。
//
// ⚠️ 前端页面**只能靠 4e 真验**（tsc + eslint，还要后端模块接进路由后 gen-api 出 OpenAPI 类型）。
// 这里的属性测试只兜「该出现的片段在不在、import 按字段裁没裁」，别当它是「编译过了」。

// genNewPageTSX 产出 features/<key>/NewPage.tsx —— 新增表单页。
func genNewPageTSX(def *ModuleDef) string {
	entity := pascal(def.Key)
	varName := camel(def.Key)
	hasEnum := hasFieldType(def, typeEnum)
	hasBool := hasFieldType(def, typeBool)
	needController := hasEnum || hasBool || hasRef(def)

	var b strings.Builder
	b.WriteString(tsSeedHeader())

	// ---- import（按字段裁）----
	b.WriteString("import { zodResolver } from '@hookform/resolvers/zod'\n")
	b.WriteString("import { Loader2 } from 'lucide-react'\n")
	b.WriteString("import { useState } from 'react'\n")
	if needController {
		b.WriteString("import { Controller, useForm } from 'react-hook-form'\n")
	} else {
		b.WriteString("import { useForm } from 'react-hook-form'\n")
	}
	b.WriteString("import { useNavigate } from 'react-router'\n\n")

	b.WriteString("import { ApiError } from '@/api/client'\n")
	b.WriteString("import { ConfirmDialog } from '@/components/ConfirmDialog'\n")
	b.WriteString("import { DetailItem, DetailPage, DetailSection } from '@/components/DetailPage'\n")
	b.WriteString("import { FormAlert } from '@/components/PageState'\n")
	writeRefSelectImport(&b, def)
	b.WriteString("import { Button } from '@/components/ui/button'\n")
	if hasBool {
		b.WriteString("import { Checkbox } from '@/components/ui/checkbox'\n")
	}
	b.WriteString("import { Input } from '@/components/ui/input'\n")
	if hasEnum {
		b.WriteString("import { SelectField } from '@/components/ui/select'\n")
	}
	writeRefTargetAPIImports(&b, def, true) // 新增页是编辑表单，选择器要 list（搜索）
	fmt.Fprintf(&b, "import { use%sMutations } from '@/features/%s/queries'\n", entity, def.Key)
	fmt.Fprintf(&b, "import { empty%s, %sSchema, type %sFormValues } from '@/features/%s/schema'\n", entity, varName, entity, def.Key)
	if hasEnum {
		fmt.Fprintf(&b, "import { %s } from '@/features/%s/types'\n", strings.Join(enumLabelImports(def), ", "), def.Key)
	}
	b.WriteString("import { useUnsavedGuard } from '@/lib/unsaved-guard'\n\n")

	// ---- 常量 ----
	fmt.Fprintf(&b, "const LIST_PATH = '%s'\n", def.Menu.Path)
	fmt.Fprintf(&b, "const FORM_ID = '%s-new-form'\n\n", def.Key)

	// ---- 组件 ----
	fmt.Fprintf(&b, "/** 新增%s。是一个页面，不是弹窗（DECISIONS.md §7.6）。 */\n", def.Name)
	fmt.Fprintf(&b, "export default function %sNewPage() {\n", entity)
	b.WriteString("  const navigate = useNavigate()\n")
	fmt.Fprintf(&b, "  const { create } = use%sMutations()\n", entity)
	fmt.Fprintf(&b, "  const form = useForm<%sFormValues>({ resolver: zodResolver(%sSchema), defaultValues: empty%s })\n", entity, varName, entity)
	b.WriteString("  const [failure, setFailure] = useState<string | null>(null)\n")
	b.WriteString("  const guard = useUnsavedGuard(form.formState.isDirty)\n")
	b.WriteString("  const errors = form.formState.errors\n\n")

	// onSubmit
	fmt.Fprintf(&b, "  async function onSubmit(values: %sFormValues) {\n", entity)
	b.WriteString("    setFailure(null)\n    try {\n")
	b.WriteString("      const created = await create.mutateAsync({\n")
	for _, f := range def.Fields {
		fmt.Fprintf(&b, "        %s: %s,\n", f.Name, submitValueExpr(f))
	}
	b.WriteString("      })\n")
	b.WriteString("      // 建完进详情页。replace 让「后退」不回到已提交的空表单。\n")
	b.WriteString("      void navigate(`${LIST_PATH}/${created.id}`, { replace: true })\n")
	b.WriteString("    } catch (error) {\n")
	b.WriteString("      if (!(error instanceof ApiError)) {\n        setFailure('创建失败了，请重试。')\n        return\n      }\n")
	fmt.Fprintf(&b, "      for (const item of error.formErrors()) {\n        form.setError(item.field as keyof %sFormValues, { message: item.message })\n      }\n", entity)
	b.WriteString("      if (error.formErrors().length === 0) setFailure(error.message)\n")
	b.WriteString("    }\n  }\n\n")

	// JSX
	b.WriteString("  return (\n    <>\n")
	fmt.Fprintf(&b, "      <DetailPage\n        title=\"新增%s\"\n        backTo={LIST_PATH}\n", def.Name)
	b.WriteString("        alert={failure ? <FormAlert message={failure} /> : null}\n")
	b.WriteString("        actions={\n          <span className=\"flex items-center gap-2\">\n")
	b.WriteString("            <Button variant=\"outline\" onClick={() => void navigate(LIST_PATH)} disabled={create.isPending}>\n              取消\n            </Button>\n")
	b.WriteString("            <Button type=\"submit\" form={FORM_ID} disabled={create.isPending}>\n")
	b.WriteString("              {create.isPending ? <Loader2 className=\"animate-spin\" /> : null}\n              创建\n            </Button>\n          </span>\n        }\n      >\n")
	b.WriteString("        <form id={FORM_ID} className=\"space-y-8\" onSubmit={(event) => void form.handleSubmit(onSubmit)(event)}>\n")
	b.WriteString("          <DetailSection title=\"基本信息\">\n")
	for _, f := range def.Fields {
		writeDetailItemEdit(&b, def, f)
	}
	b.WriteString("          </DetailSection>\n        </form>\n      </DetailPage>\n\n")
	b.WriteString("      <ConfirmDialog state={guard} />\n    </>\n  )\n}\n")

	return b.String()
}

// genListPageTSX 产出 features/<key>/ListPage.tsx —— 列表页。
func genListPageTSX(def *ModuleDef) string {
	entity := pascal(def.Key)
	entities := pascal(pluralize(def.Key))
	acts := actionSet(def)
	hasDate := hasFieldType(def, typeDate, typeTimestamp)
	enumFilters := enumFilterFields(def)
	rowActions := acts[actUpdate] || acts[actDelete]

	var b strings.Builder
	b.WriteString(tsSeedHeader())

	// ---- import（按用到的裁）----
	icons := []string{}
	if acts[actUpdate] {
		icons = append(icons, "Pencil")
	}
	if acts[actCreate] {
		icons = append(icons, "Plus")
	}
	if acts[actDelete] {
		icons = append(icons, "Trash2")
	}
	if len(icons) > 0 {
		fmt.Fprintf(&b, "import { %s } from 'lucide-react'\n", strings.Join(icons, ", "))
	}
	rr := []string{}
	if acts[actCreate] {
		rr = append(rr, "Link")
	}
	if acts[actUpdate] {
		rr = append(rr, "useNavigate")
	}
	if len(rr) > 0 {
		fmt.Fprintf(&b, "import { %s } from 'react-router'\n", strings.Join(rr, ", "))
	}
	b.WriteString("\n")
	if len(enumFilters) > 0 {
		b.WriteString("import type { FilterSpec } from '@/components/ListFilters'\n")
	}
	b.WriteString("import { ListPage, type Column } from '@/components/ListPage'\n")
	if hasDate {
		b.WriteString("import { DateTime } from '@/components/DateTime'\n")
	}
	if rowActions {
		b.WriteString("import { RowActions } from '@/components/RowActions'\n")
	}
	if acts[actCreate] {
		b.WriteString("import { Button } from '@/components/ui/button'\n")
	}
	if acts[actDelete] {
		b.WriteString("import { ConfirmDialog } from '@/components/ConfirmDialog'\n")
	}
	// queries：列表 hook + （有删除时）mutations
	if acts[actDelete] {
		fmt.Fprintf(&b, "import { use%s, use%sMutations } from '@/features/%s/queries'\n", entities, entity, def.Key)
	} else {
		fmt.Fprintf(&b, "import { use%s } from '@/features/%s/queries'\n", entities, def.Key)
	}
	// types：Entity + enum labels
	typeImports := []string{fmt.Sprintf("type %s", entity)}
	typeImports = append(enumLabelImports(def), typeImports...)
	fmt.Fprintf(&b, "import { %s } from '@/features/%s/types'\n", strings.Join(typeImports, ", "), def.Key)
	if acts[actDelete] {
		b.WriteString("import { useConfirm } from '@/lib/confirm'\n")
	}
	if acts[actUpdate] {
		b.WriteString("import { FROM_LIST_STATE } from '@/lib/detail-nav'\n")
	}
	b.WriteString("import { useListParams } from '@/lib/list-params'\n")
	if rowActions || acts[actCreate] {
		b.WriteString("import { useSession } from '@/lib/session'\n")
	}
	b.WriteString("\n")

	// ---- filterSpecs（enum filterable → 下拉）----
	if len(enumFilters) > 0 {
		b.WriteString("const filterSpecs: FilterSpec[] = [\n")
		for _, f := range enumFilters {
			fmt.Fprintf(&b, "  {\n    key: %q,\n    label: %q,\n", f.Name, f.Label)
			fmt.Fprintf(&b, "    options: Object.entries(%sLabels).map(([value, label]) => ({ value, label })),\n", camel(f.Name))
			b.WriteString("  },\n")
		}
		b.WriteString("]\n\n")
	}

	// ---- 组件 ----
	fmt.Fprintf(&b, "export default function %sListPage() {\n", entity)
	b.WriteString("  const params = useListParams()\n")
	if rowActions || acts[actCreate] {
		b.WriteString("  const { can } = useSession()\n")
	}
	if acts[actUpdate] {
		b.WriteString("  const navigate = useNavigate()\n")
	}
	if acts[actDelete] {
		b.WriteString("  const confirm = useConfirm()\n")
		fmt.Fprintf(&b, "  const { remove } = use%sMutations()\n", entity)
	}
	b.WriteString("\n")
	// query
	fmt.Fprintf(&b, "  const query = use%s({\n    page: params.page,\n    pageSize: params.pageSize,\n", entities)
	if hasSearch(def) {
		b.WriteString("    keyword: params.search || undefined,\n")
	}
	for _, f := range filterFields(def) {
		fmt.Fprintf(&b, "    %s: params.filter(%q) || undefined,\n", camel(f.Name), f.Name)
	}
	b.WriteString("  })\n\n")

	// columns
	fmt.Fprintf(&b, "  const columns: Array<Column<%s>> = [\n", entity)
	for _, f := range def.Fields {
		writeColumn(&b, def, f)
	}
	if rowActions {
		writeActionsColumn(&b, def, acts)
	}
	b.WriteString("  ]\n\n")

	// JSX
	b.WriteString("  return (\n    <>\n      <ListPage\n")
	fmt.Fprintf(&b, "        title=%q\n", def.Name+"管理")
	b.WriteString("        columns={columns}\n        query={query}\n        params={params}\n")
	b.WriteString("        rowKey={(row) => row.id}\n")
	if acts[actRead] {
		fmt.Fprintf(&b, "        rowLink={(row) => `%s/${row.id}`}\n", def.Menu.Path)
	}
	if hasSearch(def) {
		b.WriteString("        searchPlaceholder=\"搜索\"\n")
	}
	if len(enumFilters) > 0 {
		b.WriteString("        filters={filterSpecs}\n")
	}
	fmt.Fprintf(&b, "        emptyMessage=\"还没有%s\"\n", def.Name)
	if acts[actCreate] {
		b.WriteString("        actions={\n")
		fmt.Fprintf(&b, "          can('%s', 'create') ? (\n", def.Key)
		fmt.Fprintf(&b, "            <Button asChild>\n              <Link to=\"%s/new\">\n                <Plus /> 新增%s\n              </Link>\n            </Button>\n          ) : null\n        }\n", def.Menu.Path, def.Name)
	}
	b.WriteString("      />\n")
	if acts[actDelete] {
		b.WriteString("\n      <ConfirmDialog state={confirm} />\n")
	}
	b.WriteString("    </>\n  )\n}\n")

	return b.String()
}

// titleField 返回详情页标题用的字段（第一个 string/text 字段，通常是名称）；没有就返回空。
func titleField(def *ModuleDef) string {
	for _, f := range def.Fields {
		if f.Type == typeString || f.Type == typeText {
			return f.Name
		}
	}
	return ""
}

// genDetailPageTSX 产出 features/<key>/DetailPage.tsx —— 详情页（看/改同页）。
func genDetailPageTSX(def *ModuleDef) string {
	entity := pascal(def.Key)
	varName := camel(def.Key)
	acts := actionSet(def)
	canEdit := acts[actUpdate]
	canDelete := acts[actDelete]
	hasEnum := hasFieldType(def, typeEnum)
	hasBool := hasFieldType(def, typeBool)
	hasDate := hasFieldType(def, typeDate, typeTimestamp)
	needController := canEdit && (hasEnum || hasBool || hasRef(def))
	tf := titleField(def)

	var b strings.Builder
	b.WriteString(tsSeedHeader())

	// ---- import（按 canEdit/canDelete + 字段裁）----
	icons := []string{}
	if canEdit {
		icons = append(icons, "Loader2", "Pencil")
	}
	if canDelete {
		icons = append(icons, "Trash2")
	}
	if len(icons) > 0 {
		fmt.Fprintf(&b, "import { %s } from 'lucide-react'\n", strings.Join(icons, ", "))
	}
	b.WriteString("import { useState } from 'react'\n")
	if needController {
		b.WriteString("import { Controller, useForm } from 'react-hook-form'\n")
	} else if canEdit {
		b.WriteString("import { useForm } from 'react-hook-form'\n")
	}
	b.WriteString("import { useNavigate, useParams, useSearchParams } from 'react-router'\n\n")

	if canEdit {
		b.WriteString("import { ApiError } from '@/api/client'\n")
	}
	b.WriteString("import { ConfirmDialog } from '@/components/ConfirmDialog'\n")
	if hasDate {
		b.WriteString("import { DateTime } from '@/components/DateTime'\n")
	}
	b.WriteString("import { DetailItem, DetailPage, DetailSection } from '@/components/DetailPage'\n")
	if canEdit {
		b.WriteString("import { FormAlert } from '@/components/PageState'\n")
		writeRefSelectImport(&b, def) // ref 编辑控件只在编辑态出现
	}
	b.WriteString("import { Button } from '@/components/ui/button'\n")
	if canEdit && hasBool {
		b.WriteString("import { Checkbox } from '@/components/ui/checkbox'\n")
	}
	if canEdit {
		b.WriteString("import { Input } from '@/components/ui/input'\n")
	}
	if canEdit && hasEnum {
		b.WriteString("import { SelectField } from '@/components/ui/select'\n")
	}
	// queries
	mut := []string{}
	if canDelete {
		mut = append(mut, "remove")
	}
	if canEdit {
		mut = append(mut, "update")
	}
	if len(mut) > 0 {
		fmt.Fprintf(&b, "import { use%s, use%sMutations } from '@/features/%s/queries'\n", entity, entity, def.Key)
	} else {
		fmt.Fprintf(&b, "import { use%s } from '@/features/%s/queries'\n", entity, def.Key)
	}
	if canEdit {
		fmt.Fprintf(&b, "import { %sSchema, type %sFormValues } from '@/features/%s/schema'\n", varName, entity, def.Key)
	}
	// ref 目标 api：list 只在编辑态要（选择器搜索），get 读写都可能要（反查名字）。ungate ——
	// 只读页也有 ref 读态，要 get 反查。
	writeRefTargetAPIImports(&b, def, canEdit)
	// types：Entity + enum labels（view 态查表用）
	typeImports := append(enumLabelImports(def), "type "+entity)
	fmt.Fprintf(&b, "import { %s } from '@/features/%s/types'\n", strings.Join(typeImports, ", "), def.Key)
	b.WriteString("import { useConfirm } from '@/lib/confirm'\n")
	b.WriteString("import { useSession } from '@/lib/session'\n")
	if canEdit {
		b.WriteString("import { zodResolver } from '@hookform/resolvers/zod'\n")
		b.WriteString("import { useUnsavedGuard } from '@/lib/unsaved-guard'\n")
	}
	b.WriteString("\n")

	fmt.Fprintf(&b, "const LIST_PATH = '%s'\n", def.Menu.Path)
	if canEdit {
		fmt.Fprintf(&b, "const FORM_ID = '%s-detail-form'\n", def.Key)
	}
	b.WriteString("\n")

	writeDetailOuter(&b, def, entity, canEdit)
	writeDetailBody(&b, def, entity, varName, acts, tf)

	return b.String()
}

// writeDetailOuter 产出外层组件：加载数据、管理 ?edit=1、把 body keyed 挂上（切换编辑态时重置表单）。
func writeDetailOuter(b *strings.Builder, def *ModuleDef, entity string, canEdit bool) {
	fmt.Fprintf(b, "/** %s详情页。看和改在同一页，没有编辑弹窗（DECISIONS.md §7.6）。 */\n", def.Name)
	fmt.Fprintf(b, "export default function %sDetailPage() {\n", entity)
	b.WriteString("  const { id = '' } = useParams()\n")
	b.WriteString("  const [searchParams, setSearchParams] = useSearchParams()\n")
	b.WriteString("  const { can } = useSession()\n")
	fmt.Fprintf(b, "  const detail = use%s(id)\n", entity)
	b.WriteString("  const entity = detail.data\n")
	if canEdit {
		fmt.Fprintf(b, "  const editable = can('%s', 'update')\n", def.Key)
		b.WriteString("  const editing = searchParams.get('edit') === '1' && editable\n")
		b.WriteString("  const [editSeq, setEditSeq] = useState(0)\n\n")
		b.WriteString("  function setEditing(next: boolean) {\n    if (next) setEditSeq((n) => n + 1)\n")
		b.WriteString("    setSearchParams(\n      (current) => {\n        const params = new URLSearchParams(current)\n")
		b.WriteString("        if (next) params.set('edit', '1')\n        else params.delete('edit')\n        return params\n      },\n      { replace: true },\n    )\n  }\n\n")
	}
	// loading / not-found
	b.WriteString("  if (!entity) {\n    return (\n")
	fmt.Fprintf(b, "      <DetailPage\n        title=\"%s详情\"\n        backTo={LIST_PATH}\n", def.Name)
	b.WriteString("        loading={detail.isPending}\n        error={detail.isError ? detail.error : undefined}\n")
	b.WriteString("        onRetry={() => void detail.refetch()}\n      >\n        {null}\n      </DetailPage>\n    )\n  }\n\n")
	// mount body
	b.WriteString("  return (\n")
	fmt.Fprintf(b, "    <%sDetailBody\n", entity)
	if canEdit {
		b.WriteString("      key={editing ? `edit-${editSeq}` : 'read'}\n")
		b.WriteString("      entity={entity}\n      editing={editing}\n      editable={editable}\n      onEditingChange={setEditing}\n")
		b.WriteString("      onReload={async () => {\n        await detail.refetch()\n        setEditSeq((n) => n + 1)\n      }}\n")
	} else {
		b.WriteString("      entity={entity}\n")
	}
	b.WriteString("    />\n  )\n}\n\n")
}

// writeDetailBody 产出内层组件：表单 + 字段 view/edit 双态 + 保存/取消/删除。
func writeDetailBody(b *strings.Builder, def *ModuleDef, entity, varName string, acts map[string]bool, tf string) {
	canEdit := acts[actUpdate]
	canDelete := acts[actDelete]

	// 签名
	fmt.Fprintf(b, "function %sDetailBody({\n  entity,\n", entity)
	if canEdit {
		b.WriteString("  editing,\n  editable,\n  onEditingChange,\n  onReload,\n}: {\n")
		fmt.Fprintf(b, "  entity: %s\n  editing: boolean\n  editable: boolean\n  onEditingChange: (next: boolean) => void\n  onReload: () => Promise<void>\n}) {\n", entity)
	} else {
		fmt.Fprintf(b, "}: {\n  entity: %s\n}) {\n", entity)
	}
	b.WriteString("  const navigate = useNavigate()\n  const { can } = useSession()\n  const confirm = useConfirm()\n")
	mut := []string{}
	if canDelete {
		mut = append(mut, "remove")
	}
	if canEdit {
		mut = append(mut, "update")
	}
	if len(mut) > 0 {
		fmt.Fprintf(b, "  const { %s } = use%sMutations()\n", strings.Join(mut, ", "), entity)
	}
	b.WriteString("\n")

	if canEdit {
		fmt.Fprintf(b, "  const form = useForm<%sFormValues>({\n    resolver: zodResolver(%sSchema),\n    defaultValues: {\n", entity, varName)
		for _, f := range def.Fields {
			fmt.Fprintf(b, "      %s: %s,\n", f.Name, formDefaultExpr(f, entity))
		}
		b.WriteString("    },\n  })\n\n")
		b.WriteString("  const dirty = editing && form.formState.isDirty\n  const guard = useUnsavedGuard(dirty)\n")
		b.WriteString("  const [failure, setFailure] = useState<{ message: string; conflict: boolean } | null>(null)\n")
		b.WriteString("  const errors = editing ? form.formState.errors : {}\n\n")
		writeDetailOnSubmit(b, def, entity)
		writeCancelEdit(b)
	} else {
		b.WriteString("  const [failure] = useState<string | null>(null)\n\n")
	}

	writeDetailBodyReturn(b, def, acts, tf)
	b.WriteString("}\n")
}

// writeDetailOnSubmit 产出 onSubmit（update + 版本冲突翻译）。
func writeDetailOnSubmit(b *strings.Builder, def *ModuleDef, entity string) {
	fmt.Fprintf(b, "  async function onSubmit(values: %sFormValues) {\n", entity)
	b.WriteString("    setFailure(null)\n    try {\n")
	b.WriteString("      await update.mutateAsync({\n        id: entity.id,\n        version: entity.version,\n        input: {\n")
	for _, f := range def.Fields {
		fmt.Fprintf(b, "          %s: %s,\n", f.Name, submitValueExpr(f))
	}
	b.WriteString("        },\n      })\n      onEditingChange(false)\n")
	b.WriteString("      await onReload()\n")
	b.WriteString("    } catch (err) {\n")
	b.WriteString("      if (!(err instanceof ApiError)) {\n        setFailure({ message: '保存失败了，请重试。', conflict: false })\n        return\n      }\n")
	fmt.Fprintf(b, "      for (const item of err.formErrors()) {\n        form.setError(item.field as keyof %sFormValues, { message: item.message })\n      }\n", entity)
	b.WriteString("      if (err.formErrors().length === 0) {\n        setFailure({ message: err.message, conflict: err.code === 'common.version_conflict' })\n      }\n")
	b.WriteString("    }\n  }\n\n")
}

// writeCancelEdit 产出 cancelEdit（脏了就先确认）。
func writeCancelEdit(b *strings.Builder) {
	b.WriteString("  function cancelEdit() {\n    if (!dirty) {\n      onEditingChange(false)\n      return\n    }\n")
	b.WriteString("    confirm.open({\n      title: '放弃未保存的修改？',\n      description: '取消之后这次改的内容不会保留。',\n")
	b.WriteString("      confirmText: '放弃',\n      destructive: true,\n      onConfirm: () => onEditingChange(false),\n    })\n  }\n\n")
}

// writeDetailBodyReturn 产出 body 的 JSX。
func writeDetailBodyReturn(b *strings.Builder, def *ModuleDef, acts map[string]bool, tf string) {
	canEdit := acts[actUpdate]
	canDelete := acts[actDelete]
	title := "\"" + def.Name + "详情\""
	if tf != "" {
		title = "{entity." + tf + "}"
	}

	b.WriteString("  return (\n    <>\n      <DetailPage\n")
	fmt.Fprintf(b, "        title=%s\n        backTo={LIST_PATH}\n", title)
	if canEdit {
		b.WriteString("        alert={failure ? <FormAlert message={failure.message} /> : null}\n")
	}
	b.WriteString("        actions={\n")
	writeDetailActions(b, def, acts)
	b.WriteString("        }\n      >\n")

	// fields
	if canEdit {
		b.WriteString("        <form id={FORM_ID} className=\"space-y-8\" onSubmit={(event) => void form.handleSubmit(onSubmit)(event)}>\n")
		b.WriteString("          <DetailSection title=\"基本信息\">\n")
	} else {
		b.WriteString("        <DetailSection title=\"基本信息\">\n")
	}
	for _, f := range def.Fields {
		writeDetailField(b, def, f, canEdit)
	}
	if canEdit {
		b.WriteString("          </DetailSection>\n        </form>\n")
	} else {
		b.WriteString("        </DetailSection>\n")
	}
	b.WriteString("      </DetailPage>\n\n      <ConfirmDialog state={confirm} />\n")
	if canEdit {
		b.WriteString("      <ConfirmDialog state={guard} />\n")
	}
	b.WriteString("    </>\n  )\n")
	_ = canDelete
}

// writeDetailActions 产出标题栏操作区（编辑态：保存/取消；只读态：编辑 + 删除，按 can 门控）。
func writeDetailActions(b *strings.Builder, def *ModuleDef, acts map[string]bool) {
	canEdit := acts[actUpdate]
	canDelete := acts[actDelete]

	if canEdit {
		b.WriteString("          editing ? (\n            <span className=\"flex items-center gap-2\">\n")
		b.WriteString("              <Button variant=\"outline\" onClick={cancelEdit} disabled={update.isPending}>\n                取消\n              </Button>\n")
		b.WriteString("              <Button type=\"submit\" form={FORM_ID} disabled={update.isPending}>\n                {update.isPending ? <Loader2 className=\"animate-spin\" /> : null}\n                保存\n              </Button>\n            </span>\n          ) : (\n")
	}
	// 只读态操作（编辑态没有 update 时也走这里）
	b.WriteString("            <span className=\"flex items-center gap-2\">\n")
	if canEdit {
		fmt.Fprintf(b, "              {editable ? (\n                <Button variant=\"outline\" onClick={() => onEditingChange(true)}>\n                  <Pencil /> 编辑\n                </Button>\n              ) : null}\n")
	}
	if canDelete {
		fmt.Fprintf(b, "              {can('%s', 'delete') ? (\n", def.Key)
		b.WriteString("                <Button\n                  variant=\"outline\"\n                  onClick={() =>\n                    confirm.open({\n")
		fmt.Fprintf(b, "                      title: '删除%s？',\n", def.Name)
		b.WriteString("                      description: '删除后不可恢复。',\n                      confirmText: '删除',\n                      destructive: true,\n")
		b.WriteString("                      onConfirm: async () => {\n                        await remove.mutateAsync({ id: entity.id, version: entity.version })\n                        void navigate(LIST_PATH)\n                      },\n                    })\n                  }\n                >\n                  <Trash2 /> 删除\n                </Button>\n              ) : null}\n")
	}
	b.WriteString("            </span>\n")
	if canEdit {
		b.WriteString("          )\n")
	}
}

// writeDetailField 产出一个字段的 <DetailItem>：编辑态给控件，只读态给展示值。
func writeDetailField(b *strings.Builder, def *ModuleDef, f Field, canEdit bool) {
	indent := "          "
	if canEdit {
		indent = "            "
	}
	if canEdit {
		// required 星号只对必填字段、且只在编辑态显示。
		reqAttr := ""
		if f.Required {
			reqAttr = " required={editing}"
		}
		fmt.Fprintf(b, "%s<DetailItem label=%q%s error={errors.%s?.message}>\n", indent, f.Label, reqAttr, f.Name)
		b.WriteString(indent + "  {editing ? (\n")
		writeFormControl(b, def, f) // 14 空格缩进，够用
		b.WriteString(indent + "  ) : (\n")
		fmt.Fprintf(b, "%s    %s\n", indent, detailReadExpr(def, f))
		b.WriteString(indent + "  )}\n")
		b.WriteString(indent + "</DetailItem>\n\n")
	} else {
		fmt.Fprintf(b, "%s<DetailItem label=%q>\n", indent, f.Label)
		fmt.Fprintf(b, "%s  %s\n", indent, detailReadExpr(def, f))
		b.WriteString(indent + "</DetailItem>\n\n")
	}
}

// enumFilterFields 返回可筛选的 enum 字段（filterSpecs 下拉用）。
func enumFilterFields(def *ModuleDef) []Field {
	var out []Field
	for _, f := range filterFields(def) {
		if f.Type == typeEnum {
			out = append(out, f)
		}
	}
	return out
}

// writeColumn 产出一列（cell 按类型渲染，可空字段兜 '—'）。
func writeColumn(b *strings.Builder, def *ModuleDef, f Field) {
	fmt.Fprintf(b, "    { key: %q, header: %q, cell: (row) => %s },\n", f.Name, f.Label, cellExpr(def, f))
}

// cellExpr 是列表 cell 的渲染表达式（取自 row）。ref 字段显示 JOIN 出的名字（<字段>_label），
// 其余走通用 valueExpr。
func cellExpr(def *ModuleDef, f Field) string {
	if f.Type == typeRef && def.refResolved[f.Ref].DisplayField != "" {
		return refLabelValueExpr(f, "row")
	}
	return valueExpr(f, "row")
}

// valueExpr 把一个字段渲染成展示值（列表 cell / 详情 view 态共用）。可空字段（!required）兜 '—'。
func valueExpr(f Field, obj string) string {
	nm := obj + "." + f.Name
	switch f.Type {
	case typeDate, typeTimestamp:
		return fmt.Sprintf("<DateTime value={%s} />", nm) // DateTime 自己处理空值
	case typeEnum:
		lbl := camel(f.Name) + "Labels"
		if f.Required {
			return fmt.Sprintf("%s[%s] ?? %s", lbl, nm, nm)
		}
		return fmt.Sprintf("%s ? (%s[%s] ?? %s) : '—'", nm, lbl, nm, nm)
	case typeBool:
		return fmt.Sprintf("%s ? '是' : '否'", nm)
	default: // string / text / int / decimal
		if f.Required {
			return nm
		}
		return fmt.Sprintf("%s ?? '—'", nm)
	}
}

// formDefaultExpr 是详情页 useForm defaultValues 里一个字段的取值（从加载的 entity 映射到 FormValues）。
// 可空列（OpenAPI 里 T|null）兜零值；enum 要 cast 回联合类型（照 role 的做法）。
func formDefaultExpr(f Field, entityType string) string {
	nm := "entity." + f.Name
	switch f.Type {
	case typeInt:
		return fmt.Sprintf("%s ?? 0", nm)
	case typeBool:
		return fmt.Sprintf("%s ?? false", nm)
	case typeEnum:
		return fmt.Sprintf("(%s ?? '') as %sFormValues['%s']", nm, entityType, f.Name)
	default: // string / text / decimal / date
		return fmt.Sprintf("%s ?? ''", nm)
	}
}

// writeActionsColumn 产出编辑/删除操作列（按 can() 门控）。
func writeActionsColumn(b *strings.Builder, def *ModuleDef, acts map[string]bool) {
	b.WriteString("    {\n      key: 'actions',\n      header: '',\n      className: 'w-14',\n      cell: (row) => (\n        <RowActions\n          actions={[\n")
	if acts[actUpdate] {
		fmt.Fprintf(b, "            ...(can('%s', 'update')\n              ? [\n                  {\n", def.Key)
		b.WriteString("                    key: 'edit',\n                    label: '编辑',\n                    icon: <Pencil />,\n")
		fmt.Fprintf(b, "                    onSelect: () => void navigate(`%s/${row.id}?edit=1`, { state: FROM_LIST_STATE }),\n", def.Menu.Path)
		b.WriteString("                  },\n                ]\n              : []),\n")
	}
	if acts[actDelete] {
		fmt.Fprintf(b, "            ...(can('%s', 'delete')\n              ? [\n                  {\n", def.Key)
		b.WriteString("                    key: 'delete',\n                    label: '删除',\n                    icon: <Trash2 />,\n                    danger: true,\n")
		b.WriteString("                    onSelect: () =>\n                      confirm.open({\n")
		fmt.Fprintf(b, "                        title: '删除%s？',\n", def.Name)
		b.WriteString("                        description: '删除后不可恢复。',\n                        confirmText: '删除',\n                        destructive: true,\n")
		b.WriteString("                        onConfirm: () => remove.mutateAsync({ id: row.id, version: row.version }),\n")
		b.WriteString("                      }),\n                  },\n                ]\n              : []),\n")
	}
	b.WriteString("          ]}\n        />\n      ),\n    },\n")
}

// enumLabelImports 返回要从 types 里 import 的 enum 标签名（<field>Labels）。
func enumLabelImports(def *ModuleDef) []string {
	var out []string
	for _, f := range def.Fields {
		if f.Type == typeEnum {
			out = append(out, camel(f.Name)+"Labels")
		}
	}
	return out
}

// submitValueExpr 是 onSubmit 里一个字段取值表达式。
//
// ⚠️ **可选的 date/timestamp/ref 空了要发 undefined，不能发 ""**：这几类在后端 Body 上带
// format:"date"/"date-time"/"uuid"，huma 会校验空串「不是合法 date/uuid」而拒掉整个请求
// （浏览器实测过）。发 undefined 让它从 JSON 里省略，omitempty + 缺省 = 后端当没传，正好。
// 可选 string/text/decimal 没有 format 校验，空串无害，照旧。
func submitValueExpr(f Field) string {
	switch f.Type {
	case typeString, typeText:
		return fmt.Sprintf("values.%s.trim()", f.Name)
	case typeDate, typeTimestamp, typeRef:
		if !f.Required {
			return fmt.Sprintf("values.%s || undefined", f.Name)
		}
		return "values." + f.Name
	default:
		return "values." + f.Name
	}
}

// writeDetailItemEdit 产出一个字段在表单里的 <DetailItem> + 编辑控件。
func writeDetailItemEdit(b *strings.Builder, def *ModuleDef, f Field) {
	req := ""
	if f.Required {
		req = " required"
	}
	fmt.Fprintf(b, "            <DetailItem label=%q%s error={errors.%s?.message}>\n", f.Label, req, f.Name)
	writeFormControl(b, def, f)
	b.WriteString("            </DetailItem>\n\n")
}

// writeFormControl 按字段类型产出编辑控件。
func writeFormControl(b *strings.Builder, def *ModuleDef, f Field) {
	switch f.Type {
	case typeEnum:
		fmt.Fprintf(b, "              <Controller\n                control={form.control}\n                name=%q\n", f.Name)
		b.WriteString("                render={({ field }) => (\n")
		b.WriteString("                  <SelectField\n                    ref={field.ref}\n                    value={field.value}\n                    onChange={field.onChange}\n")
		fmt.Fprintf(b, "                    options={Object.entries(%sLabels).map(([value, label]) => ({ value, label }))}\n", camel(f.Name))
		b.WriteString("                  />\n                )}\n              />\n")
	case typeBool:
		fmt.Fprintf(b, "              <Controller\n                control={form.control}\n                name=%q\n", f.Name)
		b.WriteString("                render={({ field }) => (\n")
		b.WriteString("                  <label className=\"flex items-center gap-2\">\n")
		b.WriteString("                    <Checkbox checked={field.value} onCheckedChange={(v) => field.onChange(v === true)} />\n")
		fmt.Fprintf(b, "                    <span className=\"text-muted-foreground\">%s</span>\n", f.Label)
		b.WriteString("                  </label>\n                )}\n              />\n")
	case typeInt:
		fmt.Fprintf(b, "              <Input type=\"number\" {...form.register(%q, { valueAsNumber: true })} />\n", f.Name)
	case typeDate:
		fmt.Fprintf(b, "              <Input type=\"date\" {...form.register(%q)} />\n", f.Name)
	case typeRef:
		// 远程搜索选择器：搜目标模块的名字点一下，存的是 uuid（见 gen_ref.go / RefSelect）。
		writeRefControl(b, def, f)
	default: // string / text / decimal
		fmt.Fprintf(b, "              <Input {...form.register(%q)} />\n", f.Name)
	}
}
