package main

import (
	"fmt"
	"sort"
	"strconv"
	"strings"
)

// gen_frontend.go 产出前端「数据层」四件套（种子文件）：
//
//	types.ts    后端类型从 OpenAPI import（真相唯一在 Go 侧）+ Query/Input 接口 + enum 标签
//	schema.ts   zod 表单校验（只放前端能判死的：非空/长度/枚举）+ 空值默认
//	api.ts      list/get/create/update/delete 五个请求函数（都走 @/api/client）
//	queries.ts  react-query hooks（keys + useList/useDetail/useMutations）
//
// 页面（ListPage/DetailPage/NewPage）另出（JSX 复杂，受设计系统 lint 约束多）。
// 前端没有 prettier，只有 tsc（类型）+ eslint（no-unused 等），所以产出重点是类型对 +
// 不留未用 import。真正类型校验在 4e（tsc --noEmit）。

// camel 把 snake_case 转成 lowerCamelCase（service_account → serviceAccount）。
func camel(s string) string {
	p := pascal(s)
	if p == "" {
		return p
	}
	return strings.ToLower(p[:1]) + p[1:]
}

// tsType 是字段在 TS 里的类型。
func tsType(f Field) string {
	switch f.Type {
	case typeInt:
		return "number"
	case typeBool:
		return "boolean"
	default: // string/text/enum/decimal/date/timestamp 都走 string
		return "string"
	}
}

// tsEnumFields 返回所有 enum 字段（要产标签映射）。
func tsEnumFields(def *ModuleDef) []Field {
	var out []Field
	for _, f := range def.Fields {
		if f.Type == typeEnum {
			out = append(out, f)
		}
	}
	return out
}

// genTypesTS 产出 features/<key>/types.ts。
func genTypesTS(def *ModuleDef) string {
	entity := pascal(def.Key)
	var b strings.Builder
	b.WriteString(tsSeedHeader())
	b.WriteString("import type { components } from '@/api/schema'\n\n")
	b.WriteString("/**\n * 类型来自后端 OpenAPI，不手写一遍 —— 真相唯一在 Go 侧（DECISIONS.md §1）。\n")
	b.WriteString(" * 重新生成：make gen-api\n */\n")
	fmt.Fprintf(&b, "export type %s = components['schemas']['%s']\n\n", entity, entity)

	// Query：分页 + keyword + filter。
	fmt.Fprintf(&b, "/** 查询条件。 */\nexport interface %sQuery {\n", entity)
	b.WriteString("  page: number\n  pageSize: number\n")
	if hasSearch(def) {
		b.WriteString("  keyword?: string\n")
	}
	for _, f := range filterFields(def) {
		fmt.Fprintf(&b, "  %s?: %s\n", camel(f.Name), tsType(f))
	}
	b.WriteString("}\n\n")

	// Input：新增/编辑提交内容。**字段名用 snake_case（f.Name）**——它直接 JSON.stringify 进请求体
	// （见 api.ts 的 create/update 把 input 原样发），必须等于后端 <Entity>Body 的 json tag（也是 snake）。
	// 用 camelCase 的话，带下划线的字段（started_at→startedAt）后端认不出，可选字段静默丢、必填字段 422。
	fmt.Fprintf(&b, "/** 新增/编辑的提交内容。字段名 snake_case，直接进请求体，须等于后端 json tag。 */\n")
	fmt.Fprintf(&b, "export interface %sInput {\n", entity)
	for _, f := range def.Fields {
		opt := ""
		if !f.Required {
			opt = "?"
		}
		fmt.Fprintf(&b, "  %s%s: %s\n", f.Name, opt, tsType(f))
	}
	b.WriteString("}\n")

	// enum 标签：列表列和表单选项共用一份，避免两处说法不一。
	for _, f := range tsEnumFields(def) {
		fmt.Fprintf(&b, "\n/** %s的选项标签。 */\nexport const %sLabels: Record<string, string> = {\n", f.Label, camel(f.Name))
		for _, code := range sortedEnumCodes(f) {
			fmt.Fprintf(&b, "  %s: '%s',\n", tsKey(code), f.Values[code])
		}
		b.WriteString("}\n")
	}
	return b.String()
}

// genSchemaTS 产出 features/<key>/schema.ts。
func genSchemaTS(def *ModuleDef) string {
	entity := pascal(def.Key)
	varName := camel(def.Key)
	var b strings.Builder
	b.WriteString(tsSeedHeader())
	b.WriteString("import { z } from 'zod'\n\n")
	b.WriteString("/**\n * 表单校验规则。只放前端能判死的（非空、长度、枚举）——\n")
	b.WriteString(" * 「是不是被占了」这类要查后端，不在这里复制一份（DECISIONS.md §5）。\n */\n")
	// zod key 用 snake_case：FormValues 由这个 schema 推导，表单提交时 1:1 铺进 Input（也是 snake），
	// 全链路一致才不用在页面里手写一层 camel↔snake 映射。
	fmt.Fprintf(&b, "export const %sSchema = z.object({\n", varName)
	for _, f := range def.Fields {
		fmt.Fprintf(&b, "  %s: %s,\n", f.Name, zodExpr(f))
	}
	b.WriteString("})\n\n")
	fmt.Fprintf(&b, "export type %sFormValues = z.infer<typeof %sSchema>\n\n", entity, varName)
	fmt.Fprintf(&b, "export const empty%s: %sFormValues = {\n", entity, entity)
	for _, f := range def.Fields {
		fmt.Fprintf(&b, "  %s: %s,\n", f.Name, tsDefault(f))
	}
	b.WriteString("}\n")
	return b.String()
}

// genApiTS 产出 features/<key>/api.ts。
func genApiTS(def *ModuleDef) string {
	entity := pascal(def.Key)
	entities := pascal(pluralize(def.Key))
	table := pluralize(def.Key)
	acts := actionSet(def)
	var b strings.Builder
	b.WriteString(tsSeedHeader())
	// client import 按动作裁剪：多带一个用不到的（del/post/put…），tsc 的 noUnusedLocals 就报 TS6133。
	// 顺序和 client.ts 一致（字母序 + type 收尾）。getPage/PageResult 恒有（list 恒生成）。
	clientImports := []string{"getPage"}
	if acts[actDelete] {
		clientImports = append(clientImports, "del")
	}
	if acts[actRead] {
		clientImports = append(clientImports, "get")
	}
	if acts[actCreate] {
		clientImports = append(clientImports, "post")
	}
	if acts[actUpdate] {
		clientImports = append(clientImports, "put")
	}
	sort.Strings(clientImports)
	clientImports = append(clientImports, "type PageResult")
	fmt.Fprintf(&b, "import { %s } from '@/api/client'\n", strings.Join(clientImports, ", "))
	// 类型 import 也按动作裁：没有写操作就不需要 <Entity>Input。
	typeImports := []string{entity, entity + "Query"}
	if acts[actCreate] || acts[actUpdate] {
		typeImports = []string{entity, entity + "Input", entity + "Query"}
	}
	fmt.Fprintf(&b, "import type { %s } from '@/features/%s/types'\n\n", strings.Join(typeImports, ", "), def.Key)

	// list
	fmt.Fprintf(&b, "export function list%s(params: %sQuery): Promise<PageResult<%s>> {\n", entities, entity, entity)
	fmt.Fprintf(&b, "  return getPage<%s>('/%s', {\n    query: {\n      page: params.page,\n      page_size: params.pageSize,\n", entity, table)
	if hasSearch(def) {
		b.WriteString("      keyword: params.keyword,\n")
	}
	for _, f := range filterFields(def) {
		fmt.Fprintf(&b, "      %s: params.%s,\n", f.Name, camel(f.Name))
	}
	b.WriteString("    },\n  })\n}\n\n")

	// get
	if acts[actRead] {
		fmt.Fprintf(&b, "export function get%s(id: string): Promise<%s> {\n  return get<%s>(`/%s/${id}`)\n}\n\n", entity, entity, entity, table)
	}
	// create
	if acts[actCreate] {
		fmt.Fprintf(&b, "export function create%s(input: %sInput): Promise<%s> {\n  return post<%s>('/%s', input)\n}\n\n", entity, entity, entity, entity, table)
	}
	// update
	if acts[actUpdate] {
		fmt.Fprintf(&b, "export function update%s(id: string, version: number, input: %sInput): Promise<%s> {\n  return put<%s>(`/%s/${id}`, { ...input, version })\n}\n\n", entity, entity, entity, entity, table)
	}
	// delete
	if acts[actDelete] {
		fmt.Fprintf(&b, "export function delete%s(id: string, version: number): Promise<void> {\n  return del<void>(`/%s/${id}`, { version })\n}\n", entity, table)
	}
	return strings.TrimRight(b.String(), "\n") + "\n"
}

// genQueriesTS 产出 features/<key>/queries.ts。
func genQueriesTS(def *ModuleDef) string {
	entity := pascal(def.Key)
	entities := pascal(pluralize(def.Key))
	varName := camel(def.Key)
	acts := actionSet(def)
	hasMut := acts[actCreate] || acts[actUpdate] || acts[actDelete]

	var b strings.Builder
	b.WriteString(tsSeedHeader())
	// react-query imports 按需（避免未用）。
	rq := []string{"useQuery"}
	if hasMut {
		rq = append([]string{"useMutation"}, rq...)
		rq = append(rq, "useQueryClient")
	}
	fmt.Fprintf(&b, "import { %s } from '@tanstack/react-query'\n", strings.Join(rq, ", "))
	if hasMut {
		b.WriteString("import { toast } from 'sonner'\n")
	}
	b.WriteString("\n")

	// api imports 按 action 收集。
	apiFns := []string{fmt.Sprintf("list%s", entities)}
	if acts[actRead] {
		apiFns = append(apiFns, fmt.Sprintf("get%s", entity))
	}
	if acts[actCreate] {
		apiFns = append(apiFns, fmt.Sprintf("create%s", entity))
	}
	if acts[actUpdate] {
		apiFns = append(apiFns, fmt.Sprintf("update%s", entity))
	}
	if acts[actDelete] {
		apiFns = append(apiFns, fmt.Sprintf("delete%s", entity))
	}
	sort.Strings(apiFns)
	fmt.Fprintf(&b, "import { %s } from '@/features/%s/api'\n", strings.Join(apiFns, ", "), def.Key)
	typeImports := []string{fmt.Sprintf("%sQuery", entity)}
	if hasMut {
		typeImports = append([]string{fmt.Sprintf("%sInput", entity)}, typeImports...)
	}
	fmt.Fprintf(&b, "import type { %s } from '@/features/%s/types'\n\n", strings.Join(typeImports, ", "), def.Key)

	// keys
	fmt.Fprintf(&b, "export const %sKeys = {\n", varName)
	fmt.Fprintf(&b, "  all: ['%s'] as const,\n", def.Key)
	fmt.Fprintf(&b, "  list: (params: %sQuery) => [...%sKeys.all, 'list', params] as const,\n", entity, varName)
	fmt.Fprintf(&b, "  detail: (id: string) => [...%sKeys.all, 'detail', id] as const,\n}\n\n", varName)

	// useList
	fmt.Fprintf(&b, "export function use%s(params: %sQuery) {\n", entities, entity)
	fmt.Fprintf(&b, "  return useQuery({ queryKey: %sKeys.list(params), queryFn: () => list%s(params) })\n}\n", varName, entities)

	// useDetail
	if acts[actRead] {
		fmt.Fprintf(&b, "\nexport function use%s(id: string | undefined) {\n  return useQuery({\n", entity)
		fmt.Fprintf(&b, "    queryKey: %sKeys.detail(id ?? ''),\n    queryFn: () => get%s(id as string),\n    enabled: Boolean(id),\n  })\n}\n", varName, entity)
	}

	// useMutations
	if hasMut {
		writeUseMutations(&b, def, entity, varName, acts)
	}
	return b.String()
}

// writeUseMutations 产出 use<Entity>Mutations（按 action 含 create/update/remove）。
func writeUseMutations(b *strings.Builder, def *ModuleDef, entity, varName string, acts map[string]bool) {
	fmt.Fprintf(b, "\nexport function use%sMutations() {\n", entity)
	b.WriteString("  const queryClient = useQueryClient()\n\n")
	fmt.Fprintf(b, "  function invalidate() {\n    return queryClient.invalidateQueries({ queryKey: %sKeys.all })\n  }\n", varName)

	var returned []string
	if acts[actCreate] {
		fmt.Fprintf(b, "\n  const create = useMutation({\n    mutationFn: (input: %sInput) => create%s(input),\n", entity, entity)
		fmt.Fprintf(b, "    onSuccess: async () => {\n      toast.success('%s已创建')\n      await invalidate()\n    },\n  })\n", def.Name)
		returned = append(returned, "create")
	}
	if acts[actUpdate] {
		fmt.Fprintf(b, "\n  const update = useMutation({\n    mutationFn: (args: { id: string; version: number; input: %sInput }) =>\n      update%s(args.id, args.version, args.input),\n", entity, entity)
		fmt.Fprintf(b, "    onSuccess: async () => {\n      toast.success('%s已保存')\n      await invalidate()\n    },\n  })\n", def.Name)
		returned = append(returned, "update")
	}
	if acts[actDelete] {
		fmt.Fprintf(b, "\n  const remove = useMutation({\n    mutationFn: (args: { id: string; version: number }) => delete%s(args.id, args.version),\n", entity)
		fmt.Fprintf(b, "    onSuccess: async () => {\n      toast.success('%s已删除')\n      await invalidate()\n    },\n    onError: (error) => toast.error(error.message),\n  })\n", def.Name)
		returned = append(returned, "remove")
	}
	fmt.Fprintf(b, "\n  return { %s }\n}\n", strings.Join(returned, ", "))
}

// zodExpr 产出一个字段的 zod 校验表达式。
func zodExpr(f Field) string {
	switch f.Type {
	case typeEnum:
		return fmt.Sprintf("z.enum([%s])", strings.Join(quoteAll(sortedEnumCodes(f)), ", "))
	case typeBool:
		return "z.boolean()"
	case typeRef:
		if f.Required {
			return fmt.Sprintf("z.string().uuid('请选择%s')", f.Label)
		}
		return "z.string()"
	case typeInt:
		return "z.number().int()"
	case typeText:
		max := f.Max
		if max <= 0 {
			max = 2000
		}
		return fmt.Sprintf("z.string().max(%d, '%s最多 %d 个字')", max, f.Label, max)
	case typeString:
		expr := "z.string().trim()"
		if f.Required {
			expr += fmt.Sprintf(".min(1, '请填写%s')", f.Label)
		}
		if f.Max > 0 {
			expr += fmt.Sprintf(".max(%d, '%s最多 %d 个字符')", f.Max, f.Label, f.Max)
		}
		return expr
	default: // decimal / date / timestamp：字符串传，前端只判非空（若必填）
		if f.Required {
			return fmt.Sprintf("z.string().min(1, '请填写%s')", f.Label)
		}
		return "z.string()"
	}
}

// tsDefault 产出 empty<Entity> 里字段的默认值。
func tsDefault(f Field) string {
	switch f.Type {
	case typeBool:
		return strconv.FormatBool(f.Default == boolTrue)
	case typeInt:
		if f.Default != "" {
			return f.Default
		}
		return "0"
	case typeEnum:
		if f.Default != "" {
			return "'" + f.Default + "'"
		}
		codes := sortedEnumCodes(f)
		if len(codes) > 0 {
			return "'" + codes[0] + "'"
		}
		return "''"
	default: // string/text/decimal/date/timestamp
		if f.Default != "" {
			return "'" + f.Default + "'"
		}
		return "''"
	}
}

// sortedEnumCodes 返回 enum 的 code（排序，产出稳定）。
func sortedEnumCodes(f Field) []string {
	codes := make([]string, 0, len(f.Values))
	for code := range f.Values {
		codes = append(codes, code)
	}
	sort.Strings(codes)
	return codes
}

func quoteAll(ss []string) []string {
	out := make([]string, len(ss))
	for i, s := range ss {
		out[i] = "'" + s + "'"
	}
	return out
}

// tsKey 是对象字面量里的 key：合法标识符原样，否则加引号。enum code 都是标识符，这里从简。
func tsKey(s string) string { return s }

// tsSeedHeader 是前端种子文件的头（对应 seedHeader，注释语法用 //）。
func tsSeedHeader() string {
	return "// Generated by fries-gen as a starting point.\n" +
		"// Safe to edit — regeneration will not overwrite.\n\n"
}
