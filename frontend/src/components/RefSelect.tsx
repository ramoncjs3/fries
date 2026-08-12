import { useQuery } from '@tanstack/react-query'
import { Check, ChevronDown, Loader2, Search } from 'lucide-react'
import { useEffect, useState, type Ref } from 'react'

import { Popover, PopoverContent, PopoverTrigger } from '@/components/ui/popover'
import { cn } from '@/lib/utils'

/**
 * 外键字段的远程搜索选择器。**生成器给 ref 字段产出的控件**（DECISIONS.md §10.2）。
 *
 * ref 存的是目标记录的 uuid，但人认的是名字。这个组件把「输 uuid」换成「搜名字点一下」：
 * 打开就去目标模块的列表接口搜（`search`），选中把 uuid 回给表单；编辑态用 `resolveLabel`
 * 把已存的 uuid 反查成名字显示（目标模块没有 read 动作时不传，退化成显示 uuid）。
 *
 * 不自己搭 Select + 一堆 SelectItem：远程搜索要输入框 + 异步列表 + 空/加载态，Radix Select
 * 那套是「选项已知且不多」的场景，套不进来。用 Popover + 输入框 + 列表自己拼。
 */

export interface RefOption {
  value: string
  label: string
}

export function RefSelect({
  entity,
  value,
  onChange,
  search,
  resolveLabel,
  placeholder,
  inputRef,
  disabled,
}: {
  /** 目标模块 key，只用来隔离 react-query 缓存（两个 ref 字段指不同模块不能串缓存）。 */
  entity: string
  /** 当前选中的 uuid，'' 表示没选。 */
  value: string
  onChange: (id: string) => void
  /** 远程搜索：给关键字，返回候选项。 */
  search: (keyword: string) => Promise<RefOption[]>
  /** 把一个已存的 uuid 反查成显示名（编辑态回填用）。目标模块没有 read 动作时不传。 */
  resolveLabel?: (id: string) => Promise<string>
  placeholder?: string
  /** 转发到触发按钮 —— `<Controller>` 里要把 `field.ref` 传进来，出错时焦点才送得过来。 */
  inputRef?: Ref<HTMLButtonElement>
  disabled?: boolean
}) {
  const [open, setOpen] = useState(false)
  const [keyword, setKeyword] = useState('')
  // 轻量防抖：输入停 250ms 才真去搜，别每敲一下打一次接口。
  const [debounced, setDebounced] = useState('')
  useEffect(() => {
    const t = setTimeout(() => {
      setDebounced(keyword)
    }, 250)
    return () => {
      clearTimeout(t)
    }
  }, [keyword])

  const results = useQuery({
    queryKey: ['ref-select', entity, debounced],
    queryFn: () => search(debounced),
    enabled: open,
    // 输入时保留上一批结果，别闪成空白
    placeholderData: (prev) => prev,
  })

  // 编辑态：已存的 uuid 反查成名字。搜到的候选里有就用候选的（免一次请求）。
  const inResults = results.data?.find((o) => o.value === value)?.label
  const resolved = useQuery({
    queryKey: ['ref-select-label', entity, value],
    queryFn: () => resolveLabel!(value),
    enabled: !!value && !!resolveLabel && !inResults,
  })

  const displayText = value ? (inResults ?? resolved.data ?? value) : ''

  const pick = (id: string) => {
    onChange(id)
    setOpen(false)
  }

  return (
    <Popover open={open} onOpenChange={setOpen}>
      <PopoverTrigger asChild>
        <button
          ref={inputRef}
          type="button"
          disabled={disabled}
          className={cn(
            'flex h-10 w-full items-center justify-between gap-2 rounded-lg border border-input bg-background px-3',
            'transition-all duration-150',
            'outline-none focus-visible:border-accent focus-visible:ring-3 focus-visible:ring-ring',
            'disabled:cursor-not-allowed disabled:opacity-50',
          )}
        >
          <span className={cn('min-w-0 truncate text-left', !value && 'text-muted-foreground/70')}>
            {displayText || placeholder || '搜索选择…'}
          </span>
          <ChevronDown className="size-4 shrink-0 text-muted-foreground" />
        </button>
      </PopoverTrigger>
      <PopoverContent
        align="start"
        className="w-[var(--radix-popover-trigger-width)] p-0"
      >
        <div className="flex items-center gap-2 border-b border-border-subtle px-3">
          <Search className="size-4 shrink-0 text-muted-foreground" />
          <input
            autoFocus
            value={keyword}
            onChange={(e) => {
              setKeyword(e.target.value)
            }}
            placeholder="搜索…"
            className="h-9 w-full bg-transparent text-sm outline-none placeholder:text-muted-foreground/70"
          />
        </div>
        <div className="max-h-64 overflow-y-auto p-1">
          {results.isPending ? (
            <div className="flex items-center gap-2 px-2.5 py-2 text-muted-foreground">
              <Loader2 className="size-4 animate-spin" /> 搜索中…
            </div>
          ) : results.data && results.data.length > 0 ? (
            results.data.map((o) => (
              <button
                key={o.value}
                type="button"
                onClick={() => {
                  pick(o.value)
                }}
                className="flex w-full items-center justify-between gap-2 rounded-md px-2.5 py-1.5 text-left outline-none hover:bg-secondary focus:bg-secondary"
              >
                <span className="min-w-0 truncate">{o.label}</span>
                {o.value === value ? <Check className="size-3.5 shrink-0 text-accent-text" /> : null}
              </button>
            ))
          ) : (
            <div className="px-2.5 py-2 text-muted-foreground">没有匹配的记录</div>
          )}
        </div>
        {value ? (
          <div className="border-t border-border-subtle p-1">
            <button
              type="button"
              onClick={() => {
                pick('')
              }}
              className="w-full rounded-md px-2.5 py-1.5 text-left text-muted-foreground outline-none hover:bg-secondary focus:bg-secondary"
            >
              清除选择
            </button>
          </div>
        ) : null}
      </PopoverContent>
    </Popover>
  )
}
