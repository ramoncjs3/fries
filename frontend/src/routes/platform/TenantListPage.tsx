import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { Ban, CirclePlay, Plus } from 'lucide-react'
import { useState } from 'react'
import { toast } from 'sonner'

import {
  createTenant,
  listTenants,
  setTenantStatus,
  type PlatformTenant,
} from '@/api/auth'
import { ApiError } from '@/api/client'
import { ConfirmDialog } from '@/components/ConfirmDialog'
import { DateTime } from '@/components/DateTime'
import type { FilterSpec } from '@/components/ListFilters'
import { ListPage, type Column } from '@/components/ListPage'
import { RowActions } from '@/components/RowActions'
import { SecretDialog } from '@/components/SecretDialog'
import { StatusDot } from '@/components/ui/badge'
import { Button } from '@/components/ui/button'
import { useConfirm } from '@/lib/confirm'
import { useListParams } from '@/lib/list-params'
import { NewTenantDialog } from '@/routes/platform/NewTenantDialog'

/**
 * 组织列表 —— 平台管理端的主页面。
 *
 * 「成员数」来自 `tenants.user_count` 冗余列，**平台端不查客户的业务表**
 * （MULTI-TENANCY.md §6）。那个数由 users 上的触发器维护。
 *
 * 组织**只停用、不删除**（§9.3）：审计链要完整、误删无法恢复、
 * 客户过两个月又回来也很常见。所以这里没有删除按钮，是有意的。
 */

const tenantsQueryKey = ['platform', 'tenants'] as const

const filterSpecs: FilterSpec[] = [
  {
    key: 'status',
    label: '状态',
    options: [
      { value: 'active', label: '启用' },
      { value: 'suspended', label: '已停用' },
    ],
  },
]

export default function TenantListPage() {
  const params = useListParams()
  const queryClient = useQueryClient()
  const confirm = useConfirm()

  const [creating, setCreating] = useState(false)
  const [credential, setCredential] = useState<{ code: string; username: string; password: string } | null>(null)

  const query = useQuery({
    queryKey: [...tenantsQueryKey, params.page, params.pageSize, params.search, params.filter('status')],
    queryFn: () =>
      listTenants({
        page: params.page,
        pageSize: params.pageSize,
        keyword: params.search || undefined,
        status: params.filter('status') || undefined,
      }),
  })

  const create = useMutation({
    mutationFn: (input: { code: string; name: string }) => createTenant(input.code, input.name),
    onSuccess: async (result) => {
      await queryClient.invalidateQueries({ queryKey: tenantsQueryKey })
      setCreating(false)
      // ⚠️ 初始密码**只有这一次**能拿到（库里存的是哈希）。用必须手动关掉的弹窗，
      // 不能用 toast —— 一闪就没了，那这个组织就白开了。
      setCredential({
        code: result.tenant.code,
        username: result.admin_username,
        password: result.admin_password,
      })
    },
    onError: (err) => toast.error(err instanceof ApiError ? err.message : '开通失败，请重试'),
  })

  const changeStatus = useMutation({
    mutationFn: (input: { id: string; status: string; version: number }) =>
      setTenantStatus(input.id, input.status, input.version),
    onSuccess: async (updated) => {
      await queryClient.invalidateQueries({ queryKey: tenantsQueryKey })
      toast.success(updated.status === 'active' ? '已启用' : '已停用，该组织的人立刻会被登出')
    },
    onError: (err) => toast.error(err instanceof ApiError ? err.message : '操作失败，请重试'),
  })

  const columns: Array<Column<PlatformTenant>> = [
    {
      key: 'name',
      header: '组织',
      cell: (row) => (
        <span className="flex flex-col">
          <span className="font-medium">{row.name}</span>
          <span className="font-mono text-xs text-muted-foreground">{row.code}</span>
        </span>
      ),
    },
    { key: 'user_count', header: '成员数', cell: (row) => row.user_count },
    {
      key: 'status',
      header: '状态',
      cell: (row) => (
        <StatusDot tone={row.status === 'active' ? 'success' : 'neutral'}>
          {row.status === 'active' ? '启用' : '已停用'}
        </StatusDot>
      ),
    },
    { key: 'created_at', header: '开通时间', cell: (row) => <DateTime value={row.created_at} /> },
    {
      key: 'actions',
      header: '',
      cell: (row) => (
        <RowActions
          actions={[
            row.status === 'active'
              ? {
                  key: 'suspend',
                  label: '停用',
                  icon: <Ban />,
                  danger: true,
                  onSelect: () =>
                    confirm.open({
                      title: `停用「${row.name}」？`,
                      description:
                        '停用之后这家公司的人立刻会被登出，也登不进来。数据都还在，随时可以再启用。',
                      confirmText: '停用',
                      destructive: true,
                      onConfirm: () =>
                        changeStatus.mutateAsync({
                          id: row.id,
                          status: 'suspended',
                          version: row.version,
                        }),
                    }),
                }
              : {
                  key: 'activate',
                  label: '启用',
                  icon: <CirclePlay />,
                  onSelect: () =>
                    changeStatus.mutate({ id: row.id, status: 'active', version: row.version }),
                },
          ]}
        />
      ),
    },
  ]

  return (
    <>
      <ListPage
          title="组织"
          description="每个组织是一家使用这套系统的公司。开通后把公司代码和初始密码交给客户。"
          actions={
            <Button onClick={() => setCreating(true)}>
              <Plus /> 开通组织
            </Button>
          }
          filters={filterSpecs}
          searchPlaceholder="搜索组织名或公司代码"
          columns={columns}
          query={query}
          params={params}
        rowKey={(row) => row.id}
      />

      <NewTenantDialog
        open={creating}
        onOpenChange={setCreating}
        pending={create.isPending}
        onSubmit={(values) => create.mutate(values)}
      />

      <SecretDialog
        open={credential !== null}
        onOpenChange={(open) => {
          if (!open) setCredential(null)
        }}
        title="组织已开通"
        description={
          credential
            ? `把这三样一起交给客户：公司代码 ${credential.code}、账号 ${credential.username}、下面这串初始密码。客户首次登录会被要求改密。`
            : undefined
        }
        secret={credential?.password ?? ''}
      />

      <ConfirmDialog state={confirm} />
    </>
  )
}
