import { cva, type VariantProps } from 'class-variance-authority'
import type * as React from 'react'

import { cn } from '@/lib/utils'

/**
 * 状态用「小圆点 + 文字」，不用填色胶囊。
 *
 * 密集表格里满屏胶囊会很吵，而且胶囊是上一代后台最典型的长相。
 * 圆点只占 6px，颜色照样把状态说清楚。
 */
const dotVariants = cva('status-dot', {
  variants: {
    tone: {
      neutral: 'bg-muted-foreground/60',
      accent: 'bg-accent',
      success: 'bg-success',
      warning: 'bg-warning',
      danger: 'bg-destructive',
    },
  },
  defaultVariants: { tone: 'neutral' },
})

export function StatusDot({
  tone,
  children,
  className,
  ...props
}: React.ComponentProps<'span'> & VariantProps<typeof dotVariants>) {
  return (
    <span className={cn('inline-flex items-center gap-1.5 whitespace-nowrap', className)} {...props}>
      <span className={dotVariants({ tone })} />
      {children}
    </span>
  )
}

const tagVariants = cva(
  'inline-flex items-center rounded-sm px-1.5 py-0.5 text-xs font-medium whitespace-nowrap',
  {
    variants: {
      tone: {
        neutral: 'bg-secondary text-secondary-foreground',
        accent: 'bg-accent-subtle text-accent-text',
        outline: 'border border-border text-muted-foreground',
      },
    },
    defaultVariants: { tone: 'neutral' },
  },
)

/** 需要一个「块」而不是状态时用它 —— 比如角色名、类型。比胶囊克制。 */
export function Tag({
  className,
  tone,
  ...props
}: React.ComponentProps<'span'> & VariantProps<typeof tagVariants>) {
  return <span className={cn(tagVariants({ tone }), className)} {...props} />
}
