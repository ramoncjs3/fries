import { useQuery } from '@tanstack/react-query'

import { listAuditLogs } from '@/features/audit/api'
import type { AuditQuery } from '@/features/audit/types'

/** 查询 key 统一在这里拼，失效缓存时才不会写错。 */
export const auditKeys = {
  all: ['audit'] as const,
  list: (params: AuditQuery) => [...auditKeys.all, 'list', params] as const,
}

export function useAuditLogs(params: AuditQuery) {
  return useQuery({
    queryKey: auditKeys.list(params),
    queryFn: () => listAuditLogs(params),
  })
}
