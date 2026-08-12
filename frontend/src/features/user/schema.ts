import { z } from 'zod'

/**
 * 用户表单的校验规则。
 *
 * **密码不在这里**：初始密码由后端随机生成，管理员没得填（DECISIONS.md §6）。
 * 「用户名/邮箱/手机号是不是被占了」也不在这里 —— 那要查库，交给后端。
 */
export const userSchema = z.object({
  username: z
    .string()
    .trim()
    .min(2, '用户名至少 2 个字符')
    .max(64, '用户名最多 64 个字符')
    .regex(/^[a-zA-Z0-9._-]+$/, '只能用字母、数字、点、下划线和中划线'),
  display_name: z.string().trim().min(1, '请填写显示名').max(64, '显示名最多 64 个字'),
  // 可空，但填了就得是个像样的邮箱
  email: z.union([z.literal(''), z.email('邮箱格式不对')]),
  phone: z.union([z.literal(''), z.string().regex(/^1[3-9]\d{9}$/, '手机号格式不对')]),
  status: z.enum(['active', 'disabled']),
  department_id: z.string(),
  role_ids: z.array(z.string()),
})

export type UserFormValues = z.infer<typeof userSchema>

export const emptyUser: UserFormValues = {
  username: '',
  display_name: '',
  email: '',
  phone: '',
  status: 'active',
  department_id: '',
  role_ids: [],
}
