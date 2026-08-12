import {
  formatDate,
  formatDateTime,
  formatDateTimeMinute,
  formatRelative,
  type TimeValue,
} from '@/lib/datetime'

interface DateTimeProps {
  value: TimeValue
  /**
   * 精度从细到粗：full=到秒（默认，审计用）；minute=到分（详情用）；
   * date=只到天；relative=「3 分钟前」。**四个都能悬停看到精确到秒的值**。
   */
  variant?: 'full' | 'minute' | 'date' | 'relative'
  className?: string
}

/**
 * 全站唯一的时间渲染方式。**接口给的是 UTC，这里统一显示成北京时间**
 * （DECISIONS.md §2.5）。
 *
 * ESLint 禁止在页面里裸调 toLocaleString —— 那样每个页面的格式和时区都会各走各的。
 */
/** 每个精度对应一个格式化函数。加精度就在这里加一条，不要往下面堆三元表达式。 */
const formatters: Record<NonNullable<DateTimeProps['variant']>, (value: TimeValue) => string> = {
  full: formatDateTime,
  minute: formatDateTimeMinute,
  date: formatDate,
  relative: formatRelative,
}

export function DateTime({ value, variant = 'full', className }: DateTimeProps) {
  if (!value) return <span className={className}>—</span>

  // title 一律给到秒：显示的精度可以粗，但要查的时候得查得到。
  const exact = formatDateTime(value)
  const text = formatters[variant](value)

  return (
    <time
      dateTime={typeof value === 'string' ? value : value.toISOString()}
      title={`${exact}（北京时间）`}
      className={className}
    >
      {text}
    </time>
  )
}
