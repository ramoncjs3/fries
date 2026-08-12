import { Loader2 } from 'lucide-react'

import { Button } from '@/components/ui/button'
import type { ConfirmState } from '@/lib/confirm'
import { Dialog, DialogContent, DialogDescription, DialogFooter, DialogHeader, DialogTitle } from '@/components/ui/dialog'

/**
 * 二次确认。**破坏性操作只能用它**，不许用 `window.confirm`：
 * 那个长得跟系统弹窗一样、没法说清后果、也没法显示「正在删除」。
 *
 * 用法是一个 hook + 一个组件：
 *
 *	const confirm = useConfirm()
 *	confirm.open({ title: '删除「财务部」？', destructive: true, onConfirm: () => remove(...) })
 *	// 页面末尾： <ConfirmDialog state={confirm} />
 */
export function ConfirmDialog({ state }: { state: ConfirmState }) {
  const { options, close, running, run } = state

  return (
    <Dialog open={options !== null} onOpenChange={(next) => (next ? undefined : close())}>
      <DialogContent className="max-w-100">
        <DialogHeader>
          <DialogTitle>{options?.title}</DialogTitle>
          {options?.description ? <DialogDescription>{options.description}</DialogDescription> : null}
        </DialogHeader>
        <DialogFooter>
          <Button variant="outline" onClick={close} disabled={running}>
            取消
          </Button>
          <Button variant={options?.destructive ? 'destructive' : 'default'} onClick={run} disabled={running}>
            {running ? <Loader2 className="animate-spin" /> : null}
            {options?.confirmText ?? '确定'}
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  )
}
