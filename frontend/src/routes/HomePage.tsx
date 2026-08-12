import { Navigate } from 'react-router'

import { useSession } from '@/lib/session'

/**
 * 首页：跳到这个人第一个能看的菜单。
 *
 * 不做仪表盘 —— 每个项目要看的指标都不一样，脚手架不替业务决定。
 */
export default function HomePage() {
  const { me } = useSession()
  const first = me?.menus[0]

  if (first) return <Navigate to={first.path} replace />

  return (
    <div className="flex flex-1 flex-col items-center justify-center gap-2 text-center">
      <p className="font-medium">你还没有任何可访问的页面</p>
      <p className="text-sm text-muted-foreground">找管理员给你分配角色</p>
    </div>
  )
}
