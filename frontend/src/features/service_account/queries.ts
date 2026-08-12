import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { toast } from 'sonner'

import {
  createServiceAccount,
  deleteServiceAccount,
  getServiceAccount,
  listServiceAccounts,
  rotateServiceAccountKey,
  updateServiceAccount,
} from '@/features/service_account/api'
import type {
  ServiceAccountInput,
  ServiceAccountQuery,
} from '@/features/service_account/types'

export const serviceAccountKeys = {
  all: ['service-account'] as const,
  list: (params: ServiceAccountQuery) => [...serviceAccountKeys.all, 'list', params] as const,
  detail: (id: string) => [...serviceAccountKeys.all, 'detail', id] as const,
}

export function useServiceAccounts(params: ServiceAccountQuery) {
  return useQuery({
    queryKey: serviceAccountKeys.list(params),
    queryFn: () => listServiceAccounts(params),
  })
}

export function useServiceAccount(id: string | undefined) {
  return useQuery({
    queryKey: serviceAccountKeys.detail(id ?? ''),
    queryFn: () => getServiceAccount(id as string),
    enabled: Boolean(id),
  })
}

export function useServiceAccountMutations() {
  const queryClient = useQueryClient()

  function invalidate() {
    return queryClient.invalidateQueries({ queryKey: serviceAccountKeys.all })
  }

  const create = useMutation({
    mutationFn: (input: ServiceAccountInput) => createServiceAccount(input),
    // 不在这里 toast：新建成功之后要弹「密钥只显示一次」的框，
    // 再飘一条绿色提示会让人以为事情已经办完、顺手把框关掉。
    onSuccess: () => invalidate(),
  })

  const update = useMutation({
    mutationFn: (args: { id: string; version: number; input: ServiceAccountInput }) =>
      updateServiceAccount(args.id, args.version, args.input),
    onSuccess: async () => {
      toast.success('已保存')
      await invalidate()
    },
  })

  const rotate = useMutation({
    mutationFn: (id: string) => rotateServiceAccountKey(id),
    // 同 create：密钥要弹框展示，不 toast
    onSuccess: () => invalidate(),
    onError: (error) => toast.error(error.message),
  })

  const remove = useMutation({
    mutationFn: (args: { id: string; version: number }) =>
      deleteServiceAccount(args.id, args.version),
    onSuccess: async () => {
      toast.success('已删除，它的密钥立刻失效')
      await invalidate()
    },
    onError: (error) => toast.error(error.message),
  })

  return { create, update, rotate, remove }
}
