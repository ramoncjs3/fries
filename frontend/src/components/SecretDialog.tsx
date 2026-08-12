import { Check, Copy, TriangleAlert } from 'lucide-react'
import { useState } from 'react'

import { Button } from '@/components/ui/button'
import { Dialog, DialogContent, DialogDescription, DialogFooter, DialogHeader, DialogTitle } from '@/components/ui/dialog'

/**
 * 展示「只出现一次」的凭据：初始密码、重置后的临时密码、API Key。
 *
 * **不能用 toast** —— 一闪就没了，而这个值再也查不回来（库里存的是哈希）。
 * 所以是一个必须手动关掉的弹窗，带复制按钮和一句明确的警告。
 */
export function SecretDialog({
  open,
  onOpenChange,
  title,
  description,
  secret,
}: {
  open: boolean
  onOpenChange: (open: boolean) => void
  title: string
  description?: string
  secret: string
}) {
  const [copied, setCopied] = useState(false)

  async function copy() {
    try {
      await navigator.clipboard.writeText(secret)
      setCopied(true)
      window.setTimeout(() => setCopied(false), 2000)
    } catch {
      // 剪贴板可能被浏览器策略挡掉（非 https、没授权）。
      // 不用报错打断：值就明晃晃显示在那儿，手动选中也能复制。
    }
  }

  return (
    <Dialog open={open} onOpenChange={onOpenChange}>
      <DialogContent className="max-w-md">
        <DialogHeader>
          <DialogTitle>{title}</DialogTitle>
          {description ? <DialogDescription>{description}</DialogDescription> : null}
        </DialogHeader>

        <div className="space-y-3 px-5 py-4">
          <div className="flex items-center gap-2 rounded-lg border border-border bg-muted px-3 py-2.5">
            <code className="min-w-0 flex-1 select-all break-all font-mono">{secret}</code>
            <Button variant="outline" size="sm" onClick={() => void copy()}>
              {copied ? <Check /> : <Copy />}
              {copied ? '已复制' : '复制'}
            </Button>
          </div>
          <p className="flex items-start gap-1.5 text-warning">
            <TriangleAlert className="mt-0.5 size-4 shrink-0" />
            <span>关掉这个窗口之后就再也看不到了。请立刻转交本人，并让 TA 首次登录时修改。</span>
          </p>
        </div>

        <DialogFooter>
          <Button onClick={() => onOpenChange(false)}>我已保存</Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  )
}
