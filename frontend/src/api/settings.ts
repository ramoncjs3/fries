import { get, put } from './client'

/**
 * 一个配置项。**形状完全由后端给** —— 前端不知道有哪些 key，也不知道取值范围。
 *
 * ⚠️ `min` / `max` 必须每次从接口拿，不能硬编码：它们存在 `platform_settings` 里，
 * 是平台管理员随时能调的运行时值（MULTI-TENANCY.md §10.5）。
 * 前端抄一份的话，平台收紧一档之后前端还按老范围放行，用户会在提交时才被拒。
 */
export interface SettingItem {
  key: string
  /** int / bool / string —— 前端按它选控件 */
  kind: string
  name: string
  desc: string
  /** 单位（位 / 天 / 次 / 分钟），空表示没有单位 */
  unit: string
  value: unknown
  /** 平台允许的下限，没有表示不限 */
  min?: number
  /** 平台允许的上限，没有表示不限 */
  max?: number
}

/** 一组配置项，页面上显示成一段。 */
export interface SettingGroup {
  key: string
  name: string
  items: SettingItem[]
}

interface SettingsResult {
  groups: SettingGroup[]
}

/** 要改的一项。 */
export interface SettingUpdate {
  key: string
  value: unknown
}

/**
 * 租户端和平台端走的是两个路径，但**请求和响应的形状完全一样** ——
 * 所以只用一套函数，路径当参数传。
 *
 * 两边不合并成一个接口是后端的决定：路径决定认哪套会话（§15.5），
 * 也决定挂哪个权限点。前端跟着分就行。
 */
export const TENANT_SETTINGS_PATH = '/settings'
export const PLATFORM_SETTINGS_PATH = '/platform/settings'

export function fetchSettings(path: string) {
  return get<SettingsResult>(path).then((r) => r.groups)
}

export function saveSettings(path: string, items: SettingUpdate[]) {
  return put<SettingsResult>(path, { items }).then((r) => r.groups)
}
