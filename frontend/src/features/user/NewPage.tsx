import { zodResolver } from '@hookform/resolvers/zod'
import { Loader2 } from 'lucide-react'
import { useState } from 'react'
import { Controller, useForm } from 'react-hook-form'
import { useNavigate } from 'react-router'

import { ApiError } from '@/api/client'
import { ConfirmDialog } from '@/components/ConfirmDialog'
import { DetailItem, DetailPage, DetailSection } from '@/components/DetailPage'
import { FormAlert } from '@/components/PageState'
import { SecretDialog } from '@/components/SecretDialog'
import { Button } from '@/components/ui/button'
import { Checkbox } from '@/components/ui/checkbox'
import { Input } from '@/components/ui/input'
import { SelectField } from '@/components/ui/select'
import { useDepartments } from '@/features/department/queries'
import { useRoles } from '@/features/role/queries'
import { useUserMutations } from '@/features/user/queries'
import { emptyUser, userSchema, type UserFormValues } from '@/features/user/schema'
import { NO_DEPARTMENT } from '@/features/user/types'
import { useUnsavedGuard } from '@/lib/unsaved-guard'

const LIST_PATH = '/users'

/** 表单元素的 id。标题栏里的「创建」靠它关联到表单，回车提交才成立。 */
const FORM_ID = 'user-new-form'

/**
 * 新增用户。**是一个页面，不是弹窗**（DECISIONS.md §7.6）。
 *
 * 和详情页的字段有意写成两份，没有抽公共组件：新建有「用户名」而编辑没有
 * （建后不可改）、新建没有「安全」那一组、提交后的去处也不一样。
 * 硬抽出来就得在里面塞一串 `mode === 'create'` 的分支，比两份各自读得懂更糟。
 */
export default function UserNewPage() {
  const navigate = useNavigate()
  const { create } = useUserMutations()
  const departments = useDepartments({})
  const roles = useRoles({ page: 1, pageSize: 100, status: 'active' })

  /** 建完拿到的一次性初始密码。关掉弹窗才走人 —— 这串东西只出现这一次。 */
  const [created, setCreated] = useState<{
    id: string
    username: string
    password: string
  } | null>(null)

  const form = useForm<UserFormValues>({
    resolver: zodResolver(userSchema),
    defaultValues: emptyUser,
  })

  // 已经建完了就别再拦 —— 那时表单还是「脏」的，但内容已经落库了
  const guard = useUnsavedGuard(form.formState.isDirty && created === null)
  const errors = form.formState.errors
  /** 整表失败（不是某个字段的问题）。新建没有版本冲突这一说，所以不用记错误码。 */
  const [failure, setFailure] = useState<string | null>(null)

  async function onSubmit(values: UserFormValues) {
    setFailure(null)
    try {
      const result = await create.mutateAsync({
        username: values.username.trim(),
        display_name: values.display_name.trim(),
        email: values.email.trim(),
        phone: values.phone.trim(),
        status: values.status,
        department_id: values.department_id || undefined,
        role_ids: values.role_ids,
      })
      setCreated({
        id: result.user.id,
        username: result.user.username,
        password: result.initial_password,
      })
    } catch (error) {
      // ⚠️ 非 ApiError 也必须落地，否则失败会被悄悄吞掉 —— 详见详情页里那段注释
      if (!(error instanceof ApiError)) {
        setFailure('创建失败了，请重试。一直不行的话把这一页刷新一下。')
        return
      }
      for (const item of error.formErrors()) {
        form.setError(item.field as keyof UserFormValues, { message: item.message })
      }
      if (error.formErrors().length === 0) setFailure(error.message)
    }
  }

  return (
    <>
      <DetailPage
        title="新增用户"
        description="初始密码由系统随机生成，创建后只显示一次"
        backTo={LIST_PATH}
        alert={failure ? <FormAlert message={failure} /> : null}
        actions={
          <span className="flex items-center gap-2">
            <Button variant="outline" onClick={() => void navigate(LIST_PATH)} disabled={create.isPending}>
              取消
            </Button>
            <Button type="submit" form={FORM_ID} disabled={create.isPending}>
              {create.isPending ? <Loader2 className="animate-spin" /> : null}
              创建
            </Button>
          </span>
        }
      >
        <form id={FORM_ID} className="space-y-8" onSubmit={(event) => void form.handleSubmit(onSubmit)(event)}>
          <DetailSection title="基本信息">
            <DetailItem label="用户名" required error={errors.username?.message} hint="登录用，建后不可改">
              <Input autoFocus placeholder="如：zhangsan、zhang.san" {...form.register('username')} />
            </DetailItem>

            <DetailItem label="显示名" required error={errors.display_name?.message}>
              <Input placeholder="真实姓名，如：张三" {...form.register('display_name')} />
            </DetailItem>

            <DetailItem label="状态" error={errors.status?.message}>
              <Controller
                control={form.control}
                name="status"
                render={({ field }) => (
                  <SelectField
                    ref={field.ref}
                    value={field.value}
                    onChange={field.onChange}
                    options={[
                      { value: 'active', label: '启用' },
                      { value: 'disabled', label: '停用（会立刻踢下线）' },
                    ]}
                  />
                )}
              />
            </DetailItem>

            <DetailItem label="角色" error={errors.role_ids?.message}>
              <Controller
                control={form.control}
                name="role_ids"
                render={({ field }) => {
                  const picked = new Set(field.value)
                  return (
                    <div className="space-y-2 rounded-lg border border-border p-3">
                      {/* 角色一多，光看勾了几个数不过来 */}
                      <p className="text-sm text-muted-foreground">已选 {picked.size} 个</p>
                      {roles.data?.items.length ? (
                        roles.data.items.map((role) => (
                          <label key={role.id} className="flex items-center gap-2">
                            <Checkbox
                              checked={picked.has(role.id)}
                              onCheckedChange={(next) => {
                                const current = new Set(field.value)
                                if (next === true) current.add(role.id)
                                else current.delete(role.id)
                                field.onChange([...current])
                              }}
                            />
                            <span>{role.name}</span>
                            <code className="text-muted-foreground">{role.key}</code>
                          </label>
                        ))
                      ) : (
                        <p className="text-muted-foreground">还没有可用角色，先去角色管理建一个</p>
                      )}
                    </div>
                  )
                }}
              />
            </DetailItem>

            <DetailItem label="部门" error={errors.department_id?.message}>
              <Controller
                control={form.control}
                name="department_id"
                render={({ field }) => (
                  <SelectField
                    ref={field.ref}
                    value={field.value || NO_DEPARTMENT}
                    onChange={(v) => field.onChange(v === NO_DEPARTMENT ? '' : v)}
                    options={[
                      { value: NO_DEPARTMENT, label: '（不属于任何部门）' },
                      ...(departments.data?.items ?? [])
                        .filter((d) => d.status === 'active')
                        .map((d) => ({
                          value: d.id,
                          label: d.name,
                          hint: d.code,
                        })),
                    ]}
                  />
                )}
              />
            </DetailItem>

            <DetailItem label="邮箱" error={errors.email?.message} hint="可空；填了就能用它登录">
              <Input type="email" placeholder="如：zhangsan@example.com" {...form.register('email')} />
            </DetailItem>

            <DetailItem label="手机号" error={errors.phone?.message} hint="可空；填了就能用它登录">
              <Input placeholder="11 位手机号" {...form.register('phone')} />
            </DetailItem>
          </DetailSection>
        </form>

      </DetailPage>

      {/* 关掉密码弹窗才跳转 —— 先跳的话这串密码就永远拿不到了 */}
      <SecretDialog
        open={created !== null}
        onOpenChange={(next) => {
          if (next || !created) return
          void navigate(`${LIST_PATH}/${created.id}`, { replace: true })
        }}
        title="用户已创建"
        description={`${created?.username} 的初始密码`}
        secret={created?.password ?? ''}
      />

      <ConfirmDialog state={guard} />
    </>
  )
}
