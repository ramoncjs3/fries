import { render, screen } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { createMemoryRouter, Link, RouterProvider } from 'react-router'
import { describe, expect, it } from 'vitest'

import { FROM_LIST_STATE, useBackToList } from '@/lib/detail-nav'

/**
 * 「返回」要判断**身后有没有列表那条历史记录**：
 * 从列表点进来的可以退回去（筛选和页码都在那条记录上）；
 * 别人发的链接、新标签打开的没有，只能跳过去。
 */

function Detail() {
  const back = useBackToList('/users')
  return <button onClick={back}>返回</button>
}

function List() {
  return (
    <>
      <p>列表页</p>
      <Link to="/users/u1" state={FROM_LIST_STATE}>
        进详情
      </Link>
    </>
  )
}

function setup(initialEntries: string[]) {
  const router = createMemoryRouter(
    [
      { path: '/users', element: <List /> },
      { path: '/users/u1', element: <Detail /> },
    ],
    { initialEntries },
  )
  render(<RouterProvider router={router} />)
  return router
}

describe('useBackToList', () => {
  it('从列表点进来的，返回时退回那条历史记录（筛选原样）', async () => {
    const router = setup(['/users?q=zhang&page=2'])

    await userEvent.click(screen.getByRole('link', { name: '进详情' }))
    await userEvent.click(await screen.findByRole('button', { name: '返回' }))

    expect(await screen.findByText('列表页')).toBeInTheDocument()
    expect(router.state.location.search).toBe('?q=zhang&page=2')
  })

  it('直接打开详情链接（身后没有列表）时跳到干净的列表页', async () => {
    const router = setup(['/users/u1'])

    await userEvent.click(screen.getByRole('button', { name: '返回' }))

    expect(await screen.findByText('列表页')).toBeInTheDocument()
    expect(router.state.location.pathname).toBe('/users')
    expect(router.state.location.search).toBe('')
  })
})
