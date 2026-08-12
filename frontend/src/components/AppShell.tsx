import { useMutation, useQueryClient } from '@tanstack/react-query'
import { Check, ChevronRight, KeyRound, LogOut, Monitor, Moon, Search, Sun } from 'lucide-react'
import { useTheme } from 'next-themes'
import { NavLink, Outlet, useLocation, useNavigate } from 'react-router'
import { toast } from 'sonner'

import { logout } from '@/api/auth'
import { CommandPalette } from '@/components/CommandPalette'
import { MenuIcon } from '@/components/MenuIcon'
import {
  DropdownMenu,
  DropdownMenuContent,
  DropdownMenuItem,
  DropdownMenuLabel,
  DropdownMenuSeparator,
  DropdownMenuTrigger,
} from '@/components/ui/dropdown-menu'
import { useCommandPalette } from '@/lib/command-palette'
import { useSession } from '@/lib/session'
import { cn } from '@/lib/utils'

/**
 * 应用外壳：左侧栏 + 顶部条 + 内容区。
 *
 *   - **整屏不滚动**（h-svh + overflow-hidden），只有内容区内部滚。
 *     用 min-h-svh 的话整页会跟着内容长，侧边栏底部要划下去才看得见。
 *   - **侧边栏 224px，只放菜单**。品牌块高度和顶部条对齐（都是 56px），
 *     两条下边框接成一条通栏线 —— 差 1px 都会看出来。
 *   - **顶部条放导航之外的东西**：面包屑（你在哪）、全局搜索、账号。
 *     这些不该混进菜单里，混进去侧栏就变成了杂物抽屉。
 *   - 侧栏和顶栏是白的，内容区是灰白的，层级只靠这点色差，不靠投影。
 *
 * 菜单来自后端 /me，前端只渲染 —— 前后端不可能不一致（DECISIONS.md §3.6 第 ① 层）。
 */

const themeOptions = [
  { key: 'light', label: '浅色', icon: Sun },
  { key: 'dark', label: '深色', icon: Moon },
  { key: 'system', label: '跟随系统', icon: Monitor },
]

/** 菜单分组名。第 ④ 步菜单变多之后，这里要改成由后端菜单自带分组。 */
const menuGroup = '系统管理'

export function AppShell() {
  const { me, reset } = useSession()
  const navigate = useNavigate()
  const location = useLocation()
  const queryClient = useQueryClient()
  const { theme, setTheme } = useTheme()
  const palette = useCommandPalette()

  const logoutMutation = useMutation({
    mutationFn: logout,
    onSuccess: () => {
      queryClient.clear()
      reset()
      void navigate('/login', { replace: true })
    },
    onError: () => toast.error('退出失败，请重试'),
  })

  // 按前缀找，不是精确相等：详情页的路径是 `/users/:id`，而菜单里只有 `/users`。
  // 用相等的话，从列表点进详情，面包屑和侧栏高亮会一起掉回「首页」。
  const current = me?.menus.find(
    (item) => location.pathname === item.path || location.pathname.startsWith(`${item.path}/`),
  )
  // 在详情页时，面包屑那一节变成回列表的链接（列表本身就是「上一级」）
  const inSubPage = Boolean(current && location.pathname !== current.path)

  return (
    <div className="flex h-svh overflow-hidden bg-background">
      <aside className="flex w-56 shrink-0 flex-col border-r border-border-subtle bg-sidebar">
        {/* 品牌块。高度必须和顶部条一致，否则那条通栏分隔线会错位。
            第二行是「当前是哪家公司」—— 多租户下不显示它，同一个人在两家公司
            都有账号时会彻底分不清自己在哪边（MULTI-TENANCY.md §7.4）。 */}
        <div className="flex h-14 shrink-0 items-center gap-2.5 border-b border-border-subtle px-4">
          <span className="grid size-7 shrink-0 place-items-center rounded-lg bg-primary font-bold text-primary-foreground">
            F
          </span>
          <span className="flex min-w-0 flex-1 flex-col leading-tight">
            <span className="truncate font-semibold tracking-tight">fries</span>
            {/* 用 ?. 而不是 . ：类型上 tenant 一定在，但这份 me 来自 React Query 的缓存，
                后端刚改过字段时缓存里可能还是旧形状 —— 为了一行说明文字白屏不值得 */}
            <span className="truncate text-xs text-muted-foreground" title={me?.tenant?.name}>
              {me?.tenant?.name ?? ''}
            </span>
          </span>
        </div>

        <nav className="min-h-0 flex-1 space-y-0.5 overflow-y-auto px-3 py-3">
          <p className="nav-group">{menuGroup}</p>
          {me?.menus.length ? (
            me.menus.map((item) => (
              <NavLink
                key={item.key}
                to={item.path}
                className={({ isActive }) =>
                  cn(
                    'flex h-10 items-center gap-2.5 rounded-xl px-3 transition-colors',
                    isActive
                      ? 'bg-accent-subtle font-medium text-accent-text'
                      : 'text-muted-foreground hover:bg-secondary hover:text-foreground',
                  )
                }
              >
                {({ isActive }) => (
                  <>
                    <MenuIcon name={item.icon} className={cn('size-4.5 shrink-0', !isActive && 'opacity-70')} />
                    {item.name}
                  </>
                )}
              </NavLink>
            ))
          ) : (
            <p className="px-3 py-1 text-muted-foreground">还没有可访问的菜单</p>
          )}
        </nav>
      </aside>

      <div className="flex min-w-0 flex-1 flex-col overflow-hidden">
        <header className="flex h-14 shrink-0 items-center gap-3 border-b border-border-subtle bg-sidebar px-6">
          {/* 面包屑只回答「我在哪」，不做跳转 —— 分组本身没有页面 */}
          <nav aria-label="位置" className="flex min-w-0 items-center gap-1 text-muted-foreground">
            <span className="truncate">{menuGroup}</span>
            <ChevronRight className="size-3.5 shrink-0 opacity-60" />
            {inSubPage && current ? (
              <NavLink to={current.path} className="truncate hover:text-foreground">
                {current.name}
              </NavLink>
            ) : (
              <span className="truncate font-medium text-foreground">{current?.name ?? '首页'}</span>
            )}
          </nav>

          <div className="flex-1" />

          {/* 命令面板入口。⌘K 也能开，这里只是让人知道有这么个东西。 */}
          <button
            type="button"
            onClick={() => palette.setOpen(true)}
            className="flex h-9 w-64 items-center gap-2 rounded-lg border border-input bg-background px-3 text-muted-foreground transition-colors hover:border-border-strong hover:text-foreground"
          >
            <Search className="size-4 shrink-0" />
            <span className="flex-1 text-left">跳转页面…</span>
            <kbd className="rounded border border-border px-1.5 font-mono text-xs">⌘K</kbd>
          </button>

          <DropdownMenu>
            <DropdownMenuTrigger asChild>
              <button
                type="button"
                className="flex items-center gap-2 rounded-lg py-1 pl-1 pr-2 transition-colors hover:bg-secondary"
              >
                <span className="grid size-7 shrink-0 place-items-center rounded-full bg-accent-subtle text-sm font-medium text-accent-text">
                  {me?.user.display_name?.slice(0, 1) ?? '?'}
                </span>
                <span className="max-w-32 truncate">{me?.user.display_name}</span>
              </button>
            </DropdownMenuTrigger>

            <DropdownMenuContent align="end" className="w-52">
              <DropdownMenuLabel>
                {me?.user.display_name}
                <span className="ml-1 opacity-60">· {me?.user.username}</span>
              </DropdownMenuLabel>
              <DropdownMenuSeparator className="my-1 h-px bg-border" />
              <DropdownMenuItem onSelect={() => void navigate('/change-password')}>
                <KeyRound /> 修改密码
              </DropdownMenuItem>
              <DropdownMenuSeparator className="my-1 h-px bg-border" />
              <DropdownMenuLabel>主题</DropdownMenuLabel>
              {themeOptions.map((item) => (
                <DropdownMenuItem key={item.key} onSelect={() => setTheme(item.key)}>
                  <item.icon />
                  {item.label}
                  {theme === item.key ? <Check className="ml-auto size-3.5" /> : null}
                </DropdownMenuItem>
              ))}
              <DropdownMenuSeparator className="my-1 h-px bg-border" />
              <DropdownMenuItem
                onSelect={() => logoutMutation.mutate()}
                className="text-destructive focus:text-destructive"
              >
                <LogOut /> 退出登录
              </DropdownMenuItem>
            </DropdownMenuContent>
          </DropdownMenu>
        </header>

        {/* 内容区：留白由页面自己给（px-8 py-8） */}
        <main className="flex min-h-0 flex-1 flex-col overflow-hidden">
          <Outlet />
        </main>
      </div>

      <CommandPalette open={palette.open} onOpenChange={palette.setOpen} />
    </div>
  )
}
