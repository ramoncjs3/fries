import type { components } from '@/api/schema'

/**
 * 类型来自后端 OpenAPI，不手写一遍 —— 真相唯一在 Go 侧（DECISIONS.md §1）。
 * 重新生成：make gen-api
 */
export type Role = components['schemas']['Role']
export type PermissionModule = components['schemas']['PermissionModule']

/** 查询条件。 */
export interface RoleQuery {
  page: number
  pageSize: number
  keyword?: string
  status?: string
}

/** 新增/编辑的提交内容。 */
export interface RoleInput {
  key?: string
  name: string
  description: string
  data_scope: string
  status: string
  permissions: string[]
}

/**
 * 数据范围的中文说明。
 *
 * 放在这里而不是各个页面里 —— 列表的那一列和表单里的选项说的是同一件事，
 * 分头写迟早会变成两种说法。
 */
export const dataScopeLabels: Record<string, string> = {
  // 「本组织全部数据」不是啰嗦：多租户下 all 的含义是**本组织内**的全部，
  // 写成「全部数据」会让人以为跨组织也看得到（MULTI-TENANCY.md §3.2 ④）。
  // 组织是硬边界，任何角色、任何数据范围都跨不过去。
  all: '本组织全部数据',
  self: '仅本人创建',
}
