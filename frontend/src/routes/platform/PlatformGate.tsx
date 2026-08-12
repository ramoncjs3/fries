import { useQuery } from '@tanstack/react-query'
import { Loader2 } from 'lucide-react'
import { Navigate, Outlet, useLocation } from 'react-router'

import { fetchPlatformMe } from '@/api/auth'
import { ApiError } from '@/api/client'
import { PlatformContext, platformMeQueryKey } from '@/lib/platform-session'

/**
 * 平台管理端的登录闸门。
 *
 * 和租户端的 <AuthGate> 是**两套**，刻意不合并（MULTI-TENANCY.md §10.1）：
 * 两边的会话、cookie、/me 接口全是分开的。合并意味着每处都要判「现在是哪一套」，
 * 而那种判断漏一处就是把两个世界打通 —— 后端那一层已经这么分了，前端跟着分。
 *
 * 顺带：**两套 cookie 名不同**，所以一个人可以在同一个浏览器里同时登着
 * 平台端和某个租户，互不影响。
 */

export function PlatformGate() {
  const location = useLocation()
  const query = useQuery({
    queryKey: platformMeQueryKey,
    queryFn: fetchPlatformMe,
    // 未登录时返回 401，这属于正常状态，不用重试
    retry: false,
    staleTime: 30_000,
  })

  if (query.isPending) {
    return (
      <div className="grid min-h-svh place-items-center">
        <Loader2 className="size-6 animate-spin text-muted-foreground" />
      </div>
    )
  }

  if (query.isError || !query.data) {
    const error = query.error instanceof ApiError ? query.error : null
    // 首次登录必须改密：/platform/me 会返回 403，这不是没登录，是还没解锁
    if (error?.code === 'auth.must_change_password' || error?.code === 'auth.password_expired') {
      return location.pathname === '/platform/change-password' ? (
        <Outlet />
      ) : (
        <Navigate to="/platform/change-password" replace />
      )
    }
    return <Navigate to="/platform/login" replace />
  }

  return (
    <PlatformContext value={query.data}>
      <Outlet />
    </PlatformContext>
  )
}
