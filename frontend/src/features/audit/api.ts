import { getPage, type PageResult } from '@/api/client'
import type { AuditEntry, AuditQuery } from '@/features/audit/types'

/** 查审计日志。审计只读 —— 表在 DB 层就禁了 UPDATE / DELETE。 */
export function listAuditLogs(params: AuditQuery): Promise<PageResult<AuditEntry>> {
  return getPage<AuditEntry>('/audit-logs', {
    query: {
      page: params.page,
      page_size: params.pageSize,
      resource: params.resource,
      action: params.action,
      actor_id: params.actorId,
      from: params.from,
      to: params.to,
    },
  })
}
