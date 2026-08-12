import { z } from 'zod'

/**
 * 角色表单的校验规则。只放前端能判死的（非空、长度、字符集）——
 * 「标识是不是被占了」「权限点存不存在」要查后端，不在这里复制一份（DECISIONS.md §5）。
 */
export const roleSchema = z.object({
  key: z
    .string()
    .trim()
    .min(1, '请填写角色标识')
    .max(64, '角色标识最多 64 个字符')
    .regex(/^[a-z][a-z0-9_]*$/, '只能用小写字母开头，后面接小写字母、数字或下划线'),
  name: z.string().trim().min(1, '请填写角色名称').max(64, '角色名称最多 64 个字'),
  description: z.string().max(500, '说明最多 500 个字'),
  data_scope: z.enum(['all', 'self']),
  status: z.enum(['active', 'disabled']),
  permissions: z.array(z.string()),
})

export type RoleFormValues = z.infer<typeof roleSchema>

export const emptyRole: RoleFormValues = {
  key: '',
  name: '',
  description: '',
  data_scope: 'self',
  status: 'active',
  permissions: [],
}
