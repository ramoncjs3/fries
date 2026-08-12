#!/usr/bin/env bash
# 生成器自测（DECISIONS.md §10.5）：生成一个覆盖全类型的 fixture 模块 → 跑完整 make check → 必须绿。
# 改产出器模板后跑它，确认「生成一个真模块」这条路没被悄悄改坏。全程可回滚，跑完树和跑前一样。
set -euo pipefail
cd "$(dirname "$0")/.."

KEY=gentest
# test-gen 会 git checkout 这些文件来回滚；它们有未提交改动就别跑，免得连你的改动一起还原。
GUARDED="backend/cmd/server/app.go backend/internal/tenantsql/tenantsql.go frontend/src/routes/index.tsx backend/internal/repo backend/go.mod backend/go.sum frontend/src/api/schema.d.ts docs/SCHEMA.md"
for f in $GUARDED; do
  if ! git diff --quiet -- "$f" || ! git diff --cached --quiet -- "$f"; then
    echo "✗ $f 有未提交改动 —— test-gen 会回滚这些文件，先提交/stash 再跑" >&2; exit 1
  fi
done

cleanup() {
  set +e
  rm -f modules/$KEY.yaml
  rm -f backend/db/migrations/*_create_${KEY}s.sql backend/db/queries/$KEY.sql
  rm -rf backend/internal/service/$KEY frontend/src/features/$KEY
  rm -f backend/internal/handler/$KEY.go backend/internal/perm/modules/$KEY.go
  rm -f backend/internal/repo/internal/sqlcgen/$KEY.sql.go backend/gen
  git checkout -q -- $GUARDED 2>/dev/null
  make gen-sqlc >/dev/null 2>&1
}
trap cleanup EXIT

echo "→ 写 fixture 模块 modules/$KEY.yaml（覆盖全字段类型 + ref→supplier）"
cat > modules/$KEY.yaml <<YAML
key: $KEY
name: 生成器自测
generated: true
scoped: true
menu: { path: /${KEY}s, icon: truck }
fields:
  - { name: name, type: string, label: 名称, required: true, unique: true, searchable: true, max: 100 }
  - { name: note, type: text, label: 备注 }
  - { name: qty, type: int, label: 数量 }
  - { name: price, type: decimal, label: 单价, precision: [18, 2] }
  - { name: enabled, type: bool, label: 启用, default: "true" }
  - { name: status, type: enum, label: 状态, filterable: true, default: draft, values: { draft: 草稿, done: 完成 } }
  - { name: due_on, type: date, label: 截止日, filterable: true }
  - { name: seen_at, type: timestamp, label: 查看时间 }
  - { name: supplier_id, type: ref, ref: supplier, label: 供应商, required: true }
actions: [list, read, create, update, delete, export]
YAML

echo "→ make gen-module（写盘 + 自动登记）"
go run -C backend ./cmd/gen module -name $KEY

echo "→ 生成流水线 + 文档同步"
make gen-sqlc gen-tenant-queries gen-api schemadoc >/dev/null

# 跑「验证生成模块」的那套检查，不跑完整 make check 里的 `go test ./...` 全量集成测试 ——
# 那些是 testcontainers（连接偶发闪断、且慢），而且它们测的是已有流程、不测这个生成的模块。
# dev-check 已含：build/vet/全 lint/短测（短模式跳过容器测试）/selfcheck/前端 tsc+eslint+test；
# 再加 lint-structure（模块三件套齐）/lint-generated（schema 文档）/sqlc-diff（生成物同步）。
echo "→ 验证生成的模块（dev-check + 结构/生成物检查）"
make dev-check lint-structure lint-generated sqlc-diff

echo "✓ test-gen 通过：生成器产出一个真模块能编译、类型对、过全部 lint、selfcheck 与前端检查"
