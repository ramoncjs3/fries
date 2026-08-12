import { del, get, getPage, post, put, type PageResult } from '@/api/client'
import type { PermissionModule, Role, RoleInput, RoleQuery } from '@/features/role/types'

export function listRoles(params: RoleQuery): Promise<PageResult<Role>> {
  return getPage<Role>('/roles', {
    query: {
      page: params.page,
      page_size: params.pageSize,
      keyword: params.keyword,
      status: params.status,
    },
  })
}

/** 角色详情。**列表不带权限点**，要勾选树的时候才单独取。 */
export function getRole(id: string): Promise<Role> {
  return get<Role>(`/roles/${id}`)
}

/**
 * 权限点目录。**来源是后端的权限点注册表**，前端不许自己维护一份 ——
 * 那样每加一个模块都要记得回来补，必然会漏（DECISIONS.md §3.1）。
 */
export function listPermissionCatalog(): Promise<PermissionModule[]> {
  return get<PermissionModule[]>('/roles/permission-catalog')
}

export function createRole(input: RoleInput): Promise<Role> {
  return post<Role>('/roles', input)
}

export function updateRole(id: string, version: number, input: RoleInput): Promise<Role> {
  return put<Role>(`/roles/${id}`, { ...input, version })
}

export function deleteRole(id: string, version: number): Promise<void> {
  return del<void>(`/roles/${id}`, { version })
}
