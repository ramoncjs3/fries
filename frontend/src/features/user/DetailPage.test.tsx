import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { render, screen } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { createMemoryRouter, RouterProvider } from 'react-router'
import { beforeEach, describe, expect, it, vi } from 'vitest'

import UserDetailPage from '@/features/user/DetailPage'
import type { User } from '@/features/user/types'

/**
 * 用户详情页。盯的是**两个真踩过的 bug**：
 *
 *   1. 直接开 `/users/:id?edit=1` 时表单是空的 —— 组件在数据回来之前就挂载了，
 *      `useForm` 只在挂载那一刻读一次 defaultValues，之后数据到了也不会补上
 *   2. 没有 update 权限的人手敲 `?edit=1` 也能进编辑态
 */

const alice: User = {
  id: 'u1',
  username: 'alice',
  display_name: '爱丽丝',
  email: 'alice@example.com',
  phone: '13800138000',
  status: 'active',
  department_id: 'd1',
  department_name: '技术部',
  role_ids: ['r1'],
  role_names: '只读',
  must_change_password: false,
  last_login_at: null,
  locked_until: null,
  created_at: '2026-08-01T02:00:00Z',
  updated_at: '2026-08-01T02:00:00Z',
  version: 3,
}

/** 详情接口故意**慢一拍**才返回 —— 组件早挂的 bug 只有这样才复现得出来。 */
const getUser = vi.fn(
  (_id: string) => new Promise<User>((resolve) => setTimeout(() => resolve(alice), 10)),
)

vi.mock('@/features/user/api', () => ({
  getUser: (id: string) => getUser(id),
  listUsers: vi.fn(),
  createUser: vi.fn(),
  updateUser: vi.fn(),
  deleteUser: vi.fn(),
  resetUserPassword: vi.fn(),
}))

vi.mock('@/features/role/queries', () => ({
  useRoles: () => ({ data: { items: [{ id: 'r1', key: 'viewer', name: '只读' }] } }),
}))

vi.mock('@/features/department/queries', () => ({
  useDepartments: () => ({
    data: { items: [{ id: 'd1', code: 'TECH', name: '技术部', status: 'active' }] },
  }),
}))

const can = vi.fn((_resource: string, _action: string) => true)
vi.mock('@/lib/session', () => ({
  useSession: () => ({ can, me: { user: { id: 'admin', display_name: '管理员' } } }),
}))

function renderAt(path: string) {
  const client = new QueryClient({ defaultOptions: { queries: { retry: false } } })
  const router = createMemoryRouter([{ path: '/users/:id', element: <UserDetailPage /> }], {
    initialEntries: [path],
  })
  return render(
    <QueryClientProvider client={client}>
      <RouterProvider router={router} />
    </QueryClientProvider>,
  )
}

beforeEach(() => {
  can.mockReturnValue(true)
  getUser.mockClear()
})

describe('用户详情页', () => {
  it('只读态显示各字段', async () => {
    renderAt('/users/u1')

    // 用标题定位，不用文本 —— 「爱丽丝」同时是页面标题和「显示名」的值，
    // 按文本找会匹配到两个
    expect(await screen.findByRole('heading', { name: '爱丽丝' })).toBeInTheDocument()
    expect(screen.getByText('alice@example.com')).toBeInTheDocument()
    expect(screen.getByText('技术部')).toBeInTheDocument()
    // 只读态不该有任何输入框
    expect(screen.queryByRole('textbox')).not.toBeInTheDocument()
  })

  it('直接开 ?edit=1 的链接，表单要带着数据（数据到得比组件挂载晚）', async () => {
    renderAt('/users/u1?edit=1')

    // 这一条就是那个 bug：修之前这里是空字符串，而标题栏的名字却是对的
    const displayName = await screen.findByDisplayValue('爱丽丝')
    expect(displayName).toBeInTheDocument()
    expect(screen.getByDisplayValue('alice@example.com')).toBeInTheDocument()
    expect(screen.getByDisplayValue('13800138000')).toBeInTheDocument()
  })

  it('从只读点「编辑」进去，表单同样要带着数据', async () => {
    renderAt('/users/u1')

    await userEvent.click(await screen.findByRole('button', { name: /编辑/ }))

    expect(await screen.findByDisplayValue('爱丽丝')).toBeInTheDocument()
    expect(screen.getByRole('button', { name: '保存' })).toBeInTheDocument()
  })

  it('没有 update 权限时，手敲 ?edit=1 也进不了编辑态', async () => {
    can.mockImplementation((_resource: string, action: string) => action !== 'update')

    renderAt('/users/u1?edit=1')

    expect(await screen.findByRole('heading', { name: '爱丽丝' })).toBeInTheDocument()
    expect(screen.queryByRole('button', { name: '保存' })).not.toBeInTheDocument()
    expect(screen.queryByDisplayValue('爱丽丝')).not.toBeInTheDocument()
  })

  it('「保存」是提交按钮且关联到表单 —— 输入框里按回车才提交得了', async () => {
    renderAt('/users/u1?edit=1')

    const save = await screen.findByRole('button', { name: '保存' })
    expect(save).toHaveAttribute('type', 'submit')
    // 按钮在标题栏里、不在 <form> 内部，只能靠 form= 关联
    expect(save).toHaveAttribute('form')
    expect(document.getElementById(save.getAttribute('form')!)).toBeInstanceOf(HTMLFormElement)
  })
})
