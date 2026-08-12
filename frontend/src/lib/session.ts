import type { UseQueryResult } from '@tanstack/react-query'
import { createContext, use } from 'react'

import type { MeResult } from '@/api/auth'

/**
 * 当前登录者、他的权限点和菜单。
 *
 * 全局共享状态只有两个：这个 + 明暗主题（DECISIONS.md §7.1）。
 * 权限判断一律走 `useSession().can(...)`，**不要在页面里自己拼权限字符串比对**。
 */

export const meQueryKey = ['me'] as const

export interface SessionValue {
  me: MeResult | undefined
  query: UseQueryResult<MeResult>
  /** 判断有没有某个权限点，如 can('audit', 'list')。 */
  can: (resource: string, action: string) => boolean
  /** 退出后清掉缓存，避免下一个人看到上一个人的数据。 */
  reset: () => void
}

export const SessionContext = createContext<SessionValue | null>(null)

export function useSession(): SessionValue {
  const value = use(SessionContext)
  if (!value) throw new Error('useSession 必须在 <SessionProvider> 里用')
  return value
}
