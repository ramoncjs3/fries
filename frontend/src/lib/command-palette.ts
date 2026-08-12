import { useEffect, useState } from 'react'

/**
 * 监听 ⌘K / Ctrl+K，返回命令面板的开关状态。
 *
 * 单独放在 lib 里而不是和 <CommandPalette> 同文件 —— 一个文件同时导出组件和
 * 非组件，Vite 的 Fast Refresh 就失效了，改一行样式整页都要重刷。
 */
export function useCommandPalette() {
  const [open, setOpen] = useState(false)

  useEffect(() => {
    function onKeyDown(event: KeyboardEvent) {
      if (event.key.toLowerCase() !== 'k' || !(event.metaKey || event.ctrlKey)) return
      // 拦掉浏览器自己的 ⌘K（Chrome 是聚焦地址栏搜索）
      event.preventDefault()
      setOpen((v) => !v)
    }
    window.addEventListener('keydown', onKeyDown)
    return () => window.removeEventListener('keydown', onKeyDown)
  }, [])

  return { open, setOpen }
}
