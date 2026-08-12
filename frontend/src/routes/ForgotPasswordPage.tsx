import { zodResolver } from '@hookform/resolvers/zod'
import { useMutation } from '@tanstack/react-query'
import { Loader2 } from 'lucide-react'
import { useForm } from 'react-hook-form'
import { Link } from 'react-router'
import { z } from 'zod'

import { requestPasswordReset } from '@/api/auth'
import { FormField } from '@/components/FormField'
import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'

const schema = z.object({
  tenantCode: z.string().min(2, '请输入公司代码'),
  identifier: z.string().min(1, '请输入账号'),
})

type FormValues = z.infer<typeof schema>

export default function ForgotPasswordPage() {
  const form = useForm<FormValues>({
    resolver: zodResolver(schema),
    defaultValues: {
      tenantCode: localStorage.getItem('fries.tenant_code') ?? '',
      identifier: '',
    },
  })

  const mutation = useMutation({
    mutationFn: (values: FormValues) => requestPasswordReset(values.tenantCode, values.identifier),
  })

  return (
    <div className="grid min-h-svh place-items-center bg-linear-160 from-secondary via-background to-accent-subtle p-6">
      <div className="w-full max-w-100 space-y-7">
        <div className="space-y-2 text-center">
          <h1 className="text-2xl">找回密码</h1>
          <p className="text-muted-foreground">我们会把重置链接发到你登记的邮箱</p>
        </div>

        <div className="surface space-y-5 p-7">
          {/* ⚠️ 成功后一律显示同一句话，不透露账号是否存在（和后端一致，防枚举）。 */}
          {mutation.isSuccess ? (
            <div className="space-y-4">
              <p className="rounded-md bg-secondary px-3 py-2 text-sm">
                如果这个账号存在，一封带重置链接的邮件已经发出，请查收（链接 30 分钟内有效）。
              </p>
              <Button asChild variant="outline" className="w-full">
                <Link to="/login">返回登录</Link>
              </Button>
            </div>
          ) : (
            <form
              className="space-y-4"
              onSubmit={form.handleSubmit((values) => {
                mutation.mutate(values)
              })}
            >
              <FormField label="公司代码" required error={form.formState.errors.tenantCode?.message}>
                <Input
                  autoFocus
                  autoComplete="organization"
                  placeholder="管理员给你的公司代码"
                  aria-invalid={!!form.formState.errors.tenantCode}
                  {...form.register('tenantCode')}
                />
              </FormField>

              <FormField label="账号" required error={form.formState.errors.identifier?.message}>
                <Input
                  autoComplete="username"
                  placeholder="用户名 / 邮箱 / 手机号"
                  aria-invalid={!!form.formState.errors.identifier}
                  {...form.register('identifier')}
                />
              </FormField>

              <Button type="submit" size="lg" className="mt-1 w-full" disabled={mutation.isPending}>
                {mutation.isPending ? <Loader2 className="animate-spin" /> : null}
                发送重置链接
              </Button>

              <p className="text-center text-sm">
                <Link to="/login" className="text-muted-foreground hover:text-foreground">
                  想起来了？返回登录
                </Link>
              </p>
            </form>
          )}
        </div>
      </div>
    </div>
  )
}
