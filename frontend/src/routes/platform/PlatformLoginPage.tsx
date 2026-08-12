import { zodResolver } from '@hookform/resolvers/zod'
import { useMutation, useQueryClient } from '@tanstack/react-query'
import { Loader2 } from 'lucide-react'
import { useForm } from 'react-hook-form'
import { useNavigate } from 'react-router'
import { z } from 'zod'

import { platformLogin } from '@/api/auth'
import { ApiError } from '@/api/client'
import { FormField } from '@/components/FormField'
import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'
import { platformMeQueryKey } from '@/lib/platform-session'

/**
 * 平台管理端登录。
 *
 * **没有公司代码那一格** —— 平台管理员不属于任何组织（MULTI-TENANCY.md §2.3）。
 * 外观也刻意和租户端不一样（深色 + 明确写着「平台管理端」）：
 * 这是最高权限的入口，走错门要一眼看得出来。
 */

const schema = z.object({
  username: z.string().min(1, '请输入账号'),
  password: z.string().min(1, '请输入密码'),
})

type FormValues = z.infer<typeof schema>

export default function PlatformLoginPage() {
  const navigate = useNavigate()
  const queryClient = useQueryClient()

  const form = useForm<FormValues>({
    resolver: zodResolver(schema),
    defaultValues: { username: '', password: '' },
  })

  const mutation = useMutation({
    mutationFn: (values: FormValues) => platformLogin(values.username, values.password),
    onSuccess: async (result) => {
      // 理由同租户端登录：换身份时整个缓存都要清掉，不然会先渲染上一个人的数据
      queryClient.clear()
      await queryClient.invalidateQueries({ queryKey: platformMeQueryKey })
      void navigate(result.must_change_password ? '/platform/change-password' : '/platform/tenants', {
        replace: true,
      })
    },
  })

  const error = mutation.error instanceof ApiError ? mutation.error : null

  return (
    <div className="grid min-h-svh place-items-center bg-linear-160 from-slate-900 via-slate-800 to-slate-900 p-6">
      <div className="w-full max-w-100 space-y-7">
        <div className="space-y-2 text-center">
          <span className="mx-auto grid size-14 place-items-center rounded-2xl bg-white/10 text-2xl font-bold text-white">
            F
          </span>
          <h1 className="text-2xl text-white">fries 平台管理端</h1>
          <p className="text-white/60">开通和管理客户组织</p>
        </div>

        <div className="surface space-y-5 p-7">
          <form
            className="space-y-4"
            onSubmit={form.handleSubmit((values) => {
              mutation.mutate(values)
            })}
          >
            <FormField label="账号" required error={form.formState.errors.username?.message}>
              <Input
                autoFocus
                autoComplete="username"
                aria-invalid={!!form.formState.errors.username}
                {...form.register('username')}
              />
            </FormField>

            <FormField label="密码" required error={form.formState.errors.password?.message}>
              <Input
                type="password"
                autoComplete="current-password"
                aria-invalid={!!form.formState.errors.password}
                {...form.register('password')}
              />
            </FormField>

            {error ? (
              <p className="rounded-md bg-destructive/10 px-3 py-2 text-destructive">{error.message}</p>
            ) : null}

            <Button type="submit" size="lg" className="mt-1 w-full" disabled={mutation.isPending}>
              {mutation.isPending ? <Loader2 className="animate-spin" /> : null}
              登录
            </Button>
          </form>
        </div>

        <p className="text-center text-xs text-white/40">这是平台管理入口，不是客户使用的后台</p>
      </div>
    </div>
  )
}
