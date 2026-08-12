import type * as React from 'react'

import { cn } from '@/lib/utils'

export function Input({ className, type, ...props }: React.ComponentProps<'input'>) {
  return (
    <input
      type={type}
      className={cn(
        'flex h-10 w-full rounded-lg border border-input bg-background px-3',
        'transition-all duration-150',
        'placeholder:text-muted-foreground/70',
        // 聚焦时边框变主色 + 一圈低透明度光晕，比默认那圈粗黑边干净得多
        'outline-none focus-visible:border-accent focus-visible:ring-3 focus-visible:ring-ring',
        'disabled:cursor-not-allowed disabled:opacity-50',
        'aria-invalid:border-destructive aria-invalid:ring-destructive/25',
        className,
      )}
      {...props}
    />
  )
}
