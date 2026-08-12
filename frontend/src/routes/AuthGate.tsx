import { Loader2 } from 'lucide-react'
import { Navigate, Outlet, useLocation } from 'react-router'

import { ApiError } from '@/api/client'
import { useSession } from '@/lib/session'

/**
 * 登录闸门：没登录就去登录页，必须改密就去改密页。
 *
 * 判断依据是后端 /me 的返回 —— 前端不自己存登录态（token 在 httpOnly cookie 里，
 * 前端根本读不到，这正是我们要的）。
 */
export function AuthGate() {
  const { query } = useSession()
  const location = useLocation()

  if (query.isPending) {
    return (
      <div className="grid min-h-svh place-items-center">
        <Loader2 className="size-6 animate-spin text-muted-foreground" />
      </div>
    )
  }

  if (query.isError) {
    const error = query.error instanceof ApiError ? query.error : null

    // 卡在「必须改密」：/me 会返回 403，这不是没登录，是还没解锁
    if (error?.code === 'auth.must_change_password' || error?.code === 'auth.password_expired') {
      return location.pathname === '/change-password' ? <Outlet /> : <Navigate to="/change-password" replace />
    }

    // 组织被停用：**说清楚原因，别静默踢回登录页**。
    //
    // 踢回去的话，人再登一次会看到「用户名或密码错误」——
    // 登录接口必须那么回（三种失败给一样的回应，否则就成了客户名单探测器），
    // 于是整家公司被莫名其妙地挡在门外，还以为是自己密码记错了。
    //
    // 这里说出真实原因不算泄露：能走到这一步的人手上有这个组织的有效会话。
    if (error?.code === 'tenant.suspended') {
      return (
        <div className="grid min-h-svh place-items-center bg-linear-160 from-secondary via-background to-accent-subtle p-6">
          <div className="surface max-w-100 space-y-3 p-7 text-center">
            <h1 className="text-lg">组织已停用</h1>
            <p className="text-muted-foreground">{error.message}</p>
          </div>
        </div>
      )
    }

    return <Navigate to="/login" replace state={{ from: location.pathname }} />
  }

  return <Outlet />
}
