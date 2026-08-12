import { z } from 'zod'

/**
 * 部门表单的校验规则。
 *
 * **只放前端能判死的规则**：非空、长度、字符集。
 * 「编号是不是被占了」「会不会成环」这类要查库的，一律交给后端 ——
 * 前端复制一份判断逻辑就一定会和后端不一致（DECISIONS.md §5）。
 * 后端的字段级错误会通过 ApiError.formErrors() 映射回对应输入框。
 */
export const departmentSchema = z.object({
  parent_id: z.string(),
  name: z.string().trim().min(1, '请填写部门名称').max(64, '部门名称最多 64 个字'),
  code: z
    .string()
    .trim()
    .min(1, '请填写部门编号')
    .max(64, '部门编号最多 64 个字符')
    .regex(/^[A-Za-z0-9_-]+$/, '编号只能用字母、数字、下划线和中划线'),
  sort_order: z.coerce.number<number>().int('排序要填整数').min(0, '排序不能是负数').max(9999, '排序最大 9999'),
  remark: z.string().max(500, '备注最多 500 个字'),
  status: z.enum(['active', 'disabled']),
})

export type DepartmentFormValues = z.infer<typeof departmentSchema>

/** 新建时的初始值。 */
export const emptyDepartment: DepartmentFormValues = {
  parent_id: '',
  name: '',
  code: '',
  sort_order: 0,
  remark: '',
  status: 'active',
}
