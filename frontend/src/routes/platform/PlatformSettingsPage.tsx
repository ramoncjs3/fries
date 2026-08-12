import { SettingsForm } from '@/components/SettingsForm'
import { PLATFORM_SETTINGS_PATH } from '@/api/settings'

/**
 * 平台设置：平台自己的配置 + 给各组织划的可调范围。
 *
 * 平台管理员这一轮即全权（MULTI-TENANCY.md §6），所以不判权限点，
 * 能进这棵子树就能改。
 */
export default function PlatformSettingsPage() {
  return (
    <SettingsForm
      path={PLATFORM_SETTINGS_PATH}
      title="平台设置"
      description="平台自己的配置，以及各组织能把安全策略调到什么范围。收紧范围会立刻约束各组织的下一次修改。"
      canEdit
    />
  )
}
