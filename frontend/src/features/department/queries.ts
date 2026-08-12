import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { toast } from 'sonner'

import {
  addDepartmentMembers,
  createDepartment,
  deleteDepartment,
  listDepartmentCandidates,
  listDepartments,
  removeDepartmentMembers,
  updateDepartment,
} from '@/features/department/api'
import type { DepartmentInput, DepartmentQuery } from '@/features/department/types'

/** 查询 key 统一在这里拼，失效缓存时才不会写错。 */
export const departmentKeys = {
  all: ['department'] as const,
  list: (params: DepartmentQuery) => [...departmentKeys.all, 'list', params] as const,
  candidates: (id: string, keyword: string) =>
    [...departmentKeys.all, 'candidates', id, keyword] as const,
}

/** 「添加成员」弹窗的候选人。弹窗没开就不查。 */
export function useDepartmentCandidates(id: string, keyword: string, enabled: boolean) {
  return useQuery({
    queryKey: departmentKeys.candidates(id, keyword),
    queryFn: () => listDepartmentCandidates(id, keyword || undefined),
    enabled: enabled && Boolean(id),
  })
}

export function useDepartments(params: DepartmentQuery) {
  return useQuery({
    queryKey: departmentKeys.list(params),
    queryFn: () => listDepartments(params),
  })
}

/**
 * 增删改共用一个 hook。
 *
 * 三个操作失效的缓存完全一样，分开写只会出现「改完刷新了、删完忘了刷新」这种
 * 不一致。**失效整个 department 命名空间**而不是某一个 key —— 筛选条件不同的
 * 列表是不同的 key，只失效当前这个的话，切回上一个筛选看到的还是旧数据。
 */
export function useDepartmentMutations() {
  const queryClient = useQueryClient()

  function invalidate() {
    return queryClient.invalidateQueries({ queryKey: departmentKeys.all })
  }

  const create = useMutation({
    mutationFn: (input: DepartmentInput) => createDepartment(input),
    onSuccess: async () => {
      toast.success('部门已创建')
      await invalidate()
    },
  })

  const update = useMutation({
    mutationFn: (args: { id: string; version: number; input: DepartmentInput }) =>
      updateDepartment(args.id, args.version, args.input),
    onSuccess: async () => {
      toast.success('部门已保存')
      await invalidate()
    },
  })

  const remove = useMutation({
    mutationFn: (args: { id: string; version: number }) => deleteDepartment(args.id, args.version),
    onSuccess: async () => {
      toast.success('部门已删除')
      await invalidate()
    },
    // 删除的失败原因是业务规则（下面还有人/还有子部门），必须让人看见
    onError: (error) => toast.error(error.message),
  })

  // 调成员改的是 users 表，所以**两个命名空间都要失效** ——
  // 只失效 department 的话，用户列表里的「部门」列还是旧值。
  async function invalidateBoth() {
    await Promise.all([
      invalidate(),
      queryClient.invalidateQueries({ queryKey: ['user'] }),
    ])
  }

  const addMembers = useMutation({
    mutationFn: (args: { id: string; userIDs: string[] }) => addDepartmentMembers(args.id, args.userIDs),
    onSuccess: async (result) => {
      toast.success(`已加入 ${result.affected} 人`)
      await invalidateBoth()
    },
    onError: (error) => toast.error(error.message),
  })

  const removeMembers = useMutation({
    mutationFn: (args: { id: string; userIDs: string[] }) => removeDepartmentMembers(args.id, args.userIDs),
    onSuccess: async (result) => {
      toast.success(`已移出 ${result.affected} 人`)
      await invalidateBoth()
    },
    onError: (error) => toast.error(error.message),
  })

  return { create, update, remove, addMembers, removeMembers }
}
