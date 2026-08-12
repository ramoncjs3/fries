import {
  Circle,
  KeyRound,
  LayoutDashboard,
  Network,
  ScrollText,
  Settings,
  Shield,
  ShieldCheck,
  Truck,
  Users,
  type LucideIcon,
} from 'lucide-react'

/**
 * 菜单图标注册表。
 *
 * 图标名来自后端的模块声明（`perm.Menu.Icon`），这里把名字映射到组件。
 *
 * **为什么不 `import * as icons from 'lucide-react'` 按名字动态取**：那样会把
 * 整个图标库（一千多个）打进包里，首屏白多下载 1 MB。菜单图标是个封闭小集合，
 * 显式登记不费事。
 *
 * 新模块用了新图标，就在这里加一行 —— 忘了加只是显示成一个圆点，不会白屏。
 */
const registry: Record<string, LucideIcon> = {
  'scroll-text': ScrollText,
  users: Users,
  'key-round': KeyRound,
  settings: Settings,
  truck: Truck,
  'layout-dashboard': LayoutDashboard,
  network: Network,
  shield: Shield,
  'shield-check': ShieldCheck,
}

export function MenuIcon({ name, className }: { name: string; className?: string }) {
  const Icon = registry[name] ?? Circle
  return <Icon className={className} />
}
