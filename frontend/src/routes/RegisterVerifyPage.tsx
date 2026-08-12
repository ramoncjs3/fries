import { useMutation } from '@tanstack/react-query'
import { Loader2 } from 'lucide-react'
import { Link, useSearchParams } from 'react-router'

import { verifyRegistration } from '@/api/auth'
import { ApiError } from '@/api/client'
import { Button } from '@/components/ui/button'

export default function RegisterVerifyPage() {
  const [params] = useSearchParams()
  const token = params.get('token') ?? ''

  const mutation = useMutation({
    mutationFn: () => verifyRegistration(token),
  })

  // 失败文案（链接失效、代码被占用）由后端给，前端不自己编。
  const error = mutation.error instanceof ApiError ? mutation.error : null

  return (
    <div className="grid min-h-svh place-items-center bg-linear-160 from-secondary via-background to-accent-subtle p-6">
      <div className="w-full max-w-100 space-y-7">
        <div className="space-y-2 text-center">
          <h1 className="text-2xl">完成注册</h1>
          <p className="text-muted-foreground">验证邮箱，创建你的组织</p>
        </div>

        <div className="surface space-y-5 p-7">
          {!token ? (
            <div className="space-y-4">
              <p className="rounded-md bg-destructive/10 px-3 py-2 text-destructive">
                链接不完整或已失效，请重新注册。
              </p>
              <Button asChild variant="outline" className="w-full">
                <Link to="/register">重新注册</Link>
              </Button>
            </div>
          ) : mutation.isSuccess ? (
            <div className="space-y-4">
              <p className="rounded-md bg-secondary px-3 py-2 text-sm">
                组织创建成功！用下面这套信息登录：
                <br />
                公司代码 <b>{mutation.data.tenant_code}</b> · 用户名{' '}
                <b>{mutation.data.admin_username}</b> · 你注册时设的密码
              </p>
              <Button asChild size="lg" className="w-full">
                <Link to="/login">去登录</Link>
              </Button>
            </div>
          ) : (
            <div className="space-y-4">
              <p className="text-sm text-muted-foreground">点下面的按钮完成邮箱验证、创建组织。</p>
              {error ? (
                <p className="rounded-md bg-destructive/10 px-3 py-2 text-destructive">{error.message}</p>
              ) : null}
              <Button
                size="lg"
                className="w-full"
                disabled={mutation.isPending}
                onClick={() => {
                  mutation.mutate()
                }}
              >
                {mutation.isPending ? <Loader2 className="animate-spin" /> : null}
                验证并创建组织
              </Button>
            </div>
          )}
        </div>
      </div>
    </div>
  )
}
