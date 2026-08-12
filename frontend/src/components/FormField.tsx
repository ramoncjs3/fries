import type { ReactNode } from 'react'

/**
 * 表单里的一行：**标签在控件上方** + 错误 + 说明。
 *
 * 用在**窄栏表单**上：登录、改密这类居中的窄卡片，以及一次性动作的弹窗。
 * 那种宽度下标签放左边会把控件挤得没地方。
 *
 * ⚠️ **详情页不用它**，那边是「标签在左、值在右」的 `<DetailItem>`（§7.6）——
 * 详情要能一屏装下几十个字段，左标签比上标签省掉近一半高度。
 * 两种排法各有各的场合，别混着用。
 */
/** 表单里的一行：标签 + 控件 + 错误提示。所有字段都用它，间距才一致。 */
export function FormField({
  label,
  required,
  error,
  hint,
  children,
}: {
  label: string
  required?: boolean
  error?: string
  hint?: string
  children: ReactNode
}) {
  return (
    <div className="space-y-2">
      <div className="font-medium">
        {label}
        {required ? <span className="ml-0.5 text-destructive">*</span> : null}
      </div>
      {children}
      {error ? <p className="text-sm text-destructive">{error}</p> : null}
      {!error && hint ? <p className="text-sm text-muted-foreground">{hint}</p> : null}
    </div>
  )
}
