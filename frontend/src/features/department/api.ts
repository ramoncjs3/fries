import { del, getPage, post, put, type PageResult } from '@/api/client'
import type { Department, DepartmentInput, DepartmentQuery } from '@/features/department/types'
import type { User } from '@/features/user/types'

/** 查部门。**不分页** —— 树切成一页页就拼不起来了，后端一次返回全部节点。 */
export function listDepartments(params: DepartmentQuery): Promise<PageResult<Department>> {
  return getPage<Department>('/departments', {
    query: { keyword: params.keyword, status: params.status },
  })
}

export function createDepartment(input: DepartmentInput): Promise<Department> {
  return post<Department>('/departments', input)
}

/** 编辑。version 是乐观锁，取自上次读到的值（DECISIONS.md §2.4）。 */
export function updateDepartment(id: string, version: number, input: DepartmentInput): Promise<Department> {
  return put<Department>(`/departments/${id}`, { ...input, version })
}

export function deleteDepartment(id: string, version: number): Promise<void> {
  return del<void>(`/departments/${id}`, { version })
}

/** 可以加进这个部门的人：**不在**该部门里的活跃用户。 */
export function listDepartmentCandidates(id: string, keyword?: string): Promise<PageResult<User>> {
  return getPage<User>(`/departments/${id}/candidates`, { query: { keyword, limit: 50 } })
}

/** 把人加进部门。一个人只属于一个部门，加入即从原部门移出。 */
export function addDepartmentMembers(id: string, userIDs: string[]): Promise<{ affected: number }> {
  return post<{ affected: number }>(`/departments/${id}/members`, { user_ids: userIDs })
}

/** 把人移出部门。移出后不属于任何部门，账号本身不受影响。 */
export function removeDepartmentMembers(id: string, userIDs: string[]): Promise<{ affected: number }> {
  return del<{ affected: number }>(`/departments/${id}/members`, { user_ids: userIDs })
}
