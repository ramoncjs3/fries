import { DateTime } from '@/components/DateTime'
import type { FilterSpec } from '@/components/ListFilters'
import { ListPage, type Column } from '@/components/ListPage'
import { StatusDot, Tag } from '@/components/ui/badge'
import { useAuditLogs } from '@/features/audit/queries'
import type { AuditEntry } from '@/features/audit/types'
import { useListParams } from '@/lib/list-params'

/** 主体类型的中文名和色调。 */
const actorLabels: Record<string, { text: string; tone: 'accent' | 'neutral' | 'outline' }> = {
  user: { text: '用户', tone: 'accent' },
  service: { text: '系统对接', tone: 'neutral' },
  anonymous: { text: '未登录', tone: 'outline' },
  system: { text: '后台任务', tone: 'neutral' },
}

function statusTone(status: number) {
  if (status >= 500) return 'danger' as const
  if (status >= 400) return 'warning' as const
  return 'success' as const
}

const columns: Array<Column<AuditEntry>> = [
  {
    key: 'occurred_at',
    header: '时间',
    className: 'w-44 whitespace-nowrap',
    cell: (row) => <DateTime value={row.occurred_at} />,
  },
  {
    key: 'actor',
    header: '操作人',
    className: 'w-40',
    cell: (row) => {
      const label = actorLabels[row.actor_type] ?? { text: row.actor_type, tone: 'outline' as const }
      return (
        <span className="flex items-center gap-2">
          <Tag tone={label.tone}>{label.text}</Tag>
          <span className="truncate">{row.actor_name || '—'}</span>
        </span>
      )
    },
  },
  {
    key: 'action',
    header: '操作',
    cell: (row) => (
      <code>
        {row.resource}:{row.action}
      </code>
    ),
  },
  {
    key: 'path',
    header: '请求',
    // 不许折行：一折行整行就从 56px 变 72px，十行下来表格全乱
    className: 'whitespace-nowrap',
    cell: (row) => (
      <span className="text-sm text-muted-foreground">
        {row.method} {row.path}
      </span>
    ),
  },
  {
    key: 'status',
    header: '结果',
    className: 'w-24 whitespace-nowrap',
    cell: (row) => <StatusDot tone={statusTone(row.http_status)}>{row.http_status}</StatusDot>,
  },
  {
    key: 'duration',
    header: '耗时',
    className: 'w-24 whitespace-nowrap text-right tabular',
    cell: (row) => `${row.duration_ms} ms`,
  },
  {
    key: 'ip',
    header: 'IP',
    className: 'w-32 whitespace-nowrap tabular',
    cell: (row) => <span className="text-sm text-muted-foreground">{row.ip || '—'}</span>,
  },
]

const filterSpecs: FilterSpec[] = [
  { key: 'resource', label: '资源', placeholder: '如 audit' },
  { key: 'action', label: '动作', placeholder: '如 list' },
]

export default function AuditListPage() {
  const params = useListParams()
  const query = useAuditLogs({
    page: params.page,
    pageSize: params.pageSize,
    resource: params.filter('resource') || undefined,
    action: params.filter('action') || undefined,
  })

  return (
    <ListPage
      title="审计日志"
      description="谁在什么时候动了什么。只增不改不删，DB 层撤销了修改权限。"
      columns={columns}
      query={query}
      params={params}
      rowKey={(row) => row.id}
      emptyMessage="这段时间没有操作记录"
      filters={filterSpecs}
      expandable={(row) => <AuditDetail row={row} />}
    />
  )
}

/**
 * 一条审计记录展开后的样子。
 *
 * 用行内展开而不是抽屉：这些字段是**同构的补充信息**，展开两条能直接上下对比
 * ——「同一个请求前后两次改了什么」正是查审计时最常干的事（DECISIONS.md §7.6）。
 */
function AuditDetail({ row }: { row: AuditEntry }) {
  const hasDetail = Object.keys(row.detail ?? {}).length > 0
  return (
    <div className="space-y-4">
      <dl className="grid gap-x-10 gap-y-3 sm:grid-cols-3">
        <Field label="请求 ID">
          <code>{row.request_id || '—'}</code>
        </Field>
        <Field label="操作对象">
          <code>{row.resource_id ?? '—'}</code>
        </Field>
        <Field label="User-Agent">
          <span className="break-all">{row.user_agent || '—'}</span>
        </Field>
      </dl>

      <div className="space-y-1.5">
        <p className="text-xs font-medium text-muted-foreground">参数摘要</p>
        {hasDetail ? (
          <pre className="overflow-x-auto rounded-lg border border-border-subtle bg-card p-3 font-mono text-sm">
            {JSON.stringify(row.detail, null, 2)}
          </pre>
        ) : (
          <p className="text-muted-foreground">这次操作没有参数</p>
        )}
      </div>
    </div>
  )
}

function Field({ label, children }: { label: string; children: React.ReactNode }) {
  return (
    <div className="min-w-0">
      <dt className="text-xs font-medium text-muted-foreground">{label}</dt>
      <dd className="mt-0.5 min-w-0">{children}</dd>
    </div>
  )
}
