import * as DialogPrimitive from '@radix-ui/react-dialog'
import { X } from 'lucide-react'
import type * as React from 'react'

import { cn } from '@/lib/utils'

/**
 * 右侧抽屉。底层还是 Radix Dialog（焦点陷阱、ESC、点遮罩关闭都是现成的），
 * 只是贴在右边而不是居中。
 *
 * 和 Dialog 的分工：**详情用抽屉、表单用 Dialog**。
 * 居中弹窗放详情是错的 —— 内容一多就得滚，还把列表整个遮住（DECISIONS.md §7.6）。
 */

export const Sheet = DialogPrimitive.Root
export const SheetTrigger = DialogPrimitive.Trigger
export const SheetTitle = DialogPrimitive.Title
export const SheetDescription = DialogPrimitive.Description

export function SheetContent({
  className,
  children,
  ...props
}: React.ComponentProps<typeof DialogPrimitive.Content>) {
  return (
    <DialogPrimitive.Portal>
      {/* 遮罩比弹窗浅：抽屉是「在列表旁边看一眼」，不该把列表压死 */}
      <DialogPrimitive.Overlay className="fixed inset-0 z-50 bg-black/20" />
      <DialogPrimitive.Content
        className={cn(
          'fixed inset-y-0 right-0 z-50 flex w-full max-w-md flex-col border-l border-border bg-card shadow-overlay',
          'data-[state=open]:animate-sheet-in data-[state=closed]:animate-sheet-out',
          className,
        )}
        {...props}
      >
        {children}
        <DialogPrimitive.Close
          // 热区做大，理由同 dialog.tsx
          className="absolute right-3 top-3.5 grid size-8 place-items-center rounded-md text-muted-foreground transition-colors hover:bg-secondary hover:text-foreground focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring"
          aria-label="关闭"
        >
          <X className="size-4" />
        </DialogPrimitive.Close>
      </DialogPrimitive.Content>
    </DialogPrimitive.Portal>
  )
}

// pr-14 是给右上角那个 ✕ 让位 —— 不留的话标题一长就压在关闭按钮下面
export function SheetHeader({ className, ...props }: React.ComponentProps<'div'>) {
  return (
    <div
      className={cn('shrink-0 border-b border-border-subtle px-6 py-4 pr-14', className)}
      {...props}
    />
  )
}
