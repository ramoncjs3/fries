import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { toast } from 'sonner'

import {
  createRole,
  deleteRole,
  getRole,
  listPermissionCatalog,
  listRoles,
  updateRole,
} from '@/features/role/api'
import type { RoleInput, RoleQuery } from '@/features/role/types'

export const roleKeys = {
  all: ['role'] as const,
  list: (params: RoleQuery) => [...roleKeys.all, 'list', params] as const,
  detail: (id: string) => [...roleKeys.all, 'detail', id] as const,
  catalog: ['role', 'permission-catalog'] as const,
}

export function useRoles(params: RoleQuery) {
  return useQuery({ queryKey: roleKeys.list(params), queryFn: () => listRoles(params) })
}

/** 角色详情。id 为空时不发请求（弹窗没打开就不该查）。 */
export function useRole(id: string | undefined) {
  return useQuery({
    queryKey: roleKeys.detail(id ?? ''),
    queryFn: () => getRole(id as string),
    enabled: Boolean(id),
  })
}

/**
 * 权限点目录。**只会随发版变**，所以设了很长的 staleTime ——
 * 每开一次弹窗都重新拉一遍没有意义。
 */
export function usePermissionCatalog(enabled: boolean) {
  return useQuery({
    queryKey: roleKeys.catalog,
    queryFn: listPermissionCatalog,
    enabled,
    staleTime: 30 * 60 * 1000,
  })
}

export function useRoleMutations() {
  const queryClient = useQueryClient()

  function invalidate() {
    return queryClient.invalidateQueries({ queryKey: roleKeys.all })
  }

  const create = useMutation({
    mutationFn: (input: RoleInput) => createRole(input),
    onSuccess: async () => {
      toast.success('角色已创建')
      await invalidate()
    },
  })

  const update = useMutation({
    mutationFn: (args: { id: string; version: number; input: RoleInput }) =>
      updateRole(args.id, args.version, args.input),
    onSuccess: async () => {
      toast.success('角色已保存')
      await invalidate()
    },
  })

  const remove = useMutation({
    mutationFn: (args: { id: string; version: number }) => deleteRole(args.id, args.version),
    onSuccess: async () => {
      toast.success('角色已删除')
      await invalidate()
    },
    onError: (error) => toast.error(error.message),
  })

  return { create, update, remove }
}
