import type { ReactNode } from 'react'

/**
 * 页面标题块：标题在左、操作在右，下面留 24px。
 *
 * 列表页和详情页共用同一个 —— 分头写的话两边的字号和间距一定会飘。
 */
export function PageHeader({
  title,
  description,
  actions,
}: {
  title: string
  description?: string
  actions?: ReactNode
}) {
  return (
    <header className="mb-6 flex shrink-0 flex-wrap items-start justify-between gap-4">
      <div className="space-y-1">
        <h1 className="text-2xl">{title}</h1>
        {description ? <p className="text-muted-foreground">{description}</p> : null}
      </div>
      {actions ? <div className="flex items-center gap-2">{actions}</div> : null}
    </header>
  )
}
