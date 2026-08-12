import { useEffect, type RefObject } from 'react'
import { useLocation } from 'react-router'

/**
 * 记住列表滚到哪了，浏览器「后退」回来时放回去。
 *
 * react-router 自带 `<ScrollRestoration>`，但翻源码确认过**它只认 `window.scrollY`**；
 * 而我们的外壳是「整屏不滚，只有内容区内部滚」（见 `AppShell` 顶部的注释），
 * 所以那个组件在这儿用不上，这里照着它的思路盯容器。
 *
 * **位置存在内存里，不进 sessionStorage**：要解决的是「点进详情再返回」，
 * 这期间页面没有重载，内存够用；而真刷新的时候 `location.key` 会重新生成，
 * 存了也对不上号，白存一场。
 */

/** `location.key` → 滚动位置。一条历史记录一个位置，这也是浏览器自己的做法。 */
const positions = new Map<string, number>()

/**
 * @param ref 滚动容器
 * @param ready 内容是否已经渲染出来 —— **必须等它为 true 再放位置**：
 *   数据没到时容器是空的、没有可滚动高度，这时设 `scrollTop` 会被浏览器直接夹成 0。
 */
export function useScrollRestore(ref: RefObject<HTMLElement | null>, ready: boolean) {
  const { key } = useLocation()

  useEffect(() => {
    const el = ref.current
    if (!el || !ready) return

    const saved = positions.get(key)
    if (saved) el.scrollTop = saved

    function remember() {
      positions.set(key, el!.scrollTop)
    }

    el.addEventListener('scroll', remember, { passive: true })
    return () => el.removeEventListener('scroll', remember)
  }, [key, ready, ref])
}
