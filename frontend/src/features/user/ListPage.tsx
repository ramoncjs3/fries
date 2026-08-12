import { FolderInput, KeyRound, Pencil, Plus, Trash2 } from 'lucide-react'
import { useState } from 'react'
import { Link, useNavigate } from 'react-router'

import { ConfirmDialog } from '@/components/ConfirmDialog'
import { DateTime } from '@/components/DateTime'
import type { FilterSpec } from '@/components/ListFilters'
import { ListPage, type Column } from '@/components/ListPage'
import { RowActions } from '@/components/RowActions'
import { SecretDialog } from '@/components/SecretDialog'
import { StatusDot, Tag } from '@/components/ui/badge'
import { Button } from '@/components/ui/button'
import { MoveToDepartmentDialog } from '@/features/department/FormDialog'
import { useDepartments } from '@/features/department/queries'
import { useUserMutations, useUsers } from '@/features/user/queries'
import { UNASSIGNED_DEPARTMENT, type User } from '@/features/user/types'
import { useConfirm } from '@/lib/confirm'
import { FROM_LIST_STATE } from '@/lib/detail-nav'
import { useListParams } from '@/lib/list-params'
import { useSession } from '@/lib/session'

/** 状态是枚举，必须给 options —— 留成文本框只会让人敲出 400。 */
const statusOptions = [
  { value: 'active', label: '启用' },
  { value: 'disabled', label: '停用' },
]

/** 只出现一次的凭据。null 表示没有要展示的。 */
interface Secret {
  title: string
  description: string
  value: string
}

export default function UserListPage() {
  const params = useListParams()
  const navigate = useNavigate()
  const { can, me } = useSession()
  const confirm = useConfirm()
  const { remove, resetPassword } = useUserMutations()

  // 部门筛选是多选：`?department=a,b`。存 URL 上，刷新和分享都还在。
  const departments = useDepartments({})
  const pickedDepartments = params.filter('department').split(',').filter(Boolean)

  const query = useUsers({
    page: params.page,
    pageSize: params.pageSize,
    keyword: params.search || undefined,
    status: params.filter('status') || undefined,
    departmentIds: pickedDepartments.length > 0 ? pickedDepartments : undefined,
  })

  const filterSpecs: FilterSpec[] = [
    { key: 'status', label: '状态', options: statusOptions },
    {
      key: 'department',
      label: '部门',
      multiple: true,
      // **「未分配」必须有** —— 没有它就没办法回答「谁还没分部门」，
      // 那些人不属于部门树上任何节点，在部门页永远看不到。
      options: [
        { value: UNASSIGNED_DEPARTMENT, label: '未分配部门' },
        ...(departments.data?.items ?? []).map((d) => ({ value: d.id, label: d.name })),
      ],
    },
  ]

  const [secret, setSecret] = useState<Secret | null>(null)
  // 批量调岗：待调整的人 + 调完要清掉列表上的勾选
  const [moving, setMoving] = useState<string[]>([])
  const [clearPicked, setClearPicked] = useState<() => void>(() => () => undefined)

  function doResetPassword(row: User) {
    confirm.open({
      title: `重置「${row.display_name}」的密码？`,
      description: '会生成一个临时密码，并把这个人已登录的所有设备踢下线。',
      confirmText: '重置',
      destructive: true,
      onConfirm: async () => {
        const result = await resetPassword.mutateAsync(row.id)
        setSecret({
          title: '临时密码已生成',
          description: `${row.display_name}（${row.username}）的新密码`,
          value: result.password,
        })
      },
    })
  }

  const columns: Array<Column<User>> = [
    {
      key: 'display_name',
      header: '用户',
      cell: (row) => (
        <span className="flex min-w-0 items-center gap-2">
          <span className="grid size-7 shrink-0 place-items-center rounded-full bg-accent-subtle text-xs font-medium text-accent-text">
            {row.display_name.slice(0, 1)}
          </span>
          <span className="min-w-0">
            <span className="block truncate font-medium">{row.display_name}</span>
            <span className="block truncate text-sm text-muted-foreground">{row.username}</span>
          </span>
        </span>
      ),
    },
    {
      key: 'department_name',
      header: '部门',
      className: 'w-36',
      cell: (row) => row.department_name || <span className="text-muted-foreground">—</span>,
    },
    {
      key: 'role_names',
      header: '角色',
      cell: (row) =>
        row.role_names ? (
          <span className="flex flex-wrap gap-1">
            {row.role_names.split(',').map((name) => (
              <Tag key={name} tone="accent">
                {name}
              </Tag>
            ))}
          </span>
        ) : (
          // 没角色 = 登录进来什么都看不到，这是个需要处理的状态，不是普通的空值
          <Tag tone="outline">未分配</Tag>
        ),
    },
    {
      key: 'status',
      header: '状态',
      className: 'w-24 whitespace-nowrap',
      cell: (row) =>
        row.status === 'active' ? (
          <StatusDot tone="success">启用</StatusDot>
        ) : (
          <StatusDot tone="neutral">停用</StatusDot>
        ),
    },
    {
      key: 'last_login_at',
      header: '最后登录',
      className: 'w-44 whitespace-nowrap',
      cell: (row) =>
        row.last_login_at ? (
          <DateTime value={row.last_login_at} />
        ) : (
          <span className="text-muted-foreground">从未登录</span>
        ),
    },
    {
      key: 'actions',
      header: '',
      className: 'w-14',
      cell: (row) => {
        const isSelf = row.id === me?.user.id
        return (
          <RowActions
            actions={[
              // 编辑在详情页里做，这里只是深链过去并直接进编辑态
              ...(can('user', 'update')
                ? [
                    {
                      key: 'edit',
                      label: '编辑',
                      icon: <Pencil />,
                      onSelect: () => void navigate(`/users/${row.id}?edit=1`, { state: FROM_LIST_STATE }),
                    },
                  ]
                : []),
              ...(can('user', 'reset_password')
                ? [
                    {
                      key: 'reset',
                      label: '重置密码',
                      icon: <KeyRound />,
                      disabled: isSelf,
                      disabledReason: '改自己的密码请用右上角菜单里的「修改密码」',
                      onSelect: () => doResetPassword(row),
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
                      onSelect: () =>
                        confirm.open({
                          title: `删除「${row.display_name}」？`,
                          description: '会立刻踢下线。历史操作记录仍保留在审计日志里。',
                          confirmText: '删除',
                          destructive: true,
                          onConfirm: () => remove.mutateAsync({ id: row.id, version: row.version }),
                        }),
                    },
                  ]
                : []),
            ]}
          />
        )
      },
    },
  ]

  return (
    <>
      <ListPage
        title="用户管理"
        description="账号、角色和部门。初始密码由系统生成，本人首次登录必须修改。"
        columns={columns}
        query={query}
        params={params}
        rowKey={(row) => row.id}
        rowLink={(row) => `/users/${row.id}`}
        searchPlaceholder="搜索用户名、显示名、邮箱、手机号"
        filters={filterSpecs}
        emptyMessage="还没有用户"
        bulkActions={
          can('user', 'update')
            ? (selected, clear) => (
                <Button
                  variant="outline"
                  size="sm"
                  onClick={() => {
                    setMoving(selected.map((u) => u.id))
                    setClearPicked(() => clear)
                  }}
                >
                  <FolderInput /> 调整部门
                </Button>
              )
            : undefined
        }
        actions={
          can('user', 'create') ? (
            <Button asChild>
              <Link to="/users/new">
                <Plus /> 新增用户
              </Link>
            </Button>
          ) : null
        }
      />

      <SecretDialog
        open={secret !== null}
        onOpenChange={(next) => (next ? undefined : setSecret(null))}
        title={secret?.title ?? ''}
        description={secret?.description}
        secret={secret?.value ?? ''}
      />

      <MoveToDepartmentDialog
        userIDs={moving}
        onClose={() => setMoving([])}
        onDone={() => {
          clearPicked()
          setMoving([])
        }}
      />

      <ConfirmDialog state={confirm} />
    </>
  )
}
