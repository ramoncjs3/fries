import { del, get, getPage, post, put, type PageResult } from '@/api/client'
import type {
  CreatedKey,
  ServiceAccount,
  ServiceAccountInput,
  ServiceAccountQuery,
} from '@/features/service_account/types'

export function listServiceAccounts(
  params: ServiceAccountQuery,
): Promise<PageResult<ServiceAccount>> {
  return getPage<ServiceAccount>('/service-accounts', {
    query: {
      page: params.page,
      page_size: params.pageSize,
      keyword: params.keyword,
      status: params.status,
    },
  })
}

export function getServiceAccount(id: string): Promise<ServiceAccount> {
  return get<ServiceAccount>(`/service-accounts/${id}`)
}

/** 新建。**返回里带完整密钥，只有这一次** —— 拿到之后必须立刻展示给人。 */
export function createServiceAccount(input: ServiceAccountInput): Promise<CreatedKey> {
  return post<CreatedKey>('/service-accounts', input)
}

export function updateServiceAccount(
  id: string,
  version: number,
  input: ServiceAccountInput,
): Promise<ServiceAccount> {
  return put<ServiceAccount>(`/service-accounts/${id}`, { ...input, version })
}

/** 轮换密钥。⚠️ **旧密钥当场失效**，对接方在换上新的之前会一直 401。 */
export function rotateServiceAccountKey(id: string): Promise<CreatedKey> {
  return post<CreatedKey>(`/service-accounts/${id}/rotate-key`)
}

export function deleteServiceAccount(id: string, version: number): Promise<void> {
  return del<void>(`/service-accounts/${id}`, { version })
}
