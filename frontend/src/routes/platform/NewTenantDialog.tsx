import { zodResolver } from '@hookform/resolvers/zod'
import { useForm } from 'react-hook-form'
import { z } from 'zod'

import { FormDialog } from '@/components/FormDialog'
import { FormField } from '@/components/FormField'
import { Input } from '@/components/ui/input'

/**
 * 开通组织。用 <FormDialog> 而不是独立页面：只有两个字段，而且开完立刻要看那串
 * 一次性凭据，跳页会打断这个动作 —— 正是 DECISIONS.md §7.6 说的
 * 「FormDialog 只给一次动作用」的场景。
 *
 * 公司代码的规则和后端、和数据库的 CHECK 约束**三处必须一致**（MULTI-TENANCY.md §9.1）。
 * 这里校验一遍是为了当场给人话，后端那两道是兜底。
 */

const schema = z.object({
  name: z.string().min(1, '请输入组织名').max(64),
  code: z
    .string()
    .min(2, '至少 2 个字符')
    .max(32, '最多 32 个字符')
    .regex(/^[a-z0-9][a-z0-9-]*[a-z0-9]$/, '只能用小写字母、数字和中划线，首尾不能是中划线'),
})

type FormValues = z.infer<typeof schema>

export function NewTenantDialog({
  open,
  onOpenChange,
  pending,
  onSubmit,
}: {
  open: boolean
  onOpenChange: (open: boolean) => void
  pending: boolean
  onSubmit: (values: FormValues) => void
}) {
  const form = useForm<FormValues>({
    resolver: zodResolver(schema),
    defaultValues: { name: '', code: '' },
  })

  return (
    <FormDialog
      open={open}
      onOpenChange={(next) => {
        if (!next) form.reset()
        onOpenChange(next)
      }}
      title="开通组织"
      description="会自动建好这个组织的第一个管理员，并给出只显示一次的初始密码。"
      submitText="开通"
      submitting={pending}
      dirty={form.formState.isDirty}
      onSubmit={form.handleSubmit((values) => {
        onSubmit(values)
      })}
    >
      <FormField label="组织名" required error={form.formState.errors.name?.message}>
        <Input autoFocus placeholder="如：某某科技有限公司" {...form.register('name')} />
      </FormField>

      <FormField
        label="公司代码"
        required
        error={form.formState.errors.code?.message}
        hint="客户登录时要填这个。建后不能改，选一个短好记的"
      >
        <Input placeholder="如：acme" {...form.register('code')} />
      </FormField>
    </FormDialog>
  )
}
