import { Slot } from '@radix-ui/react-slot'
import { cva, type VariantProps } from 'class-variance-authority'
import type * as React from 'react'

import { cn } from '@/lib/utils'

// 只在本文件用：导出它会让 Fast Refresh 失效。
const buttonVariants = cva(
  [
    'inline-flex items-center justify-center gap-1.5 whitespace-nowrap rounded-md',
    'text-sm font-medium transition-all duration-150',
    // 焦点环是彩色的、带偏移 —— 默认那圈灰边是「老」的味道之一
    'outline-none focus-visible:ring-3 focus-visible:ring-ring focus-visible:border-accent',
    'disabled:pointer-events-none disabled:opacity-45',
    '[&_svg]:size-3.5 [&_svg]:shrink-0',
  ],
  {
    variants: {
      variant: {
        // 主按钮是**近黑**，不是蓝。中性色扛主视觉，彩色只做点缀（§7.5）。
        // 加一道内高光，看着是「有厚度的一块」而不是一个色块。
        default: [
          'bg-primary text-primary-foreground',
          'inset-shadow-2xs inset-shadow-white/12',
          'hover:brightness-125 active:brightness-95 dark:hover:brightness-92',
        ],
        // 蓝按钮。**慎用** —— 一个页面最多一个，否则蓝就不再是「点缀」了
        accent: [
          'bg-accent text-accent-foreground',
          'inset-shadow-2xs inset-shadow-white/12',
          'hover:brightness-112 active:brightness-95',
        ],
        destructive: 'bg-destructive text-destructive-foreground hover:brightness-110',
        outline: 'border border-input bg-card shadow-control hover:bg-secondary',
        secondary: 'bg-secondary text-secondary-foreground hover:brightness-97',
        ghost: 'text-muted-foreground hover:bg-secondary hover:text-foreground',
        link: 'text-accent-text underline-offset-4 hover:underline',
      },
      size: {
        default: 'h-8 px-3',
        sm: 'h-7 px-2.5 text-xs',
        lg: 'h-10 px-4',
        icon: 'size-8',
      },
    },
    defaultVariants: { variant: 'default', size: 'default' },
  },
)

export interface ButtonProps
  extends React.ButtonHTMLAttributes<HTMLButtonElement>,
    VariantProps<typeof buttonVariants> {
  asChild?: boolean
}

export function Button({ className, variant, size, asChild = false, ...props }: ButtonProps) {
  const Comp = asChild ? Slot : 'button'
  return <Comp className={cn(buttonVariants({ variant, size }), className)} {...props} />
}
