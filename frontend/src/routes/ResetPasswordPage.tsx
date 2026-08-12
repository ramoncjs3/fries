import { zodResolver } from '@hookform/resolvers/zod'
import { useMutation } from '@tanstack/react-query'
import { Loader2 } from 'lucide-react'
import { useForm } from 'react-hook-form'
import { Link, useNavigate, useSearchParams } from 'react-router'
import { z } from 'zod'

import { resetPassword } from '@/api/auth'
import { ApiError } from '@/api/client'
import { FormField } from '@/components/FormField'
import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'

const schema = z
  .object({
    newPassword: z.string().min(1, '请输入新密码'),
    confirm: z.string().min(1, '请再输入一次'),
  })
  .refine((v) => v.newPassword === v.confirm, {
    message: '两次输入的密码不一致',
    path: ['confirm'],
  })

type FormValues = z.infer<typeof schema>

export default function ResetPasswordPage() {
  const navigate = useNavigate()
  const [params] = useSearchParams()
  const token = params.get('token') ?? ''

  const form = useForm<FormValues>({
    resolver: zodResolver(schema),
    defaultValues: { newPassword: '', confirm: '' },
  })

  const mutation = useMutation({
    mutationFn: (values: FormValues) => resetPassword(token, values.newPassword),
    onSuccess: () => {
      void navigate('/login', { replace: true })
    },
  })

  // 具体的失败原因（token 失效、密码太弱）由后端给中文文案，前端不自己编。
  const error = mutation.error instanceof ApiError ? mutation.error : null

  return (
    <div className="grid min-h-svh place-items-center bg-linear-160 from-secondary via-background to-accent-subtle p-6">
      <div className="w-full max-w-100 space-y-7">
        <div className="space-y-2 text-center">
          <h1 className="text-2xl">设置新密码</h1>
          <p className="text-muted-foreground">设置完成后，你在其它设备上的登录都会失效</p>
        </div>

        <div className="surface space-y-5 p-7">
          {!token ? (
            <div className="space-y-4">
              <p className="rounded-md bg-destructive/10 px-3 py-2 text-destructive">
                链接不完整或已失效，请重新申请找回密码。
              </p>
              <Button asChild variant="outline" className="w-full">
                <Link to="/forgot-password">重新申请</Link>
              </Button>
            </div>
          ) : (
            <form
              className="space-y-4"
              onSubmit={form.handleSubmit((values) => {
                mutation.mutate(values)
              })}
            >
              <FormField label="新密码" required error={form.formState.errors.newPassword?.message}>
                <Input
                  autoFocus
                  type="password"
                  autoComplete="new-password"
                  aria-invalid={!!form.formState.errors.newPassword}
                  {...form.register('newPassword')}
                />
              </FormField>

              <FormField label="确认新密码" required error={form.formState.errors.confirm?.message}>
                <Input
                  type="password"
                  autoComplete="new-password"
                  aria-invalid={!!form.formState.errors.confirm}
                  {...form.register('confirm')}
                />
              </FormField>

              {error ? (
                <p className="rounded-md bg-destructive/10 px-3 py-2 text-destructive">{error.message}</p>
              ) : null}

              <Button type="submit" size="lg" className="mt-1 w-full" disabled={mutation.isPending}>
                {mutation.isPending ? <Loader2 className="animate-spin" /> : null}
                设置新密码
              </Button>
            </form>
          )}
        </div>
      </div>
    </div>
  )
}
