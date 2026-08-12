-- 部门（DECISIONS.md §2）。

-- name: ListDepartments :many
-- 部门是树，**一次全取**：内部系统撑死几百个节点，分页反而会把树切断。
-- 排序保证同一个父下按 sort_order，前端拼树时不用再排一遍。
SELECT d.*, (SELECT count(*) FROM users u
             WHERE u.tenant_id = d.tenant_id AND u.department_id = d.id
               AND u.deleted_at IS NULL) AS user_count
FROM departments d
WHERE d.tenant_id = sqlc.arg('tenant_id')
  AND d.deleted_at IS NULL
  AND (sqlc.narg('keyword')::varchar IS NULL
       OR d.name ILIKE sqlc.narg('keyword') OR d.code ILIKE sqlc.narg('keyword'))
  AND (sqlc.narg('status')::varchar IS NULL OR d.status = sqlc.narg('status'))
ORDER BY d.sort_order, d.name;

-- name: GetDepartment :one
SELECT * FROM departments
WHERE tenant_id = sqlc.arg('tenant_id') AND id = sqlc.arg('id') AND deleted_at IS NULL;

-- name: CreateDepartment :one
INSERT INTO departments (tenant_id, id, parent_id, name, code, sort_order, remark, status, created_by)
VALUES (sqlc.arg('tenant_id'), sqlc.arg('id'), sqlc.narg('parent_id'), sqlc.arg('name'),
        sqlc.arg('code'), sqlc.arg('sort_order'), sqlc.arg('remark'), sqlc.arg('status'),
        sqlc.narg('created_by'))
RETURNING *;

-- name: UpdateDepartment :one
-- 乐观锁：影响 0 行说明版本对不上，service 层翻成 common.version_conflict（§2.4）
UPDATE departments
SET parent_id  = sqlc.narg('parent_id'),
    name       = sqlc.arg('name'),
    code       = sqlc.arg('code'),
    sort_order = sqlc.arg('sort_order'),
    remark     = sqlc.arg('remark'),
    status     = sqlc.arg('status'),
    version    = version + 1
WHERE tenant_id = sqlc.arg('tenant_id')
  AND id = sqlc.arg('id')
  AND version = sqlc.arg('version')
  AND deleted_at IS NULL
RETURNING *;

-- name: SoftDeleteDepartment :one
UPDATE departments
SET deleted_at = now(), version = version + 1
WHERE tenant_id = sqlc.arg('tenant_id')
  AND id = sqlc.arg('id')
  AND version = sqlc.arg('version')
  AND deleted_at IS NULL
RETURNING id;

-- name: CountDepartmentChildren :one
SELECT count(*) FROM departments
WHERE tenant_id = sqlc.arg('tenant_id') AND parent_id = sqlc.arg('parent_id') AND deleted_at IS NULL;

-- name: CountDepartmentUsers :one
SELECT count(*) FROM users
WHERE tenant_id = sqlc.arg('tenant_id') AND department_id = sqlc.arg('department_id')
  AND deleted_at IS NULL;

-- name: ListDepartmentSubtreeIDs :many
-- 取一个节点和它所有后代的 id。改父节点时用来挡「把自己挂到自己的子孙下面」——
-- 那会造出一个从树上断开的环，整棵树直接查不出来。
--
-- ⚠️ **递归那一半也要带 tenant_id**（§10.7）。种子那半带了就完事，是最容易漏的一处：
-- 递归部分只靠 parent_id 关联，看着「跟着种子走自然就在租户里」。
-- 复合外键确实保证了不会有跨租户的父子关系，所以实际泄不了 —— 但这里要的是**纵深防御**：
-- 不依赖另一个约束的正确性。SQL 静态检查也会拦下不带租户条件的那一半。
WITH RECURSIVE subtree AS (
    SELECT d.id AS node_id FROM departments d
    WHERE d.tenant_id = sqlc.arg('tenant_id') AND d.id = sqlc.arg('id') AND d.deleted_at IS NULL
    UNION ALL
    SELECT d.id FROM departments d
    JOIN subtree s ON d.parent_id = s.node_id
    WHERE d.tenant_id = sqlc.arg('tenant_id') AND d.deleted_at IS NULL
)
SELECT node_id FROM subtree;
