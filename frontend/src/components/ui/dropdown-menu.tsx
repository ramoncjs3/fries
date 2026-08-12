import * as DropdownMenuPrimitive from '@radix-ui/react-dropdown-menu'
import type * as React from 'react'

import { cn } from '@/lib/utils'

export const DropdownMenu = DropdownMenuPrimitive.Root
export const DropdownMenuTrigger = DropdownMenuPrimitive.Trigger
export const DropdownMenuSeparator = DropdownMenuPrimitive.Separator

export function DropdownMenuContent({
  className,
  sideOffset = 4,
  ...props
}: React.ComponentProps<typeof DropdownMenuPrimitive.Content>) {
  return (
    <DropdownMenuPrimitive.Portal>
      <DropdownMenuPrimitive.Content
        sideOffset={sideOffset}
        className={cn(
          // **底色必须是不透明的 bg-popover**：浮层压在表单/表格上，
          // 半透明的话下面的字会透上来，两层字叠在一起没法看。
          'z-50 min-w-40 overflow-hidden rounded-lg border border-border bg-popover p-1 text-popover-foreground shadow-overlay',
          className,
        )}
        {...props}
      />
    </DropdownMenuPrimitive.Portal>
  )
}

export function DropdownMenuItem({
  className,
  ...props
}: React.ComponentProps<typeof DropdownMenuPrimitive.Item>) {
  return (
    <DropdownMenuPrimitive.Item
      className={cn(
        'relative flex cursor-default select-none items-center gap-2 rounded-md px-2 py-1.5 text-sm outline-none',
        'focus:bg-secondary data-[disabled]:pointer-events-none data-[disabled]:opacity-50',
        '[&_svg]:size-4 [&_svg]:shrink-0',
        className,
      )}
      {...props}
    />
  )
}

export function DropdownMenuLabel({
  className,
  ...props
}: React.ComponentProps<typeof DropdownMenuPrimitive.Label>) {
  return (
    <DropdownMenuPrimitive.Label
      className={cn('px-2 py-1.5 text-xs text-muted-foreground', className)}
      {...props}
    />
  )
}
