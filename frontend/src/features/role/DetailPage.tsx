import { zodResolver } from '@hookform/resolvers/zod'
import { Loader2, Pencil, Trash2 } from 'lucide-react'
import { useState } from 'react'
import { Controller, useForm } from 'react-hook-form'
import { useNavigate, useParams, useSearchParams } from 'react-router'

import { ApiError } from '@/api/client'
import { ConfirmDialog } from '@/components/ConfirmDialog'
import { DateTime } from '@/components/DateTime'
import { DetailBlock, DetailItem, DetailPage, DetailSection } from '@/components/DetailPage'
import { FormAlert } from '@/components/PageState'
import { RowActions } from '@/components/RowActions'
import { StatusDot, Tag } from '@/components/ui/badge'
import { Button } from '@/components/ui/button'
import { Checkbox } from '@/components/ui/checkbox'
import { Input } from '@/components/ui/input'
import { SelectField } from '@/components/ui/select'
import { usePermissionCatalog, useRole, useRoleMutations } from '@/features/role/queries'
import { roleSchema, type RoleFormValues } from '@/features/role/schema'
import { dataScopeLabels, type PermissionModule, type Role } from '@/features/role/types'
import { useConfirm } from '@/lib/confirm'
import { useSession } from '@/lib/session'
import { useUnsavedGuard } from '@/lib/unsaved-guard'

const LIST_PATH = '/roles'
const FORM_ID = 'role-detail-form'

/**
 * 角色详情页。看和改在同一页，没有编辑弹窗（DECISIONS.md §7.6）。
 *
 * 和用户详情页的骨架完全一样，差别只在字段：**权限勾选树来自后端**
 * （`GET /roles/permission-catalog`），前端不许自己维护一份。
 */
export default function RoleDetailPage() {
  const { id = '' } = useParams()
  const [searchParams, setSearchParams] = useSearchParams()
  const { can } = useSession()
  const detail = useRole(id)
  const catalog = usePermissionCatalog(true)
  const role = detail.data

  // 内置角色（超级管理员）的权限是写死的，改了等于把自己锁在门外（§3.5.1）
  const editable = can('role', 'update') && role?.builtin === false
  const editing = searchParams.get('edit') === '1' && editable
  const [editSeq, setEditSeq] = useState(0)

  function setEditing(next: boolean) {
    if (next) setEditSeq((n) => n + 1)
    setSearchParams(
      (current) => {
        const params = new URLSearchParams(current)
        if (next) params.set('edit', '1')
        else params.delete('edit')
        return params
      },
      { replace: true },
    )
  }

  // ⚠️ 数据（详情**和**权限目录）都到齐了才挂表单 —— 早挂一步 defaultValues 就是空的，
  // 之后数据到了也不会补上。理由见 docs/MEMORY.md
  if (!role || catalog.data === undefined) {
    return (
      <DetailPage
        title="角色详情"
        backTo={LIST_PATH}
        loading={detail.isPending || catalog.isPending}
        error={detail.isError ? detail.error : undefined}
        onRetry={() => void detail.refetch()}
      >
        {null}
      </DetailPage>
    )
  }

  return (
    <RoleDetailBody
      key={editing ? `edit-${editSeq}` : 'read'}
      role={role}
      catalog={catalog.data}
      editing={editing}
      editable={editable}
      onEditingChange={setEditing}
      onReload={async () => {
        await detail.refetch()
        setEditSeq((n) => n + 1)
      }}
    />
  )
}

function RoleDetailBody({
  role,
  catalog,
  editing,
  editable,
  onEditingChange,
  onReload,
}: {
  role: Role
  catalog: PermissionModule[]
  editing: boolean
  editable: boolean
  onEditingChange: (next: boolean) => void
  onReload: () => Promise<void>
}) {
  const navigate = useNavigate()
  const { can } = useSession()
  const confirm = useConfirm()
  const { remove, update } = useRoleMutations()

  const form = useForm<RoleFormValues>({
    resolver: zodResolver(roleSchema),
    defaultValues: {
      key: role.key,
      name: role.name,
      description: role.description,
      data_scope: role.data_scope as RoleFormValues['data_scope'],
      status: role.status as RoleFormValues['status'],
      permissions: role.permissions,
    },
  })

  const dirty = editing && form.formState.isDirty
  const guard = useUnsavedGuard(dirty)
  const [failure, setFailure] = useState<{ message: string; conflict: boolean } | null>(null)
  const errors = editing ? form.formState.errors : {}

  async function onSubmit(values: RoleFormValues) {
    setFailure(null)
    try {
      await update.mutateAsync({
        id: role.id,
        version: role.version,
        input: {
          name: values.name.trim(),
          description: values.description,
          data_scope: values.data_scope,
          status: values.status,
          permissions: values.permissions,
        },
      })
      onEditingChange(false)
    } catch (err) {
      if (!(err instanceof ApiError)) {
        setFailure({ message: '保存失败了，请重试。一直不行的话把这一页刷新一下。', conflict: false })
        return
      }
      for (const item of err.formErrors()) {
        form.setError(item.field as keyof RoleFormValues, { message: item.message })
      }
      if (err.formErrors().length === 0) {
        setFailure({ message: err.message, conflict: err.code === 'common.version_conflict' })
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
    <>
      <DetailPage
        title={role.name}
        description={role.key}
        backTo={LIST_PATH}
        alert={
          failure ? (
            <FormAlert
              message={failure.message}
              action={failure.conflict ? { label: '重新加载', onClick: () => void onReload() } : undefined}
            />
          ) : null
        }
        actions={
          editing ? (
            <span className="flex items-center gap-2">
              <Button variant="outline" onClick={cancelEdit} disabled={update.isPending}>
                取消
              </Button>
              <Button type="submit" form={FORM_ID} disabled={update.isPending}>
                {update.isPending ? <Loader2 className="animate-spin" /> : null}
                保存
              </Button>
            </span>
          ) : (
            <span className="flex items-center gap-1">
              {editable ? (
                <Button variant="outline" onClick={() => onEditingChange(true)}>
                  <Pencil /> 编辑
                </Button>
              ) : null}
              <RowActions
                actions={
                  can('role', 'delete')
                    ? [
                        {
                          key: 'delete',
                          label: '删除',
                          icon: <Trash2 />,
                          danger: true,
                          disabled: role.builtin || role.user_count > 0,
                          disabledReason: role.builtin ? '内置角色不可删除' : '还有成员在用这个角色',
                          onSelect: () =>
                            confirm.open({
                              title: `删除角色「${role.name}」？`,
                              description: '删除后这个角色的权限策略会一起清掉。',
                              confirmText: '删除',
                              destructive: true,
                              onConfirm: async () => {
                                await remove.mutateAsync({ id: role.id, version: role.version })
                                void navigate(LIST_PATH, { replace: true })
                              },
                            }),
                        },
                      ]
                    : []
                }
              />
            </span>
          )
        }
      >
        <form id={FORM_ID} className="space-y-8" onSubmit={(event) => void form.handleSubmit(onSubmit)(event)}>
          <DetailSection title="基本信息">
            {/* key 是 Casbin 策略里的身份，改了等于换了个角色，而已签发的会话还带着老 key */}
            <DetailItem label="角色标识" hint={editing ? '权限策略里的身份，建后不可改' : undefined}>
              <code className="text-muted-foreground">{role.key}</code>
            </DetailItem>

            <DetailItem label="角色名称" required={editing} error={errors.name?.message}>
              {editing ? (
                <Input autoFocus placeholder="如：只读、财务管理员" {...form.register('name')} />
              ) : (
                role.name
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
              ) : role.status === 'active' ? (
                <StatusDot tone="success">启用</StatusDot>
              ) : (
                <StatusDot tone="neutral">停用</StatusDot>
              )}
            </DetailItem>

            <DetailItem
              label="数据范围"
              error={errors.data_scope?.message}
              hint={editing ? '一个人有多个角色时取最宽的那个' : undefined}
            >
              {editing ? (
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
              ) : (
                (dataScopeLabels[role.data_scope] ?? role.data_scope)
              )}
            </DetailItem>

            <DetailItem label="说明" error={errors.description?.message}>
              {editing ? (
                <Input placeholder="选填，这个角色是给谁用的" {...form.register('description')} />
              ) : (
                role.description || '—'
              )}
            </DetailItem>

            <DetailItem label="成员">
              {role.user_count > 0 ? `${role.user_count} 人` : <span className="text-muted-foreground">还没有人用</span>}
            </DetailItem>

            <DetailItem label="创建于">
              <DateTime value={role.created_at} variant="minute" />
            </DetailItem>
          </DetailSection>

          <PermissionSection
            form={form}
            catalog={catalog}
            editing={editing}
            builtin={role.builtin}
            permissions={role.permissions}
          />
        </form>
      </DetailPage>

      <ConfirmDialog state={confirm} />
      <ConfirmDialog state={guard} />
    </>
  )
}

/**
 * 权限那一组。它比别的字段大得多，单独一节。
 *
 * **勾选树来自后端注册表**，前端不许自己维护一份 —— 那样每加一个模块都要记得
 * 回来补，必然会漏（DECISIONS.md §3.7）。
 */
function PermissionSection({
  form,
  catalog,
  editing,
  builtin,
  permissions,
}: {
  form: ReturnType<typeof useForm<RoleFormValues>>
  catalog: PermissionModule[]
  editing: boolean
  builtin: boolean
  permissions: string[]
}) {
  if (!editing) {
    return (
      <DetailSection title="权限">
        {builtin ? (
          <DetailItem label="范围">
            <span className="text-muted-foreground">内置角色，拥有全部权限</span>
          </DetailItem>
        ) : (
          catalog.map((module) => {
            const picked = module.actions.filter((a) => permissions.includes(a.point))
            return (
              <DetailItem key={module.key} label={module.name}>
                {picked.length > 0 ? (
                  <span className="flex flex-wrap gap-1.5">
                    {picked.map((action) => (
                      <Tag key={action.point} tone="accent">
                        {action.name}
                      </Tag>
                    ))}
                  </span>
                ) : (
                  <span className="text-muted-foreground">无</span>
                )}
              </DetailItem>
            )
          })
        )}
      </DetailSection>
    )
  }

  // 编辑态的勾选树是宽内容，用 DetailBlock 占满整张卡片，不走字段列
  return (
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
                {catalog.map((module) => {
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
  )
}
