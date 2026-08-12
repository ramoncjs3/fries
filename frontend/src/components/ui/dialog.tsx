import * as DialogPrimitive from '@radix-ui/react-dialog'
import { X } from 'lucide-react'
import type * as React from 'react'

import { cn } from '@/lib/utils'

export const Dialog = DialogPrimitive.Root
export const DialogTrigger = DialogPrimitive.Trigger
export const DialogClose = DialogPrimitive.Close

export function DialogOverlay({ className, ...props }: React.ComponentProps<typeof DialogPrimitive.Overlay>) {
  return (
    <DialogPrimitive.Overlay
      className={cn(
        'fixed inset-0 z-50 bg-black/35 backdrop-blur-sm',
        className,
      )}
      {...props}
    />
  )
}

export function DialogContent({
  className,
  children,
  showClose = true,
  ...props
}: React.ComponentProps<typeof DialogPrimitive.Content> & {
  /** 命令面板这类「ESC 就关」的浮层不需要右上角那个 X，摆着反而碍事。 */
  showClose?: boolean
}) {
  return (
    <DialogPrimitive.Portal>
      <DialogOverlay />
      <DialogPrimitive.Content
        className={cn(
          'fixed left-1/2 top-1/2 z-50 flex w-full max-w-lg -translate-x-1/2 -translate-y-1/2 flex-col',
          // 表单再长也不会撑出屏幕：内容区自己滚，页脚永远看得见（DECISIONS.md §7.1）
          'max-h-[85vh] gap-0 rounded-xl border border-border bg-popover shadow-overlay',
          'inset-shadow-2xs inset-shadow-white/40 dark:inset-shadow-white/6',
          className,
        )}
        {...props}
      >
        {children}
        {showClose ? (
          <DialogPrimitive.Close
            // ⚠️ 热区必须比图标大。只给 14px 的图标当热区的话，
            // 鼠标差几个像素就点不中，用户的感受是「这个 X 是坏的」。
            className="absolute right-3 top-3 grid size-8 place-items-center rounded-md text-muted-foreground transition-colors hover:bg-secondary hover:text-foreground focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring"
            aria-label="关闭"
          >
            <X className="size-4" />
          </DialogPrimitive.Close>
        ) : null}
      </DialogPrimitive.Content>
    </DialogPrimitive.Portal>
  )
}

export function DialogHeader({ className, ...props }: React.ComponentProps<'div'>) {
  return <div className={cn('flex flex-col gap-1 border-b border-border px-5 py-3.5', className)} {...props} />
}

export function DialogFooter({ className, ...props }: React.ComponentProps<'div'>) {
  return (
    <div
      className={cn('flex shrink-0 items-center justify-end gap-2 border-t border-border px-5 py-3', className)}
      {...props}
    />
  )
}

export function DialogTitle({ className, ...props }: React.ComponentProps<typeof DialogPrimitive.Title>) {
  return <DialogPrimitive.Title className={cn('font-semibold', className)} {...props} />
}

export function DialogDescription({
  className,
  ...props
}: React.ComponentProps<typeof DialogPrimitive.Description>) {
  return <DialogPrimitive.Description className={cn('text-sm text-muted-foreground', className)} {...props} />
}
