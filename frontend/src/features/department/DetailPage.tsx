import { zodResolver } from '@hookform/resolvers/zod'
import { Loader2, Pencil, Plus, Trash2 } from 'lucide-react'
import { useState, type ReactNode } from 'react'
import { Controller, useForm } from 'react-hook-form'

import { ApiError } from '@/api/client'
import { ConfirmDialog } from '@/components/ConfirmDialog'
import { DetailItem, DetailSection } from '@/components/DetailPage'
import { FormAlert } from '@/components/PageState'
import { RowActions } from '@/components/RowActions'
import { StatusDot } from '@/components/ui/badge'
import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'
import { SelectField } from '@/components/ui/select'
import { useDepartmentMutations } from '@/features/department/queries'
import { departmentSchema, type DepartmentFormValues } from '@/features/department/schema'
import { ROOT_VALUE, subtreeIDs, type Department } from '@/features/department/types'
import { useConfirm } from '@/lib/confirm'
import { useSession } from '@/lib/session'
import { useUnsavedGuard } from '@/lib/unsaved-guard'

const FORM_ID = 'department-detail-form'

/**
 * 部门详情。**它不是一条独立路由**，而是部门页右半边的那个面板。
 *
 * 别的模块（用户、角色）的详情是 `/xxx/:id` 整页；部门不是，因为左边那棵树就是它的
 * 导航 —— 点一个节点看它的详情，跳走整页反而把树丢了。所以这里是「左树右详情」，
 * 选中的是哪个节点存在 URL 上（`?detail=<id>`）。
 *
 * 但**编辑仍然是就地改，不弹窗**，和全站一致（DECISIONS.md §7.6）：
 * 点「编辑」这个面板变成表单，保存/取消在面板标题栏里。
 */
export function DepartmentDetailPane({
  department,
  all,
  editing,
  onEditingChange,
  onAddChild,
  onDeleted,
  children,
}: {
  department: Department
  /** 全部部门，用来渲染「上级」下拉。 */
  all: Department[]
  editing: boolean
  onEditingChange: (next: boolean) => void
  onAddChild: () => void
  onDeleted: () => void
  /** 成员列表那一段。只在只读态显示 —— 改部门信息时它只是干扰。 */
  children: ReactNode
}) {
  const { can } = useSession()
  const confirm = useConfirm()
  const { remove, update } = useDepartmentMutations()

  const form = useForm<DepartmentFormValues>({
    resolver: zodResolver(departmentSchema),
    defaultValues: {
      parent_id: department.parent_id ?? '',
      name: department.name,
      code: department.code,
      sort_order: department.sort_order,
      remark: department.remark,
      status: department.status as DepartmentFormValues['status'],
    },
  })

  const dirty = editing && form.formState.isDirty
  // 盯住 detail：部门页换一个树节点只改查询串，不盯就会静默丢掉正在改的内容
  const guard = useUnsavedGuard(dirty, ['detail'])
  const [failure, setFailure] = useState<{ message: string; conflict: boolean } | null>(null)
  const errors = editing ? form.formState.errors : {}

  /**
   * 上级候选：要排掉**自己和自己的所有后代**。
   * 不排的话就能把部门挂到自己的子孙下面，造出一个从根断开的环 ——
   * 后端也拦（`department.cycle`），但让它出现在下拉里本身就是错的。
   */
  const excluded = subtreeIDs(all, department.id)
  const parentOptions = all.filter((item) => !excluded.has(item.id))
  const parentName = all.find((item) => item.id === department.parent_id)?.name

  async function onSubmit(values: DepartmentFormValues) {
    setFailure(null)
    try {
      await update.mutateAsync({
        id: department.id,
        version: department.version,
        input: {
          parent_id: values.parent_id || undefined,
          name: values.name.trim(),
          code: values.code.trim(),
          sort_order: values.sort_order,
          remark: values.remark,
          status: values.status,
        },
      })
      onEditingChange(false)
    } catch (error) {
      if (!(error instanceof ApiError)) {
        setFailure({ message: '保存失败了，请重试。一直不行的话把这一页刷新一下。', conflict: false })
        return
      }
      for (const item of error.formErrors()) {
        form.setError(item.field as keyof DepartmentFormValues, { message: item.message })
      }
      if (error.formErrors().length === 0) {
        setFailure({ message: error.message, conflict: error.code === 'common.version_conflict' })
      }
    }
  }

  function cancelEdit() {
    if (!dirty) {
      onEditingChange(false)
      return
    }
    confirm.open({
      title: '放弃未保存的修改？',
      description: '取消之后这次改的内容不会保留。',
      confirmText: '放弃',
      destructive: true,
      onConfirm: () => onEditingChange(false),
    })
  }

  return (
    <div className="flex min-h-0 flex-1 flex-col">
      {/* 面板标题行。**不跟着内容滚** —— 和详情页的标题栏是同一条理由（§7.6） */}
      <div className="flex shrink-0 items-start justify-between gap-3 border-b border-border-subtle px-6 py-4">
        <div className="min-w-0 space-y-0.5">
          <h2 className="truncate text-xl">{department.name}</h2>
          <p className="flex items-center gap-2 truncate">
            <code className="text-muted-foreground">{department.code}</code>
            {department.status === 'active' ? (
              <StatusDot tone="success">启用</StatusDot>
            ) : (
              <StatusDot tone="neutral">停用</StatusDot>
            )}
          </p>
        </div>

        <div className="flex shrink-0 items-center gap-1">
          {editing ? (
            <>
              <Button variant="outline" onClick={cancelEdit} disabled={update.isPending}>
                取消
              </Button>
              <Button type="submit" form={FORM_ID} disabled={update.isPending}>
                {update.isPending ? <Loader2 className="animate-spin" /> : null}
                保存
              </Button>
            </>
          ) : (
            <>
              {can('department', 'update') ? (
                <Button variant="outline" onClick={() => onEditingChange(true)}>
                  <Pencil /> 编辑
                </Button>
              ) : null}
              <RowActions
                actions={[
                  ...(can('department', 'create')
                    ? [{ key: 'add', label: '新增子部门', icon: <Plus />, onSelect: onAddChild }]
                    : []),
                  ...(can('department', 'delete')
                    ? [
                        {
                          key: 'delete',
                          label: '删除',
                          icon: <Trash2 />,
                          danger: true,
                          // 前端先挡一道，提示更具体；后端仍然会再判一次
                          disabled: department.user_count > 0,
                          disabledReason: '先把这个部门的成员转走',
                          onSelect: () =>
                            confirm.open({
                              title: `删除「${department.name}」？`,
                              description: '删除后不可恢复。该部门的历史记录仍会保留在审计日志里。',
                              confirmText: '删除',
                              destructive: true,
                              onConfirm: async () => {
                                await remove.mutateAsync({
                                  id: department.id,
                                  version: department.version,
                                })
                                onDeleted()
                              },
                            }),
                        },
                      ]
                    : []),
                ]}
              />
            </>
          )}
        </div>
      </div>

      <div className="min-h-0 flex-1 space-y-8 overflow-y-auto px-6 py-5">
        {failure ? <FormAlert message={failure.message} /> : null}

        <form id={FORM_ID} className="space-y-8" onSubmit={(event) => void form.handleSubmit(onSubmit)(event)}>
          <DetailSection title="基本信息">
            <DetailItem label="部门名称" required={editing} error={errors.name?.message}>
              {editing ? (
                <Input autoFocus placeholder="如：技术部、财务部" {...form.register('name')} />
              ) : (
                department.name
              )}
            </DetailItem>

            <DetailItem
              label="部门编号"
              required={editing}
              error={errors.code?.message}
              hint={editing ? '给工资系统、OA 这类外部系统对账用。建后尽量别改' : undefined}
            >
              {editing ? (
                <Input placeholder="如：TECH、TECH-BE、FIN" {...form.register('code')} />
              ) : (
                <code className="text-muted-foreground">{department.code}</code>
              )}
            </DetailItem>

            <DetailItem label="上级部门" error={errors.parent_id?.message}>
              {editing ? (
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
                        ...parentOptions.map((item) => ({
                          value: item.id,
                          label: item.name,
                          hint: item.code,
                        })),
                      ]}
                    />
                  )}
                />
              ) : (
                (parentName ?? <span className="text-muted-foreground">一级部门</span>)
              )}
            </DetailItem>

            <DetailItem label="状态" error={errors.status?.message}>
              {editing ? (
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
              ) : department.status === 'active' ? (
                <StatusDot tone="success">启用</StatusDot>
              ) : (
                <StatusDot tone="neutral">停用</StatusDot>
              )}
            </DetailItem>

            <DetailItem
              label="排序"
              error={errors.sort_order?.message}
              hint={editing ? '同级之间小的在前' : undefined}
            >
              {editing ? (
                <Input type="number" min={0} max={9999} {...form.register('sort_order')} />
              ) : (
                department.sort_order
              )}
            </DetailItem>

            <DetailItem label="备注" error={errors.remark?.message}>
              {editing ? (
                <Input placeholder="选填，比如这个部门是做什么的" {...form.register('remark')} />
              ) : (
                department.remark || '—'
              )}
            </DetailItem>
          </DetailSection>
        </form>

        {/* 成员只在只读态显示：正在改部门信息时，下面那张人员表只是干扰 */}
        {editing ? null : children}
      </div>

      <ConfirmDialog state={confirm} />
      <ConfirmDialog state={guard} />
    </div>
  )
}
