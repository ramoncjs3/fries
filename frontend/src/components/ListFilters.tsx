import { ChevronDown, ListFilter, Search, X } from 'lucide-react'
import { useEffect, useRef, useState } from 'react'

import { Button } from '@/components/ui/button'
import { Checkbox } from '@/components/ui/checkbox'
import { Input } from '@/components/ui/input'
import { Popover, PopoverContent, PopoverTrigger } from '@/components/ui/popover'
import { SelectField } from '@/components/ui/select'
import type { ListParams } from '@/lib/list-params'
import { cn } from '@/lib/utils'

/**
 * 列表页的筛选条：搜索框 + 「筛选」按钮 + 已生效条件的 chip。
 *
 * **筛选项是数据不是 JSX**（`FilterSpec[]`）—— 模块只描述「筛什么」，
 * 不关心「长什么样」。改排版是改这一个文件的事，不用挨个模块动。
 *
 * 顺序是**固定的**：筛选 → 清空 → 搜索框 → 条件 chip。
 * **两个按钮永远在最左边**，位置不随筛选多少而变；后面那些（搜索框、chip）
 * 数量会长，放前面的话按钮会被一路挤到右边找不着。
 */

export interface FilterOption {
  value: string
  label: string
}

export interface FilterSpec {
  /** URL 上的参数名，也是传给后端的字段名。 */
  key: string
  /** 给人看的名字。**必须有** —— 只靠 placeholder 的话一输入内容提示就没了。 */
  label: string
  placeholder?: string
  /**
   * 取值是**一组固定枚举**时必须传 options，渲染成下拉。
   *
   * ⚠️ 枚举字段绝不能留成文本框：后端认的是 `active` / `disabled`，
   * 用户看到的是「启用」「停用」，让人手敲只会敲出 400。
   */
  options?: FilterOption[]
  /** 多选。选中的值在 URL 上用逗号连起来。 */
  multiple?: boolean
}

/** 控件宽度。208px 太窄，放不下稍长一点的值。 */
const FIELD_WIDTH = 'w-72'

/** 下拉里「不筛这一项」的取值。空串在 Radix Select 里是保留值。 */
const ANY_VALUE = '__any__'

interface ListFiltersProps {
  specs: FilterSpec[]
  params: ListParams
  /** 不传就不显示搜索框。 */
  searchPlaceholder?: string
}

export function ListFilters({ specs, params, searchPlaceholder }: ListFiltersProps) {
  const active = specs.filter((spec) => params.filter(spec.key))
  const hasAny = active.length > 0 || Boolean(params.search)

  return (
    <div className="mb-4 flex shrink-0 flex-wrap items-center gap-2">
      <FilterPopover specs={specs} params={params} count={active.length} />

      <Button variant="ghost" disabled={!hasAny} onClick={() => params.reset(specs.map((s) => s.key))}>
        清空
      </Button>

      {searchPlaceholder ? <SearchBox params={params} placeholder={searchPlaceholder} /> : null}

      {active.map((spec) => (
        <span
          key={spec.key}
          className="flex h-10 items-center gap-1.5 rounded-lg border border-border bg-secondary px-3"
        >
          <span className="text-muted-foreground">{spec.label}：</span>
          <span className="font-medium">{displayValue(spec, params.filter(spec.key))}</span>
          <button
            type="button"
            aria-label={`清除${spec.label}`}
            className="ml-0.5 rounded text-muted-foreground transition-colors hover:text-foreground"
            onClick={() => params.setFilter(spec.key, '')}
          >
            <X className="size-3.5" />
          </button>
        </span>
      ))}
    </div>
  )
}

/** chip 上显示中文，不显示 `active` 这种后端枚举值；多选就用顿号连起来。 */
function displayValue(spec: FilterSpec, raw: string) {
  if (!spec.options) return raw
  const label = (v: string) => spec.options?.find((o) => o.value === v)?.label ?? v
  return spec.multiple ? raw.split(',').filter(Boolean).map(label).join('、') : label(raw)
}

/**
 * 多选下拉。**不用 Radix Select** —— 它是单选语义，选一个就自动关，
 * 多选时每勾一个都要重新点开，用起来很难受。这里用 Popover + 复选框。
 */
function MultiSelect({
  options,
  value,
  onChange,
  placeholder,
}: {
  options: FilterOption[]
  value: string[]
  onChange: (values: string[]) => void
  placeholder: string
}) {
  const picked = new Set(value)
  const label =
    value.length === 0
      ? placeholder
      : value.map((v) => options.find((o) => o.value === v)?.label ?? v).join('、')

  return (
    <Popover>
      <PopoverTrigger asChild>
        <button
          type="button"
          className={cn(
            'flex h-10 w-full items-center justify-between gap-2 rounded-lg border border-input bg-background px-3 text-left',
            'outline-none focus-visible:border-accent focus-visible:ring-3 focus-visible:ring-ring',
            value.length === 0 && 'text-muted-foreground/70',
          )}
        >
          <span className="min-w-0 truncate">{label}</span>
          <ChevronDown className="size-4 shrink-0 text-muted-foreground" />
        </button>
      </PopoverTrigger>
      <PopoverContent className="max-h-72 w-72 overflow-y-auto p-1">
        {options.map((option) => (
          <label
            key={option.value}
            className="flex cursor-pointer items-center gap-2.5 rounded-md px-2.5 py-2 hover:bg-secondary"
          >
            <Checkbox
              checked={picked.has(option.value)}
              onCheckedChange={() => {
                const next = new Set(picked)
                if (!next.delete(option.value)) next.add(option.value)
                onChange([...next])
              }}
            />
            <span className="min-w-0 truncate">{option.label}</span>
          </label>
        ))}
      </PopoverContent>
    </Popover>
  )
}

/**
 * 受控的筛选输入框。
 *
 * **不能用 defaultValue**：非受控的输入框只在挂载时读一次，外面把条件清空了
 * （点「清空」、浏览器后退、换个分享链接进来）框里的字还在，看着像没清掉。
 *
 * 但也不能直接绑 URL 上的值 —— URL 是 300ms 防抖之后才更新的，直接绑会打一个字
 * 卡一下。所以本地留一份即时值，只在**外部值不是自己刚写进去的那个**时才回灌。
 */
function useFilterInput(external: string, commit: (value: string) => void) {
  const [local, setLocal] = useState(external)
  const sent = useRef(external)

  useEffect(() => {
    if (external === sent.current) return
    sent.current = external
    setLocal(external)
  }, [external])

  return {
    value: local,
    onChange(event: React.ChangeEvent<HTMLInputElement>) {
      const next = event.target.value
      setLocal(next)
      sent.current = next.trim()
      commit(next.trim())
    },
  }
}

function FilterInput({
  placeholder,
  value,
  onCommit,
  className,
}: {
  placeholder?: string
  value: string
  onCommit: (value: string) => void
  className?: string
}) {
  const field = useFilterInput(value, onCommit)
  return <Input className={className} placeholder={placeholder} {...field} />
}

/** 搜索框：左侧放大镜 + 输入。 */
function SearchBox({
  params,
  placeholder,
}: {
  params: ListParams
  placeholder: string
}) {
  return (
    <div className={cn('relative', FIELD_WIDTH)}>
      <Search className="pointer-events-none absolute left-3 top-1/2 size-4 -translate-y-1/2 text-muted-foreground" />
      <FilterInput
        className="pl-9"
        placeholder={placeholder}
        value={params.search}
        onCommit={(value) => params.setSearch(value)}
      />
    </div>
  )
}

/** 「筛选」按钮 + 弹出面板，面板里每个字段一行。 */
function FilterPopover({
  specs,
  params,
  count,
}: {
  specs: FilterSpec[]
  params: ListParams
  count: number
}) {
  return (
    <Popover>
      <PopoverTrigger asChild>
        <Button variant="outline" className="h-10">
          <ListFilter />
          筛选
          {count > 0 ? (
            <span className="ml-0.5 rounded bg-accent-subtle px-1.5 font-medium text-accent-text">{count}</span>
          ) : null}
        </Button>
      </PopoverTrigger>
      <PopoverContent className="w-80 space-y-3">
        {specs.map((spec) => (
          <label key={spec.key} className="flex flex-col gap-1.5">
            <span className="text-xs font-medium text-muted-foreground">{spec.label}</span>
            {spec.options && spec.multiple ? (
              <MultiSelect
                options={spec.options}
                value={params.filter(spec.key).split(',').filter(Boolean)}
                onChange={(values) => params.setFilter(spec.key, values.join(','))}
                placeholder={spec.placeholder ?? '全部'}
              />
            ) : spec.options ? (
              <SelectField
                value={params.filter(spec.key) || ANY_VALUE}
                onChange={(v) => params.setFilter(spec.key, v === ANY_VALUE ? '' : v)}
                options={[{ value: ANY_VALUE, label: '全部' }, ...spec.options]}
              />
            ) : (
              <FilterInput
                placeholder={spec.placeholder}
                value={params.filter(spec.key)}
                onCommit={(value) => params.setTextFilter(spec.key, value)}
              />
            )}
          </label>
        ))}
      </PopoverContent>
    </Popover>
  )
}
