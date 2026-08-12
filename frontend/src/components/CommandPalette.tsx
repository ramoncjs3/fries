import { useMutation, useQueryClient } from '@tanstack/react-query'
import { KeyRound, LogOut, Monitor, Moon, Search, Sun } from 'lucide-react'
import { useTheme } from 'next-themes'
import { useEffect, useMemo, useRef, useState, type ReactNode } from 'react'
import { useNavigate } from 'react-router'
import { toast } from 'sonner'

import { logout } from '@/api/auth'
import { MenuIcon } from '@/components/MenuIcon'
import { Dialog, DialogContent, DialogTitle } from '@/components/ui/dialog'
import { useSession } from '@/lib/session'
import { cn } from '@/lib/utils'

/**
 * ⌘K 命令面板：输关键词，回车跳过去。
 *
 * 候选来自**后端下发的菜单**（`/me`），所以看不见的页面也搜不出来 ——
 * 权限第 ① 层在这里同样成立（DECISIONS.md §3.6）。
 *
 * 现在只覆盖「跳页面」和几个账号动作。数据级搜索（搜用户、搜日志）
 * 是第 ⑥ 步的事 —— 那需要后端有一个统一的搜索接口，现在没有。
 */

interface Command {
  id: string
  group: string
  label: string
  icon: ReactNode
  run: () => void
}

export function CommandPalette({ open, onOpenChange }: { open: boolean; onOpenChange: (open: boolean) => void }) {
  const { me, reset } = useSession()
  const navigate = useNavigate()
  const queryClient = useQueryClient()
  const { setTheme } = useTheme()

  const [keyword, setKeyword] = useState('')
  const [cursor, setCursor] = useState(0)
  const listRef = useRef<HTMLDivElement>(null)

  const logoutMutation = useMutation({
    mutationFn: logout,
    onSuccess: () => {
      queryClient.clear()
      reset()
      void navigate('/login', { replace: true })
    },
    onError: () => toast.error('退出失败，请重试'),
  })

  const commands = useMemo<Command[]>(() => {
    const pages: Command[] = (me?.menus ?? []).map((item) => ({
      id: `page:${item.key}`,
      group: '页面',
      label: item.name,
      icon: <MenuIcon name={item.icon} className="size-4" />,
      run: () => void navigate(item.path),
    }))

    const actions: Command[] = [
      {
        id: 'action:change-password',
        group: '动作',
        label: '修改密码',
        icon: <KeyRound className="size-4" />,
        run: () => void navigate('/change-password'),
      },
      {
        id: 'theme:light',
        group: '动作',
        label: '切换到浅色',
        icon: <Sun className="size-4" />,
        run: () => setTheme('light'),
      },
      {
        id: 'theme:dark',
        group: '动作',
        label: '切换到深色',
        icon: <Moon className="size-4" />,
        run: () => setTheme('dark'),
      },
      {
        id: 'theme:system',
        group: '动作',
        label: '主题跟随系统',
        icon: <Monitor className="size-4" />,
        run: () => setTheme('system'),
      },
      {
        id: 'action:logout',
        group: '动作',
        label: '退出登录',
        icon: <LogOut className="size-4" />,
        run: () => logoutMutation.mutate(),
      },
    ]

    return [...pages, ...actions]
  }, [me, navigate, setTheme, logoutMutation])

  const matched = useMemo(() => {
    const q = keyword.trim().toLowerCase()
    if (!q) return commands
    return commands.filter((c) => c.label.toLowerCase().includes(q))
  }, [commands, keyword])

  // 每次打开都从干净状态开始，否则会带着上次的关键词和选中项
  useEffect(() => {
    if (open) {
      setKeyword('')
      setCursor(0)
    }
  }, [open])

  // 关键词变了，候选也变了，光标必须回到第一条，不然会指向不存在的项
  useEffect(() => setCursor(0), [keyword])

  function runAt(index: number) {
    const command = matched[index]
    if (!command) return
    onOpenChange(false)
    command.run()
  }

  function onKeyDown(event: React.KeyboardEvent) {
    if (event.key === 'ArrowDown' || event.key === 'ArrowUp') {
      event.preventDefault()
      if (matched.length === 0) return
      const delta = event.key === 'ArrowDown' ? 1 : -1
      setCursor((c) => (c + delta + matched.length) % matched.length)
      return
    }
    if (event.key === 'Enter') {
      event.preventDefault()
      runAt(cursor)
    }
  }

  // 键盘移动光标时把选中项滚进视野，否则长列表里选中项会跑到看不见的地方
  useEffect(() => {
    listRef.current?.querySelector('[data-active="true"]')?.scrollIntoView({ block: 'nearest' })
  }, [cursor])

  let lastGroup = ''

  return (
    <Dialog open={open} onOpenChange={onOpenChange}>
      <DialogContent className="max-w-lg gap-0 p-0" showClose={false}>
        <DialogTitle className="sr-only">命令面板</DialogTitle>

        <div className="flex h-12 shrink-0 items-center gap-2 border-b border-border px-4">
          <Search className="size-4 shrink-0 text-muted-foreground" />
          <input
            autoFocus
            value={keyword}
            onChange={(e) => setKeyword(e.target.value)}
            onKeyDown={onKeyDown}
            placeholder="跳转页面、执行动作…"
            className="min-w-0 flex-1 bg-transparent outline-none placeholder:text-muted-foreground/70"
          />
          <kbd className="rounded border border-border px-1.5 font-mono text-xs text-muted-foreground">
            ESC
          </kbd>
        </div>

        <div ref={listRef} className="max-h-80 overflow-y-auto p-2">
          {matched.length === 0 ? (
            <p className="px-2 py-6 text-center text-muted-foreground">没有匹配的结果</p>
          ) : (
            matched.map((command, index) => {
              const newGroup = command.group !== lastGroup
              lastGroup = command.group
              return (
                <div key={command.id}>
                  {newGroup ? <p className="nav-group">{command.group}</p> : null}
                  <button
                    type="button"
                    data-active={index === cursor}
                    onMouseEnter={() => setCursor(index)}
                    onClick={() => runAt(index)}
                    className={cn(
                      'flex h-10 w-full items-center gap-2.5 rounded-lg px-2.5 text-left transition-colors',
                      index === cursor ? 'bg-accent-subtle text-accent-text' : 'text-foreground',
                    )}
                  >
                    {command.icon}
                    {command.label}
                  </button>
                </div>
              )
            })
          )}
        </div>
      </DialogContent>
    </Dialog>
  )
}
