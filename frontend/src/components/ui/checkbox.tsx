import * as CheckboxPrimitive from '@radix-ui/react-checkbox'
import { Check, Minus } from 'lucide-react'
import type * as React from 'react'

import { cn } from '@/lib/utils'

/**
 * 复选框。支持 `checked="indeterminate"`（半选）——
 * 权限勾选树的模块行要靠它表达「这个模块勾了一部分」。
 */
export function Checkbox({ className, ...props }: React.ComponentProps<typeof CheckboxPrimitive.Root>) {
  return (
    <CheckboxPrimitive.Root
      className={cn(
        'peer size-4 shrink-0 rounded-xs border border-border-strong bg-card transition-colors',
        'data-[state=checked]:border-accent data-[state=checked]:bg-accent data-[state=checked]:text-accent-foreground',
        'data-[state=indeterminate]:border-accent data-[state=indeterminate]:bg-accent data-[state=indeterminate]:text-accent-foreground',
        'outline-none focus-visible:ring-3 focus-visible:ring-ring',
        'disabled:cursor-not-allowed disabled:opacity-50',
        className,
      )}
      {...props}
    >
      <CheckboxPrimitive.Indicator className="grid place-items-center text-current">
        {props.checked === 'indeterminate' ? <Minus className="size-3" /> : <Check className="size-3" />}
      </CheckboxPrimitive.Indicator>
    </CheckboxPrimitive.Root>
  )
}
