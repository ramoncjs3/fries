import { useEffect } from 'react'
import { useBlocker } from 'react-router'

import { useConfirm, type ConfirmState } from '@/lib/confirm'

/**
 * 有未保存的修改时，拦住「离开这一页」。
 *
 * 详情页内编辑之后这条就必须有了：以前编辑是弹窗，关弹窗是个明确动作，`FormDialog`
 * 自己拦得住；现在改在页面里，人可能点侧栏菜单、点面包屑、按浏览器后退 ——
 * 每一条路都会不声不响地把刚敲的东西冲掉。
 *
 * 拦截用的是 react-router 自带的 `useBlocker`（要求 data router，我们用的
 * `createBrowserRouter` 正是），**不自己监听 popstate** —— 那个自己写必错。
 *
 * ⚠️ 拦不住的是**关标签页/刷新**，那得靠 `beforeunload`，浏览器只允许弹它自己的
 * 原生框，文案也改不了。这里一并挂上。
 */
/**
 * @param dirty 有没有未保存的修改
 * @param watchParams 额外要盯住的查询参数名。**左右分栏页必须传** ——
 *   见下面 blocker 里的说明。
 */
export function useUnsavedGuard(dirty: boolean, watchParams: string[] = []): ConfirmState {
  const confirm = useConfirm()

  const blocker = useBlocker(
    ({ currentLocation, nextLocation }) => {
      if (!dirty) return false
      // 换页面，拦
      if (currentLocation.pathname !== nextLocation.pathname) return true

      // 同一个 pathname 下，**只有换记录才算离开，换模式不算**：
      //   - 退出编辑态是摘掉 `?edit=1` —— 不拦。都拦的话「保存」成功之后
      //     会被自己弹一个「放弃未保存的修改？」，而东西已经存进去了（踩过）
      //   - 部门页换一个树节点是改 `?detail=` —— **必须拦**。不拦的话正在改的
      //     内容一声不吭就没了，页面上什么提示都没有（也踩过）
      const before = new URLSearchParams(currentLocation.search)
      const after = new URLSearchParams(nextLocation.search)
      return watchParams.some((key) => before.get(key) !== after.get(key))
    },
  )

  useEffect(() => {
    if (blocker.state !== 'blocked') return
    confirm.open({
      title: '放弃未保存的修改？',
      description: '离开之后这次改的内容不会保留。',
      confirmText: '放弃',
      destructive: true,
      onConfirm: () => blocker.proceed(),
      // 取消掉就得把 blocker 复位，否则它一直卡在 blocked，
      // 下一次导航会被直接吞掉，表现是「点菜单没反应」
      onCancel: () => blocker.reset(),
    })
    // confirm 每次渲染都是新对象，进依赖会把自己弹成死循环
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [blocker.state])

  // 关标签页 / 刷新只能交给浏览器的原生提示
  useEffect(() => {
    if (!dirty) return
    function warn(event: BeforeUnloadEvent) {
      event.preventDefault()
    }
    window.addEventListener('beforeunload', warn)
    return () => window.removeEventListener('beforeunload', warn)
  }, [dirty])

  return confirm
}
