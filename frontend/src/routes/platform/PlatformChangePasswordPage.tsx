import { zodResolver } from '@hookform/resolvers/zod'
import { useMutation, useQueryClient } from '@tanstack/react-query'
import { Loader2 } from 'lucide-react'
import { useForm } from 'react-hook-form'
import { useNavigate } from 'react-router'
import { toast } from 'sonner'
import { z } from 'zod'

import { changePlatformPassword } from '@/api/auth'
import { ApiError } from '@/api/client'
import { FormField } from '@/components/FormField'
import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'
import { platformMeQueryKey } from '@/lib/platform-session'

// 平台管理员改密。和租户端那份长得一样，但走的是**平台自己的接口**：
// 平台的密码要求写死在后端且比租户严（MULTI-TENANCY.md §9.2），
// 不吃租户级的密码策略。
//
// 只做「两次输入一致」这种纯前端能判的校验 —— 强度要求后端说了算，
// 前端复制一份就一定会和后端不一致。
const schema = z
  .object({
    old_password: z.string().min(1, '请输入原密码'),
    new_password: z.string().min(1, '请输入新密码'),
    confirm: z.string().min(1, '请再输入一次新密码'),
  })
  .refine((v) => v.new_password === v.confirm, {
    path: ['confirm'],
    message: '两次输入的新密码不一致',
  })

type FormValues = z.infer<typeof schema>

export default function PlatformChangePasswordPage() {
  const navigate = useNavigate()
  const queryClient = useQueryClient()

  const form = useForm<FormValues>({
    resolver: zodResolver(schema),
    defaultValues: { old_password: '', new_password: '', confirm: '' },
  })

  const mutation = useMutation({
    mutationFn: (values: FormValues) => changePlatformPassword(values.old_password, values.new_password),
    onSuccess: async () => {
      toast.success('密码已修改')
      await queryClient.invalidateQueries({ queryKey: platformMeQueryKey })
      void navigate('/platform/tenants', { replace: true })
    },
    onError: (error) => {
      // 后端的字段级错误直接映射到表单字段上（DECISIONS.md §4.6）
      if (error instanceof ApiError) {
        for (const item of error.formErrors()) {
          form.setError(item.field as keyof FormValues, { message: item.message })
        }
      }
    },
  })

  const error = mutation.error instanceof ApiError ? mutation.error : null
  const generalError = error && error.formErrors().length === 0 ? error.message : null

  return (
    <div className="grid min-h-svh place-items-center bg-linear-160 from-secondary via-background to-accent-subtle p-6">
      <div className="surface w-full max-w-100 space-y-5 p-7">
        <div className="space-y-1">
          <h1 className="text-xl">修改密码</h1>
          <p className="text-muted-foreground">首次登录或密码过期时必须先改密码</p>
        </div>

        <form
          className="space-y-4"
          onSubmit={form.handleSubmit((values) => {
            mutation.mutate(values)
          })}
        >
          <FormField label="原密码" required error={form.formState.errors.old_password?.message}>
            <Input type="password" autoComplete="current-password" {...form.register('old_password')} />
          </FormField>

          <FormField label="新密码" required error={form.formState.errors.new_password?.message}>
            <Input type="password" autoComplete="new-password" {...form.register('new_password')} />
          </FormField>

          <FormField label="确认新密码" required error={form.formState.errors.confirm?.message}>
            <Input type="password" autoComplete="new-password" {...form.register('confirm')} />
          </FormField>

          {generalError ? (
            <p className="rounded-md bg-destructive/10 px-3 py-2 text-destructive">{generalError}</p>
          ) : null}

          <Button type="submit" size="lg" className="mt-1 w-full" disabled={mutation.isPending}>
            {mutation.isPending ? <Loader2 className="animate-spin" /> : null}
            保存
          </Button>
        </form>
      </div>
    </div>
  )
}
