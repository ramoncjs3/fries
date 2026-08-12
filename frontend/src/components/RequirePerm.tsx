import { ShieldOff } from 'lucide-react'
import type { ReactNode } from 'react'

import { useSession } from '@/lib/session'

/**
 * 路由守卫：防直输 URL 绕过菜单（DECISIONS.md §3.6 第 ② 层）。
 *
 * **这不是安全边界** —— 真正的拦截线在后端的 Casbin 中间件。
 * 这一层只是别让用户点进一个必然 403 的页面。
 */
export function RequirePerm({
  resource,
  action,
  children,
}: {
  resource: string
  action: string
  children: ReactNode
}) {
  const { can } = useSession()

  if (!can(resource, action)) {
    return (
      <div className="flex flex-1 flex-col items-center justify-center gap-3 text-center">
        <ShieldOff className="size-8 text-muted-foreground/60" />
        <div className="space-y-1">
          <p className="font-medium">没有访问权限</p>
          <p className="text-sm text-muted-foreground">
            需要 <code>{resource}:{action}</code> 权限，找管理员开通
          </p>
        </div>
      </div>
    )
  }
  return children
}
