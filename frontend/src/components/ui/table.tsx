import type * as React from 'react'

import { cn } from '@/lib/utils'

/**
 * 表格是这类系统的主体，所以细节都在这儿：
 *   - 表头吸顶，滚动时列名不丢
 *   - 只有横向分隔线，**没有竖线** —— 竖线是老式表格最明显的特征
 *   - 行 hover 用极低透明度的强调色，不是灰
 */

export function Table({ className, ...props }: React.ComponentProps<'table'>) {
  return (
    <div className="w-full overflow-x-auto">
      <table className={cn('w-full caption-bottom border-separate border-spacing-0', className)} {...props} />
    </div>
  )
}

export function TableHeader({ className, ...props }: React.ComponentProps<'thead'>) {
  // 吸顶表头。**底色必须不透明**，否则滚动时数据会从表头下面透出来
  return <thead className={cn('sticky top-0 z-10 bg-card', className)} {...props} />
}

export function TableBody({ className, ...props }: React.ComponentProps<'tbody'>) {
  return <tbody className={className} {...props} />
}

export function TableRow({ className, ...props }: React.ComponentProps<'tr'>) {
  return <tr className={cn('group transition-colors hover:bg-muted/50', className)} {...props} />
}

export function TableHead({ className, ...props }: React.ComponentProps<'th'>) {
  return (
    <th
      className={cn(
        // 高度走 --spacing-table-head（44px）。**别用 h-11** —— 根字号 14px，
        // 那是 38.5px，和设计稿差一截（DECISIONS.md §7.5）。
        'col-label h-table-head border-b border-border-subtle bg-muted/40 px-5 text-left align-middle whitespace-nowrap',
        className,
      )}
      {...props}
    />
  )
}

export function TableCell({ className, ...props }: React.ComponentProps<'td'>) {
  return (
    <td
      className={cn(
        // 同上，走 --spacing-table-row（56px）。这个值还被 list-params 的自适应分页
        // 当作行高用，改了要一起改。
        'h-table-row border-b border-border-subtle px-5 align-middle',
        'group-last:border-b-0',
        className,
      )}
      {...props}
    />
  )
}
