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
import { Input } from '@/components/ui/input'
import { SelectField } from '@/components/ui/select'
import { useRoles } from '@/features/role/queries'
import { useServiceAccountMutations } from '@/features/service_account/queries'
import {
  emptyServiceAccount,
  serviceAccountSchema,
  type ServiceAccountFormValues,
} from '@/features/service_account/schema'
import type { CreatedKey } from '@/features/service_account/types'
import { useUnsavedGuard } from '@/lib/unsaved-guard'

const LIST_PATH = '/service-accounts'
const FORM_ID = 'service-account-new-form'

/**
 * 新增机器账号。**是一个页面，不是弹窗**（DECISIONS.md §7.6）。
 *
 * 建完弹一个「密钥只显示一次」的框 —— 关掉才走人。和新增用户那一页同一个手法，
 * 但这里更要紧：用户至少还能重置密码，而机器账号的密钥只能**轮换**，
 * 而轮换会让对接方当场断线。
 */
export default function ServiceAccountNewPage() {
  const navigate = useNavigate()
  const { create } = useServiceAccountMutations()
  const roles = useRoles({ page: 1, pageSize: 100, status: 'active' })

  /** 建完拿到的一次性密钥。关掉弹窗才走人。 */
  const [created, setCreated] = useState<CreatedKey | null>(null)
  const [failure, setFailure] = useState<string | null>(null)

  const form = useForm<ServiceAccountFormValues>({
    resolver: zodResolver(serviceAccountSchema),
    defaultValues: emptyServiceAccount,
  })

  // 已经建完了就别再拦 —— 那时表单还是「脏」的，但内容已经落库了
  const guard = useUnsavedGuard(form.formState.isDirty && created === null)
  const errors = form.formState.errors

  async function onSubmit(values: ServiceAccountFormValues) {
    setFailure(null)
    try {
      setCreated(
        await create.mutateAsync({
          name: values.name.trim(),
          description: values.description.trim(),
          role_id: values.role_id,
          status: values.status,
          // 日期选择器给的是 yyyy-MM-dd，补成当天结束 —— 只填日期的人想的是
          // 「这天之后失效」，而不是「这天零点失效」
          expires_at: values.expires_at ? `${values.expires_at}T23:59:59Z` : undefined,
        }),
      )
    } catch (error) {
      // ⚠️ 非 ApiError 也必须落地，否则失败会被悄悄吞掉
      if (!(error instanceof ApiError)) {
        setFailure('创建失败了，请重试。一直不行的话把这一页刷新一下。')
        return
      }
      for (const item of error.formErrors()) {
        form.setError(item.field as keyof ServiceAccountFormValues, { message: item.message })
      }
      if (error.formErrors().length === 0) setFailure(error.message)
    }
  }

  const roleOptions = (roles.data?.items ?? []).map((role) => ({
    value: role.id,
    label: role.name,
  }))

  return (
    <>
      <DetailPage
        title="新增机器账号"
        description="密钥由系统生成，创建后只显示一次"
        backTo={LIST_PATH}
        alert={failure ? <FormAlert message={failure} /> : null}
        actions={
          <span className="flex items-center gap-2">
            <Button
              variant="outline"
              onClick={() => void navigate(LIST_PATH)}
              disabled={create.isPending}
            >
              取消
            </Button>
            <Button type="submit" form={FORM_ID} disabled={create.isPending}>
              {create.isPending ? <Loader2 className="animate-spin" /> : null}
              创建
            </Button>
          </span>
        }
      >
        <form
          id={FORM_ID}
          className="space-y-8"
          onSubmit={(event) => void form.handleSubmit(onSubmit)(event)}
        >
          <DetailSection title="基本信息">
            <DetailItem
              label="名称"
              required
              error={errors.name?.message}
              hint="写清楚是给谁用的，比如「ERP 同步」"
            >
              <Input autoFocus placeholder="如：ERP 同步" {...form.register('name')} />
            </DetailItem>

            <DetailItem label="说明" error={errors.description?.message}>
              <Input placeholder="这个账号干什么用、联系人是谁" {...form.register('description')} />
            </DetailItem>

            <DetailItem
              label="角色"
              required
              error={errors.role_id?.message}
              hint="决定它能调哪些接口。按最小够用来选，别直接给管理员。"
            >
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
            </DetailItem>
          </DetailSection>

          <DetailSection title="有效期">
            <DetailItem
              label="到期时间"
              error={errors.expires_at?.message}
              hint="留空表示不过期。到期后它的调用会直接失败，不需要人工去停用。"
            >
              <Input type="date" {...form.register('expires_at')} />
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
                      { value: 'disabled', label: '停用（密钥立刻不可用）' },
                    ]}
                  />
                )}
              />
            </DetailItem>
          </DetailSection>
        </form>
      </DetailPage>

      <SecretDialog
        open={created !== null}
        onOpenChange={(open) => {
          if (!open) {
            setCreated(null)
            void navigate(LIST_PATH)
          }
        }}
        title="机器账号已创建"
        description={
          created
            ? `把下面这串密钥交给「${created.account.name}」的对接方，放进请求头 X-API-Key。关掉这个框就再也拿不到了，丢了只能轮换。`
            : undefined
        }
        secret={created?.key ?? ''}
      />

      <ConfirmDialog state={guard} />
    </>
  )
}
