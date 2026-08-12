import { zodResolver } from '@hookform/resolvers/zod'
import { useMutation, useQueryClient } from '@tanstack/react-query'
import { Loader2 } from 'lucide-react'
import { useForm } from 'react-hook-form'
import { Link, useNavigate } from 'react-router'
import { z } from 'zod'

import { login } from '@/api/auth'
import { ApiError } from '@/api/client'
import { FormField } from '@/components/FormField'
import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'
import { meQueryKey } from '@/lib/session'

/**
 * 记住上次填的公司代码。**它不是凭据**，只是「这是哪家公司」，
 * 所以放 localStorage 不违反红线 #10 —— token 和敏感数据才不许放。
 * 不记的话每个人每次登录都要重敲一遍自己公司的代码。
 */
const TENANT_CODE_KEY = 'fries.tenant_code'

const schema = z.object({
  tenantCode: z.string().min(2, '请输入公司代码'),
  account: z.string().min(1, '请输入账号'),
  password: z.string().min(1, '请输入密码'),
})

type FormValues = z.infer<typeof schema>

export default function LoginPage() {
  const navigate = useNavigate()
  const queryClient = useQueryClient()

  const form = useForm<FormValues>({
    resolver: zodResolver(schema),
    defaultValues: {
      tenantCode: localStorage.getItem(TENANT_CODE_KEY) ?? '',
      account: '',
      password: '',
    },
  })

  const mutation = useMutation({
    mutationFn: (values: FormValues) => login(values.tenantCode, values.account, values.password),
    onSuccess: async (result, values) => {
      localStorage.setItem(TENANT_CODE_KEY, values.tenantCode)

      // **整个缓存清掉，不只是 /me**（MULTI-TENANCY.md §11.5）。
      //
      // 两个理由，第二个是多租户带来的：
      //  1. 登录前那次 /me 是 401，缓存里还留着那个错误，不清的话下一个页面的
      //     闸门会拿着旧错误把人踢回登录页。
      //  2. ⚠️ 会话过期是**没有走退出登录**的 —— 401 只是跳回登录页，缓存原封不动。
      //     这时换一家公司的账号登进来，用户/部门列表会先渲染**上一家公司的缓存**
      //     再被后台重取替换（全局策略就是「先渲染旧数据、后台重取」）。
      //     那一瞬间是实打实的跨租户数据泄露，共用电脑时尤其要命。
      queryClient.clear()
      await queryClient.invalidateQueries({ queryKey: meQueryKey })

      // 首次登录 / 密码过期：直接去改密页，别让他在系统里到处撞 403
      void navigate(result.must_change_password ? '/change-password' : '/', { replace: true })
    },
  })

  const error = mutation.error instanceof ApiError ? mutation.error : null

  return (
    <div className="grid min-h-svh place-items-center bg-linear-160 from-secondary via-background to-accent-subtle p-6">
      <div className="w-full max-w-100 space-y-7">
        {/* 品牌在卡片外面、居中 —— 卡片里只放表单 */}
        <div className="space-y-2 text-center">
          <span className="mx-auto grid size-14 place-items-center rounded-2xl bg-primary text-2xl font-bold text-primary-foreground">
            F
          </span>
          <h1 className="text-2xl">fries</h1>
          <p className="text-muted-foreground">后台管理系统 · 请登录</p>
        </div>

        <div className="surface space-y-5 p-7">

        <form
          className="space-y-4"
          onSubmit={form.handleSubmit((values) => {
            mutation.mutate(values)
          })}
        >
          <FormField
            label="公司代码"
            required
            error={form.formState.errors.tenantCode?.message}
          >
            <Input
              autoFocus
              autoComplete="organization"
              placeholder="管理员给你的公司代码"
              aria-invalid={!!form.formState.errors.tenantCode}
              {...form.register('tenantCode')}
            />
          </FormField>

          <FormField label="账号" required error={form.formState.errors.account?.message}>
            <Input
              autoComplete="username"
              placeholder="用户名 / 邮箱 / 手机号"
              aria-invalid={!!form.formState.errors.account}
              {...form.register('account')}
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

          {/* 登录失败的原因由后端给中文文案，前端不自己编 */}
          {error ? (
            <p className="rounded-md bg-destructive/10 px-3 py-2 text-destructive">{error.message}</p>
          ) : null}

          <Button type="submit" size="lg" className="mt-1 w-full" disabled={mutation.isPending}>
            {mutation.isPending ? <Loader2 className="animate-spin" /> : null}
            登录
          </Button>

          <p className="flex justify-center gap-4 text-sm text-muted-foreground">
            <Link to="/forgot-password" className="hover:text-foreground">
              忘记密码？
            </Link>
            {/* 是否真能注册由后端平台开关决定；关着时注册页提交会给出「未开放」提示。 */}
            <Link to="/register" className="hover:text-foreground">
              注册组织
            </Link>
          </p>
        </form>
        </div>

        <p className="text-center text-xs text-muted-foreground">fries · 内部管理系统</p>
      </div>
    </div>
  )
}
