import { Loader2 } from 'lucide-react'

/** 懒加载页面时的占位。 */
export function PageFallback() {
  return (
    <div className="grid min-h-64 place-items-center">
      <Loader2 className="size-5 animate-spin text-muted-foreground" />
    </div>
  )
}
