import { lazy, Suspense } from "react";
import { createBrowserRouter, Navigate } from "react-router";

import { AppShell } from "@/components/AppShell";
import { RequirePerm } from "@/components/RequirePerm";
import { TenantSessionLayout } from "@/lib/session-provider";
import { AuthGate } from "@/routes/AuthGate";
import { PlatformShell } from "@/routes/platform/PlatformShell";
import { PlatformGate } from "@/routes/platform/PlatformGate";
import { PageFallback } from "@/routes/PageFallback";

/**
 * 路由表。
 *
 * 每个业务页面都要满足两条（DECISIONS.md §3.6、§7.3）：
 *   1. 懒加载 —— 不然首屏要把所有页面一起下下来
 *   2. 包在 <RequirePerm> 里 —— 防直输 URL 绕过菜单。
 *      **这不是安全边界**，真正的拦截线在后端；这层只是别让人点进必然 403 的页面。
 *
 * 第 ⑤ 步的生成器会往这里加模块路由，所以保持这个形状别乱改。
 */

const LoginPage = lazy(() => import("@/routes/LoginPage"));
const ForgotPasswordPage = lazy(() => import("@/routes/ForgotPasswordPage"));
const ResetPasswordPage = lazy(() => import("@/routes/ResetPasswordPage"));
const RegisterPage = lazy(() => import("@/routes/RegisterPage"));
const RegisterVerifyPage = lazy(() => import("@/routes/RegisterVerifyPage"));
const ChangePasswordPage = lazy(() => import("@/routes/ChangePasswordPage"));
const HomePage = lazy(() => import("@/routes/HomePage"));
const NotFoundPage = lazy(() => import("@/routes/NotFoundPage"));
const AuditListPage = lazy(() => import("@/features/audit/ListPage"));
const DepartmentListPage = lazy(() => import("@/features/department/ListPage"));
const RoleListPage = lazy(() => import("@/features/role/ListPage"));
const RoleNewPage = lazy(() => import("@/features/role/NewPage"));
const RoleDetailPage = lazy(() => import("@/features/role/DetailPage"));
const SupplierListPage = lazy(() => import("@/features/supplier/ListPage"));
const SupplierNewPage = lazy(() => import("@/features/supplier/NewPage"));
const SupplierDetailPage = lazy(() => import("@/features/supplier/DetailPage"));
const UserListPage = lazy(() => import("@/features/user/ListPage"));
const UserDetailPage = lazy(() => import("@/features/user/DetailPage"));
const UserNewPage = lazy(() => import("@/features/user/NewPage"));
const PlatformLoginPage = lazy(
  () => import("@/routes/platform/PlatformLoginPage"),
);
const TenantListPage = lazy(() => import("@/routes/platform/TenantListPage"));
const PlatformSettingsPage = lazy(
  () => import("@/routes/platform/PlatformSettingsPage"),
);
const SettingsPage = lazy(() => import("@/routes/SettingsPage"));
const ServiceAccountListPage = lazy(
  () => import("@/features/service_account/ListPage"),
);
const ServiceAccountNewPage = lazy(
  () => import("@/features/service_account/NewPage"),
);
const ServiceAccountDetailPage = lazy(
  () => import("@/features/service_account/DetailPage"),
);
const PlatformChangePasswordPage = lazy(
  () => import("@/routes/platform/PlatformChangePasswordPage"),
);

function lazyPage(element: React.ReactNode) {
  return <Suspense fallback={<PageFallback />}>{element}</Suspense>;
}

export const router = createBrowserRouter([
  { path: "/login", element: lazyPage(<LoginPage />) },
  // 忘记密码两页都是公开的（用户还没登录），和登录页并列，不进 AuthGate。
  { path: "/forgot-password", element: lazyPage(<ForgotPasswordPage />) },
  { path: "/reset-password", element: lazyPage(<ResetPasswordPage />) },
  // 自助注册两页也是公开的。是否真能注册由后端的平台开关决定（关着时提交会被拒）。
  { path: "/register", element: lazyPage(<RegisterPage />) },
  { path: "/register/verify", element: lazyPage(<RegisterVerifyPage />) },

  /*
   * 平台管理端。**和租户端完全分开的一棵子树**（MULTI-TENANCY.md §6、§10.1）：
   * 独立的闸门（<PlatformGate>）、独立的会话和 cookie、独立的外壳。
   *
   * 不复用 <AuthGate> 和 <AppShell> 是有意的 —— 复用意味着它们里面到处要判
   * 「现在是平台还是租户」，而那种判断漏一处就是把两个世界打通。
   * 后端已经这么分了（两套会话表、Realm 对齐），前端跟着分。
   */
  { path: "/platform/login", element: lazyPage(<PlatformLoginPage />) },
  {
    element: <PlatformGate />,
    children: [
      {
        path: "/platform/change-password",
        element: lazyPage(<PlatformChangePasswordPage />),
      },
      {
        element: <PlatformShell />,
        children: [
          { path: "/platform/tenants", element: lazyPage(<TenantListPage />) },
          {
            path: "/platform/settings",
            element: lazyPage(<PlatformSettingsPage />),
          },
        ],
      },
      {
        path: "/platform",
        element: <Navigate to="/platform/tenants" replace />,
      },
    ],
  },

  /*
   * 租户端。<TenantSessionLayout> 把租户会话**限定在这棵子树里** ——
   * 挂在应用最外层的话，平台管理端的页面也会去查租户的 /me，401 之后
   * 人就被顶到租户登录页去了（浏览器实测踩过）。
   */
  {
    element: <TenantSessionLayout />,
    children: [
      {
        element: <AuthGate />,
        children: [
          {
            path: "/change-password",
            element: lazyPage(<ChangePasswordPage />),
          },
          {
            element: <AppShell />,
            children: [
              { index: true, element: lazyPage(<HomePage />) },
              {
                // 新增在 :id 前面只是为了好读 —— react-router 按具体程度排序
                path: "/service-accounts/new",
                element: lazyPage(
                  <RequirePerm resource="service_account" action="create">
                    <ServiceAccountNewPage />
                  </RequirePerm>,
                ),
              },
              {
                path: "/service-accounts/:id",
                element: lazyPage(
                  <RequirePerm resource="service_account" action="list">
                    <ServiceAccountDetailPage />
                  </RequirePerm>,
                ),
              },
              {
                path: "/service-accounts",
                element: lazyPage(
                  <RequirePerm resource="service_account" action="list">
                    <ServiceAccountListPage />
                  </RequirePerm>,
                ),
              },
              {
                // 路径和 perm 里那个模块的 Menu.Path 必须一致 ——
                // 菜单是后端给的，对不上就是「菜单点了 404」
                path: "/settings/security",
                element: lazyPage(
                  <RequirePerm resource="settings.security" action="list">
                    <SettingsPage />
                  </RequirePerm>,
                ),
              },
              {
                path: "/audit",
                element: lazyPage(
                  <RequirePerm resource="audit" action="list">
                    <AuditListPage />
                  </RequirePerm>,
                ),
              },
              {
                path: "/users",
                element: lazyPage(
                  <RequirePerm resource="user" action="list">
                    <UserListPage />
                  </RequirePerm>,
                ),
              },
              {
                // 新增也是页面，不是弹窗（DECISIONS.md §7.6）。
                // 放在 /users/:id 前面只是为了好读 —— react-router 按具体程度排序，
                // 静态段本来就赢过动态段，`new` 不会被当成 id
                path: "/users/new",
                element: lazyPage(
                  <RequirePerm resource="user" action="create">
                    <UserNewPage />
                  </RequirePerm>,
                ),
              },
              {
                // 详情页和列表页**平级，不嵌套** —— 嵌套会让列表一直挂在那儿，
                // 那就退回抽屉了。列表的状态不靠「留在内存里」保住，靠 URL + 查询缓存
                path: "/users/:id",
                element: lazyPage(
                  <RequirePerm resource="user" action="list">
                    <UserDetailPage />
                  </RequirePerm>,
                ),
              },
              {
                path: "/roles",
                element: lazyPage(
                  <RequirePerm resource="role" action="list">
                    <RoleListPage />
                  </RequirePerm>,
                ),
              },
              {
                path: "/roles/new",
                element: lazyPage(
                  <RequirePerm resource="role" action="create">
                    <RoleNewPage />
                  </RequirePerm>,
                ),
              },
              {
                path: "/roles/:id",
                element: lazyPage(
                  <RequirePerm resource="role" action="list">
                    <RoleDetailPage />
                  </RequirePerm>,
                ),
              },
              {
                path: "/suppliers",
                element: lazyPage(
                  <RequirePerm resource="supplier" action="list">
                    <SupplierListPage />
                  </RequirePerm>,
                ),
              },
              {
                path: "/suppliers/new",
                element: lazyPage(
                  <RequirePerm resource="supplier" action="create">
                    <SupplierNewPage />
                  </RequirePerm>,
                ),
              },
              {
                path: "/suppliers/:id",
                element: lazyPage(
                  <RequirePerm resource="supplier" action="read">
                    <SupplierDetailPage />
                  </RequirePerm>,
                ),
              },
              {
                path: "/departments",
                element: lazyPage(
                  <RequirePerm resource="department" action="list">
                    <DepartmentListPage />
                  </RequirePerm>,
                ),
              },
              { path: "*", element: lazyPage(<NotFoundPage />) },
            ],
          },
        ],
      },
    ],
  },
  { path: "*", element: <Navigate to="/" replace /> },
]);
