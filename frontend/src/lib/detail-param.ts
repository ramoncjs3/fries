import { useCallback, useMemo } from 'react'
import { useSearchParams } from 'react-router'

/**
 * **左右分栏页的页面状态**，存在 URL 上 —— 现在只有部门管理在用：
 * 选中的是哪个节点（`?detail=<id>`）、右边那个面板是在看还是在改（`?pane=`）。
 *
 * 存组件 state 里最省事，但刷新就没了、链接发给同事打开是空的、浏览器后退会
 * 直接退出整个页面。存 URL 这三件事都顺带解决了。
 *
 * ⚠️ **看详情不要用它**。详情一律是独立页面 `/xxx/:id`（DECISIONS.md §7.6），
 * 走 `<ListPage rowLink>` + `lib/detail-nav.ts`。这个 hook 管的是「同一页里的状态」，
 * 不是「跳到另一页」。
 */
export function useDetailParam() {
  const [searchParams, setSearchParams] = useSearchParams()
  const id = searchParams.get('detail') ?? ''

  /**
   * 一次改多个参数。**要一起改的必须在同一次调用里改完** ——
   * `setSearchParams` 的函数式写法拿到的是当前这次渲染的参数，同一 tick 里连调几次
   * 每次都从同一份旧值算起，后一次会盖掉前一次（docs/MEMORY.md 记过这个坑）。
   */
  const update = useCallback(
    (mutate: (params: URLSearchParams) => void, options?: { replace?: boolean }) => {
      setSearchParams(
        (current) => {
          const params = new URLSearchParams(current)
          mutate(params)
          return params
        },
        // 默认留一条历史：换一条记录（换选中的节点）时「后退」要回得到上一条。
        // 但**换模式要传 replace** —— 进/出编辑态各堆一条历史的话，
        // 「后退」得按好几次才出得去，和整页详情的 `?edit=1` 也不一致
        { replace: options?.replace ?? false },
      )
    },
    [setSearchParams],
  )

  const setID = useCallback(
    (next: string) => {
      update((params) => {
        if (next) params.set('detail', next)
        else params.delete('detail')
        // 换了个节点就回到「看」的状态 —— 带着上一个节点的编辑态过去是错的
        params.delete('pane')
        params.delete('under')
      })
    },
    [update],
  )

  return useMemo(
    () => ({ id, setID, close: () => setID(''), params: searchParams, update }),
    [id, setID, searchParams, update],
  )
}
