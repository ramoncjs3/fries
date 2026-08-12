import { KeyRound, Pencil, Plus, Trash2 } from 'lucide-react'
import { useState } from 'react'
import { Link, useNavigate } from 'react-router'

import { ConfirmDialog } from '@/components/ConfirmDialog'
import { DateTime } from '@/components/DateTime'
import type { FilterSpec } from '@/components/ListFilters'
import { ListPage, type Column } from '@/components/ListPage'
import { RowActions } from '@/components/RowActions'
import { SecretDialog } from '@/components/SecretDialog'
import { StatusDot } from '@/components/ui/badge'
import { Button } from '@/components/ui/button'
import {
  useServiceAccountMutations,
  useServiceAccounts,
} from '@/features/service_account/queries'
import type { CreatedKey, ServiceAccount } from '@/features/service_account/types'
import { useConfirm } from '@/lib/confirm'
import { FROM_LIST_STATE } from '@/lib/detail-nav'
import { useListParams } from '@/lib/list-params'
import { useSession } from '@/lib/session'

const filterSpecs: FilterSpec[] = [
  {
    key: 'status',
    label: '状态',
    options: [
      { value: 'active', label: '启用' },
      { value: 'disabled', label: '停用' },
    ],
  },
]

export default function ServiceAccountListPage() {
  const params = useListParams()
  const { can } = useSession()
  const navigate = useNavigate()
  const confirm = useConfirm()
  const { rotate, remove } = useServiceAccountMutations()

  /** 轮换出来的新密钥。**只出现这一次**，关掉就没了。 */
  const [rotated, setRotated] = useState<CreatedKey | null>(null)

  const query = useServiceAccounts({
    page: params.page,
    pageSize: params.pageSize,
    keyword: params.search || undefined,
    status: params.filter('status') || undefined,
  })

  const columns: Array<Column<ServiceAccount>> = [
    {
      key: 'name',
      header: '名称',
      cell: (row) => (
        <span className="flex flex-col">
          <span className="font-medium">{row.name}</span>
          {row.description ? (
            <span className="text-sm text-muted-foreground">{row.description}</span>
          ) : null}
        </span>
      ),
    },
    {
      key: 'key_prefix',
      header: '密钥前缀',
      className: 'w-40',
      // 只显示前缀 —— 后半段只有哈希。展示它是为了让人对得上号：
      // 对接方报「我这串不好使了」，你得知道说的是哪一个。
      cell: (row) => <code>fsa_{row.key_prefix}_…</code>,
    },
    { key: 'role_name', header: '角色', className: 'w-32', cell: (row) => row.role_name },
    {
      key: 'last_used_at',
      header: '最后使用',
      className: 'w-40 whitespace-nowrap',
      cell: (row) =>
        row.last_used_at ? (
          <DateTime value={row.last_used_at} />
        ) : (
          <span className="text-muted-foreground">从未使用</span>
        ),
    },
    {
      key: 'expires_at',
      header: '到期',
      className: 'w-40 whitespace-nowrap',
      cell: (row) =>
        row.expires_at ? (
          <DateTime value={row.expires_at} />
        ) : (
          <span className="text-muted-foreground">不过期</span>
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
      key: 'actions',
      header: '',
      className: 'w-14',
      cell: (row) => (
        <RowActions
          actions={[
            ...(can('service_account', 'update')
              ? [
                  {
                    key: 'edit',
                    label: '编辑',
                    icon: <Pencil />,
                    onSelect: () =>
                      void navigate(`/service-accounts/${row.id}?edit=1`, {
                        state: FROM_LIST_STATE,
                      }),
                  },
                ]
              : []),
            ...(can('service_account', 'rotate_key')
              ? [
                  {
                    key: 'rotate',
                    label: '轮换密钥',
                    icon: <KeyRound />,
                    onSelect: () =>
                      confirm.open({
                        title: `给「${row.name}」换一副新密钥？`,
                        // 说清后果：这不是「生成一串备用的」，是让对方当场断线
                        description:
                          '旧密钥会立刻失效，对接方在换上新密钥之前会一直调用失败。新密钥只显示一次。',
                        confirmText: '轮换',
                        destructive: true,
                        onConfirm: async () => {
                          setRotated(await rotate.mutateAsync(row.id))
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
                        title: `删除机器账号「${row.name}」？`,
                        description: '删除后它的密钥立刻失效，用它对接的系统会开始调用失败。',
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
        title="机器账号"
        description="给外部系统对接用的身份。它拿 API Key 调接口，能做什么由绑定的角色决定。"
        columns={columns}
        query={query}
        params={params}
        rowKey={(row) => row.id}
        rowLink={(row) => `/service-accounts/${row.id}`}
        searchPlaceholder="搜索名称或说明"
        filters={filterSpecs}
        emptyMessage="还没有机器账号"
        actions={
          can('service_account', 'create') ? (
            <Button asChild>
              <Link to="/service-accounts/new">
                <Plus /> 新增机器账号
              </Link>
            </Button>
          ) : null
        }
      />

      <SecretDialog
        open={rotated !== null}
        onOpenChange={(open) => {
          if (!open) setRotated(null)
        }}
        title="新密钥已生成"
        description={
          rotated
            ? `把它交给「${rotated.account.name}」的对接方。旧密钥已经失效，对方换上之前会一直调用失败。这串只显示这一次。`
            : undefined
        }
        secret={rotated?.key ?? ''}
      />

      <ConfirmDialog state={confirm} />
    </>
  )
}
