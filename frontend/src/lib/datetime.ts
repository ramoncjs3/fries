/**
 * 时间显示。**接口传的是 UTC，页面上一律显示北京时间**（DECISIONS.md §2.5）。
 *
 * 时区常量只写在这一处。将来真要做多时区，改这里 + 加用户偏好即可，
 * 不用满项目找 `toLocaleString`（ESLint 也禁止裸调它）。
 */

/** 全站显示时区。内部系统，所有人说的都是北京时间，不跟浏览器时区走。 */
export const DISPLAY_TIME_ZONE = 'Asia/Shanghai'

const dateTimeFormatter = new Intl.DateTimeFormat('zh-CN', {
  timeZone: DISPLAY_TIME_ZONE,
  year: 'numeric',
  month: '2-digit',
  day: '2-digit',
  hour: '2-digit',
  minute: '2-digit',
  second: '2-digit',
  hour12: false,
})

const minuteFormatter = new Intl.DateTimeFormat('zh-CN', {
  timeZone: DISPLAY_TIME_ZONE,
  year: 'numeric',
  month: '2-digit',
  day: '2-digit',
  hour: '2-digit',
  minute: '2-digit',
  hour12: false,
})

const dateFormatter = new Intl.DateTimeFormat('zh-CN', {
  timeZone: DISPLAY_TIME_ZONE,
  year: 'numeric',
  month: '2-digit',
  day: '2-digit',
})

/** 后端来的时间：RFC3339 带 Z 的字符串，或者已经是 Date。 */
export type TimeValue = string | Date | null | undefined

function toDate(value: TimeValue): Date | null {
  if (!value) return null
  const date = value instanceof Date ? value : new Date(value)
  return Number.isNaN(date.getTime()) ? null : date
}

/** 格式化成 `2026/08/07 16:15:02`（北京时间）。 */
export function formatDateTime(value: TimeValue): string {
  const date = toDate(value)
  return date ? dateTimeFormatter.format(date) : '—'
}

/**
 * 到分钟：`2026/08/07 16:15`（北京时间）。
 *
 * 详情里的「创建于」「最后登录」用这个 —— 秒只在排查同一秒内的并发时才有用，
 * 那是审计日志的活。详情里多出来的 `:02` 只是噪音，还让一列时间参差不齐。
 */
export function formatDateTimeMinute(value: TimeValue): string {
  const date = toDate(value)
  return date ? minuteFormatter.format(date) : '—'
}

/** 只要日期：`2026/08/07`（北京时间）。 */
export function formatDate(value: TimeValue): string {
  const date = toDate(value)
  return date ? dateFormatter.format(date) : '—'
}

/** 相对时间：「3 分钟前」。列表里看「多久之前」比看绝对时间快。 */
export function formatRelative(value: TimeValue): string {
  const date = toDate(value)
  if (!date) return '—'

  const seconds = Math.round((Date.now() - date.getTime()) / 1000)
  if (Math.abs(seconds) < 60) return '刚刚'

  const units: Array<[Intl.RelativeTimeFormatUnit, number]> = [
    ['year', 365 * 24 * 3600],
    ['month', 30 * 24 * 3600],
    ['day', 24 * 3600],
    ['hour', 3600],
    ['minute', 60],
  ]
  const rtf = new Intl.RelativeTimeFormat('zh-CN', { numeric: 'auto' })
  for (const [unit, size] of units) {
    if (Math.abs(seconds) >= size) {
      return rtf.format(-Math.round(seconds / size), unit)
    }
  }
  return '刚刚'
}

/** 把 Date 转成后端要的 RFC3339 UTC 字符串。 */
export function toUTCString(date: Date): string {
  return date.toISOString()
}
