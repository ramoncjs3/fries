import * as SelectPrimitive from '@radix-ui/react-select'
import { Check, ChevronDown } from 'lucide-react'
import type * as React from 'react'

import { cn } from '@/lib/utils'

/**
 * 下拉选择。尺寸和 `<Input>` 对齐（40px / 12px 圆角），
 * 否则筛选条上一排控件高度会差几个像素，看着就是没对齐（DECISIONS.md §7.5）。
 */

export const Select = SelectPrimitive.Root
export const SelectValue = SelectPrimitive.Value

export function SelectTrigger({
  className,
  children,
  ...props
}: React.ComponentProps<typeof SelectPrimitive.Trigger>) {
  return (
    <SelectPrimitive.Trigger
      className={cn(
        'flex h-10 w-full items-center justify-between gap-2 rounded-lg border border-input bg-background px-3',
        'transition-all duration-150',
        'data-[placeholder]:text-muted-foreground/70',
        'outline-none focus-visible:border-accent focus-visible:ring-3 focus-visible:ring-ring',
        'disabled:cursor-not-allowed disabled:opacity-50',
        'aria-invalid:border-destructive aria-invalid:ring-destructive/25',
        className,
      )}
      {...props}
    >
      <span className="min-w-0 truncate text-left">{children}</span>
      <SelectPrimitive.Icon asChild>
        <ChevronDown className="size-4 shrink-0 text-muted-foreground" />
      </SelectPrimitive.Icon>
    </SelectPrimitive.Trigger>
  )
}

export function SelectContent({
  className,
  children,
  position = 'popper',
  ...props
}: React.ComponentProps<typeof SelectPrimitive.Content>) {
  return (
    <SelectPrimitive.Portal>
      <SelectPrimitive.Content
        position={position}
        sideOffset={4}
        className={cn(
          // 底色不透明（§7.5）；宽度跟随触发器，选项文字才不会被截断
          'z-50 max-h-72 min-w-[var(--radix-select-trigger-width)] overflow-hidden rounded-lg border border-border bg-popover p-1 shadow-overlay',
          className,
        )}
        {...props}
      >
        <SelectPrimitive.Viewport className="max-h-72 overflow-y-auto">{children}</SelectPrimitive.Viewport>
      </SelectPrimitive.Content>
    </SelectPrimitive.Portal>
  )
}

export function SelectItem({
  className,
  children,
  ...props
}: React.ComponentProps<typeof SelectPrimitive.Item>) {
  return (
    <SelectPrimitive.Item
      className={cn(
        'relative flex h-9 cursor-default select-none items-center gap-2 rounded-md pl-2.5 pr-8 outline-none',
        'focus:bg-secondary data-[disabled]:pointer-events-none data-[disabled]:opacity-40',
        className,
      )}
      {...props}
    >
      <SelectPrimitive.ItemText>{children}</SelectPrimitive.ItemText>
      <SelectPrimitive.ItemIndicator className="absolute right-2.5">
        <Check className="size-3.5 text-accent-text" />
      </SelectPrimitive.ItemIndicator>
    </SelectPrimitive.Item>
  )
}

export interface SelectOption {
  value: string
  label: string
  /** 右侧的次要说明，比如部门编号。 */
  hint?: string
}

/**
 * 带选项的下拉。**页面一律用它，不要自己拼 Select + SelectItem**。
 *
 * ⚠️ 关键是 `<SelectValue>` 传了显式的 children：
 * Radix 自己解析「当前值对应哪个选项的文字」只在挂载时算一次，
 * 之后用 `form.reset()` 把值换掉，触发器上**还是空的** ——
 * 表现就是「明明有值，下拉框却什么都不显示」。这个坑踩过一次。
 */
export function SelectField({
  options,
  value,
  onChange,
  placeholder,
  className,
  ref,
}: {
  options: SelectOption[]
  value: string
  onChange: (value: string) => void
  placeholder?: string
  className?: string
  /**
   * 转发到触发按钮上。**`<Controller>` 里一定要把 `field.ref` 传进来** ——
   * react-hook-form 的 `shouldFocusError`（默认开着）靠它把焦点送到第一个出错的字段。
   * 不传的话，出错的是下拉框时页面纹丝不动，人只看到「点了保存没反应」。
   */
  ref?: React.Ref<HTMLButtonElement>
}) {
  const current = options.find((o) => o.value === value)

  return (
    <Select value={value} onValueChange={onChange}>
      <SelectTrigger ref={ref} className={className}>
        <SelectValue placeholder={placeholder}>{current?.label}</SelectValue>
      </SelectTrigger>
      <SelectContent>
        {options.map((option) => (
          <SelectItem key={option.value} value={option.value}>
            {option.label}
            {option.hint ? <span className="ml-2 text-muted-foreground">{option.hint}</span> : null}
          </SelectItem>
        ))}
      </SelectContent>
    </Select>
  )
}
