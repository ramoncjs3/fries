import { del, get, getPage, post, put, type PageResult } from '@/api/client'
import type { CreatedUser, User, UserInput, UserQuery } from '@/features/user/types'

export function listUsers(params: UserQuery): Promise<PageResult<User>> {
  return getPage<User>('/users', {
    query: {
      page: params.page,
      page_size: params.pageSize,
      keyword: params.keyword,
      status: params.status,
      department_id: params.departmentIds,
    },
  })
}

/** 用户详情。**列表不带 role_ids**，编辑和看详情时才单独取。 */
export function getUser(id: string): Promise<User> {
  return get<User>(`/users/${id}`)
}

/** 新建用户。返回值里的 initial_password 只出现这一次，不会再查得到。 */
export function createUser(input: UserInput): Promise<CreatedUser> {
  return post<CreatedUser>('/users', input)
}

export function updateUser(id: string, version: number, input: UserInput): Promise<User> {
  return put<User>(`/users/${id}`, { ...input, version })
}

export function deleteUser(id: string, version: number): Promise<void> {
  return del<void>(`/users/${id}`, { version })
}

/** 重置密码。返回的临时密码同样只出现这一次。 */
export function resetUserPassword(id: string): Promise<{ password: string }> {
  return post<{ password: string }>(`/users/${id}/reset-password`)
}
