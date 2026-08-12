import { zodResolver } from '@hookform/resolvers/zod'
import { useMutation } from '@tanstack/react-query'
import { Loader2 } from 'lucide-react'
import { useForm } from 'react-hook-form'
import { Link } from 'react-router'
import { z } from 'zod'

import { register as registerOrg } from '@/api/auth'
import { ApiError } from '@/api/client'
import { FormField } from '@/components/FormField'
import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'

const schema = z
  .object({
    companyName: z.string().min(1, '请输入组织名'),
    code: z
      .string()
      .min(2, '公司代码至少 2 位')
      .max(32, '公司代码最多 32 位')
      .regex(/^[a-z0-9][a-z0-9-]*[a-z0-9]$/, '只能用小写字母、数字和中划线，首尾不能是中划线'),
    email: z.string().email('请输入有效邮箱'),
    password: z.string().min(1, '请输入密码'),
    confirm: z.string().min(1, '请再输入一次'),
  })
  .refine((v) => v.password === v.confirm, { message: '两次输入的密码不一致', path: ['confirm'] })

type FormValues = z.infer<typeof schema>

export default function RegisterPage() {
  const form = useForm<FormValues>({
    resolver: zodResolver(schema),
    defaultValues: { companyName: '', code: '', email: '', password: '', confirm: '' },
  })

  const mutation = useMutation({
    mutationFn: (v: FormValues) => registerOrg(v.email, v.companyName, v.code, v.password),
  })

  // 失败文案（自助注册未开放、代码非法等）由后端给，前端不自己编。
  const error = mutation.error instanceof ApiError ? mutation.error : null

  return (
    <div className="grid min-h-svh place-items-center bg-linear-160 from-secondary via-background to-accent-subtle p-6">
      <div className="w-full max-w-100 space-y-7">
        <div className="space-y-2 text-center">
          <h1 className="text-2xl">注册组织</h1>
          <p className="text-muted-foreground">创建一个新组织，你就是它的管理员</p>
        </div>

        <div className="surface space-y-5 p-7">
          {mutation.isSuccess ? (
            <div className="space-y-4">
              <p className="rounded-md bg-secondary px-3 py-2 text-sm">
                验证邮件已发到你的邮箱，点里面的链接完成注册（24 小时内有效）。
              </p>
              <Button asChild variant="outline" className="w-full">
                <Link to="/login">返回登录</Link>
              </Button>
            </div>
          ) : (
            <form
              className="space-y-4"
              onSubmit={form.handleSubmit((v) => {
                mutation.mutate(v)
              })}
            >
              <FormField label="组织名" required error={form.formState.errors.companyName?.message}>
                <Input
                  autoFocus
                  placeholder="你的公司 / 团队名"
                  aria-invalid={!!form.formState.errors.companyName}
                  {...form.register('companyName')}
                />
              </FormField>

              <FormField label="公司代码" required error={form.formState.errors.code?.message}>
                <Input
                  autoComplete="off"
                  placeholder="登录时要填，小写字母/数字/中划线"
                  aria-invalid={!!form.formState.errors.code}
                  {...form.register('code')}
                />
              </FormField>

              <FormField label="管理员邮箱" required error={form.formState.errors.email?.message}>
                <Input
                  type="email"
                  autoComplete="email"
                  aria-invalid={!!form.formState.errors.email}
                  {...form.register('email')}
                />
              </FormField>

              <FormField label="密码" required error={form.formState.errors.password?.message}>
                <Input
                  type="password"
                  autoComplete="new-password"
                  aria-invalid={!!form.formState.errors.password}
                  {...form.register('password')}
                />
              </FormField>

              <FormField label="确认密码" required error={form.formState.errors.confirm?.message}>
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
                注册
              </Button>

              <p className="text-center text-sm">
                <Link to="/login" className="text-muted-foreground hover:text-foreground">
                  已有账号？返回登录
                </Link>
              </p>
            </form>
          )}
        </div>
      </div>
    </div>
  )
}
