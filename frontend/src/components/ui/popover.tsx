import * as PopoverPrimitive from '@radix-ui/react-popover'
import type * as React from 'react'

import { cn } from '@/lib/utils'

/**
 * 浮层。和 DropdownMenu 的分工：**里面要放表单控件就用 Popover**。
 * DropdownMenu 的键盘导航会抢走按键，输入框在里面根本打不了字。
 */

export const Popover = PopoverPrimitive.Root
export const PopoverTrigger = PopoverPrimitive.Trigger
export const PopoverAnchor = PopoverPrimitive.Anchor

export function PopoverContent({
  className,
  align = 'start',
  sideOffset = 6,
  ...props
}: React.ComponentProps<typeof PopoverPrimitive.Content>) {
  return (
    <PopoverPrimitive.Portal>
      <PopoverPrimitive.Content
        align={align}
        sideOffset={sideOffset}
        className={cn(
          // 底色必须不透明：浮层压在表格上，半透明会让两层字叠在一起（§7.5）
          'z-50 rounded-xl border border-border bg-popover p-4 text-popover-foreground shadow-overlay',
          className,
        )}
        {...props}
      />
    </PopoverPrimitive.Portal>
  )
}
