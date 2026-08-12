import { zodResolver } from '@hookform/resolvers/zod'
import { Loader2 } from 'lucide-react'
import { useState } from 'react'
import { Controller, useForm } from 'react-hook-form'
import { useNavigate } from 'react-router'

import { ApiError } from '@/api/client'
import { ConfirmDialog } from '@/components/ConfirmDialog'
import { DetailBlock, DetailItem, DetailPage, DetailSection } from '@/components/DetailPage'
import { FormAlert } from '@/components/PageState'
import { Button } from '@/components/ui/button'
import { Checkbox } from '@/components/ui/checkbox'
import { Input } from '@/components/ui/input'
import { SelectField } from '@/components/ui/select'
import { usePermissionCatalog, useRoleMutations } from '@/features/role/queries'
import { emptyRole, roleSchema, type RoleFormValues } from '@/features/role/schema'
import { dataScopeLabels } from '@/features/role/types'
import { useUnsavedGuard } from '@/lib/unsaved-guard'

const LIST_PATH = '/roles'
const FORM_ID = 'role-new-form'

/** 新增角色。是一个页面，不是弹窗（DECISIONS.md §7.6）。 */
export default function RoleNewPage() {
  const navigate = useNavigate()
  const { create } = useRoleMutations()
  const catalog = usePermissionCatalog(true)

  const form = useForm<RoleFormValues>({
    resolver: zodResolver(roleSchema),
    defaultValues: emptyRole,
  })

  const [failure, setFailure] = useState<string | null>(null)
  const guard = useUnsavedGuard(form.formState.isDirty)
  const errors = form.formState.errors

  async function onSubmit(values: RoleFormValues) {
    setFailure(null)
    try {
      const created = await create.mutateAsync({
        key: values.key.trim(),
        name: values.name.trim(),
        description: values.description,
        data_scope: values.data_scope,
        status: values.status,
        permissions: values.permissions,
      })
      // 建完直接进它的详情页。replace 是为了让「后退」不会回到一个已经提交过的空表单
      void navigate(`${LIST_PATH}/${created.id}`, { replace: true })
    } catch (error) {
      if (!(error instanceof ApiError)) {
        setFailure('创建失败了，请重试。一直不行的话把这一页刷新一下。')
        return
      }
      for (const item of error.formErrors()) {
        form.setError(item.field as keyof RoleFormValues, { message: item.message })
      }
      if (error.formErrors().length === 0) setFailure(error.message)
    }
  }

  return (
    <>
      <DetailPage
        title="新增角色"
        description="角色标识建后不可改 —— 它是权限策略里的身份"
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
            <DetailItem
              label="角色标识"
              required
              error={errors.key?.message}
              hint="权限策略里的身份，建后不可改"
            >
              <Input autoFocus placeholder="如：viewer、finance_admin" {...form.register('key')} />
            </DetailItem>

            <DetailItem label="角色名称" required error={errors.name?.message}>
              <Input placeholder="如：只读、财务管理员" {...form.register('name')} />
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
                      { value: 'disabled', label: '停用' },
                    ]}
                  />
                )}
              />
            </DetailItem>

            <DetailItem
              label="数据范围"
              error={errors.data_scope?.message}
              hint="一个人有多个角色时取最宽的那个"
            >
              <Controller
                control={form.control}
                name="data_scope"
                render={({ field }) => (
                  <SelectField
                    ref={field.ref}
                    value={field.value}
                    onChange={field.onChange}
                    options={Object.entries(dataScopeLabels).map(([value, label]) => ({ value, label }))}
                  />
                )}
              />
            </DetailItem>

            <DetailItem label="说明" error={errors.description?.message}>
              <Input placeholder="选填，这个角色是给谁用的" {...form.register('description')} />
            </DetailItem>
          </DetailSection>

          <DetailBlock title="权限">
            <Controller
              control={form.control}
              name="permissions"
              render={({ field }) => {
                const selected = new Set(field.value)
                const togglePoints = (points: string[], next: boolean) => {
                  const current = new Set(field.value)
                  points.forEach((p) => (next ? current.add(p) : current.delete(p)))
                  field.onChange([...current])
                }

                return (
                  <div className="space-y-2">
                    <p className="text-sm text-muted-foreground">已选 {selected.size} 项</p>
                    <div className="divide-y divide-border-subtle rounded-lg border border-border">
                      {(catalog.data ?? []).map((module) => {
                        const points = module.actions.map((a) => a.point)
                        const checkedCount = points.filter((p) => selected.has(p)).length
                        const state =
                          checkedCount === 0 ? false : checkedCount === points.length ? true : 'indeterminate'
                        return (
                          <div key={module.key} className="space-y-2 p-3">
                            <label className="flex items-center gap-2">
                              <Checkbox
                                checked={state}
                                onCheckedChange={(next) => togglePoints(points, next === true)}
                              />
                              <span className="font-medium">{module.name}</span>
                              <code className="text-muted-foreground">{module.key}</code>
                            </label>
                            <div className="flex flex-wrap gap-x-5 gap-y-2 pl-6">
                              {module.actions.map((action) => (
                                <label key={action.point} className="flex items-center gap-2">
                                  <Checkbox
                                    checked={selected.has(action.point)}
                                    onCheckedChange={(next) => togglePoints([action.point], next === true)}
                                  />
                                  <span>{action.name}</span>
                                </label>
                              ))}
                            </div>
                          </div>
                        )
                      })}
                    </div>
                  </div>
                )
              }}
            />
          </DetailBlock>
        </form>
      </DetailPage>

      <ConfirmDialog state={guard} />
    </>
  )
}
