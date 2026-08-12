import { ArrowLeft } from 'lucide-react'
import type { ReactNode } from 'react'

import { PageHeader } from '@/components/PageHeader'
import { ErrorState, LoadingRows } from '@/components/PageState'
import { Button } from '@/components/ui/button'
import { useBackToList } from '@/lib/detail-nav'

/**
 * 详情页骨架。**看详情一律用它，不用抽屉**（DECISIONS.md §7.6）。
 *
 * 留白、标题字号、卡片样式全部和 `<ListPage>` 走同一套 —— 从列表点进详情时
 * 标题不该跳一下。字段是两列还是一列这种事也在这里定死，不留给每个模块自己发挥。
 *
 * 内容多就多堆几个 `<DetailSection>`，页面往下长没关系 —— 这正是详情做成整页
 * 而不是 448px 抽屉的理由。
 */

interface DetailPageProps {
  title: string
  description?: string
  /**
   * 该模块的列表页路径，比如 `/users`。**必填** —— 详情页可能是别人发的链接
   * 直接打开的，身后没有列表可退，那时就跳到这里（见 `useBackToList`）。
   */
  backTo: string
  actions?: ReactNode
  /**
   * 提交失败的横幅（`<FormAlert>`）。**渲染在卡片最顶上**，紧挨着标题栏里的
   * 「保存」—— 让每个模块自己决定放哪的话，早晚有人放到页面最底下，
   * 那等于没有。
   */
  alert?: ReactNode
  loading?: boolean
  error?: unknown
  onRetry?: () => void
  children: ReactNode
}

export function DetailPage({
  title,
  description,
  backTo,
  actions,
  alert,
  loading,
  error,
  onRetry,
  children,
}: DetailPageProps) {
  const back = useBackToList(backTo)

  return (
    <div className="flex min-h-0 flex-1 flex-col overflow-hidden">
      {/* 标题和操作**不跟着滚**。字段一多，「保存」滚出屏幕就等于没有 ——
          那正是当初嫌弃编辑弹窗的毛病，改成页面可不能原样再犯一次。
          内容限宽：整页宽度是有，但字段不该拉成横穿屏幕的一条线，
          标签在最左值在最右，眼睛要横扫一大段才对得上。 */}
      <div className="w-full max-w-3xl shrink-0 px-8 pt-8">
        <div className="mb-2 flex">
          <Button variant="ghost" onClick={back}>
            <ArrowLeft /> 返回
          </Button>
        </div>

        <PageHeader title={title} description={description} actions={actions} />
      </div>

      <div className="min-h-0 flex-1 overflow-y-auto px-8 pb-8">
        <div className="surface w-full max-w-3xl overflow-hidden">
          {loading ? (
            <LoadingRows rows={4} />
          ) : error ? (
            <ErrorState error={error} onRetry={onRetry} />
          ) : (
            <div className="space-y-8 p-6">
              {alert}
              {children}
            </div>
          )}
        </div>
      </div>
    </div>
  )
}

/**
 * 详情里的一组字段。
 *
 * **字段是「标签在左、值在右」的一行一条**，不是两列网格：一行一条无论多宽都对得齐，
 * 标签列固定宽度，眼睛顺着一条竖线往下扫。字段多的时候，左标签比「标签在上」
 * 省掉近一半高度 —— 详情页要能装下几十个字段而不至于要滚半天。
 */
export function DetailSection({ title, children }: { title?: string; children: ReactNode }) {
  return (
    <section>
      {/* 标题下压一条线，这一组的范围才有边界。**只有这一条线** ——
          每个字段再各带一条下边框的话，整块就读成账本了，而这是一个人的档案，
          不是一张数据表（§7.6）。行与行靠间距和标签列对齐，够分了。 */}
      {title ? (
        <h2 className="section-label mb-2 border-b border-border-subtle pb-3">{title}</h2>
      ) : null}
      <dl>{children}</dl>
    </section>
  )
}

/**
 * 一整块自成一体的内容（权限勾选树、成员表格这类），**不是字段列表**。
 *
 * 和 `<DetailSection>` 的区别只有两点：内容不包在 `<dl>` 里（塞非 dt/dd 进 dl 是无效
 * HTML），也不受字段值那个 `max-w` 限制 —— 这类内容要占满整张卡片才够用。
 * 标题样式两者完全一致，排在一起看不出接缝。
 */
export function DetailBlock({ title, children }: { title?: string; children: ReactNode }) {
  return (
    <section>
      {title ? (
        <h2 className="section-label mb-2 border-b border-border-subtle pb-3">{title}</h2>
      ) : null}
      {children}
    </section>
  )
}

/**
 * 详情里的一个字段。**只读和编辑用的是同一个组件** —— 只读时 children 传文字，
 * 编辑时传输入控件，标签的位置和这一行的高度都不变，切换模式不跳版。
 *
 * 这就是详情页内编辑能成立的关键：如果两种模式各排各的，一点「编辑」整页都在动，
 * 人会找不到刚才在看的那一行。
 */
export function DetailItem({
  label,
  required,
  hint,
  error,
  children,
}: {
  label: string
  /** 必填星号。只在编辑态传，只读态标上没有意义。 */
  required?: boolean
  /** 字段说明。有错误时让位给错误 —— 两条一起显示太吵。 */
  hint?: string
  error?: string
  children: ReactNode
}) {
  return (
    <div className="flex gap-4 py-1">
      {/* 标签列：**和值同字号（14px），只靠颜色弱化**。
          试过压到 12px，中文在 14px 的值旁边立刻显得像附注 —— 而标签列恰恰是
          眼睛要顺着往下扫的那一列，不能比它说明的东西还弱。层级交给上面的
          区块标题（16px/600 前景色）去拉，不靠把标签压小。
          固定宽 + 禁止换行：一换行这一条就变两行高，整列的节奏立刻乱掉。
          min-h-10 和控件同高，标签才跟值对得上。 */}
      <dt className="flex min-h-10 w-28 shrink-0 items-center whitespace-nowrap text-muted-foreground">
        {label}
        {required ? <span className="ml-0.5 text-destructive">*</span> : null}
      </dt>
      <dd className="min-w-0 flex-1">
        {/* 值和控件都限宽：手机号这种 11 位的字段配一个横贯整卡片的输入框，
            看着就不像认真做的。只读文字撑到和 h-10 的输入框一样高 ——
            这一行在两种模式下高度相同，点「编辑」不跳版。 */}
        <div className="flex min-h-10 max-w-md flex-col justify-center break-words">{children}</div>
        {error ? <p className="pb-1.5 text-sm text-destructive">{error}</p> : null}
        {!error && hint ? <p className="pb-1.5 text-sm text-muted-foreground">{hint}</p> : null}
      </dd>
    </div>
  )
}
