import { zodResolver } from '@hookform/resolvers/zod'
import { KeyRound, Loader2, Pencil, Trash2 } from 'lucide-react'
import { useState } from 'react'
import { Controller, useForm } from 'react-hook-form'
import { useNavigate, useParams, useSearchParams } from 'react-router'

import { ApiError } from '@/api/client'
import { ConfirmDialog } from '@/components/ConfirmDialog'
import { DateTime } from '@/components/DateTime'
import { DetailItem, DetailPage, DetailSection } from '@/components/DetailPage'
import { FormAlert } from '@/components/PageState'
import { RowActions } from '@/components/RowActions'
import { SecretDialog } from '@/components/SecretDialog'
import { StatusDot } from '@/components/ui/badge'
import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'
import { SelectField } from '@/components/ui/select'
import { useRoles } from '@/features/role/queries'
import {
  useServiceAccount,
  useServiceAccountMutations,
} from '@/features/service_account/queries'
import {
  serviceAccountSchema,
  type ServiceAccountFormValues,
} from '@/features/service_account/schema'
import type { CreatedKey, ServiceAccount } from '@/features/service_account/types'
import { useConfirm } from '@/lib/confirm'
import { useSession } from '@/lib/session'
import { useUnsavedGuard } from '@/lib/unsaved-guard'

const LIST_PATH = '/service-accounts'
const FORM_ID = 'service-account-detail-form'

/** RFC3339 → yyyy-MM-dd，给 `<input type="date">` 用。空值原样返回。 */
function toDateInput(value: string | null | undefined): string {
  return value ? value.slice(0, 10) : ''
}

export default function ServiceAccountDetailPage() {
  const { id = '' } = useParams()
  const [searchParams, setSearchParams] = useSearchParams()
  const { can } = useSession()
  const detail = useServiceAccount(id)
  const roles = useRoles({ page: 1, pageSize: 100, status: 'active' })
  const account = detail.data

  const editable = can('service_account', 'update')
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

  // ⚠️ 数据到齐了才挂表单 —— 早挂一步 defaultValues 就是空的，之后数据到了也不会补上
  // （docs/MEMORY.md 记过这个坑）。角色列表也要等：编辑态的下拉要用它。
  if (!account || roles.data === undefined) {
    return (
      <DetailPage
        title="机器账号"
        backTo={LIST_PATH}
        loading={detail.isPending || roles.isPending}
        error={detail.isError ? detail.error : undefined}
        onRetry={() => void detail.refetch()}
      >
        {null}
      </DetailPage>
    )
  }

  return (
    <ServiceAccountDetailBody
      key={editing ? `edit-${editSeq}` : 'read'}
      account={account}
      roleOptions={roles.data.items.map((role) => ({ value: role.id, label: role.name }))}
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

function ServiceAccountDetailBody({
  account,
  roleOptions,
  editing,
  editable,
  onEditingChange,
  onReload,
}: {
  account: ServiceAccount
  roleOptions: Array<{ value: string; label: string }>
  editing: boolean
  editable: boolean
  onEditingChange: (next: boolean) => void
  onReload: () => Promise<void>
}) {
  const navigate = useNavigate()
  const { can } = useSession()
  const confirm = useConfirm()
  const { remove, update, rotate } = useServiceAccountMutations()

  /** 轮换出来的新密钥，只出现这一次。 */
  const [rotated, setRotated] = useState<CreatedKey | null>(null)

  const form = useForm<ServiceAccountFormValues>({
    resolver: zodResolver(serviceAccountSchema),
    defaultValues: {
      name: account.name,
      description: account.description,
      role_id: account.role_id,
      status: account.status as ServiceAccountFormValues['status'],
      expires_at: toDateInput(account.expires_at),
    },
  })

  const dirty = editing && form.formState.isDirty
  const guard = useUnsavedGuard(dirty)
  const [failure, setFailure] = useState<{ message: string; conflict: boolean } | null>(null)
  const errors = editing ? form.formState.errors : {}

  async function onSubmit(values: ServiceAccountFormValues) {
    setFailure(null)
    try {
      await update.mutateAsync({
        id: account.id,
        version: account.version,
        input: {
          name: values.name.trim(),
          description: values.description,
          role_id: values.role_id,
          status: values.status,
          expires_at: values.expires_at ? `${values.expires_at}T23:59:59Z` : undefined,
        },
      })
      onEditingChange(false)
    } catch (err) {
      if (!(err instanceof ApiError)) {
        setFailure({
          message: '保存失败了，请重试。一直不行的话把这一页刷新一下。',
          conflict: false,
        })
        return
      }
      for (const item of err.formErrors()) {
        form.setError(item.field as keyof ServiceAccountFormValues, { message: item.message })
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
        title={account.name}
        description={`fsa_${account.key_prefix}_…`}
        backTo={LIST_PATH}
        alert={
          failure ? (
            <FormAlert
              message={failure.message}
              action={
                failure.conflict ? { label: '重新加载', onClick: () => void onReload() } : undefined
              }
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
                actions={[
                  ...(can('service_account', 'rotate_key')
                    ? [
                        {
                          key: 'rotate',
                          label: '轮换密钥',
                          icon: <KeyRound />,
                          onSelect: () =>
                            confirm.open({
                              title: `给「${account.name}」换一副新密钥？`,
                              description:
                                '旧密钥会立刻失效，对接方在换上新密钥之前会一直调用失败。新密钥只显示一次。',
                              confirmText: '轮换',
                              destructive: true,
                              onConfirm: async () => {
                                setRotated(await rotate.mutateAsync(account.id))
                                await onReload()
                              },
                            }),
                        },
                      ]
                    : []),
                  ...(can('service_account', 'delete')
                    ? [
                        {
                          key: 'delete',
                          label: '删除',
                          icon: <Trash2 />,
                          danger: true,
                          onSelect: () =>
                            confirm.open({
                              title: `删除机器账号「${account.name}」？`,
                              description:
                                '删除后它的密钥立刻失效，用它对接的系统会开始调用失败。',
                              confirmText: '删除',
                              destructive: true,
                              onConfirm: async () => {
                                await remove.mutateAsync({
                                  id: account.id,
                                  version: account.version,
                                })
                                void navigate(LIST_PATH, { replace: true })
                              },
                            }),
                        },
                      ]
                    : []),
                ]}
              />
            </span>
          )
        }
      >
        <form
          id={FORM_ID}
          className="space-y-8"
          onSubmit={(event) => void form.handleSubmit(onSubmit)(event)}
        >
          <DetailSection title="基本信息">
            <DetailItem label="名称" required={editing} error={errors.name?.message}>
              {editing ? (
                <Input autoFocus placeholder="如：ERP 同步" {...form.register('name')} />
              ) : (
                account.name
              )}
            </DetailItem>

            <DetailItem label="说明" error={errors.description?.message}>
              {editing ? (
                <Input
                  placeholder="这个账号干什么用、联系人是谁"
                  {...form.register('description')}
                />
              ) : (
                account.description || <span className="text-muted-foreground">—</span>
              )}
            </DetailItem>

            <DetailItem
              label="角色"
              required={editing}
              error={errors.role_id?.message}
              hint={editing ? '改完立刻生效，它当场就按新角色的权限走' : undefined}
            >
              {editing ? (
                <Controller
                  control={form.control}
                  name="role_id"
                  render={({ field }) => (
                    <SelectField
                      ref={field.ref}
                      value={field.value}
                      onChange={field.onChange}
                      options={roleOptions}
                      placeholder="选择角色"
                    />
                  )}
                />
              ) : (
                account.role_name
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
                        { value: 'disabled', label: '停用（密钥立刻不可用）' },
                      ]}
                    />
                  )}
                />
              ) : account.status === 'active' ? (
                <StatusDot tone="success">启用</StatusDot>
              ) : (
                <StatusDot tone="neutral">停用</StatusDot>
              )}
            </DetailItem>
          </DetailSection>

          <DetailSection title="密钥">
            {/* 只有前缀。后半段只存哈希，谁都取不回来 —— 丢了只能轮换 */}
            <DetailItem label="密钥前缀" hint="完整密钥只在创建和轮换时显示一次，之后取不回来">
              <code>fsa_{account.key_prefix}_…</code>
            </DetailItem>

            <DetailItem
              label="到期时间"
              error={errors.expires_at?.message}
              hint={editing ? '留空表示不过期。到期后它的调用会直接失败。' : undefined}
            >
              {editing ? (
                <Input type="date" {...form.register('expires_at')} />
              ) : account.expires_at ? (
                <DateTime value={account.expires_at} />
              ) : (
                <span className="text-muted-foreground">不过期</span>
              )}
            </DetailItem>

            <DetailItem label="最后使用">
              {account.last_used_at ? (
                <DateTime value={account.last_used_at} />
              ) : (
                <span className="text-muted-foreground">从未使用</span>
              )}
            </DetailItem>

            <DetailItem label="创建时间">
              <DateTime value={account.created_at} />
            </DetailItem>
          </DetailSection>
        </form>
      </DetailPage>

      <SecretDialog
        open={rotated !== null}
        onOpenChange={(open) => {
          if (!open) setRotated(null)
        }}
        title="新密钥已生成"
        description="把它交给对接方。旧密钥已经失效，对方换上之前会一直调用失败。这串只显示这一次。"
        secret={rotated?.key ?? ''}
      />

      <ConfirmDialog state={confirm} />
      <ConfirmDialog state={guard} />
    </>
  )
}
