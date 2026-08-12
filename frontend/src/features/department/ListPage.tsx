import { FolderInput, Plus, Search, UserMinus, UserPlus, UserRoundX } from 'lucide-react'
import { useEffect, useMemo, useState, type ReactNode } from 'react'

import { ConfirmDialog } from '@/components/ConfirmDialog'
import { FormDialog } from '@/components/FormDialog'
import { PageHeader } from '@/components/PageHeader'
import { EmptyState, ErrorState, LoadingRows } from '@/components/PageState'
import { RowActions } from '@/components/RowActions'
import { TreePanel, type TreeNodeData } from '@/components/TreePanel'
import { Button } from '@/components/ui/button'
import { Checkbox } from '@/components/ui/checkbox'
import { Input } from '@/components/ui/input'
import { DepartmentDetailPane } from '@/features/department/DetailPage'
import { MoveToDepartmentDialog } from '@/features/department/FormDialog'
import { DepartmentNewPane } from '@/features/department/NewPage'
import {
  useDepartmentCandidates,
  useDepartmentMutations,
  useDepartments,
} from '@/features/department/queries'
import { buildTree, type DepartmentNode } from '@/features/department/types'
import { useUsers } from '@/features/user/queries'
import { UNASSIGNED_DEPARTMENT, type User } from '@/features/user/types'
import { useConfirm } from '@/lib/confirm'
import { useDetailParam } from '@/lib/detail-param'
import { useSession } from '@/lib/session'
import { cn } from '@/lib/utils'

/** 「未分配」在 URL 上的取值。它不是一个真部门，是树外面的一个入口。 */
const UNASSIGNED = '__unassigned__'

/** 在树里按 id 找一个节点。 */
function findNode(nodes: DepartmentNode[], id: string): DepartmentNode | undefined {
  for (const node of nodes) {
    if (node.id === id) return node
    const hit = findNode(node.children, id)
    if (hit) return hit
  }
  return undefined
}

/**
 * 部门管理：**左树右详情**。
 *
 * 为什么不用 `<ListPage>`（§7.1 说新模块只能用三个骨架）：组织结构的重点是
 * **层级关系**和「这个部门都有谁」，表格两样都表达不好 —— 缩进看不出父子，
 * 成员更是完全看不到。这是钉钉/飞书/企业微信都在用的形态，
 * 也是 fries 里「树形模块」的样板（DECISIONS.md §7.6）。
 */
export default function DepartmentListPage() {
  const { can } = useSession()
  const confirm = useConfirm()
  const selection = useDetailParam()

  const [keyword, setKeyword] = useState('')
  const query = useDepartments({ keyword: keyword || undefined })

  const all = useMemo(() => query.data?.items ?? [], [query.data])
  const tree = useMemo(() => buildTree(all), [all])

  // 从**树**里找而不是扁平表：只有树节点上才有 total_count（含下级人数）
  const selected = useMemo(() => findNode(tree, selection.id), [tree, selection.id])

  // 没选中就默认选第一个 —— 右边空着不如直接给内容
  useEffect(() => {
    if (!selection.id && tree.length > 0) selection.setID(tree[0].id)
  }, [selection, tree])

  /**
   * 右侧面板是「看」「改」还是「新建」，存在 URL 上（`?pane=`）。
   *
   * 和别的模块的 `?edit=1` 是同一套思路：刷新还在这个模式、链接发得出去。
   * 新建时把默认上级也带上（`?pane=new&under=<id>`），空的就是建一级部门。
   */
  const pane = selection.params.get('pane') ?? ''
  const creatingUnder = selection.params.get('under') ?? ''

  function setPane(next: '' | 'edit' | 'new', under?: string) {
    selection.update(
      (params) => {
        if (next) params.set('pane', next)
        else params.delete('pane')
        if (next === 'new' && under) params.set('under', under)
        else params.delete('under')
      },
      // 换模式不堆历史，和整页详情的 `?edit=1` 保持一致
      { replace: true },
    )
  }

  // 没分配部门的人有多少 —— 用来在树底下挂一个入口
  const unassigned = useUsers({ page: 1, pageSize: 1, departmentIds: [UNASSIGNED_DEPARTMENT] })

  const nodes: TreeNodeData[] = useMemo(() => {
    const toNode = (node: DepartmentNode): TreeNodeData => ({
      id: node.id,
      label: (
        <span className={node.status === 'active' ? undefined : 'text-muted-foreground line-through'}>
          {node.name}
        </span>
      ),
      // 树上显示的是**含下级的合计** —— 扫一眼是想知道「这条线有多少人」，
      // 而不是「这一层挂了几个」。精确的拆分在右边的详情里给。
      meta: node.total_count > 0 ? node.total_count : undefined,
      children: node.children.map(toNode),
    })
    return tree.map(toNode)
  }, [tree])

  return (
    <>
      <div className="flex min-h-0 flex-1 flex-col overflow-hidden px-8 py-8">
        <PageHeader
          title="部门管理"
          description="组织结构。停用的部门不影响已经在里面的人，只是新建时选不到。"
          actions={
            can('department', 'create') ? (
              <Button onClick={() => setPane('new')}>
                <Plus /> 新增部门
              </Button>
            ) : null
          }
        />

        <div className="flex min-h-0 flex-1 gap-5">
          {query.isPending ? (
            <div className="surface flex-1">
              <LoadingRows rows={6} />
            </div>
          ) : query.isError ? (
            <div className="surface flex-1">
              <ErrorState error={query.error} onRetry={() => void query.refetch()} />
            </div>
          ) : (
            <>
              <TreePanel
                nodes={nodes}
                selectedID={selection.id}
                onSelect={selection.setID}
                searchPlaceholder="搜索部门"
                onSearch={setKeyword}
                empty="还没有部门，点右上角新增一个"
                footer={
                  /*
                   * 「未分配」不是一个真部门，是树外面的一个入口。
                   *
                   * **不建一个「公司」根部门把所有人塞进去**：那样每个人都得挂在它下面，
                   * 而「谁还没分部门」这个问题反而更查不出来了 —— 大家都在根节点上，
                   * 分没分过看不出来。真正要回答的是「有没有人还没被安排」，
                   * 所以这里直接给一个按 department_id IS NULL 查的入口。
                   */
                  <button
                    type="button"
                    onClick={() => selection.setID(UNASSIGNED)}
                    className={cn(
                      'flex h-9 w-full items-center gap-2 rounded-lg px-2 transition-colors',
                      selection.id === UNASSIGNED
                        ? 'bg-accent-subtle font-medium text-accent-text'
                        : 'text-muted-foreground hover:bg-secondary hover:text-foreground',
                    )}
                  >
                    <UserRoundX className="size-4 shrink-0" />
                    <span className="flex-1 text-left">未分配部门</span>
                    {unassigned.data?.pagination.total ? (
                      <span className="shrink-0">{unassigned.data.pagination.total}</span>
                    ) : null}
                  </button>
                }
              />

              <div className="surface flex min-w-0 flex-1 flex-col overflow-hidden">
                {pane === 'new' ? (
                  <DepartmentNewPane
                    // 换一个上级就重挂，让表单带着新的默认值初始化
                    key={`new-${creatingUnder}`}
                    parentID={creatingUnder}
                    all={all}
                    onCreated={(id) => selection.setID(id)}
                    onCancel={() => setPane('')}
                  />
                ) : selection.id === UNASSIGNED ? (
                  <UnassignedPanel />
                ) : selected ? (
                  <DepartmentDetailPane
                    // 进编辑态时重挂，表单才吃得到当时最新的数据（docs/MEMORY.md）
                    key={`${selected.id}-${pane}`}
                    department={selected}
                    all={all}
                    editing={pane === 'edit'}
                    onEditingChange={(next) => setPane(next ? 'edit' : '')}
                    onAddChild={() => setPane('new', selected.id)}
                    onDeleted={() => selection.close()}
                  >
                    <DepartmentMembers department={selected} />
                  </DepartmentDetailPane>
                ) : (
                  <EmptyState message="左边选一个部门" />
                )}
              </div>
            </>
          )}
        </div>
      </div>

      <ConfirmDialog state={confirm} />
    </>
  )
}

/**
 * 选中部门的**可操作成员区**。渲染在详情面板里（`DetailPage.tsx` 的 children）。
 *
 * 不是只读清单：加人、移人是部门管理最高频的动作，
 * 让人绕到用户管理去一个个改部门是不能接受的。
 */
function DepartmentMembers({ department }: { department: DepartmentNode }) {
  const { can } = useSession()
  const confirm = useConfirm()
  const { removeMembers } = useDepartmentMutations()

  // 看成员要有 user:list —— 没有就整块不渲染，不是灰掉（DECISIONS.md §3.4）
  const canSeeMembers = can('user', 'list')
  const canMoveMembers = can('user', 'update')
  const [pickerOpen, setPickerOpen] = useState(false)

  const members = useUsers({
    page: 1,
    pageSize: 50,
    departmentIds: canSeeMembers ? [department.id] : undefined,
  })
  const users = members.data?.items ?? []
  const selection = usePicked(users)
  const [moving, setMoving] = useState<string[]>([])

  if (!canSeeMembers) return null

  return (
    <section className="space-y-2">
      <div className="flex items-center justify-between gap-3">
        <h2 className="section-label">
          直属成员 {department.user_count}
          {/* 有下级时把合计也标出来：不然「技术部 3 人」和树上的 5 对不上，
              人会以为数字算错了 */}
          {department.total_count !== department.user_count ? (
            <span className="ml-2 font-normal tracking-normal text-muted-foreground">
              含下级 {department.total_count}
            </span>
          ) : null}
        </h2>
        <div className="flex items-center gap-2">
          {canMoveMembers && selection.list.length > 0 ? (
            <Button variant="outline" size="sm" onClick={() => setMoving(selection.list)}>
              <FolderInput /> 移至其他部门（{selection.list.length}）
            </Button>
          ) : null}
          {canMoveMembers ? (
            <Button variant="outline" size="sm" onClick={() => setPickerOpen(true)}>
              <UserPlus /> 添加成员
            </Button>
          ) : null}
        </div>
      </div>

      <PeopleList
        users={users}
        loading={members.isPending}
        empty="还没有人在这个部门。下级部门的成员不算在这里。"
        picked={selection.picked}
        onTogglePick={selection.toggle}
        onToggleAll={selection.toggleAll}
        selectable={canMoveMembers}
        rowAction={
          canMoveMembers
            ? (user) => (
                <RowActions
                  actions={[
                    {
                      key: 'move',
                      label: '移至其他部门',
                      icon: <FolderInput />,
                      onSelect: () => setMoving([user.id]),
                    },
                    {
                      key: 'remove',
                      label: '移出部门',
                      icon: <UserMinus />,
                      danger: true,
                      onSelect: () =>
                        confirm.open({
                          title: `把「${user.display_name}」移出${department.name}？`,
                          description: '移出后 TA 不属于任何部门，账号本身不受影响。',
                          confirmText: '移出',
                          onConfirm: () =>
                            removeMembers.mutateAsync({ id: department.id, userIDs: [user.id] }),
                        }),
                    },
                  ]}
                />
              )
            : undefined
        }
      />

      <MemberPicker
        departmentID={department.id}
        departmentName={department.name}
        open={pickerOpen}
        onOpenChange={setPickerOpen}
      />
      <MoveToDepartmentDialog
        userIDs={moving}
        onClose={() => setMoving([])}
        onDone={() => {
          selection.clear()
          setMoving([])
        }}
      />
      <ConfirmDialog state={confirm} />
    </section>
  )
}

/** 「添加成员」弹窗：搜人、勾选、一次加一批。 */
function MemberPicker({
  departmentID,
  departmentName,
  open,
  onOpenChange,
}: {
  departmentID: string
  departmentName: string
  open: boolean
  onOpenChange: (open: boolean) => void
}) {
  const [keyword, setKeyword] = useState('')
  const [picked, setPicked] = useState<Set<string>>(new Set())
  const { addMembers } = useDepartmentMutations()
  const candidates = useDepartmentCandidates(departmentID, keyword, open)

  // 每次打开都从干净状态开始，否则会带着上次的勾选
  useEffect(() => {
    if (open) {
      setKeyword('')
      setPicked(new Set())
    }
  }, [open])

  function toggle(id: string) {
    setPicked((current) => {
      const next = new Set(current)
      if (!next.delete(id)) next.add(id)
      return next
    })
  }

  return (
    <FormDialog
      open={open}
      onOpenChange={onOpenChange}
      title={`添加成员到「${departmentName}」`}
      description="一个人只能属于一个部门，加入即从原部门移出"
      submitText={picked.size > 0 ? `加入 ${picked.size} 人` : '加入'}
      submitting={addMembers.isPending}
      onSubmit={async () => {
        if (picked.size === 0) return
        await addMembers.mutateAsync({ id: departmentID, userIDs: [...picked] })
        onOpenChange(false)
      }}
    >
      <div className="relative">
        <Search className="pointer-events-none absolute left-3 top-1/2 size-4 -translate-y-1/2 text-muted-foreground" />
        <Input
          className="pl-9"
          placeholder="搜索用户名或显示名"
          value={keyword}
          onChange={(e) => setKeyword(e.target.value.trim())}
        />
      </div>

      {candidates.isPending ? (
        <LoadingRows rows={4} />
      ) : candidates.data?.items.length ? (
        <ul className="rounded-lg border border-border-subtle">
          {candidates.data.items.map((user) => (
            <li key={user.id} className="border-b border-border-subtle last:border-b-0">
              <label className="flex cursor-pointer items-center gap-3 px-3 py-2.5 hover:bg-secondary">
                <Checkbox checked={picked.has(user.id)} onCheckedChange={() => toggle(user.id)} />
                <span className="min-w-0 flex-1">
                  <span className="block truncate font-medium">{user.display_name}</span>
                  <span className="block truncate text-muted-foreground">
                    <code>{user.username}</code>
                  </span>
                </span>
                {/* 已经在别的部门就说清楚，免得加完才发现把人从别处挖走了 */}
                {user.department_name ? (
                  <span className="shrink-0 text-muted-foreground">现属 {user.department_name}</span>
                ) : null}
              </label>
            </li>
          ))}
        </ul>
      ) : (
        <p className="py-6 text-center text-muted-foreground">
          {keyword ? '没有匹配的人' : '所有活跃用户都已在这个部门里'}
        </p>
      )}
    </FormDialog>
  )
}

/**
 * 人员列表。**直属成员**和**未分配**两处共用 —— 它们要干的事情是一样的
 * （看是谁、勾一批、调到别的部门去），没理由长得不一样。
 */
function PeopleList({
  users,
  loading,
  empty,
  picked,
  onTogglePick,
  onToggleAll,
  selectable,
  rowAction,
}: {
  users: User[]
  loading: boolean
  empty: ReactNode
  picked: Set<string>
  onTogglePick: (id: string) => void
  onToggleAll: () => void
  selectable: boolean
  rowAction?: (user: User) => ReactNode
}) {
  if (loading) return <LoadingRows rows={3} />
  if (users.length === 0) {
    return (
      <p className="rounded-lg border border-dashed border-border px-3 py-8 text-center text-muted-foreground">
        {empty}
      </p>
    )
  }

  const allPicked = users.every((u) => picked.has(u.id))

  return (
    <ul className="rounded-lg border border-border-subtle">
      {selectable ? (
        <li className="flex items-center gap-3 border-b border-border-subtle bg-muted/40 px-3 py-2">
          <Checkbox
            aria-label="全选"
            checked={allPicked ? true : users.some((u) => picked.has(u.id)) ? 'indeterminate' : false}
            onCheckedChange={onToggleAll}
          />
          <span className="text-muted-foreground">全选</span>
        </li>
      ) : null}

      {users.map((user) => (
        <li
          key={user.id}
          className="flex items-center gap-3 border-b border-border-subtle px-3 py-2.5 last:border-b-0"
        >
          {selectable ? (
            <Checkbox
              aria-label={`选中 ${user.display_name}`}
              checked={picked.has(user.id)}
              onCheckedChange={() => onTogglePick(user.id)}
            />
          ) : null}
          <span className="grid size-7 shrink-0 place-items-center rounded-full bg-accent-subtle text-xs font-medium text-accent-text">
            {user.display_name.slice(0, 1)}
          </span>
          <span className="min-w-0 flex-1">
            <span className="block truncate font-medium">{user.display_name}</span>
            <span className="block truncate text-muted-foreground">
              <code>{user.username}</code>
              {user.role_names ? <span className="ml-2">{user.role_names}</span> : null}
            </span>
          </span>
          {rowAction?.(user)}
        </li>
      ))}
    </ul>
  )
}

/** 一组勾选状态。两个人员列表各用一份。 */
function usePicked(users: User[]) {
  const [picked, setPicked] = useState<Set<string>>(new Set())

  function toggle(id: string) {
    setPicked((current) => {
      const next = new Set(current)
      if (!next.delete(id)) next.add(id)
      return next
    })
  }

  function toggleAll() {
    setPicked((current) =>
      users.every((u) => current.has(u.id)) ? new Set() : new Set(users.map((u) => u.id)),
    )
  }

  return { picked, toggle, toggleAll, clear: () => setPicked(new Set()), list: [...picked] }
}

/** 「未分配部门」面板：把还没安排部门的人列出来，**并且就地分配**。 */
function UnassignedPanel() {
  const { can } = useSession()
  const canMove = can('user', 'update')
  const members = useUsers({ page: 1, pageSize: 50, departmentIds: [UNASSIGNED_DEPARTMENT] })
  const users = members.data?.items ?? []
  const selection = usePicked(users)
  const [moving, setMoving] = useState<string[]>([])

  return (
    <div className="flex min-h-0 flex-1 flex-col">
      <div className="flex shrink-0 items-start justify-between gap-3 border-b border-border-subtle px-6 py-4">
        <div className="min-w-0">
          <h2 className="text-xl">未分配部门</h2>
          <p className="text-muted-foreground">这些人还没被安排到任何部门</p>
        </div>
        {canMove && selection.list.length > 0 ? (
          <Button onClick={() => setMoving(selection.list)}>
            <FolderInput /> 分配 {selection.list.length} 人
          </Button>
        ) : null}
      </div>

      <div className="min-h-0 flex-1 overflow-y-auto px-6 py-5">
        <PeopleList
          users={users}
          loading={members.isPending}
          empty="所有人都已分配部门"
          picked={selection.picked}
          onTogglePick={selection.toggle}
          onToggleAll={selection.toggleAll}
          selectable={canMove}
          rowAction={
            canMove
              ? (user) => (
                  <Button variant="ghost" size="sm" onClick={() => setMoving([user.id])}>
                    分配
                  </Button>
                )
              : undefined
          }
        />
      </div>

      <MoveToDepartmentDialog
        userIDs={moving}
        title={`把 ${moving.length} 人分配到部门`}
        // 他们本来就没部门，再给个「移出部门」是废选项
        allowUnassign={false}
        onClose={() => setMoving([])}
        onDone={() => {
          selection.clear()
          setMoving([])
        }}
      />
    </div>
  )
}
