import { get, getPage, post, type PageResult } from '@/api/client'
import type { components } from '@/api/schema'

/*
 * 类型**全部从 OpenAPI 生成的 schema 里取**，不手写一遍
 * —— 真相唯一在 Go 侧（DECISIONS.md §1）。后端改了字段，这里编译期就报错。
 * 重新生成：make gen-api
 */

/** 当前用户，登录和 /me 都会返回。 */
export type Identity = components['schemas']['Identity']

/** 菜单项。**由后端按权限过滤好**，前端只渲染（DECISIONS.md §3.6）。 */
export type MenuItem = components['schemas']['MenuItem']

export type LoginResult = components['schemas']['LoginResult']

export type MeResult = components['schemas']['MeResult']

export function login(tenantCode: string, account: string, password: string) {
  return post<LoginResult>('/auth/login', { tenant_code: tenantCode, account, password })
}

export function logout() {
  return post<void>('/auth/logout')
}

export function fetchMe() {
  return get<MeResult>('/me')
}

export function changeOwnPassword(oldPassword: string, newPassword: string) {
  return post<void>('/me/password', { old_password: oldPassword, new_password: newPassword })
}

/**
 * 申请重置密码邮件。**不管账号是否存在，后端都返回成功**（防用户枚举），
 * 所以前端也一律显示同一句「如果账号存在，邮件已发出」，不做任何区分。
 */
export function requestPasswordReset(tenantCode: string, identifier: string) {
  return post<void>('/auth/forgot-password', { tenant_code: tenantCode, identifier })
}

/** 用邮件里的一次性 token 设置新密码。 */
export function resetPassword(token: string, newPassword: string) {
  return post<void>('/auth/reset-password', { token, new_password: newPassword })
}

/** 自助注册验证成功后返回的登录信息。 */
export type VerifiedResult = components['schemas']['VerifiedResult']

/**
 * 自助注册申请。需平台开启「允许自助注册」，否则后端返回 registration.disabled。
 * 邮箱/公司代码是否已存在不会在这一步暴露（防枚举）。
 */
export function register(email: string, companyName: string, code: string, password: string) {
  return post<void>('/auth/register', {
    email,
    company_name: companyName,
    code,
    password,
  })
}

/** 用注册验证邮件里的一次性 token 完成建组织，返回登录要用的公司代码。 */
export function verifyRegistration(token: string) {
  return post<VerifiedResult>('/auth/register/verify', { token })
}

/* ---------------------------------------------------------------- 平台管理端
 *
 * 平台端和租户端是**两套身份**：独立的会话、独立的 cookie（MULTI-TENANCY.md §10.1）。
 * 所以它的接口也单独一组，别和上面那几个混着用 —— 混用会出现「以为登着平台，
 * 实际调的是租户接口」这种最难查的问题。
 */

export type PlatformMeResult = components['schemas']['PlatformMeResult']
export type PlatformTenant = components['schemas']['Tenant']
export type CreatedTenant = components['schemas']['CreatedTenant']

export function platformLogin(username: string, password: string) {
  return post<LoginResult>('/platform/auth/login', { username, password })
}

export function platformLogout() {
  return post<void>('/platform/auth/logout')
}

export function fetchPlatformMe() {
  return get<PlatformMeResult>('/platform/me')
}

export function changePlatformPassword(oldPassword: string, newPassword: string) {
  return post<void>('/platform/me/password', {
    old_password: oldPassword,
    new_password: newPassword,
  })
}

export interface TenantQuery {
  page: number
  pageSize: number
  keyword?: string
  status?: string
}

export function listTenants(params: TenantQuery): Promise<PageResult<PlatformTenant>> {
  return getPage<PlatformTenant>('/platform/tenants', {
    query: {
      page: params.page,
      page_size: params.pageSize,
      keyword: params.keyword,
      status: params.status,
    },
  })
}

/** 开一个组织。返回的初始密码**只有这一次**能拿到，拿到就要交给客户。 */
export function createTenant(code: string, name: string): Promise<CreatedTenant> {
  return post<CreatedTenant>('/platform/tenants', { code, name })
}

export function setTenantStatus(id: string, status: string, version: number): Promise<PlatformTenant> {
  return post<PlatformTenant>(`/platform/tenants/${id}/status`, { status, version })
}
