import { AlertCircle, Inbox } from 'lucide-react'
import type { ReactNode } from 'react'

import { Button } from '@/components/ui/button'
import { Skeleton } from '@/components/ui/skeleton'
import { ApiError } from '@/api/client'

/**
 * 加载 / 空 / 错误三种状态（DECISIONS.md §7.3：三种状态都要有）。
 * 骨架组件内部用它，页面一般不用自己写。
 */

export function LoadingRows({ rows = 6 }: { rows?: number }) {
  return (
    <div className="space-y-2 p-5">
      {Array.from({ length: rows }, (_, i) => (
        <Skeleton key={i} className="h-10 w-full" />
      ))}
    </div>
  )
}

export function EmptyState({ message = '没有数据', action }: { message?: string; action?: ReactNode }) {
  return (
    <div className="flex flex-col items-center justify-center gap-3 py-20 text-center">
      <Inbox className="size-9 text-muted-foreground/50" />
      <p className="text-muted-foreground">{message}</p>
      {action}
    </div>
  )
}

/**
 * 提交失败的横幅。**放在表单顶部，紧挨着「保存」** ——
 * 挂在页面最底下的话，按钮在最上面，人根本看不见，只觉得「点了没反应」。
 *
 * `action` 给的是**出路**，不只是说明。最典型的是版本冲突：光说「被别人改了」
 * 没用，得给一个「重新加载」让人能继续干活。
 */
export function FormAlert({
  message,
  action,
}: {
  message: string
  action?: { label: string; onClick: () => void }
}) {
  return (
    <div
      role="alert"
      className="flex items-start gap-3 rounded-lg bg-destructive/10 px-4 py-3 text-destructive"
    >
      <AlertCircle className="mt-0.5 size-4 shrink-0" />
      <p className="min-w-0 flex-1">{message}</p>
      {action ? (
        <Button variant="outline" size="sm" className="shrink-0" onClick={action.onClick}>
          {action.label}
        </Button>
      ) : null}
    </div>
  )
}

export function ErrorState({ error, onRetry }: { error: unknown; onRetry?: () => void }) {
  const apiError = error instanceof ApiError ? error : null

  return (
    <div className="flex flex-col items-center justify-center gap-3 py-20 text-center">
      <AlertCircle className="size-9 text-destructive/70" />
      <div className="space-y-1">
        <p className="font-medium">{apiError?.message ?? '出错了'}</p>
        {/* 5xx 只给通用文案，request_id 是唯一能定位日志的线索，一定要显示出来 */}
        {apiError?.requestId ? (
          <p className="text-sm text-muted-foreground">
            请求 ID：<code className="tabular">{apiError.requestId}</code>
          </p>
        ) : null}
      </div>
      {onRetry ? (
        <Button variant="outline" size="sm" onClick={onRetry}>
          重试
        </Button>
      ) : null}
    </div>
  )
}
