import { useQuery, useQueryClient } from '@tanstack/react-query'
import type { ReactNode } from 'react'

import { Outlet } from 'react-router'

import { fetchMe } from '@/api/auth'
import { meQueryKey, SessionContext, type SessionValue } from '@/lib/session'

export function SessionProvider({ children }: { children: ReactNode }) {
  const queryClient = useQueryClient()
  const query = useQuery({
    queryKey: meQueryKey,
    queryFn: fetchMe,
    // 未登录时 /me 返回 401，这属于正常状态，不用重试
    retry: false,
    staleTime: 30_000,
  })

  const permissions = new Set(query.data?.permissions ?? [])

  const value: SessionValue = {
    me: query.data,
    query,
    can: (resource, action) => permissions.has(`${resource}:${action}`),
    reset: () => queryClient.clear(),
  }

  return <SessionContext value={value}>{children}</SessionContext>
}

/**
 * 路由布局版本：把租户会话**限定在租户那棵子树里**。
 *
 * ⚠️ 它原来挂在整个应用的最外层，于是**平台管理端的页面上也会去查租户的 /me**。
 * 平台管理员根本没有租户会话，那一查必然 401，而 401 会触发全局跳转 ——
 * 人刚打开平台登录页就被顶到租户登录页去了（浏览器实测踩到的，类型检查看不出来）。
 *
 * 顺带还省掉一件事：每打开一个平台页面就往审计里写一条匿名的 auth:me，
 * 那些记录会落在平台级那条哈希链上，纯属噪音。
 */
export function TenantSessionLayout() {
  return (
    <SessionProvider>
      <Outlet />
    </SessionProvider>
  )
}
