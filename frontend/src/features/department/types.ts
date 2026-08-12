import type { components } from '@/api/schema'

/**
 * 一个部门节点。类型来自后端 OpenAPI，不手写一遍 —— 真相唯一在 Go 侧
 * （DECISIONS.md §1）。后端改了字段这里编译期就报错。重新生成：make gen-api
 */
export type Department = components['schemas']['Department']

/**
 * 「作为一级部门」在上级下拉里的取值。
 *
 * 不能用空串 —— 空串在 Radix Select 里是保留值（表示「没选」），选项用了它不显示。
 * 提交前会换回 undefined。
 */
export const ROOT_VALUE = '__root__'

/** 查询条件。部门不分页，只有筛选。 */
export interface DepartmentQuery {
  keyword?: string
  status?: string
}

/** 新增/编辑的提交内容。 */
export interface DepartmentInput {
  parent_id?: string
  name: string
  code: string
  sort_order: number
  remark: string
  status: string
}

/** 拼好树之后的节点：多一个 children、层级，和「含下级」的人数合计。 */
export interface DepartmentNode extends Department {
  children: DepartmentNode[]
  depth: number
  /**
   * 自己 + 所有后代的直属人数。
   *
   * **两个数都要留着**，因为它们回答的是两个问题：
   *   - `user_count`  这个部门自己有几个人（删部门、发通知看它）
   *   - `total_count` 这条线上一共几个人（看规模、汇报人数看它）
   *
   * 在前端算而不是让后端多返一列：扁平全量数据本来就一次取回来了，
   * 这里算是精确的，还省一次递归查询。
   */
  total_count: number
}

/**
 * 把扁平列表拼成树。
 *
 * 后端返回的是**扁平的全量节点**（树切成分页就拼不起来了），拼树是前端的事。
 *
 * 注意最后那一段：父节点被筛掉、但子节点命中关键词时，子节点会挂不上任何父亲。
 * 不管的话它就从界面上消失了 —— 搜到了却看不见，比搜不到还难受。所以这些
 * **孤儿节点一律提到根上**。
 */
export function buildTree(list: Department[]): DepartmentNode[] {
  const nodes = new Map<string, DepartmentNode>()
  for (const item of list) {
    nodes.set(item.id, { ...item, children: [], depth: 0, total_count: item.user_count })
  }

  const roots: DepartmentNode[] = []
  for (const node of nodes.values()) {
    const parent = node.parent_id ? nodes.get(node.parent_id) : undefined
    if (parent) parent.children.push(node)
    else roots.push(node)
  }

  // 深度是渲染缩进用的，人数合计要自底向上加，两个都得等树拼完才算得出来
  const stamp = (list: DepartmentNode[], depth: number): number => {
    let sum = 0
    for (const node of list) {
      node.depth = depth
      node.total_count = node.user_count + stamp(node.children, depth + 1)
      sum += node.total_count
    }
    return sum
  }
  stamp(roots, 0)
  return roots
}

/** 一个节点和它所有后代的 id。选上级部门时要把这些排除掉，否则就成环了。 */
export function subtreeIDs(list: Department[], rootID: string): Set<string> {
  const childrenOf = new Map<string, string[]>()
  for (const item of list) {
    if (!item.parent_id) continue
    const siblings = childrenOf.get(item.parent_id) ?? []
    siblings.push(item.id)
    childrenOf.set(item.parent_id, siblings)
  }

  const out = new Set<string>([rootID])
  const stack = [rootID]
  while (stack.length > 0) {
    const id = stack.pop() as string
    for (const child of childrenOf.get(id) ?? []) {
      if (out.has(child)) continue // 数据真出了环也不至于死循环
      out.add(child)
      stack.push(child)
    }
  }
  return out
}
