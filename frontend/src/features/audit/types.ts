import type { components } from '@/api/schema'

/**
 * 一条审计记录。类型来自后端 OpenAPI，不手写一遍 —— 真相唯一在 Go 侧
 * （DECISIONS.md §1）。后端改了字段这里编译期就报错。重新生成：make gen-api
 */
export type AuditEntry = components['schemas']['AuditEntry']

/** 查询条件。这是前端自己的东西（分页参数怎么组织），不来自后端。 */
export interface AuditQuery {
  page: number
  pageSize: number
  resource?: string
  action?: string
  actorId?: string
  from?: string
  to?: string
}
