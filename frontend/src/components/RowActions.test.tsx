import { render, screen } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { describe, expect, it, vi } from 'vitest'

import { RowActions } from '@/components/RowActions'

describe('RowActions', () => {
  it('一条操作都没有时不渲染 —— 权限不够的人不该看到一个点开是空的 ⋯', () => {
    const { container } = render(<RowActions actions={[]} />)
    expect(container).toBeEmptyDOMElement()
  })

  it('有操作时渲染菜单', async () => {
    const onSelect = vi.fn()
    render(<RowActions actions={[{ key: 'edit', label: '编辑', onSelect }]} />)

    await userEvent.click(screen.getByRole('button', { name: '更多操作' }))
    await userEvent.click(await screen.findByText('编辑'))

    expect(onSelect).toHaveBeenCalledOnce()
  })

  it('禁用的项要说明原因 —— 灰掉却不说为什么最气人', async () => {
    render(
      <RowActions
        actions={[
          { key: 'del', label: '删除', danger: true, disabled: true, disabledReason: '不能删除自己', onSelect: vi.fn() },
        ]}
      />,
    )

    await userEvent.click(screen.getByRole('button', { name: '更多操作' }))
    expect(await screen.findByText('删除')).toHaveAttribute('title', '不能删除自己')
  })
})
