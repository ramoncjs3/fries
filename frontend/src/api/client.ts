/**
 * **全站唯一的请求出口**（DECISIONS.md §7.1）。
 *
 * 页面不许裸调 fetch（ESLint 拦着）。所有请求都从这里走，才有一处地方统一处理：
 * cookie 携带、CSRF 头、错误码翻译、401 跳登录、响应封套解包。
 */

import type { ErrorCode } from './errorCodes'

/**
 * CSRF token 所在的 cookie 名，和后端 auth.SessionConfig.CSRFCookieName() 对应。
 *
 * ⚠️ **两套**：平台管理端有自己的会话和 cookie（MULTI-TENANCY.md §10.1），
 * cookie 名必须不同 —— 同名的话，一个人在同一浏览器里登了平台又登租户，
 * 后登的会把先登的顶掉。
 *
 * 后果就是这里也要分：拿租户的 CSRF token 去请求平台接口，后端验不过，
 * 用户看到「请求校验失败，请刷新页面」，刷新多少次都没用（浏览器实测踩过）。
 */
const CSRF_COOKIE = 'fries_session_csrf'
const PLATFORM_CSRF_COOKIE = 'fries_session_platform_csrf'
const CSRF_HEADER = 'X-CSRF-Token'

/** 接口前缀。开发时 vite 代理到后端，生产同源。 */
const API_PREFIX = '/api/v1'

/** 单对象响应的封套（后端 httpx.Data）。 */
interface DataEnvelope<T> {
  data: T
  request_id: string
}

/** 分页信息（后端 httpx.Pagination）。 */
export interface Pagination {
  page: number
  page_size: number
  total: number
}

/** 列表响应的封套（后端 httpx.Page）。 */
interface PageEnvelope<T> {
  data: T[]
  pagination: Pagination
  request_id: string
}

/** 分页结果，页面组件拿到的形态。 */
export interface PageResult<T> {
  items: T[]
  pagination: Pagination
}

/** RFC 9457 错误响应里的字段级错误。 */
export interface FieldError {
  location?: string
  message: string
  value?: unknown
}

/** 后端返回的 RFC 9457 错误体（DECISIONS.md §4.3）。 */
interface Problem {
  type: string
  title: string
  status: number
  detail?: string
  code: string
  request_id?: string
  errors?: FieldError[]
}

/**
 * ApiError 是页面唯一需要认识的错误类型。
 *
 * 判断错误只看 `code`（机器可判），给用户看的文案用 `detail`（后端给的中文）。
 */
export class ApiError extends Error {
  // code 收成联合类型：页面里 `err.code === 'xxx'` 打错一个字母，tsc 当场报，
  // 不再静默不匹配（DECISIONS.md §4.7.3）。联合由 make errcodes-ts 从后端注册表生成。
  readonly code: ErrorCode
  readonly status: number
  readonly requestId: string
  readonly fieldErrors: FieldError[]

  constructor(problem: Problem) {
    super(problem.detail || problem.title || '请求失败')
    this.name = 'ApiError'
    // 线上传过来的 code 是裸 string（可能是前端还不认识的新码），在这道边界收一次窄。
    this.code = problem.code as ErrorCode
    this.status = problem.status
    this.requestId = problem.request_id ?? ''
    this.fieldErrors = problem.errors ?? []
  }

  /** 把字段级错误转成 react-hook-form 能直接 setError 的形式。 */
  formErrors(): Array<{ field: string; message: string }> {
    return this.fieldErrors
      .filter((e) => e.location?.startsWith('body.'))
      .map((e) => ({ field: e.location!.slice('body.'.length), message: e.message }))
  }
}

/** 网络层直接失败（断网、超时）时也包成 ApiError，页面只处理一种错误。 */
function networkError(cause: unknown): ApiError {
  return new ApiError({
    type: 'about:blank',
    title: 'Network Error',
    status: 0,
    code: 'common.network_error',
    detail: `连不上服务器（${cause instanceof Error ? cause.message : String(cause)}）`,
  })
}

function readCookie(name: string): string {
  const prefix = `${name}=`
  for (const part of document.cookie.split('; ')) {
    if (part.startsWith(prefix)) return decodeURIComponent(part.slice(prefix.length))
  }
  return ''
}

/** 未认证时的回调，由 App 注册：跳登录页。放这里是为了不让 client 依赖路由。 */
let onUnauthenticated: (() => void) | null = null

export function setUnauthenticatedHandler(handler: () => void) {
  onUnauthenticated = handler
}

interface RequestOptions {
  method?: string
  /** 请求体，会被 JSON 序列化。 */
  body?: unknown
  /**
   * query 参数，值为 undefined / null / '' 的自动丢掉。
   *
   * 数组会展开成同名的多个参数（`?id=a&id=b`）——
   * 后端（huma）的多值 query 就是这么收的，不是逗号分隔。
   */
  query?: Record<string, string | number | boolean | undefined | null | string[]>
  signal?: AbortSignal
}

function buildURL(path: string, query?: RequestOptions['query']): string {
  const url = new URL(API_PREFIX + path, window.location.origin)
  for (const [key, value] of Object.entries(query ?? {})) {
    if (value === undefined || value === null || value === '') continue
    if (Array.isArray(value)) {
      // append 不是 set —— set 会把前一个同名参数覆盖掉，只剩最后一个
      value.forEach((item) => url.searchParams.append(key, item))
      continue
    }
    url.searchParams.set(key, String(value))
  }
  return url.pathname + url.search
}

async function request<T>(path: string, options: RequestOptions = {}): Promise<T> {
  const method = options.method ?? 'GET'
  const headers: Record<string, string> = {}

  if (options.body !== undefined) {
    headers['Content-Type'] = 'application/json'
  }
  // 写请求带上 CSRF token：值从非 httpOnly 的 cookie 里读，放进请求头
  // —— 跨站请求带得上 cookie，但读不到它的值，也就凑不出这个头（DECISIONS.md §6）。
  if (!['GET', 'HEAD', 'OPTIONS'].includes(method)) {
    // 按**请求的目标接口**挑 cookie，不是按当前页面 —— 登录那一刻页面还在
    // /platform/login 上，但请求打的就是平台接口，两者恰好一致；
    // 而将来若有页面同时调两边，按目标挑才是对的。
    const token = readCookie(path.startsWith('/platform') ? PLATFORM_CSRF_COOKIE : CSRF_COOKIE)
    if (token) headers[CSRF_HEADER] = token
  }

  let response: Response
  try {
    response = await fetch(buildURL(path, options.query), {
      method,
      headers,
      // 会话在 httpOnly cookie 里，必须带上
      credentials: 'same-origin',
      body: options.body === undefined ? undefined : JSON.stringify(options.body),
      signal: options.signal,
    })
  } catch (cause) {
    throw networkError(cause)
  }

  if (response.status === 204) {
    return undefined as T
  }

  const text = await response.text()
  let payload: unknown = null
  if (text) {
    try {
      payload = JSON.parse(text)
    } catch {
      payload = null
    }
  }

  if (!response.ok) {
    const problem = (payload ?? {
      type: 'about:blank',
      title: response.statusText,
      status: response.status,
      code: 'common.internal_error',
    }) as Problem
    const error = new ApiError(problem)

    // 会话没了就回登录页。这一处集中处理，页面不用各自判断。
    if (error.status === 401) {
      onUnauthenticated?.()
    }
    throw error
  }

  return payload as T
}

/** GET 单对象。 */
export async function get<T>(path: string, options?: Omit<RequestOptions, 'method' | 'body'>): Promise<T> {
  const envelope = await request<DataEnvelope<T>>(path, { ...options, method: 'GET' })
  return envelope.data
}

/** GET 列表（带分页）。 */
export async function getPage<T>(
  path: string,
  options?: Omit<RequestOptions, 'method' | 'body'>,
): Promise<PageResult<T>> {
  const envelope = await request<PageEnvelope<T>>(path, { ...options, method: 'GET' })
  return { items: envelope.data, pagination: envelope.pagination }
}

/** POST，返回解包后的 data。 */
export async function post<T>(path: string, body?: unknown, options?: RequestOptions): Promise<T> {
  const envelope = await request<DataEnvelope<T>>(path, { ...options, method: 'POST', body })
  return envelope?.data
}

/** PUT。 */
export async function put<T>(path: string, body?: unknown, options?: RequestOptions): Promise<T> {
  const envelope = await request<DataEnvelope<T>>(path, { ...options, method: 'PUT', body })
  return envelope?.data
}

/**
 * DELETE。**带 body** —— 删除也要带乐观锁 version（DECISIONS.md §2.4），
 * 塞 query string 里不如放 body 干净。RFC 9110 允许 DELETE 带 body。
 */
export async function del<T>(path: string, body?: unknown, options?: RequestOptions): Promise<T> {
  const envelope = await request<DataEnvelope<T>>(path, { ...options, method: 'DELETE', body })
  return envelope?.data
}
