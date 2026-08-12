package main

import (
	"strings"
	"testing"
)

func TestGenTypesTS(t *testing.T) {
	def := validGenerated()
	src := genTypesTS(&def)
	for _, want := range []string{
		"Safe to edit",
		"import type { components } from '@/api/schema'",
		"export type Supplier = components['schemas']['Supplier']",
		"export interface SupplierQuery {",
		"page: number",
		"keyword?: string",   // 有可搜字段
		"status?: string",    // filterable
		"startedAt?: string", // Query filter 用 camelCase（api.ts 里映射成 snake 查询参数）
		"export interface SupplierInput {",
		"name: string",        // required 无 ?
		"credit?: string",     // 可选 decimal → string + ?
		"started_at?: string", // ⚠️ Input 用 snake_case：直接进请求体，须等于后端 json tag
		"export const statusLabels: Record<string, string> = {",
		"active: '合作中',",
		"terminated: '已终止',",
	} {
		if !strings.Contains(src, want) {
			t.Errorf("types.ts 应包含：%s", want)
		}
	}
}

func TestGenSchemaTS(t *testing.T) {
	def := validGenerated()
	src := genSchemaTS(&def)
	for _, want := range []string{
		"import { z } from 'zod'",
		"export const supplierSchema = z.object({",
		"name: z.string().trim().min(1, '请填写供应商名称').max(100, '供应商名称最多 100 个字符'),",
		"status: z.enum(['active', 'terminated']),",
		"credit: z.string(),",     // 可选 decimal 只判字符串
		"started_at: z.string(),", // ⚠️ zod key snake_case，和 Input 一致
		"remark: z.string().max(2000, '备注最多 2000 个字'),",
		"export type SupplierFormValues = z.infer<typeof supplierSchema>",
		"export const emptySupplier: SupplierFormValues = {",
		"status: 'active',", // enum 默认值
		"credit: '',",
		"started_at: '',", // ⚠️ empty key snake_case
	} {
		if !strings.Contains(src, want) {
			t.Errorf("schema.ts 应包含：%s", want)
		}
	}
}

func TestGenApiTS(t *testing.T) {
	def := validGenerated()
	src := genApiTS(&def)
	for _, want := range []string{
		"import { del, get, getPage, post, put, type PageResult } from '@/api/client'",
		"import type { Supplier, SupplierInput, SupplierQuery } from '@/features/supplier/types'",
		"export function listSuppliers(params: SupplierQuery): Promise<PageResult<Supplier>> {",
		"getPage<Supplier>('/suppliers'",
		"keyword: params.keyword,",
		"status: params.status,",
		"started_at: params.startedAt,", // query 用后端字段名，值取 camel
		"export function getSupplier(id: string): Promise<Supplier> {",
		"export function createSupplier(input: SupplierInput): Promise<Supplier> {",
		"export function updateSupplier(id: string, version: number, input: SupplierInput): Promise<Supplier> {",
		"export function deleteSupplier(id: string, version: number): Promise<void> {",
	} {
		if !strings.Contains(src, want) {
			t.Errorf("api.ts 应包含：%s", want)
		}
	}
}

func TestGenQueriesTS(t *testing.T) {
	def := validGenerated()
	src := genQueriesTS(&def)
	for _, want := range []string{
		"import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'",
		"import { toast } from 'sonner'",
		"createSupplier, deleteSupplier, getSupplier, listSuppliers, updateSupplier", // 排序后
		"import type { SupplierInput, SupplierQuery } from '@/features/supplier/types'",
		"export const supplierKeys = {",
		"all: ['supplier'] as const,",
		"export function useSuppliers(params: SupplierQuery) {",
		"export function useSupplier(id: string | undefined) {",
		"export function useSupplierMutations() {",
		"toast.success('供应商已创建')",
		"return { create, update, remove }",
	} {
		if !strings.Contains(src, want) {
			t.Errorf("queries.ts 应包含：%s", want)
		}
	}
}

// TestGenFrontendReadOnly 只读模块（list+read）不产 create/update/delete 的 api/mutation，
// 也不 import toast/useMutation（否则 eslint no-unused 报错）。
func TestGenFrontendReadOnly(t *testing.T) {
	def := ModuleDef{
		Key: "ledger", Name: "台账", Generated: true, Scoped: true,
		Menu:    Menu{Path: "/ledgers", Icon: "book"},
		Fields:  []Field{{Name: "title", Type: typeString, Label: "标题", Required: true, Max: 100}},
		Actions: []string{actList, actRead},
	}
	api := genApiTS(&def)
	if strings.Contains(api, "createLedger") || strings.Contains(api, "deleteLedger") {
		t.Error("只读模块不该产 create/delete api")
	}
	// api.ts 的 client import 要按动作裁：只读模块用不到 del/post/put，带上会被 tsc noUnusedLocals 拦。
	if !strings.Contains(api, "import { get, getPage, type PageResult } from '@/api/client'") {
		t.Errorf("只读模块 api.ts 应只 import get/getPage/PageResult，实际：\n%s", api)
	}
	if strings.Contains(api, "SupplierInput") || strings.Contains(api, "LedgerInput") {
		t.Error("只读模块 api.ts 不该 import <Entity>Input（没有写操作）")
	}
	q := genQueriesTS(&def)
	if strings.Contains(q, "useMutation") || strings.Contains(q, "toast") {
		t.Error("只读模块不该 import useMutation/toast（未用会被 eslint 拦）")
	}
	if !strings.Contains(q, "import { useQuery } from '@tanstack/react-query'") {
		t.Error("只读模块只该 import useQuery")
	}
}
