import { MoreHorizontal } from 'lucide-react'
import type { ReactNode } from 'react'

import {
  DropdownMenu,
  DropdownMenuContent,
  DropdownMenuItem,
  DropdownMenuSeparator,
  DropdownMenuTrigger,
} from '@/components/ui/dropdown-menu'
import { cn } from '@/lib/utils'

/**
 * 表格行尾的操作菜单。
 *
 * 每行摊开三四个按钮的话，表格右侧就成了一片按钮墙，行一多完全看不清 ——
 * 收进一个 `⋯` 里，主操作（新增）留在页面右上角。
 *
 * `stopPropagation` 是必须的：行本身可能带点击（展开、开抽屉），
 * 不拦住的话点菜单会顺带触发整行的行为。
 */
export interface RowAction {
  key: string
  label: string
  icon?: ReactNode
  onSelect: () => void
  /** 危险操作（删除之类）显示成红色，并在菜单里单独分一组。 */
  danger?: boolean
  disabled?: boolean
  /** 禁用的原因，鼠标悬停时显示 —— 灰掉却不说为什么最气人。 */
  disabledReason?: string
}

export function RowActions({ actions }: { actions: RowAction[] }) {
  // 一条都没有就别渲染那个 `⋯`：权限不够的人点开是个空菜单，
  // 看着像坏了。调用方按权限拼数组，拼空是正常情况，不该在这儿露出来。
  if (actions.length === 0) return null

  const normal = actions.filter((a) => !a.danger)
  const danger = actions.filter((a) => a.danger)

  return (
    <DropdownMenu>
      <DropdownMenuTrigger asChild>
        <button
          type="button"
          aria-label="更多操作"
          onClick={(e) => e.stopPropagation()}
          className="grid size-8 place-items-center rounded-md text-muted-foreground transition-colors hover:bg-secondary hover:text-foreground"
        >
          <MoreHorizontal className="size-4" />
        </button>
      </DropdownMenuTrigger>
      <DropdownMenuContent align="end" onClick={(e) => e.stopPropagation()}>
        {normal.map((action) => (
          <Item key={action.key} action={action} />
        ))}
        {normal.length > 0 && danger.length > 0 ? (
          <DropdownMenuSeparator className="my-1 h-px bg-border" />
        ) : null}
        {danger.map((action) => (
          <Item key={action.key} action={action} />
        ))}
      </DropdownMenuContent>
    </DropdownMenu>
  )
}

function Item({ action }: { action: RowAction }) {
  return (
    <DropdownMenuItem
      disabled={action.disabled}
      title={action.disabled ? action.disabledReason : undefined}
      onSelect={action.onSelect}
      className={cn(action.danger && 'text-destructive focus:text-destructive')}
    >
      {action.icon}
      {action.label}
    </DropdownMenuItem>
  )
}
