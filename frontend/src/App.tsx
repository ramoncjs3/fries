import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { ThemeProvider } from 'next-themes'
import { RouterProvider } from 'react-router'
import { Toaster } from 'sonner'

import { setUnauthenticatedHandler } from '@/api/client'
import { PLATFORM_PREFIX } from '@/lib/platform-session'
import { router } from '@/routes'

/**
 * 数据刷新策略**全局配一次，不靠每个页面自觉**（DECISIONS.md §7.2）。
 *
 * 先渲染缓存的旧数据、后台重取、无缝替换：秒开且是最新的。
 */
const queryClient = new QueryClient({
  defaultOptions: {
    queries: {
      staleTime: 0,
      refetchOnMount: 'always',
      refetchOnWindowFocus: true,
      retry: 1,
    },
  },
})

// 任何请求拿到 401 都回登录页。集中处理一次，页面不用各自判断。
//
// 走路由跳转而不是 window.location —— 整页刷新会把已经加载好的 JS 全丢掉重来，
// 会话过期本来只是换个页面，不该表现成「网站重开了一遍」。
//
// ⚠️ **要分清回哪个登录页**（MULTI-TENANCY.md §6）：平台管理端是另一套身份，
// 把平台管理员踢到租户登录页去，他会对着「公司代码」那一格发愣 ——
// 而他根本不属于任何组织。浏览器实测踩到过：站在 /platform/login 上时，
// 租户那边的 /me 拿到 401，直接把人顶到了 /login。
setUnauthenticatedHandler(() => {
  const current = router.state.location.pathname
  const target = current.startsWith(PLATFORM_PREFIX) ? '/platform/login' : '/login'
  if (current !== target) {
    void router.navigate(target, { replace: true })
  }
})

export default function App() {
  return (
    <QueryClientProvider client={queryClient}>
      <ThemeProvider attribute="class" defaultTheme="light" enableSystem disableTransitionOnChange>
        <RouterProvider router={router} />
        {/*
            提示统一走右下角，**不放顶部**：顶部正是标题和主操作所在的位置，
            提示条盖上去等于把人刚点完的东西挡住。

            也**不用 richColors** —— 那是一整块饱和绿/红，在我们这套低饱和的界面里
            像贴了张便利贴。改成和卡片一样的白底 + 细边，只用一个小图标区分成败。
          */}
        <Toaster
          position="bottom-right"
          closeButton
          duration={3500}
          toastOptions={{
            classNames: {
              toast: 'surface shadow-overlay gap-2.5 px-4 py-3',
              title: 'font-medium',
              description: 'text-muted-foreground',
              success: '[&_[data-icon]]:text-success',
              error: '[&_[data-icon]]:text-destructive',
              warning: '[&_[data-icon]]:text-warning',
            },
          }}
        />
      </ThemeProvider>
    </QueryClientProvider>
  )
}
