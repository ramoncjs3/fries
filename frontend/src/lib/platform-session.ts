import { createContext, use } from 'react'

import type { PlatformMeResult } from '@/api/auth'

/**
 * 平台管理端的会话上下文。
 *
 * 和租户端的 `lib/session.ts` 是**两套**，刻意不合并（MULTI-TENANCY.md §10.1）：
 * 两边的会话、cookie、/me 接口全是分开的，合并意味着到处要判「现在是哪一套」。
 *
 * hook 和常量单独放在 lib 里 —— 一个文件同时导出组件和非组件，
 * Vite 的 Fast Refresh 就失效了，改一行样式整页都要重刷（和 lib/confirm.ts 同理）。
 */

/** 平台管理端的路由前缀。判断「现在在哪一套里」一律用它，别到处写字符串。 */
export const PLATFORM_PREFIX = '/platform'

/** 当前是不是平台管理端的页面。 */
export function isPlatformPath(pathname: string): boolean {
  return pathname === PLATFORM_PREFIX || pathname.startsWith(`${PLATFORM_PREFIX}/`)
}

export const platformMeQueryKey = ['platform-me'] as const

export const PlatformContext = createContext<PlatformMeResult | null>(null)

export function usePlatformMe(): PlatformMeResult {
  const value = use(PlatformContext)
  if (!value) throw new Error('usePlatformMe 必须在 <PlatformGate> 里用')
  return value
}
