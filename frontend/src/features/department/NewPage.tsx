import { zodResolver } from '@hookform/resolvers/zod'
import { Loader2 } from 'lucide-react'
import { useState } from 'react'
import { Controller, useForm } from 'react-hook-form'

import { ApiError } from '@/api/client'
import { ConfirmDialog } from '@/components/ConfirmDialog'
import { DetailItem, DetailSection } from '@/components/DetailPage'
import { FormAlert } from '@/components/PageState'
import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'
import { SelectField } from '@/components/ui/select'
import { useDepartmentMutations } from '@/features/department/queries'
import { emptyDepartment, departmentSchema, type DepartmentFormValues } from '@/features/department/schema'
import { ROOT_VALUE, type Department } from '@/features/department/types'
import { useUnsavedGuard } from '@/lib/unsaved-guard'

const FORM_ID = 'department-new-form'

/**
 * 新增部门。**和详情一样，是部门页右半边的面板，不是独立路由** ——
 * 理由见 `DetailPage.tsx` 顶部那段：左边那棵树是它的上下文，跳走整页就没了。
 *
 * 新建时上级默认取当前选中的节点，所以最常见的「在这个部门下面加一个子部门」
 * 一步就完成，不用再去下拉里找。
 */
export function DepartmentNewPane({
  parentID,
  all,
  onCreated,
  onCancel,
}: {
  /** 默认上级。空串表示建一个一级部门。 */
  parentID: string
  all: Department[]
  onCreated: (id: string) => void
  onCancel: () => void
}) {
  const { create } = useDepartmentMutations()
  const [failure, setFailure] = useState<string | null>(null)

  const form = useForm<DepartmentFormValues>({
    resolver: zodResolver(departmentSchema),
    defaultValues: { ...emptyDepartment, parent_id: parentID },
  })

  // 盯住 detail：新建到一半点了左边的树，也该先问一句
  const guard = useUnsavedGuard(form.formState.isDirty, ['detail'])
  const errors = form.formState.errors
  const parentName = all.find((item) => item.id === parentID)?.name

  async function onSubmit(values: DepartmentFormValues) {
    setFailure(null)
    try {
      const created = await create.mutateAsync({
        parent_id: values.parent_id || undefined,
        name: values.name.trim(),
        code: values.code.trim(),
        sort_order: values.sort_order,
        remark: values.remark,
        status: values.status,
      })
      onCreated(created.id)
    } catch (error) {
      if (!(error instanceof ApiError)) {
        setFailure('创建失败了，请重试。一直不行的话把这一页刷新一下。')
        return
      }
      for (const item of error.formErrors()) {
        form.setError(item.field as keyof DepartmentFormValues, { message: item.message })
      }
      if (error.formErrors().length === 0) setFailure(error.message)
    }
  }

  return (
    <div className="flex min-h-0 flex-1 flex-col">
      <div className="flex shrink-0 items-start justify-between gap-3 border-b border-border-subtle px-6 py-4">
        <div className="min-w-0 space-y-0.5">
          <h2 className="truncate text-xl">新增部门</h2>
          <p className="truncate text-muted-foreground">
            {parentName ? `建在「${parentName}」下面` : '建一个一级部门'}
          </p>
        </div>
        <div className="flex shrink-0 items-center gap-2">
          <Button variant="outline" onClick={onCancel} disabled={create.isPending}>
            取消
          </Button>
          <Button type="submit" form={FORM_ID} disabled={create.isPending}>
            {create.isPending ? <Loader2 className="animate-spin" /> : null}
            创建
          </Button>
        </div>
      </div>

      <div className="min-h-0 flex-1 space-y-8 overflow-y-auto px-6 py-5">
        {failure ? <FormAlert message={failure} /> : null}

        <form id={FORM_ID} className="space-y-8" onSubmit={(event) => void form.handleSubmit(onSubmit)(event)}>
          <DetailSection title="基本信息">
            <DetailItem label="部门名称" required error={errors.name?.message}>
              <Input autoFocus placeholder="如：技术部、财务部" {...form.register('name')} />
            </DetailItem>

            <DetailItem
              label="部门编号"
              required
              error={errors.code?.message}
              hint="给工资系统、OA 这类外部系统对账用。建后尽量别改"
            >
              <Input placeholder="如：TECH、TECH-BE、FIN" {...form.register('code')} />
            </DetailItem>

            <DetailItem label="上级部门" error={errors.parent_id?.message}>
              <Controller
                control={form.control}
                name="parent_id"
                render={({ field }) => (
                  <SelectField
                    ref={field.ref}
                    value={field.value || ROOT_VALUE}
                    onChange={(value) => field.onChange(value === ROOT_VALUE ? '' : value)}
                    options={[
                      { value: ROOT_VALUE, label: '（作为一级部门）' },
                      ...all.map((item) => ({ value: item.id, label: item.name, hint: item.code })),
                    ]}
                  />
                )}
              />
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

            <DetailItem label="排序" error={errors.sort_order?.message} hint="同级之间小的在前">
              <Input type="number" min={0} max={9999} {...form.register('sort_order')} />
            </DetailItem>

            <DetailItem label="备注" error={errors.remark?.message}>
              <Input placeholder="选填，比如这个部门是做什么的" {...form.register('remark')} />
            </DetailItem>
          </DetailSection>
        </form>
      </div>

      <ConfirmDialog state={guard} />
    </div>
  )
}
