import '@testing-library/jest-dom/vitest'

import { cleanup } from '@testing-library/react'
import { afterEach, vi } from 'vitest'

/**
 * 测试环境的公共准备。
 *
 * jsdom 缺几个我们用到的浏览器 API，不补就是一片红 —— 而且报错信息跟被测的东西
 * 毫无关系，很容易查半天。
 */

// 每个用例之间把 DOM 清干净，否则上一个用例的弹窗还挂在 body 上，
// 下一个用例 getByRole 会一次找到两个
afterEach(() => cleanup())

// Radix 的下拉、弹窗都要它，jsdom 没有实现
if (!window.matchMedia) {
  window.matchMedia = (query: string) =>
    ({
      matches: false,
      media: query,
      onchange: null,
      addEventListener: vi.fn(),
      removeEventListener: vi.fn(),
      addListener: vi.fn(),
      removeListener: vi.fn(),
      dispatchEvent: vi.fn(),
    }) as unknown as MediaQueryList
}

// Radix Select 在测量选项位置时会用到，jsdom 里都是空实现
window.HTMLElement.prototype.scrollIntoView = vi.fn()
window.HTMLElement.prototype.hasPointerCapture = vi.fn(() => false)
window.HTMLElement.prototype.setPointerCapture = vi.fn()
window.HTMLElement.prototype.releasePointerCapture = vi.fn()

if (!window.ResizeObserver) {
  window.ResizeObserver = class {
    observe() {}
    unobserve() {}
    disconnect() {}
  }
}
