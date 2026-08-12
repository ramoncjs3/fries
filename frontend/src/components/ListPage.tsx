import type { UseQueryResult } from '@tanstack/react-query'
import { ChevronLeft, ChevronRight } from 'lucide-react'
import { Fragment, useRef, useState, type ReactNode } from 'react'
import { Link, useNavigate } from 'react-router'

import type { PageResult } from '@/api/client'
import { ListFilters, type FilterSpec } from '@/components/ListFilters'
import { PageHeader } from '@/components/PageHeader'
import { EmptyState, ErrorState, LoadingRows } from '@/components/PageState'
import { Button } from '@/components/ui/button'
import { Checkbox } from '@/components/ui/checkbox'
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from '@/components/ui/table'
import { FROM_LIST_STATE } from '@/lib/detail-nav'
import type { ListParams } from '@/lib/list-params'
import { useScrollRestore } from '@/lib/scroll-restore'
import { cn } from '@/lib/utils'

/**
 * 列表页骨架。**新模块只能用它填内容，不允许自己搭页面结构**（DECISIONS.md §7.1）。
 *
 * 页面分三段：标题块、筛选条、表格卡片。筛选项用 `FilterSpec[]` 声明，
 * 长成什么样由 `<ListFilters>` 决定 —— 模块不该操心筛选条的排版。
 */

export interface Column<T> {
  /** 列标识，用作 React key。 */
  key: string
  header: ReactNode
  /** 渲染单元格。 */
  cell: (row: T) => ReactNode
  /** 额外的 className，比如右对齐数字列。 */
  className?: string
}

interface ListPageProps<T> {
  title: string
  description?: string
  /** 右上角的主操作，比如「新增」。 */
  actions?: ReactNode
  /** 筛选项声明。长成什么样由 <ListFilters> 说了算。 */
  filters?: FilterSpec[]
  /** 搜索框占位文案；不传就不显示搜索框。 */
  searchPlaceholder?: string
  columns: Array<Column<T>>
  query: UseQueryResult<PageResult<T>>
  params: ListParams
  rowKey: (row: T) => string
  /**
   * 点一行去哪个详情页，返回路径（比如 `/users/${row.id}`）。**看详情一律用它**，
   * 不要自己在 `onRowClick` 里 navigate —— 它多做了三件事：
   *
   *   1. 自动带上「我是从列表来的」记号，详情页的「返回」才知道能退回来
   *      （筛选和页码都在那条历史记录上，见 `lib/detail-nav.ts`）
   *   2. 首列渲染成真链接，⌘/Ctrl+点击能开新标签、右键能复制地址
   *   3. 键盘能 Tab 到它 —— 只挂 onClick 的行，键盘用户根本进不去
   */
  rowLink?: (row: T) => string
  /** 点一行触发别的事（不是去详情页）。要去详情页请用 `rowLink`。 */
  onRowClick?: (row: T) => void
  /**
   * 行内展开的内容。传了就在最左边加一列箭头。
   *
   * 只适合**同构的补充信息**（一条日志的原始 detail、一单的商品明细）——
   * 字段一多就该换抽屉，别把列表撑成两层表格（DECISIONS.md §7.6）。
   */
  expandable?: (row: T) => ReactNode
  /**
   * 传了就在最左边加一列复选框，并在选中时浮出批量操作条。
   *
   * 参数是当前选中的行和「清空选择」——批量操作做完要自己调 clear()，
   * 不然操作完了勾还留在那儿，人会以为没生效。
   */
  bulkActions?: (selected: T[], clear: () => void) => ReactNode
  emptyMessage?: string
}

export function ListPage<T>({
  title,
  description,
  actions,
  filters,
  searchPlaceholder,
  columns,
  query,
  params,
  rowKey,
  rowLink,
  onRowClick,
  expandable,
  bulkActions,
  emptyMessage,
}: ListPageProps<T>) {
  const [expanded, setExpanded] = useState<Set<string>>(new Set())
  const [picked, setPicked] = useState<Set<string>>(new Set())
  const navigate = useNavigate()

  // 从详情页退回来时把滚动位置放回去。等数据渲染出来再放，见 useScrollRestore。
  const scrollRef = useRef<HTMLDivElement>(null)
  useScrollRestore(scrollRef, !query.isPending)

  function toggle(key: string) {
    setExpanded((current) => {
      const next = new Set(current)
      if (!next.delete(key)) next.add(key)
      return next
    })
  }

  const rows = query.data?.items ?? []
  const pickedRows = rows.filter((row) => picked.has(rowKey(row)))
  const allPicked = rows.length > 0 && pickedRows.length === rows.length
  const somePicked = pickedRows.length > 0 && !allPicked

  function togglePick(key: string) {
    setPicked((current) => {
      const next = new Set(current)
      if (!next.delete(key)) next.add(key)
      return next
    })
  }

  function toggleAll() {
    // 只操作当前页：跨页全选是「我以为只选了这一页」的经典事故来源
    setPicked(allPicked ? new Set() : new Set(rows.map(rowKey)))
  }

  const pagination = query.data?.pagination
  const total = pagination?.total ?? 0
  const pageCount = pagination ? Math.max(1, Math.ceil(total / pagination.page_size)) : 1

  return (
    <div ref={scrollRef} className="flex min-h-0 flex-1 flex-col overflow-y-auto px-8 py-8">
      <PageHeader title={title} description={description} actions={actions} />

      {/* 筛选独立一行，控件 40px，给足位置 */}
      {searchPlaceholder || filters?.length ? (
        <ListFilters specs={filters ?? []} params={params} searchPlaceholder={searchPlaceholder} />
      ) : null}

      {/* 表格是一张卡片。**高度跟着内容走，不撑满** ——
          一页就 10 条，撑满的话表格底下会空出一大块，很难看。 */}
      <div className="surface flex shrink-0 flex-col overflow-hidden">
        <div className="overflow-x-auto">
        {query.isPending ? (
          // 骨架行数 = 每页条数，加载完不会跳一下
          <LoadingRows rows={params.pageSize} />
        ) : query.isError ? (
          <ErrorState error={query.error} onRetry={() => void query.refetch()} />
        ) : rows.length === 0 ? (
          <EmptyState message={emptyMessage ?? '没有符合条件的数据'} />
        ) : (
          <Table>
            <TableHeader>
              <TableRow>
                {bulkActions ? (
                  <TableHead className="w-10 pr-0">
                    <Checkbox
                      aria-label="全选本页"
                      checked={allPicked ? true : somePicked ? 'indeterminate' : false}
                      onCheckedChange={toggleAll}
                    />
                  </TableHead>
                ) : null}
                {expandable ? <TableHead className="w-10" /> : null}
                {columns.map((column) => (
                  <TableHead key={column.key} className={column.className}>
                    {column.header}
                  </TableHead>
                ))}
              </TableRow>
            </TableHeader>
            <TableBody>
              {rows.map((row) => {
                const key = rowKey(row)
                const isOpen = expanded.has(key)
                const to = rowLink?.(row)
                return (
                  <Fragment key={key}>
                    <TableRow
                      onClick={
                        to
                          ? () => void navigate(to, { state: FROM_LIST_STATE })
                          : onRowClick
                            ? () => onRowClick(row)
                            : expandable
                              ? () => toggle(key)
                              : undefined
                      }
                      className={cn((to || onRowClick || expandable) && 'cursor-pointer')}
                    >
                      {bulkActions ? (
                        <TableCell className="pr-0" onClick={(e) => e.stopPropagation()}>
                          <Checkbox
                            aria-label="选中此行"
                            checked={picked.has(key)}
                            onCheckedChange={() => togglePick(key)}
                          />
                        </TableCell>
                      ) : null}
                      {expandable ? (
                        <TableCell className="pr-0">
                          {/* 整行都能点开，这个箭头只是告诉人「这里能展开」 */}
                          <ChevronRight
                            aria-hidden
                            className={cn(
                              'size-4 text-muted-foreground transition-transform',
                              isOpen && 'rotate-90',
                            )}
                          />
                        </TableCell>
                      ) : null}
                      {columns.map((column, index) => (
                        <TableCell key={column.key} className={column.className}>
                          {/* 首列包一层真链接。**stopPropagation 不能省** ——
                              不然行的 onClick 也会跑一遍，同一个地址进两次历史，
                              「返回」要按两下才出得去。 */}
                          {to && index === 0 ? (
                            <Link
                              to={to}
                              state={FROM_LIST_STATE}
                              onClick={(event) => event.stopPropagation()}
                              className="block"
                            >
                              {column.cell(row)}
                            </Link>
                          ) : (
                            column.cell(row)
                          )}
                        </TableCell>
                      ))}
                    </TableRow>
                    {expandable && isOpen ? (
                      <tr>
                        {/* 展开区不套 TableRow：它有 56px 固定高和 hover 底色，都不适用 */}
                        <td
                          colSpan={columns.length + (bulkActions ? 2 : 1)}
                          className="border-b border-border-subtle bg-muted/40 px-5 py-4"
                        >
                          {expandable(row)}
                        </td>
                      </tr>
                    ) : null}
                  </Fragment>
                )
              })}
            </TableBody>
          </Table>
        )}
        </div>

        {pagination && total > 0 ? (
          <div className="flex shrink-0 items-center justify-between gap-3 border-t border-border-subtle px-5 py-3 text-muted-foreground">
          <span>
            共 {total} 条 · 第 {pagination.page} / {pageCount} 页
          </span>
            <div className="flex items-center gap-2">
              <Button
                variant="outline"
                size="sm"
                disabled={pagination.page <= 1 || query.isFetching}
                onClick={() => params.setPage(pagination.page - 1)}
              >
                <ChevronLeft /> 上一页
              </Button>
              <Button
                variant="outline"
                size="sm"
                disabled={pagination.page >= pageCount || query.isFetching}
                onClick={() => params.setPage(pagination.page + 1)}
              >
                下一页 <ChevronRight />
              </Button>
            </div>
          </div>
        ) : null}
      </div>

      {/* 批量操作条浮在底部中间：不占布局、离手最近，选中才出现 */}
      {bulkActions && pickedRows.length > 0 ? (
        <div className="pointer-events-none sticky bottom-0 z-20 flex justify-center pt-4">
          <div className="surface pointer-events-auto flex items-center gap-3 px-4 py-2.5 shadow-overlay">
            <span className="whitespace-nowrap">已选 {pickedRows.length} 项</span>
            <span className="h-4 w-px bg-border" />
            {bulkActions(pickedRows, () => setPicked(new Set()))}
            <Button variant="ghost" size="sm" onClick={() => setPicked(new Set())}>
              取消
            </Button>
          </div>
        </div>
      ) : null}
    </div>
  )
}
