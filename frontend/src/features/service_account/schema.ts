import { z } from 'zod'

/**
 * 机器账号表单的校验规则。只放前端能判死的 ——
 * 「名称是不是被占了」「角色存不存在」要查后端，不在这里复制一份。
 *
 * ⚠️ 到期时间**故意不在这里判「必须是将来」**：前端时钟和服务端时钟不一定一致，
 * 卡在前端会出现「我明明填的是明天」却提交不了的情况。后端会拦（它拿的是服务端时间），
 * 前端只把日期选择器的下限设到今天，两边各干各的。
 */
export const serviceAccountSchema = z.object({
  name: z.string().trim().min(1, '请填写名称').max(64, '名称最多 64 个字'),
  description: z.string().max(500, '说明最多 500 个字'),
  role_id: z.string().min(1, '请选择角色'),
  status: z.enum(['active', 'disabled']),
  expires_at: z.string(),
})

export type ServiceAccountFormValues = z.infer<typeof serviceAccountSchema>

export const emptyServiceAccount: ServiceAccountFormValues = {
  name: '',
  description: '',
  role_id: '',
  status: 'active',
  expires_at: '',
}
