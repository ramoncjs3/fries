import type { components } from '@/api/schema'

/**
 * 类型来自后端 OpenAPI，不手写一遍 —— 真相唯一在 Go 侧（DECISIONS.md §1）。
 * 重新生成：make gen-api
 */
export type ServiceAccount = components['schemas']['ServiceAccount']
export type CreatedKey = components['schemas']['CreatedKey']

/** 查询条件。 */
export interface ServiceAccountQuery {
  page: number
  pageSize: number
  keyword?: string
  status?: string
}

/** 新增/编辑的提交内容。 */
export interface ServiceAccountInput {
  name: string
  description: string
  role_id: string
  status: string
  /** 空表示不过期 */
  expires_at?: string
}
