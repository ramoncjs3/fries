import { Lock, Pencil, Plus, Trash2 } from 'lucide-react'
import { Link, useNavigate } from 'react-router'

import type { FilterSpec } from '@/components/ListFilters'
import { ListPage, type Column } from '@/components/ListPage'
import { RowActions } from '@/components/RowActions'
import { StatusDot, Tag } from '@/components/ui/badge'
import { Button } from '@/components/ui/button'
import { ConfirmDialog } from '@/components/ConfirmDialog'
import { useRoleMutations, useRoles } from '@/features/role/queries'
import { dataScopeLabels, type Role } from '@/features/role/types'
import { useConfirm } from '@/lib/confirm'
import { FROM_LIST_STATE } from '@/lib/detail-nav'
import { useListParams } from '@/lib/list-params'
import { useSession } from '@/lib/session'

const filterSpecs: FilterSpec[] = [
  {
    key: 'status',
    label: '状态',
    // 枚举字段一律用 options 渲染成下拉，不能留成文本框（§7.5）
    options: [
      { value: 'active', label: '启用' },
      { value: 'disabled', label: '停用' },
    ],
  },
]

export default function RoleListPage() {
  const params = useListParams()
  const { can } = useSession()
  const navigate = useNavigate()
  const confirm = useConfirm()
  const { remove } = useRoleMutations()

  const query = useRoles({
    page: params.page,
    pageSize: params.pageSize,
    keyword: params.search || undefined,
    status: params.filter('status') || undefined,
  })

  const columns: Array<Column<Role>> = [
    {
      key: 'name',
      header: '角色',
      cell: (row) => (
        <span className="flex items-center gap-2">
          <span className="font-medium">{row.name}</span>
          {row.builtin ? (
            <Tag tone="outline">
              <Lock className="mr-1 size-3" />
              内置
            </Tag>
          ) : null}
        </span>
      ),
    },
    { key: 'key', header: '标识', className: 'w-40', cell: (row) => <code>{row.key}</code> },
    {
      key: 'data_scope',
      header: '数据范围',
      className: 'w-32 whitespace-nowrap',
      cell: (row) => dataScopeLabels[row.data_scope] ?? row.data_scope,
    },
    {
      key: 'permission_count',
      header: '权限',
      className: 'w-24 whitespace-nowrap tabular',
      // 内置 admin 是通配权限，显示条数会让人以为它只有一条
      cell: (row) => (row.builtin ? <span className="text-muted-foreground">全部</span> : row.permission_count),
    },
    {
      key: 'user_count',
      header: '成员',
      className: 'w-20 whitespace-nowrap tabular',
      cell: (row) => (row.user_count > 0 ? row.user_count : <span className="text-muted-foreground">—</span>),
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
      key: 'actions',
      header: '',
      className: 'w-14',
      cell: (row) => (
        <RowActions
          actions={[
            ...(can('role', 'update')
              ? [
                  {
                    key: 'edit',
                    label: '编辑',
                    icon: <Pencil />,
                    disabled: row.builtin,
                    disabledReason: '内置角色不可修改',
                    // 编辑在详情页里做，这里只是深链过去并直接进编辑态
                    onSelect: () =>
                      void navigate(`/roles/${row.id}?edit=1`, { state: FROM_LIST_STATE }),
                  },
                ]
              : []),
            ...(can('role', 'delete')
              ? [
                  {
                    key: 'delete',
                    label: '删除',
                    icon: <Trash2 />,
                    danger: true,
                    disabled: row.builtin || row.user_count > 0,
                    disabledReason: row.builtin ? '内置角色不可删除' : '还有成员在用这个角色',
                    onSelect: () =>
                      confirm.open({
                        title: `删除角色「${row.name}」？`,
                        description: '删除后不可恢复。已经绑定这个角色的人会失去对应权限。',
                        confirmText: '删除',
                        destructive: true,
                        onConfirm: () => remove.mutateAsync({ id: row.id, version: row.version }),
                      }),
                  },
                ]
              : []),
          ]}
        />
      ),
    },
  ]

  return (
    <>
      <ListPage
        title="角色管理"
        description="角色决定能做什么（功能权限）和能看多少（数据范围）。内置角色不可改，兜底用。"
        columns={columns}
        query={query}
        params={params}
        rowKey={(row) => row.id}
        rowLink={(row) => `/roles/${row.id}`}
        searchPlaceholder="搜索名称或标识"
        filters={filterSpecs}
        emptyMessage="还没有角色"
        actions={
          can('role', 'create') ? (
            <Button asChild>
              <Link to="/roles/new">
                <Plus /> 新增角色
              </Link>
            </Button>
          ) : null
        }
      />

      <ConfirmDialog state={confirm} />
    </>
  )
}
