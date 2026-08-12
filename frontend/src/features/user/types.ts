import type { components } from '@/api/schema'

/**
 * 类型来自后端 OpenAPI，不手写一遍 —— 真相唯一在 Go 侧（DECISIONS.md §1）。
 * 重新生成：make gen-api
 */
export type User = components['schemas']['User']
export type CreatedUser = components['schemas']['CreatedUser']

/** 查询条件。 */
export interface UserQuery {
  page: number
  pageSize: number
  keyword?: string
  status?: string
  /** 可多选。传 `none` 表示「没有部门的人」。 */
  departmentIds?: string[]
}

/** 「没有部门」在筛选里的取值，和后端约定的一致。 */
export const UNASSIGNED_DEPARTMENT = 'none'

/**
 * 「不属于任何部门」在**下拉框**里的取值。
 *
 * 和上面那个筛选用的是两回事：这个纯粹是前端占位，提交前会换成 undefined。
 * 不能用空串 —— 空串在 Radix Select 里是保留值（表示「没选」），选项用了它会不显示。
 */
export const NO_DEPARTMENT = '__none__'


/** 新增/编辑的提交内容。 */
export interface UserInput {
  username?: string
  display_name: string
  email: string
  phone: string
  status: string
  department_id?: string
  role_ids: string[]
}
