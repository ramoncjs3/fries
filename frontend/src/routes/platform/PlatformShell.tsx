import { useMutation, useQueryClient } from '@tanstack/react-query'
import { LogOut } from 'lucide-react'
import { NavLink, Outlet, useNavigate } from 'react-router'

import { platformLogout } from '@/api/auth'
import { Button } from '@/components/ui/button'
import { usePlatformMe } from '@/lib/platform-session'
import { cn } from '@/lib/utils'

/**
 * 平台管理端的外壳：顶栏 + 导航。
 *
 * ⚠️ **不复用租户端的 `<AppShell>`**（MULTI-TENANCY.md §10.1）。
 * 复用意味着里面到处要判「现在是平台还是租户」，而那种判断漏一处
 * 就是把两个世界打通 —— 后端已经分成两套会话表 + Realm 对齐了，前端跟着分。
 *
 * 导航也**不走后端的菜单树**：那棵树只吐 `Realm: tenant` 的模块（§3.2 ②），
 * 平台端的模块本来就不在里面。这一轮平台管理员即全权（§6），
 * 页面就这么两个，写死比引一套菜单机制划算。真要分权时再改。
 */
const NAV = [
  { to: '/platform/tenants', label: '组织' },
  { to: '/platform/settings', label: '平台设置' },
]

export function PlatformShell() {
  const me = usePlatformMe()
  const navigate = useNavigate()
  const queryClient = useQueryClient()

  const logout = useMutation({
    mutationFn: platformLogout,
    onSuccess: () => {
      // 换身份必须清缓存 —— 上一个身份的数据还在缓存里的话，
      // 下一个人进来会先看到它再被后台重取替换（MEMORY.md 记过同一个坑）
      queryClient.clear()
      void navigate('/platform/login', { replace: true })
    },
  })

  return (
    <div className="min-h-svh bg-background">
      <header className="flex h-14 items-center gap-3 border-b border-border-subtle bg-sidebar px-6">
        <span className="grid size-7 place-items-center rounded-lg bg-primary font-bold text-primary-foreground">
          F
        </span>
        <span className="font-semibold tracking-tight">fries 平台管理端</span>

        <nav className="ml-4 flex items-center gap-1">
          {NAV.map((tab) => (
            <NavLink
              key={tab.to}
              to={tab.to}
              className={({ isActive }) =>
                cn(
                  'rounded-md px-3 py-1.5 text-muted-foreground transition-colors hover:text-foreground',
                  isActive && 'bg-accent-subtle text-foreground',
                )
              }
            >
              {tab.label}
            </NavLink>
          ))}
        </nav>

        <div className="flex-1" />
        <span className="text-muted-foreground">{me.user.display_name}</span>
        <Button variant="ghost" size="sm" onClick={() => logout.mutate()}>
          <LogOut /> 退出
        </Button>
      </header>

      <div className="p-6">
        <Outlet />
      </div>
    </div>
  )
}
