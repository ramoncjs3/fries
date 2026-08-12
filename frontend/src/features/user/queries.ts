import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { toast } from 'sonner'

import {
  createUser,
  deleteUser,
  getUser,
  listUsers,
  resetUserPassword,
  updateUser,
} from '@/features/user/api'
import type { UserInput, UserQuery } from '@/features/user/types'

export const userKeys = {
  all: ['user'] as const,
  list: (params: UserQuery) => [...userKeys.all, 'list', params] as const,
  detail: (id: string) => [...userKeys.all, 'detail', id] as const,
}

export function useUsers(params: UserQuery) {
  return useQuery({ queryKey: userKeys.list(params), queryFn: () => listUsers(params) })
}

/** 用户详情。id 为空时不发请求。 */
export function useUser(id: string | undefined) {
  return useQuery({
    queryKey: userKeys.detail(id ?? ''),
    queryFn: () => getUser(id as string),
    enabled: Boolean(id),
  })
}

export function useUserMutations() {
  const queryClient = useQueryClient()

  function invalidate() {
    return queryClient.invalidateQueries({ queryKey: userKeys.all })
  }

  const create = useMutation({
    mutationFn: (input: UserInput) => createUser(input),
    // 这里**不弹 toast**：初始密码要用弹窗展示并让人复制，toast 一闪就没了
    onSuccess: () => invalidate(),
  })

  const update = useMutation({
    mutationFn: (args: { id: string; version: number; input: UserInput }) =>
      updateUser(args.id, args.version, args.input),
    onSuccess: async () => {
      toast.success('用户已保存')
      await invalidate()
    },
  })

  const remove = useMutation({
    mutationFn: (args: { id: string; version: number }) => deleteUser(args.id, args.version),
    onSuccess: async () => {
      toast.success('用户已删除')
      await invalidate()
    },
    onError: (error) => toast.error(error.message),
  })

  const resetPassword = useMutation({
    mutationFn: (id: string) => resetUserPassword(id),
    onSuccess: () => invalidate(),
    onError: (error) => toast.error(error.message),
  })

  return { create, update, remove, resetPassword }
}
