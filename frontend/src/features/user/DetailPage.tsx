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
import { StatusDot, Tag } from '@/components/ui/badge'
import { Button } from '@/components/ui/button'
import { Checkbox } from '@/components/ui/checkbox'
import { Input } from '@/components/ui/input'
import { SelectField } from '@/components/ui/select'
import { useDepartments } from '@/features/department/queries'
import { useRoles } from '@/features/role/queries'
import { useUser, useUserMutations } from '@/features/user/queries'
import { userSchema, type UserFormValues } from '@/features/user/schema'
import { NO_DEPARTMENT, type User } from '@/features/user/types'
import { useConfirm } from '@/lib/confirm'
import { useSession } from '@/lib/session'
import { useUnsavedGuard } from '@/lib/unsaved-guard'

/** 用户列表页的路径。返回按钮和删除后的去处都用它。 */
const LIST_PATH = '/users'

/** 表单元素的 id。标题栏里的「保存」靠它关联到表单，回车提交才成立。 */
const FORM_ID = 'user-detail-form'


/**
 * 用户详情页。**看和改在同一页**，没有编辑弹窗（DECISIONS.md §7.6）。
 *
 * 只读态和编辑态是**同一棵 JSX 树**，每一行自己决定渲染文字还是控件 ——
 * 分成两个组件写的话，加字段时必然漏掉一边。行高和标签位置由 `<DetailItem>`
 * 保证两态一致，点「编辑」整页不会动。
 */
export default function UserDetailPage() {
  const { id = '' } = useParams()
  const [searchParams, setSearchParams] = useSearchParams()
  const { can } = useSession()
  const detail = useUser(id)
  const user = detail.data

  // 编辑态存 URL：列表页的「编辑」能直接深链进来，刷新也还在这个模式。
  // **必须再判一次权限**：`?edit=1` 是谁都能手敲的，只藏「编辑」按钮的话，
  // 没有 update 权限的人照样能进编辑态，填半天再被后端 403 顶回来。
  // （这不是安全边界，真正的拦截在后端；这层是别让人做注定失败的事）
  const editing = searchParams.get('edit') === '1' && can('user', 'update')
  // 每次进编辑都换一个 key，让表单带着**当时最新的**数据重新挂载。
  // 用 user.version 当 key 是不行的：编辑到一半后台刷新（切窗口回来就会刷）
  // 一旦版本变了就会重挂，人正在敲的东西直接没了
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

  // ⚠️ **数据没到之前不许挂载下面那个组件**：它的 useForm 只在挂载那一刻读一次
  // defaultValues，早挂一步表单就是空的，而且之后数据到了也不会自己填上
  // （key 没变就不会重挂）。直接带 ?edit=1 打开链接时最容易撞上，实测踩过。
  if (!user) {
    return (
      <DetailPage
        title="用户详情"
        backTo={LIST_PATH}
        loading={detail.isPending}
        error={detail.isError ? detail.error : undefined}
        onRetry={() => void detail.refetch()}
      >
        {null}
      </DetailPage>
    )
  }

  return (
    <UserDetailBody
      key={editing ? `edit-${editSeq}` : 'read'}
      user={user}
      editing={editing}
      onEditingChange={setEditing}
      // 版本冲突时的出路：先把最新数据取回来，再换 key 重挂表单，
      // 让它带着新版本号重新初始化。不 await 就 bump 的话，重挂时拿到的还是旧数据
      onReload={async () => {
        await detail.refetch()
        setEditSeq((n) => n + 1)
      }}
    />
  )
}

function UserDetailBody({
  user,
  editing,
  onEditingChange,
  onReload,
}: {
  user: User
  editing: boolean
  onEditingChange: (next: boolean) => void
  onReload: () => Promise<void>
}) {
  const navigate = useNavigate()
  const { can, me } = useSession()
  const confirm = useConfirm()
  const { remove, resetPassword, update } = useUserMutations()

  const departments = useDepartments({})
  // 角色不多，一次取满够用；真多到 200 个再换成搜索式选择器
  const roles = useRoles({ page: 1, pageSize: 100, status: 'active' })

  const [password, setPassword] = useState('')

  // 初始值只从 defaultValues 进，不用 reset —— 理由见 docs/MEMORY.md
  const form = useForm<UserFormValues>({
    resolver: zodResolver(userSchema),
    defaultValues: {
      username: user.username,
      display_name: user.display_name,
      email: user.email,
      phone: user.phone,
      status: user.status as UserFormValues['status'],
      department_id: user.department_id ?? '',
      role_ids: user.role_ids,
    },
  })

  const dirty = editing && form.formState.isDirty
  const guard = useUnsavedGuard(dirty)

  /** 整表失败（不是某个字段的问题）。冲突要额外给一条出路，所以留着是不是冲突。 */
  const [failure, setFailure] = useState<{ message: string; conflict: boolean } | null>(null)

  const errors = editing ? form.formState.errors : {}

  const roleNames = user.role_ids
    .map((rid) => roles.data?.items.find((r) => r.id === rid))
    .filter((r) => r !== undefined)

  const lockedUntil = user.locked_until
  const locked = lockedUntil !== null && lockedUntil !== undefined && new Date(lockedUntil) > new Date()

  // 自己的密码得走右上角菜单，自己也删不掉自己（DECISIONS.md §3.5.1）
  const isSelf = user.id === me?.user.id

  async function onSubmit(values: UserFormValues) {
    setFailure(null)
    try {
      await update.mutateAsync({
        id: user.id,
        version: user.version,
        input: {
          display_name: values.display_name.trim(),
          email: values.email.trim(),
          phone: values.phone.trim(),
          status: values.status,
          department_id: values.department_id || undefined,
          role_ids: values.role_ids,
        },
      })
      onEditingChange(false)
    } catch (err) {
      // ⚠️ **非 ApiError 也必须落地**。只认 ApiError 的话，别的异常（我们自己
      // 代码里的 bug、读响应体时断网）会被悄悄吞掉 —— 按钮转一圈回来什么都没变，
      // 人以为存上了。这类「以为成功其实失败」的坑最贵。
      if (!(err instanceof ApiError)) {
        setFailure({ message: '保存失败了，请重试。一直不行的话把这一页刷新一下。', conflict: false })
        return
      }
      for (const item of err.formErrors()) {
        form.setError(item.field as keyof UserFormValues, { message: item.message })
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

  function doResetPassword() {
    confirm.open({
      title: `重置「${user.display_name}」的密码？`,
      description: '会生成一个临时密码，并把这个人已登录的所有设备踢下线。',
      confirmText: '重置',
      destructive: true,
      onConfirm: async () => {
        const result = await resetPassword.mutateAsync(user.id)
        setPassword(result.password)
      },
    })
  }

  function doDelete() {
    confirm.open({
      title: `删除「${user.display_name}」？`,
      description: '会立刻踢下线。历史操作记录仍保留在审计日志里。',
      confirmText: '删除',
      destructive: true,
      onConfirm: async () => {
        await remove.mutateAsync({ id: user.id, version: user.version })
        // 删完这一页已经没有内容了。replace 是为了让「后退」不会再回到这个
        // 已经不存在的详情页
        void navigate(LIST_PATH, { replace: true })
      },
    })
  }

  return (
    <>
      <DetailPage
        title={user.display_name}
        description={user.username}
        backTo={LIST_PATH}
        alert={
          failure ? (
            <FormAlert
              message={failure.message}
              // 冲突光说「被别人改了」没用，得给条路走：把最新数据取回来重新填
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
              {/* 按钮在 <form> 外面（它在标题栏里），靠 form= 属性关联 ——
                    这样输入框里按回车也能提交，和原来的编辑弹窗手感一致 */}
              <Button type="submit" form={FORM_ID} disabled={update.isPending}>
                {update.isPending ? <Loader2 className="animate-spin" /> : null}
                保存
              </Button>
            </span>
          ) : (
            <span className="flex items-center gap-1">
              {can('user', 'update') ? (
                <Button variant="outline" onClick={() => onEditingChange(true)}>
                  <Pencil /> 编辑
                </Button>
              ) : null}
              <RowActions
                actions={[
                  ...(can('user', 'reset_password')
                    ? [
                        {
                          key: 'reset',
                          label: '重置密码',
                          icon: <KeyRound />,
                          disabled: isSelf,
                          disabledReason: '改自己的密码请用右上角菜单里的「修改密码」',
                          onSelect: doResetPassword,
                        },
                      ]
                    : []),
                  ...(can('user', 'delete')
                    ? [
                        {
                          key: 'delete',
                          label: '删除',
                          icon: <Trash2 />,
                          danger: true,
                          disabled: isSelf,
                          disabledReason: '不能删除自己',
                          onSelect: doDelete,
                        },
                      ]
                    : []),
                ]}
              />
            </span>
          )
        }
      >
        <form id={FORM_ID} className="space-y-8" onSubmit={(event) => void form.handleSubmit(onSubmit)(event)}>
          <DetailSection title="基本信息">
            {/* 用户名建后不可改（它是登录身份），所以编辑态也是文字 */}
            <DetailItem label="用户名" hint={editing ? '登录用，建后不可改' : undefined}>
              <span className="text-muted-foreground">{user.username}</span>
            </DetailItem>

            <DetailItem label="显示名" required={editing} error={errors.display_name?.message}>
              {editing ? (
                <Input autoFocus placeholder="真实姓名，如：张三" {...form.register('display_name')} />
              ) : (
                user.display_name
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
                        { value: 'disabled', label: '停用（会立刻踢下线）' },
                      ]}
                    />
                  )}
                />
              ) : user.status === 'active' ? (
                <StatusDot tone="success">启用</StatusDot>
              ) : (
                <StatusDot tone="neutral">停用</StatusDot>
              )}
            </DetailItem>

            <DetailItem label="角色" error={errors.role_ids?.message}>
              {editing ? (
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
              ) : roleNames.length > 0 ? (
                <span className="flex flex-wrap gap-1.5">
                  {roleNames.map((role) => (
                    <Tag key={role.id} tone="accent">
                      {role.name}
                    </Tag>
                  ))}
                </span>
              ) : (
                <span className="text-muted-foreground">还没分配角色，这个人登录后什么都看不到</span>
              )}
            </DetailItem>

            <DetailItem label="部门" error={errors.department_id?.message}>
              {editing ? (
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
              ) : (
                user.department_name || <span className="text-muted-foreground">未分配</span>
              )}
            </DetailItem>

            <DetailItem
              label="邮箱"
              error={errors.email?.message}
              hint={editing ? '可空；填了就能用它登录' : undefined}
            >
              {editing ? (
                <Input type="email" placeholder="如：zhangsan@example.com" {...form.register('email')} />
              ) : (
                user.email || '—'
              )}
            </DetailItem>

            <DetailItem
              label="手机号"
              error={errors.phone?.message}
              hint={editing ? '可空；填了就能用它登录' : undefined}
            >
              {editing ? <Input placeholder="11 位手机号" {...form.register('phone')} /> : user.phone || '—'}
            </DetailItem>

            <DetailItem label="创建于">
              <DateTime value={user.created_at} variant="minute" />
            </DetailItem>
          </DetailSection>
        </form>

        <DetailSection title="安全">
          <DetailItem label="首次须改密">{user.must_change_password ? '是' : '否'}</DetailItem>
          <DetailItem label="最后登录">
            {user.last_login_at ? (
              <DateTime value={user.last_login_at} variant="minute" />
            ) : (
              <span className="text-muted-foreground">从未登录</span>
            )}
          </DetailItem>
          {locked ? (
            <DetailItem label="锁定至">
              <span className="text-destructive">
                <DateTime value={lockedUntil} variant="minute" />
              </span>
              <span className="ml-2 text-muted-foreground">登录失败次数过多</span>
            </DetailItem>
          ) : null}
        </DetailSection>
      </DetailPage>

      <SecretDialog
        open={password !== ''}
        onOpenChange={(next) => (next ? undefined : setPassword(''))}
        title="临时密码已生成"
        description={`${user.display_name}（${user.username}）的新密码`}
        secret={password}
      />

      <ConfirmDialog state={confirm} />
      {/* 离开页面的拦截自己带一个确认框，和上面那个是两回事 */}
      <ConfirmDialog state={guard} />
    </>
  )
}
