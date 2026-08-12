import { useCallback, useState } from 'react'

/**
 * 二次确认的状态。配合 `<ConfirmDialog state={confirm} />` 用。
 *
 * hook 单独放在 lib 里 —— 一个文件同时导出组件和非组件，Vite 的 Fast Refresh
 * 就失效了，改一行样式整页都要重刷。
 */
export interface ConfirmOptions {
  title: string
  description?: string
  confirmText?: string
  destructive?: boolean
  /** 返回 Promise 的话，等它完成再关 —— 中途的失败要留在弹窗里说。 */
  onConfirm: () => void | Promise<unknown>
  /**
   * 点「取消」或者点遮罩关掉时调一次。
   *
   * 大多数场景不用管（不确认就是什么都不做）。但有的调用方在弹之前就挂起了状态，
   * 取消时必须复位 —— 比如 `useUnsavedGuard` 拦住的那次导航，不复位它会一直卡着，
   * 之后点什么都没反应。
   */
  onCancel?: () => void
}

export interface ConfirmState {
  open: (options: ConfirmOptions) => void
  options: ConfirmOptions | null
  close: () => void
  running: boolean
  run: () => void
}

export function useConfirm(): ConfirmState {
  const [options, setOptions] = useState<ConfirmOptions | null>(null)
  const [running, setRunning] = useState(false)

  const close = useCallback(() => {
    // 正在执行时不许关：关了就看不到失败提示，也不知道到底删没删
    if (running) return
    // onCancel 是副作用，**不能放进 setOptions 的更新函数里** ——
    // StrictMode 会把更新函数跑两次，副作用就跟着跑两次
    options?.onCancel?.()
    setOptions(null)
  }, [options, running])

  const run = useCallback(() => {
    if (!options) return
    const result = options.onConfirm()
    if (!(result instanceof Promise)) {
      setOptions(null)
      return
    }
    setRunning(true)
    result
      .then(() => setOptions(null))
      // 失败不关弹窗，错误提示由 mutation 的 onError 弹 toast
      .catch(() => undefined)
      .finally(() => setRunning(false))
  }, [options])

  return { open: setOptions, options, close, running, run }
}
