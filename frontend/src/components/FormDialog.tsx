import { Loader2 } from 'lucide-react'
import type { FormEvent, ReactNode } from 'react'

import { ConfirmDialog } from '@/components/ConfirmDialog'
import { Button } from '@/components/ui/button'
import { useConfirm } from '@/lib/confirm'
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from '@/components/ui/dialog'

/**
 * 表单弹窗骨架。**只用于「一次性动作」** —— 批量调部门、往部门里加人这类
 * 「选一批东西然后确认」的操作。
 *
 * ⚠️ **单条记录的新增和编辑不许用它**：那些一律是页面（`/xxx/new`、`/xxx/:id?edit=1`），
 * 见 DECISIONS.md §7.6。字段一多弹窗就撑爆，内容区自己滚、人一边填一边找不到「保存」。
 *
 * 它封的是几个反复踩到的坑（DECISIONS.md §7.1）：
 *   - 表单一长就滑不动：内容区自己滚，**保存/取消永远可见**
 *   - 找不到关闭按钮：右上角 X、ESC、点遮罩三条路都通
 *   - 改了没保存就手滑关掉：脏了就先弹确认
 *   - 提交中重复点：按钮禁用 + loading
 *
 * ⚠️ 脏检查用的是**自己的确认框，不是 `window.confirm`**。
 * 原生对话框会被不少环境静默屏蔽（内嵌浏览器、WebView、开了「阻止弹窗」的标签页），
 * 屏蔽之后它直接返回 false —— 表现就是**这个弹窗永远关不掉**，ESC 和 ✕ 都没反应。
 * 这个坑真踩过一次。
 */

interface FormDialogProps {
  open: boolean
  onOpenChange: (open: boolean) => void
  title: string
  description?: string
  children: ReactNode
  onSubmit: () => void | Promise<void>
  submitting?: boolean
  submitText?: string
  /** 表单是否被改过。改过又要关闭时会先确认一次。 */
  dirty?: boolean
  /** 危险操作（删除之类）用红色主按钮。 */
  destructive?: boolean
}

export function FormDialog({
  open,
  onOpenChange,
  title,
  description,
  children,
  onSubmit,
  submitting = false,
  submitText = '保存',
  dirty = false,
  destructive = false,
}: FormDialogProps) {
  const discard = useConfirm()

  function requestClose(next: boolean) {
    if (next) {
      onOpenChange(true)
      return
    }
    if (submitting) return // 提交中不许关
    if (dirty) {
      discard.open({
        title: '放弃未保存的修改？',
        description: '关掉之后这次改的内容不会保留。',
        confirmText: '放弃',
        destructive: true,
        onConfirm: () => onOpenChange(false),
      })
      return
    }
    onOpenChange(false)
  }

  function handleSubmit(event: FormEvent) {
    event.preventDefault()
    void onSubmit()
  }

  return (
    <Dialog open={open} onOpenChange={requestClose}>
      <DialogContent>
        <DialogHeader>
          <DialogTitle>{title}</DialogTitle>
          {description ? <DialogDescription>{description}</DialogDescription> : null}
        </DialogHeader>

        <form onSubmit={handleSubmit} className="contents">
          {/* 内容区自己滚 —— 表单再长，下面的页脚也不会被挤出屏幕 */}
          <div className="min-h-0 flex-1 overflow-y-auto px-5 py-4">
            <div className="space-y-4">{children}</div>
          </div>

          <DialogFooter>
            <Button type="button" variant="outline" onClick={() => requestClose(false)} disabled={submitting}>
              取消
            </Button>
            <Button type="submit" disabled={submitting} variant={destructive ? 'destructive' : 'default'}>
              {submitting ? <Loader2 className="animate-spin" /> : null}
              {submitText}
            </Button>
          </DialogFooter>
        </form>
      </DialogContent>

      {/* 确认框套在 Dialog 里面：它比表单弹窗更靠上，ESC 先关它，再按一次才关表单 */}
      <ConfirmDialog state={discard} />
    </Dialog>
  )
}
