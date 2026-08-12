import { render, screen } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { useState } from 'react'
import { createMemoryRouter, Link, RouterProvider } from 'react-router'
import { describe, expect, it } from 'vitest'

import { ConfirmDialog } from '@/components/ConfirmDialog'
import { useUnsavedGuard } from '@/lib/unsaved-guard'

/**
 * 离开拦截的两条规矩，**都是线上现象倒推出来的**：
 *
 *   1. 只比 pathname —— 比了查询串的话，退出编辑态（摘掉 `?edit=1`）会被自己拦下来，
 *      「保存」成功之后弹一个「放弃未保存的修改？」，而东西已经存进去了
 *   2. 取消要复位 blocker —— 不复位它一直卡在 blocked，之后点任何菜单都没反应
 */

function Editor({ watch = [] }: { watch?: string[] }) {
  const [dirty, setDirty] = useState(true)
  const guard = useUnsavedGuard(dirty, watch)

  return (
    <>
      <button onClick={() => setDirty(false)}>标记为已保存</button>
      <Link to="/here?detail=a&edit=1">同一条记录进编辑态</Link>
      <Link to="/here?detail=b">换一条记录</Link>
      <Link to="/elsewhere">去别的页面</Link>
      <ConfirmDialog state={guard} />
    </>
  )
}

function renderAt(path: string, watch?: string[]) {
  const router = createMemoryRouter(
    [
      { path: '/here', element: <Editor watch={watch} /> },
      { path: '/elsewhere', element: <p>另一个页面</p> },
    ],
    { initialEntries: [path] },
  )
  return { router, ...render(<RouterProvider router={router} />) }
}

describe('useUnsavedGuard', () => {
  it('同一页改查询串不算离开，不该拦', async () => {
    const { router } = renderAt('/here?detail=a')

    await userEvent.click(screen.getByRole('link', { name: '同一条记录进编辑态' }))

    expect(screen.queryByText('放弃未保存的修改？')).not.toBeInTheDocument()
    expect(router.state.location.search).toBe('?detail=a&edit=1')
  })

  it('换页面时会拦下来', async () => {
    renderAt('/here')

    await userEvent.click(screen.getByRole('link', { name: '去别的页面' }))

    expect(await screen.findByText('放弃未保存的修改？')).toBeInTheDocument()
    expect(screen.queryByText('另一个页面')).not.toBeInTheDocument()
  })

  it('点「放弃」才真的走', async () => {
    renderAt('/here')

    await userEvent.click(screen.getByRole('link', { name: '去别的页面' }))
    await userEvent.click(await screen.findByRole('button', { name: '放弃' }))

    expect(await screen.findByText('另一个页面')).toBeInTheDocument()
  })

  it('点「取消」要把 blocker 复位，否则下一次导航会被吞掉', async () => {
    renderAt('/here')

    // 第一次：拦住 → 取消
    await userEvent.click(screen.getByRole('link', { name: '去别的页面' }))
    await userEvent.click(await screen.findByRole('button', { name: '取消' }))

    // 第二次：必须还能再拦一次。复位失败的话这里什么都不会发生 ——
    // 现象就是「点菜单没反应」，很难联想到是上一次拦截没复位
    await userEvent.click(screen.getByRole('link', { name: '去别的页面' }))
    expect(await screen.findByText('放弃未保存的修改？')).toBeInTheDocument()
  })

  it('盯住的参数变了要拦 —— 左右分栏页换一条记录也是「离开」', async () => {
    // 部门页踩过：编辑到一半点左边另一个树节点，改动一声不吭就没了。
    // 那次导航只改了 `?detail=`，pathname 没变
    renderAt('/here?detail=a', ['detail'])

    await userEvent.click(screen.getByRole('link', { name: '换一条记录' }))

    expect(await screen.findByText('放弃未保存的修改？')).toBeInTheDocument()
  })

  it('没盯的参数变了不拦 —— 同一条记录换个模式不算离开', async () => {
    renderAt('/here?detail=a', ['detail'])

    await userEvent.click(screen.getByRole('link', { name: '同一条记录进编辑态' }))

    expect(screen.queryByText('放弃未保存的修改？')).not.toBeInTheDocument()
  })

  it('没有未保存的修改就直接放行', async () => {
    renderAt('/here')

    await userEvent.click(screen.getByRole('button', { name: '标记为已保存' }))
    await userEvent.click(screen.getByRole('link', { name: '去别的页面' }))

    expect(await screen.findByText('另一个页面')).toBeInTheDocument()
  })
})
