import { SettingsForm } from '@/components/SettingsForm'
import { TENANT_SETTINGS_PATH } from '@/api/settings'
import { useSession } from '@/lib/session'

/**
 * 本组织的安全设置。
 *
 * ⚠️ **不放在 `features/` 下面**：那个目录只允许 ListPage / NewPage / DetailPage
 * 三种页面（`make lint-structure` 有白名单），而配置是单表单页，
 * 形态上和 ChangePasswordPage 一类，归 routes/。
 */
export default function SettingsPage() {
  // 能进这个页面（settings.security:list）不代表能改 —— 读和改是两个权限点
  const { can } = useSession()
  const canEdit = can('settings.security', 'update')

  return (
    <SettingsForm
      path={TENANT_SETTINGS_PATH}
      title="安全设置"
      description="密码策略和登录锁定。改完立即生效，但密码策略只影响下次改密——已有密码不受影响。"
      canEdit={canEdit}
    />
  )
}
