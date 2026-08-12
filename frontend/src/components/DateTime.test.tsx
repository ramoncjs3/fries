import { render, screen } from '@testing-library/react'
import { describe, expect, it } from 'vitest'

import { DateTime } from '@/components/DateTime'

/**
 * 时间显示。**接口给的是 UTC，页面上一律显示北京时间**（DECISIONS.md §2.5）——
 * 这条错了不会报错，只会悄悄差 8 小时，所以必须有测试盯着。
 */
describe('DateTime', () => {
  // 2026-08-01T02:00:00Z = 北京时间当天 10:00
  const utc = '2026-08-01T02:00:00Z'

  it('默认到秒，且换算成北京时间', () => {
    render(<DateTime value={utc} />)
    expect(screen.getByText('2026/08/01 10:00:00')).toBeInTheDocument()
  })

  it('minute 档到分钟 —— 详情页用这个，秒是噪音', () => {
    render(<DateTime value={utc} variant="minute" />)
    expect(screen.getByText('2026/08/01 10:00')).toBeInTheDocument()
  })

  it('date 档只到天', () => {
    render(<DateTime value={utc} variant="date" />)
    expect(screen.getByText('2026/08/01')).toBeInTheDocument()
  })

  it('不管显示到哪一档，悬停都能看到精确到秒的值', () => {
    render(<DateTime value={utc} variant="date" />)
    expect(screen.getByText('2026/08/01')).toHaveAttribute('title', '2026/08/01 10:00:00（北京时间）')
  })

  it('空值显示成破折号，不是 Invalid Date', () => {
    render(<DateTime value={null} />)
    expect(screen.getByText('—')).toBeInTheDocument()
  })
})
