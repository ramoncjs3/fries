import { clsx, type ClassValue } from 'clsx'
import { twMerge } from 'tailwind-merge'

/** 合并 className，后来的 Tailwind 类会覆盖前面冲突的那个。shadcn 组件都用它。 */
export function cn(...inputs: ClassValue[]) {
  return twMerge(clsx(inputs))
}
