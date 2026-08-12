import { useCallback, useEffect, useMemo, useRef, useState } from 'react'
import { useSearchParams } from 'react-router'

/**
 * 列表页的查询参数，**同步到 URL**（DECISIONS.md §7.3）——
 * 刷新页面、分享链接、浏览器后退都能保持筛选状态。
 *
 * 搜索框自带 300ms 防抖：不加的话每敲一个字就打一次接口。
 */

/** 搜索防抖时长。 */
const SEARCH_DEBOUNCE_MS = 300

/** 窗口尺寸变化的防抖时长。拖窗口时不该每一帧都换一次每页条数。 */
const RESIZE_DEBOUNCE_MS = 200

export interface ListParams {
  page: number
  pageSize: number
  /** 已防抖的搜索词，直接拿去请求。 */
  search: string
  setPage: (page: number) => void
  setSearch: (search: string) => void
  /** 设置筛选项（下拉、开关这类一次到位的）。翻页会重置到第 1 页。 */
  setFilter: (key: string, value: string) => void
  /** 设置文本筛选项。**带 300ms 防抖** —— 输入框每敲一个字都打一次接口是不行的。 */
  setTextFilter: (key: string, value: string) => void
  /** 读一个筛选项。 */
  filter: (key: string) => string
  /** 清空搜索词和所有筛选项，回到第 1 页。 */
  reset: (keys: string[]) => void
}

/**
 * 每页条数按视口高度算，让表格正好铺满一屏。
 *
 * 固定条数在两头都不对：小屏上撑破一屏要滚，大屏上表格底下空一大片。
 * 下限 10 是「再小也别太密」，上限 50 是后端 page_size 的安全余量。
 */
/** 和 ui/table.tsx 里 `<td>` 的高度对应，改一处要改两处。 */
const ROW_HEIGHT = 56
const MIN_PAGE_SIZE = 10
const MAX_PAGE_SIZE = 50
/**
 * 表格行之外那些固定占位，是量出来的：
 * 外壳顶栏 56 + 页面上下内边距 64 + 标题块 78 + 筛选行 80 + 表头 44 + 分页条 43。
 * 宁可估大一点 —— 估小了表格会撑破一屏，那比底下空一行难受得多。
 */
const CHROME_HEIGHT = 365

function viewportPageSize(): number {
  if (typeof window === 'undefined') return MIN_PAGE_SIZE
  const rows = Math.floor((window.innerHeight - CHROME_HEIGHT) / ROW_HEIGHT)
  return Math.min(MAX_PAGE_SIZE, Math.max(MIN_PAGE_SIZE, rows))
}

/** 监听窗口高度，返回当前该显示多少行。 */
function useAutoPageSize(): number {
  const [size, setSize] = useState(viewportPageSize)

  useEffect(() => {
    let timer = 0
    function onResize() {
      window.clearTimeout(timer)
      timer = window.setTimeout(() => setSize(viewportPageSize()), RESIZE_DEBOUNCE_MS)
    }
    window.addEventListener('resize', onResize)
    return () => {
      window.clearTimeout(timer)
      window.removeEventListener('resize', onResize)
    }
  }, [])

  return size
}

// 不传 defaultPageSize 就按视口高度自适应。
export function useListParams(defaultPageSize?: number): ListParams {
  const [searchParams, setSearchParams] = useSearchParams()
  const autoPageSize = useAutoPageSize()
  const fallbackPageSize = defaultPageSize ?? autoPageSize

  const urlPageSize = Number(searchParams.get('page_size') ?? '') || 0
  const pageSize = urlPageSize > 0 ? urlPageSize : fallbackPageSize
  // 每页条数变了，原来的页码可能已经越界，回第一页最省事也最好解释
  const rawPage = Number(searchParams.get('page') ?? '1') || 1
  const lastPageSize = useRef(pageSize)
  const page = lastPageSize.current === pageSize ? rawPage : 1
  const urlSearch = searchParams.get('q') ?? ''

  // 输入框的即时值和 URL 上的值分开：URL 只在防抖之后更新
  const [debouncedSearch, setDebouncedSearch] = useState(urlSearch)
  const pendingSearch = useRef(urlSearch)
  // 文本筛选项的「最后一次输入」，用来做和搜索框同样的防抖
  const pendingFilters = useRef<Record<string, string>>({})

  useEffect(() => {
    setDebouncedSearch(urlSearch)
    pendingSearch.current = urlSearch
  }, [urlSearch])

  const update = useCallback(
    (mutate: (next: URLSearchParams) => void) => {
      setSearchParams(
        (current) => {
          const next = new URLSearchParams(current)
          mutate(next)
          return next
        },
        { replace: true },
      )
    },
    [setSearchParams],
  )

  // 每页条数变了会把页码拉回第 1 页，**URL 也得跟着回去** ——
  // 只改渲染用的值不改 URL 的话，刷新一下又跳回那个已经越界的页码，页面是空的。
  useEffect(() => {
    if (lastPageSize.current === pageSize) return
    lastPageSize.current = pageSize
    if (rawPage !== 1) update((next) => next.delete('page'))
  }, [pageSize, rawPage, update])

  const setPage = useCallback(
    (value: number) => {
      update((next) => {
        if (value <= 1) next.delete('page')
        else next.set('page', String(value))
      })
    },
    [update],
  )

  const setSearch = useCallback(
    (value: string) => {
      pendingSearch.current = value
      window.setTimeout(() => {
        // 防抖窗口内又改了就不提交这一次
        if (pendingSearch.current !== value) return
        setDebouncedSearch(value)
        update((next) => {
          if (value) next.set('q', value)
          else next.delete('q')
          next.delete('page') // 换了搜索词就回第一页
        })
      }, SEARCH_DEBOUNCE_MS)
    },
    [update],
  )

  const setFilter = useCallback(
    (key: string, value: string) => {
      update((next) => {
        if (value) next.set(key, value)
        else next.delete(key)
        next.delete('page')
      })
    },
    [update],
  )

  const setTextFilter = useCallback(
    (key: string, value: string) => {
      pendingFilters.current[key] = value
      window.setTimeout(() => {
        if (pendingFilters.current[key] !== value) return
        setFilter(key, value)
      }, SEARCH_DEBOUNCE_MS)
    },
    [setFilter],
  )

  const filter = useCallback((key: string) => searchParams.get(key) ?? '', [searchParams])

  /**
   * 清空所有条件。
   *
   * ⚠️ **必须在一次 update 里删完**。setSearchParams 的函数式写法拿到的是当前这次
   * 渲染的参数，同一个 tick 里连调几次，每次都从同一份旧值算起，后一次会把前一次
   * 的结果盖掉 —— 表现出来就是「点了清空一点反应都没有」。
   *
   * 同时要把两个防抖的待提交值清掉，否则已经排队的 setTimeout 会在 300ms 后
   * 把刚清掉的条件再写回去。
   */
  const reset = useCallback(
    (keys: string[]) => {
      pendingSearch.current = ''
      pendingFilters.current = {}
      setDebouncedSearch('')
      update((next) => {
        keys.forEach((key) => next.delete(key))
        next.delete('q')
        next.delete('page')
      })
    },
    [update],
  )

  return useMemo(
    () => ({
      page,
      pageSize,
      search: debouncedSearch,
      setPage,
      setSearch,
      setFilter,
      setTextFilter,
      filter,
      reset,
    }),
    [page, pageSize, debouncedSearch, setPage, setSearch, setFilter, setTextFilter, filter, reset],
  )
}
