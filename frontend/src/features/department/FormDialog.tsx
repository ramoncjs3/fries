import { useEffect, useState } from 'react'

import { FormDialog } from '@/components/FormDialog'
import { FormField } from '@/components/FormField'
import { SelectField } from '@/components/ui/select'
import { useDepartmentMutations, useDepartments } from '@/features/department/queries'

/** 「移出部门」在下拉里的取值。空串在 Radix Select 里是保留值（表示未选），只能另找一个。 */
const UNASSIGN_VALUE = '__unassign__'

/**
 * 「把这些人调到哪个部门」弹窗。
 *
 * 用户管理的批量调岗、部门页的「移至其他部门」「分配到部门」都用它 ——
 * 这三处问的是同一个问题，不该有三套长得不一样的界面。
 */
export function MoveToDepartmentDialog({
  userIDs,
  title,
  /** 允许「移出部门」这个选项。从「未分配」那边过来时没意义，就关掉。 */
  allowUnassign = true,
  onClose,
  onDone,
}: {
  userIDs: string[]
  title?: string
  allowUnassign?: boolean
  onClose: () => void
  onDone: () => void
}) {
  const departments = useDepartments({})
  const { addMembers, removeMembers } = useDepartmentMutations()
  const [target, setTarget] = useState('')

  const open = userIDs.length > 0

  // 每次打开都清掉上次选的目标，否则会带着上一次的选择
  useEffect(() => {
    if (open) setTarget('')
  }, [open])

  return (
    <FormDialog
      open={open}
      onOpenChange={(next) => (next ? undefined : onClose())}
      title={title ?? `调整 ${userIDs.length} 人的部门`}
      description="一个人只能属于一个部门，调整即从原部门移出"
      submitText="确定"
      submitting={addMembers.isPending || removeMembers.isPending}
      onSubmit={async () => {
        if (!target) return
        if (target === UNASSIGN_VALUE) {
          // 移出走「从原部门删掉」。路径上的部门 ID 只是占位，
          // 真正生效的是把 department_id 置空。
          await removeMembers.mutateAsync({ id: departments.data?.items[0]?.id ?? '', userIDs })
        } else {
          await addMembers.mutateAsync({ id: target, userIDs })
        }
        onDone()
      }}
    >
      <FormField label="目标部门" required>
        <SelectField
          value={target}
          onChange={setTarget}
          placeholder="选一个部门"
          options={[
            ...(allowUnassign ? [{ value: UNASSIGN_VALUE, label: '（移出部门）' }] : []),
            ...(departments.data?.items ?? [])
              .filter((d) => d.status === 'active')
              .map((d) => ({ value: d.id, label: d.name, hint: d.code })),
          ]}
        />
      </FormField>
    </FormDialog>
  )
}
