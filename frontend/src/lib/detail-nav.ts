import { useCallback } from 'react'
import { useLocation, useNavigate } from 'react-router'

/**
 * 列表页 ⇄ 详情页之间的来回（DECISIONS.md §7.6）。
 *
 * **筛选条件不需要我们抄一份带走。** 列表的搜索、筛选、页码本来就写在它自己的
 * URL 上，而且是用 `replace` 写的（见 `useListParams`）—— 也就是说列表在浏览器
 * 历史里始终是**一条带着完整状态的记录**。「返回列表还是原样」这件事，
 * 退回那条记录就够了，再加上 React Query 的缓存，回去连闪都不闪。
 *
 * 唯一要判断的是**身后有没有那条记录**：从列表点进来的有；
 * 别人发的链接、⌘+点击开的新标签、直接输 URL 进来的没有，只能跳过去。
 */

/**
 * 跳详情页时打在 history state 上的记号。`<ListPage>` 会自动带上，模块不用管。
 *
 * 这个记号**跟着浏览器历史走，刷新也还在** —— 在详情页按 F5 之后，
 * 「返回」依然知道自己是从列表来的。
 */
export const FROM_LIST_STATE = { fromList: true }

/**
 * 详情页的「返回」。能退就退回列表那条历史记录（筛选、页码、滚动位置原样），
 * 退不了就跳到干净的列表页。
 *
 * @param listPath 该模块的列表页路径，比如 `/users`
 */
export function useBackToList(listPath: string): () => void {
  const navigate = useNavigate()
  const location = useLocation()
  const cameFromList = (location.state as { fromList?: boolean } | null)?.fromList === true

  return useCallback(() => {
    // replace: true —— 跳过去的这一下不该再往历史里堆一条，
    // 否则「返回」两次才出得去，人会以为按钮坏了
    if (cameFromList) void navigate(-1)
    else void navigate(listPath, { replace: true })
  }, [cameFromList, listPath, navigate])
}
