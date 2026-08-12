import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { Loader2 } from 'lucide-react'
import { useEffect, useState } from 'react'
import { toast } from 'sonner'

import {
  fetchSettings,
  saveSettings,
  type SettingGroup,
  type SettingItem,
  type SettingUpdate,
} from '@/api/settings'
import { ApiError } from '@/api/client'
import { FormField } from '@/components/FormField'
import { PageHeader } from '@/components/PageHeader'
import { Button } from '@/components/ui/button'
import { Checkbox } from '@/components/ui/checkbox'
import { Input } from '@/components/ui/input'

/**
 * 配置表单。**整张表由后端返回的注册表渲染** —— 前端不硬编码任何 key、
 * 任何取值范围、任何控件类型。
 *
 * 这么做不是为了炫技，是因为取值范围是**运行时值**：平台管理员随时能收紧
 * （MULTI-TENANCY.md §10.5）。前端抄一份范围的话，平台一收紧前端就和后端对不上，
 * 用户会在点保存的那一刻才被拒 —— 而且看不出为什么。
 *
 * 租户端和平台端共用它，差别只在 `path`。
 */
export function SettingsForm({
  path,
  title,
  description,
  canEdit,
}: {
  path: string
  title: string
  description: string
  /** 没有修改权限时只读展示 —— 菜单能进来不代表能改（读和改是两个权限点）。 */
  canEdit: boolean
}) {
  const queryClient = useQueryClient()
  const queryKey = ['settings', path]

  const query = useQuery({ queryKey, queryFn: () => fetchSettings(path) })

  // 草稿：只放**改动过**的项。空对象 = 没有未保存的改动。
  //
  // 不把整份配置拷进本地 state 是有意的：那样一来后台刷新（别的标签页改了配置、
  // 或者保存后的返回值）就会被本地副本盖掉，用户看到的是过期的值。
  const [draft, setDraft] = useState<Record<string, unknown>>({})
  useEffect(() => {
    setDraft({})
  }, [query.data])

  const mutation = useMutation({
    mutationFn: (items: SettingUpdate[]) => saveSettings(path, items),
    onSuccess: (groups) => {
      // 保存接口把最新的一份带回来了，直接换掉缓存，不用再发一次 GET
      queryClient.setQueryData(queryKey, groups)
      setDraft({})
      toast.success('设置已保存')
    },
    onError: (error) => {
      toast.error(error instanceof ApiError ? error.message : '保存失败')
    },
  })

  const groups = query.data ?? []
  const dirty = Object.keys(draft).length > 0
  const valueOf = (item: SettingItem) => (item.key in draft ? draft[item.key] : item.value)

  // 页面外壳和列表/详情页一致：同一个 padded 滚动容器 + 共享 PageHeader（DECISIONS.md §7.1），
  // 别自己搭一套，否则标题字号、页边距一定会飘（就出现过标题贴边、和别的页不是一个风格）。
  return (
    <div className="flex min-h-0 flex-1 flex-col overflow-y-auto px-8 py-8">
      <PageHeader title={title} description={description} />

      {query.isPending ? (
        <p className="text-muted-foreground">加载中…</p>
      ) : query.error ? (
        <p className="text-destructive">读取设置失败：{query.error.message}</p>
      ) : (
        <div className="space-y-6">
          {groups.map((group) => (
            <SettingsGroup
              key={group.key}
              group={group}
              canEdit={canEdit}
              valueOf={valueOf}
              onChange={(key, value) => {
                setDraft((prev) => ({ ...prev, [key]: value }))
              }}
            />
          ))}

          {canEdit ? (
            <div className="flex items-center gap-3">
              <Button
                disabled={!dirty || mutation.isPending}
                onClick={() => {
                  mutation.mutate(
                    Object.entries(draft).map(([key, value]) => ({ key, value })),
                  )
                }}
              >
                {mutation.isPending ? <Loader2 className="animate-spin" /> : null}
                保存
              </Button>
              {dirty ? <span className="text-muted-foreground">有未保存的修改</span> : null}
            </div>
          ) : null}
        </div>
      )}
    </div>
  )
}

function SettingsGroup({
  group,
  canEdit,
  valueOf,
  onChange,
}: {
  group: SettingGroup
  canEdit: boolean
  valueOf: (item: SettingItem) => unknown
  onChange: (key: string, value: unknown) => void
}) {
  return (
    <section className="surface space-y-5 p-6">
      <h2 className="font-medium">{group.name}</h2>
      {group.items.map((item) => (
        <FormField key={item.key} label={item.name} hint={hintOf(item)}>
          <SettingControl
            item={item}
            value={valueOf(item)}
            disabled={!canEdit}
            onChange={(v) => {
              onChange(item.key, v)
            }}
          />
        </FormField>
      ))}
    </section>
  )
}

/** 按 kind 选控件。后端加一种类型时这里要跟着加一条，否则会退化成只读文本。 */
function SettingControl({
  item,
  value,
  disabled,
  onChange,
}: {
  item: SettingItem
  value: unknown
  disabled: boolean
  onChange: (value: unknown) => void
}) {
  if (item.kind === 'bool') {
    return (
      <Checkbox
        checked={value === true}
        disabled={disabled}
        onCheckedChange={(checked) => {
          onChange(checked === true)
        }}
      />
    )
  }

  if (item.kind === 'int') {
    return (
      <Input
        type="number"
        inputMode="numeric"
        // 范围来自后端，不是写死的
        min={item.min}
        max={item.max}
        disabled={disabled}
        value={value === null || value === undefined ? '' : String(value)}
        onChange={(e) => {
          // 清空 = 不设置（上下界那几项允许留空表示不限制）
          onChange(e.target.value === '' ? null : Number(e.target.value))
        }}
      />
    )
  }

  return (
    <Input
      disabled={disabled}
      value={value === null || value === undefined ? '' : String(value)}
      onChange={(e) => {
        onChange(e.target.value)
      }}
    />
  )
}

/** 说明 + 单位 + 平台允许的范围，拼成输入框下面那一行。 */
function hintOf(item: SettingItem): string {
  const parts = [item.desc]
  if (item.unit) {
    parts.push(`单位：${item.unit}`)
  }
  if (item.min !== undefined && item.max !== undefined) {
    parts.push(`允许范围 ${item.min}–${item.max}`)
  } else if (item.min !== undefined) {
    parts.push(`不能小于 ${item.min}`)
  } else if (item.max !== undefined) {
    parts.push(`不能大于 ${item.max}`)
  }
  return parts.join('　')
}
