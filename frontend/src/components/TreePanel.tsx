import { ChevronRight, Search } from 'lucide-react'
import { useState, type ReactNode } from 'react'

import { Input } from '@/components/ui/input'
import { cn } from '@/lib/utils'

/**
 * 左侧树面板。**层级靠缩进 + 竖直连接线**表达，不靠一堆空格。
 *
 * 只画竖线不画横向拐角：拐角在 Finder / VSCode 那种深层级下有用，
 * 但内部系统的树撑死四五层，横线只会让本来就窄的一列更花。
 *
 * 组件不关心数据从哪来 —— 传进来的是已经拼好的树，谁来拼是页面的事。
 */

export interface TreeNodeData {
  id: string
  label: ReactNode
  /** 右侧的次要信息，比如成员数。 */
  meta?: ReactNode
  children: TreeNodeData[]
}

export function TreePanel({
  nodes,
  selectedID,
  onSelect,
  searchPlaceholder,
  onSearch,
  footer,
  empty,
}: {
  nodes: TreeNodeData[]
  selectedID: string
  onSelect: (id: string) => void
  searchPlaceholder?: string
  onSearch?: (keyword: string) => void
  /** 钉在树底部的一块，比如「未分配」入口。 */
  footer?: ReactNode
  empty?: ReactNode
}) {
  // 默认全展开：内部系统的树层级浅、节点少，收起来反而要人一层层点开才看得到东西
  const [collapsed, setCollapsed] = useState<Set<string>>(new Set())

  function toggle(id: string) {
    setCollapsed((current) => {
      const next = new Set(current)
      if (!next.delete(id)) next.add(id)
      return next
    })
  }

  return (
    <div className="surface flex w-72 shrink-0 flex-col overflow-hidden">
      {searchPlaceholder ? (
        <div className="shrink-0 border-b border-border-subtle p-3">
          <div className="relative">
            <Search className="pointer-events-none absolute left-3 top-1/2 size-4 -translate-y-1/2 text-muted-foreground" />
            <Input
              className="pl-9"
              placeholder={searchPlaceholder}
              onChange={(e) => onSearch?.(e.target.value.trim())}
            />
          </div>
        </div>
      ) : null}

      <div className="min-h-0 flex-1 overflow-y-auto p-2">
        {nodes.length === 0 ? (
          <p className="px-2 py-6 text-center text-muted-foreground">{empty ?? '没有数据'}</p>
        ) : (
          nodes.map((node) => (
            <TreeNode
              key={node.id}
              node={node}
              depth={0}
              collapsed={collapsed}
              onToggle={toggle}
              selectedID={selectedID}
              onSelect={onSelect}
            />
          ))
        )}
      </div>

      {footer ? <div className="shrink-0 border-t border-border-subtle p-2">{footer}</div> : null}
    </div>
  )
}

function TreeNode({
  node,
  depth,
  collapsed,
  onToggle,
  selectedID,
  onSelect,
}: {
  node: TreeNodeData
  depth: number
  collapsed: Set<string>
  onToggle: (id: string) => void
  selectedID: string
  onSelect: (id: string) => void
}) {
  const open = !collapsed.has(node.id)
  const hasChildren = node.children.length > 0

  // **点行只选中，不折叠**。
  //
  // 试过「点行顺带展开/收起」，但那样选中一个父节点就会把它的子节点收起来，
  // 想看「这个部门下面有哪些组」反而要再点一次展开 —— 越用越别扭。
  // 折叠交给前面那个箭头，它的热区已经放大到 20px。

  return (
    <>
      <div
        role="treeitem"
        aria-selected={node.id === selectedID}
        aria-expanded={hasChildren ? open : undefined}
        tabIndex={0}
        onClick={() => onSelect(node.id)}
        onKeyDown={(e) => {
          if (e.key === 'Enter' || e.key === ' ') {
            e.preventDefault()
            onSelect(node.id)
          }
        }}
        className={cn(
          'flex h-9 cursor-pointer items-center gap-1 rounded-lg pr-2 transition-colors',
          node.id === selectedID
            ? 'bg-accent-subtle font-medium text-accent-text'
            : 'hover:bg-secondary',
        )}
      >
        {/* 每一层一根竖线。线画在缩进格子里，节点之间才连得起来 */}
        {Array.from({ length: depth }, (_, i) => (
          <span key={i} className="h-full w-4 shrink-0 border-l border-border" aria-hidden />
        ))}

        {hasChildren ? (
          <button
            type="button"
            aria-label={open ? '收起' : '展开'}
            onClick={(e) => {
              e.stopPropagation()
              onToggle(node.id)
            }}
            className="grid size-5 shrink-0 place-items-center rounded text-muted-foreground hover:bg-border hover:text-foreground"
          >
            <ChevronRight className={cn('size-3.5 transition-transform', open && 'rotate-90')} />
          </button>
        ) : (
          <span className="w-5 shrink-0" aria-hidden />
        )}

        <span className="min-w-0 flex-1 truncate">{node.label}</span>
        {node.meta ? <span className="shrink-0 text-muted-foreground">{node.meta}</span> : null}
      </div>

      {hasChildren && open
        ? node.children.map((child) => (
            <TreeNode
              key={child.id}
              node={child}
              depth={depth + 1}
              collapsed={collapsed}
              onToggle={onToggle}
              selectedID={selectedID}
              onSelect={onSelect}
            />
          ))
        : null}
    </>
  )
}
