package main

import (
	"fmt"
	"go/format"
	"strings"
)

// gen_service.go 产出 internal/service/<key>/service.go —— 模块的业务层（种子文件）。
//
// ⚠️ 多租户是模板的第一性（MULTI-TENANCY.md §7.4）：每个对外方法第一行都 s.tenant(ctx)
// 取本请求的租户句柄（ForTenant），业务代码碰不到 tenant_id。软删除走 deleted_at，
// 改/删带乐观锁 version，唯一冲突翻成人话（uk_<表>_<字段> → Err<字段>Taken）。
//
// 产出的类型（View/Input/ListFilter）用 sqlc 生成的原生类型，fromRow / Args 直接赋值，
// 不做 int↔int32 之类的来回转换 —— 转换点多一个，出错面就多一个。类型对齐见 §10.2：
//
//	string/text/enum  -> string
//	int               -> int32
//	decimal           -> decimal.Decimal
//	bool              -> bool
//	date/timestamp    -> time.Time（sqlc.yaml 里 date/timestamptz 都 override 成 time.Time）
//
// 可空列（!required）一律指针（sqlc emit_pointers_for_null_types）。这是种子文件：
// 生成一次，之后归人/AI 所有，重生成不覆盖 —— 业务规则（校验、联动）在这里手补。

// goSvcType 是字段在 service 层的 Go 类型 —— 和 sqlc 生成的列类型一致。
// !required 的列可空，一律指针。
func goSvcType(f Field) string {
	base := svcBaseType(f)
	if !f.Required {
		return "*" + base
	}
	return base
}

// svcBaseType 是字段的基础 Go 类型（不含指针）。
func svcBaseType(f Field) string {
	switch f.Type {
	case typeInt:
		return "int32"
	case typeDecimal:
		return "decimal.Decimal"
	case typeBool:
		return "bool"
	case typeDate, typeTimestamp:
		return "time.Time"
	case typeRef:
		return "uuid.UUID"
	default: // string / text / enum 都落到 Go 的 string（其值恰好等于 typeString）
		return typeString
	}
}

// svcImports 按用到的字段类型算出 service.go 的 import，分三组返回（stdlib / 三方 / 本地），
// 组间空行 —— 和 goimports 的 local-prefixes 分组一致（.golangci.yml）。
// 组内顺序无所谓，format.Source 会排。
func svcImports(def *ModuleDef) [][]string {
	stdlib := []string{"context", "errors", "time"}
	third := []string{"github.com/google/uuid", "github.com/jackc/pgx/v5"}
	local := []string{
		"github.com/ramoncjs3/fries/internal/audit",
		"github.com/ramoncjs3/fries/internal/authz",
		"github.com/ramoncjs3/fries/internal/errs",
		"github.com/ramoncjs3/fries/internal/repo",
	}
	for _, f := range def.Fields {
		if f.Type == typeDecimal {
			third = append(third, "github.com/shopspring/decimal")
			break
		}
	}
	return [][]string{stdlib, third, local}
}

// filterFields 返回可筛选字段（filterClause 对应的 narg 参数，一律指针）。
func filterFields(def *ModuleDef) []Field {
	var out []Field
	for _, f := range def.Fields {
		if f.Filterable {
			out = append(out, f)
		}
	}
	return out
}

// hasSearch 判断有没有可搜字段（searchClause 是否产出 keyword 参数）。
func hasSearch(def *ModuleDef) bool {
	for _, f := range def.Fields {
		if f.Searchable {
			return true
		}
	}
	return false
}

// genService 产出模块业务层的完整源码。
func genService(def *ModuleDef) string {
	entity := pascal(def.Key)
	entities := pascal(pluralize(def.Key))
	table := pluralize(def.Key)

	var b strings.Builder
	b.WriteString(seedHeader)
	fmt.Fprintf(&b, "// Package %s 是%s模块的业务层。handler 只调这里，不直接碰 repo（红线 #6）。\n",
		def.Key, def.Name)
	fmt.Fprintf(&b, "package %s\n\n", def.Key)

	writeImports(&b, svcImports(def))

	writeTypes(&b, def, entity)
	writeConstructor(&b, def.Name)
	writeList(&b, def, entity, entities)
	writeGet(&b, def, entity)
	writeCreate(&b, def, entity)
	writeUpdate(&b, def, entity)
	writeDelete(&b, entity)
	writeHelpers(&b, def, entity, table)

	// 过一遍 gofmt：手拼的缩进/对齐靠不住，交给 format.Source 收口。
	// 模板是我们自己的，格式化失败说明产出器写错了 —— 直接 panic 把 bug 顶出来。
	return formatGo(b.String())
}

// formatGo 把产出的 Go 源码 gofmt 一遍。模板出问题就 panic（测试会兜住）。
func formatGo(src string) string {
	out, err := format.Source([]byte(src))
	if err != nil {
		panic(fmt.Sprintf("产出的 Go 无法格式化（产出器 bug）：%v\n\n%s", err, src))
	}
	return string(out)
}

// writeImports 产出分组 import 块（组间空行）。
// 条目里已含引号的（带别名，形如 `x "path"`）原样写，否则按路径加引号。
func writeImports(b *strings.Builder, groups [][]string) {
	b.WriteString("import (\n")
	for i, group := range groups {
		if i > 0 {
			b.WriteString("\n")
		}
		for _, imp := range group {
			if strings.Contains(imp, "\"") {
				fmt.Fprintf(b, "\t%s\n", imp)
			} else {
				fmt.Fprintf(b, "\t%q\n", imp)
			}
		}
	}
	b.WriteString(")\n\n")
}

// writeTypes 产出 View / Input / ListFilter 三个结构。
func writeTypes(b *strings.Builder, def *ModuleDef, entity string) {
	// View：对外返回。类型名带模块前缀（原样进 OpenAPI schema 名，别的模块也有 Item/Entry，撞了 huma 会飘）。
	fmt.Fprintf(b, "// %s 是%s对外的视图。\n", entity, def.Name)
	fmt.Fprintf(b, "type %s struct {\n", entity)
	fmt.Fprintf(b, "\tID        uuid.UUID `json:\"id\"`\n")
	for _, f := range def.Fields {
		fmt.Fprintf(b, "\t%s %s `json:%q` // %s\n", pascal(f.Name), goSvcType(f), f.Name, f.Label)
	}
	// ref 反查出来的目标名字（只读，来自查询 JOIN 出的 <字段>_label；空 = 没关联/目标已删）。
	for _, f := range refLabelFields(def) {
		fmt.Fprintf(b, "\t%sLabel string `json:\"%s_label\"` // %s名称（只读）\n", pascal(f.Name), f.Name, f.Label)
	}
	fmt.Fprintf(b, "\tCreatedAt time.Time `json:\"created_at\"`\n")
	fmt.Fprintf(b, "\tUpdatedAt time.Time `json:\"updated_at\"`\n")
	fmt.Fprintf(b, "\tVersion   int32     `json:\"version\"` // 乐观锁版本号，更新时原样传回\n")
	b.WriteString("}\n\n")

	// Input：新增/编辑入参（不含 id / 时间戳 / version）。
	fmt.Fprintf(b, "// Input 是新增/编辑%s的入参。\n", def.Name)
	b.WriteString("type Input struct {\n")
	for _, f := range def.Fields {
		fmt.Fprintf(b, "\t%s %s // %s\n", pascal(f.Name), goSvcType(f), f.Label)
	}
	b.WriteString("}\n\n")

	// ListFilter：查询条件。keyword 走 ILIKE，filterable 走等值（指针，nil 即不筛）。
	fmt.Fprintf(b, "// ListFilter 是%s列表的查询条件。\n", def.Name)
	b.WriteString("type ListFilter struct {\n")
	if hasSearch(def) {
		b.WriteString("\tKeyword  string // 关键字，对可搜字段做 ILIKE\n")
	}
	for _, f := range filterFields(def) {
		fmt.Fprintf(b, "\t%s *%s // 按%s筛，nil 不筛\n", pascal(f.Name), svcBaseType(f), f.Label)
	}
	b.WriteString("\tPage     int\n")
	b.WriteString("\tPageSize int\n")
	b.WriteString("}\n\n")
}

func writeConstructor(b *strings.Builder, name string) {
	fmt.Fprintf(b, "// Service 是%s服务。\n", name)
	b.WriteString("type Service struct {\n\tstore *repo.Store\n}\n\n")
	fmt.Fprintf(b, "// New 造%s服务。\n", name)
	b.WriteString("func New(store *repo.Store) *Service { return &Service{store: store} }\n\n")
	b.WriteString("// tenant 取当前请求的租户句柄。每个对外方法第一行都该是它 ——\n")
	b.WriteString("// 没有租户就报错，不是放行（MULTI-TENANCY.md §1.2 ②）。\n")
	b.WriteString("func (s *Service) tenant(ctx context.Context) (*repo.TenantQueries, error) {\n")
	b.WriteString("\tid, err := authz.MustTenant(ctx)\n")
	b.WriteString("\tif err != nil {\n\t\treturn nil, err\n\t}\n")
	b.WriteString("\treturn s.store.ForTenant(id), nil\n}\n\n")
}

// writeList 产出 List（分页 + 关键字 + 筛选 + 总数）。
func writeList(b *strings.Builder, def *ModuleDef, entity, entities string) {
	search := hasSearch(def)
	filters := filterFields(def)

	fmt.Fprintf(b, "// List 分页查询%s。\n", def.Name)
	fmt.Fprintf(b, "func (s *Service) List(ctx context.Context, f ListFilter) ([]%s, int64, error) {\n", entity)
	b.WriteString("\tq, err := s.tenant(ctx)\n\tif err != nil {\n\t\treturn nil, 0, err\n\t}\n")
	if search {
		b.WriteString("\tkeyword := repo.LikePattern(f.Keyword)\n")
	}

	// List args：Limit/Offset 恒有，keyword/filter 按需。
	fmt.Fprintf(b, "\trows, err := q.List%s(ctx, repo.List%sArgs{\n", entities, entities)
	b.WriteString("\t\tLimit:  int32(f.PageSize),\n")
	b.WriteString("\t\tOffset: int32((f.Page - 1) * f.PageSize),\n")
	if search {
		b.WriteString("\t\tKeyword: keyword,\n")
	}
	for _, f := range filters {
		fmt.Fprintf(b, "\t\t%s: f.%s,\n", pascal(f.Name), pascal(f.Name))
	}
	b.WriteString("\t})\n")
	b.WriteString("\tif err != nil {\n\t\treturn nil, 0, errs.Internal.Wrap(err)\n\t}\n")

	// Count args：0 参裸调，1 参裸传，≥2 参用 Args 结构（sqlc 的生成规则）。
	writeCountCall(b, entities, search, filters)

	fmt.Fprintf(b, "\tout := make([]%s, 0, len(rows))\n", entity)
	labelRefs := refLabelFields(def)
	if len(labelRefs) == 0 {
		b.WriteString("\tfor _, row := range rows {\n\t\tout = append(out, fromRow(row))\n\t}\n")
	} else {
		// 有 ref 名字反查：查询用了 sqlc.embed(t)，行是 {<Entity>, <字段>Label}。
		b.WriteString("\tfor _, row := range rows {\n")
		fmt.Fprintf(b, "\t\titem := fromRow(row.%s)\n", entity)
		for _, f := range labelRefs {
			fmt.Fprintf(b, "\t\titem.%sLabel = textOr(row.%sLabel)\n", pascal(f.Name), pascal(f.Name))
		}
		b.WriteString("\t\tout = append(out, item)\n\t}\n")
	}
	b.WriteString("\treturn out, total, nil\n}\n\n")
}

// writeCountCall 按 count 查询的参数个数选调用形态。
func writeCountCall(b *strings.Builder, entities string, search bool, filters []Field) {
	n := len(filters)
	if search {
		n++
	}
	switch n {
	case 0:
		fmt.Fprintf(b, "\ttotal, err := q.Count%s(ctx)\n", entities)
	case 1:
		arg := "keyword"
		if !search {
			arg = "f." + pascal(filters[0].Name)
		}
		fmt.Fprintf(b, "\ttotal, err := q.Count%s(ctx, %s)\n", entities, arg)
	default:
		fmt.Fprintf(b, "\ttotal, err := q.Count%s(ctx, repo.Count%sArgs{\n", entities, entities)
		if search {
			b.WriteString("\t\tKeyword: keyword,\n")
		}
		for _, f := range filters {
			fmt.Fprintf(b, "\t\t%s: f.%s,\n", pascal(f.Name), pascal(f.Name))
		}
		b.WriteString("\t})\n")
	}
	b.WriteString("\tif err != nil {\n\t\treturn nil, 0, errs.Internal.Wrap(err)\n\t}\n")
}

func writeGet(b *strings.Builder, def *ModuleDef, entity string) {
	fmt.Fprintf(b, "// Get 按 id 查一行（句柄已绑租户，改 URL 里的 id 只会查到 0 行）。\n")
	fmt.Fprintf(b, "func (s *Service) Get(ctx context.Context, id uuid.UUID) (%s, error) {\n", entity)
	b.WriteString("\tq, err := s.tenant(ctx)\n")
	fmt.Fprintf(b, "\tif err != nil {\n\t\treturn %s{}, err\n\t}\n", entity)
	fmt.Fprintf(b, "\trow, err := q.Get%s(ctx, id)\n", entity)
	fmt.Fprintf(b, "\tif err != nil {\n\t\treturn %s{}, notFoundOr(err)\n\t}\n", entity)
	labelRefs := refLabelFields(def)
	if len(labelRefs) == 0 {
		b.WriteString("\treturn fromRow(row), nil\n}\n\n")
	} else {
		fmt.Fprintf(b, "\tout := fromRow(row.%s)\n", entity)
		for _, f := range labelRefs {
			fmt.Fprintf(b, "\tout.%sLabel = textOr(row.%sLabel)\n", pascal(f.Name), pascal(f.Name))
		}
		b.WriteString("\treturn out, nil\n}\n\n")
	}
}

func writeCreate(b *strings.Builder, def *ModuleDef, entity string) {
	fmt.Fprintf(b, "// Create 新增一条%s。\n", def.Name)
	fmt.Fprintf(b, "func (s *Service) Create(ctx context.Context, in Input) (%s, error) {\n", entity)
	b.WriteString("\tq, err := s.tenant(ctx)\n")
	fmt.Fprintf(b, "\tif err != nil {\n\t\treturn %s{}, err\n\t}\n", entity)
	b.WriteString("\tin.applyDefaults()\n")
	b.WriteString("\tid, err := uuid.NewV7()\n")
	fmt.Fprintf(b, "\tif err != nil {\n\t\treturn %s{}, errs.Internal.Wrap(err)\n\t}\n", entity)
	fmt.Fprintf(b, "\trow, err := q.Create%s(ctx, repo.Create%sArgs{\n", entity, entity)
	b.WriteString("\t\tID: id,\n")
	for _, f := range def.Fields {
		fmt.Fprintf(b, "\t\t%s: in.%s,\n", pascal(f.Name), pascal(f.Name))
	}
	b.WriteString("\t\tCreatedBy: actorID(ctx),\n")
	b.WriteString("\t})\n")
	fmt.Fprintf(b, "\tif err != nil {\n\t\treturn %s{}, uniqueOr(err)\n\t}\n", entity)
	b.WriteString("\taudit.SetResourceID(ctx, row.ID)\n")
	b.WriteString("\treturn fromRow(row), nil\n}\n\n")
}

func writeUpdate(b *strings.Builder, def *ModuleDef, entity string) {
	fmt.Fprintf(b, "// Update 编辑%s。version 对不上返回 common.version_conflict（§2.4）。\n", def.Name)
	fmt.Fprintf(b, "func (s *Service) Update(ctx context.Context, id uuid.UUID, version int, in Input) (%s, error) {\n", entity)
	b.WriteString("\tq, err := s.tenant(ctx)\n")
	fmt.Fprintf(b, "\tif err != nil {\n\t\treturn %s{}, err\n\t}\n", entity)
	b.WriteString("\tin.applyDefaults()\n")
	fmt.Fprintf(b, "\trow, err := q.Update%s(ctx, repo.Update%sArgs{\n", entity, entity)
	b.WriteString("\t\tID: id,\n")
	for _, f := range def.Fields {
		fmt.Fprintf(b, "\t\t%s: in.%s,\n", pascal(f.Name), pascal(f.Name))
	}
	b.WriteString("\t\tVersion: int32(version),\n")
	b.WriteString("\t})\n")
	b.WriteString("\tif err != nil {\n")
	fmt.Fprintf(b, "\t\tif errors.Is(err, pgx.ErrNoRows) {\n\t\t\treturn %s{}, conflictOrNotFound(ctx, q, id)\n\t\t}\n", entity)
	fmt.Fprintf(b, "\t\treturn %s{}, uniqueOr(err)\n\t}\n", entity)
	b.WriteString("\taudit.SetResourceID(ctx, row.ID)\n")
	b.WriteString("\treturn fromRow(row), nil\n}\n\n")
}

func writeDelete(b *strings.Builder, entity string) {
	fmt.Fprintf(b, "// Delete 软删除。version 对不上或已不在返回冲突/未找到。\n")
	b.WriteString("func (s *Service) Delete(ctx context.Context, id uuid.UUID, version int) error {\n")
	b.WriteString("\tq, err := s.tenant(ctx)\n\tif err != nil {\n\t\treturn err\n\t}\n")
	fmt.Fprintf(b, "\trows, err := q.SoftDelete%s(ctx, repo.SoftDelete%sArgs{ID: id, Version: int32(version)})\n", entity, entity)
	b.WriteString("\tif err != nil {\n\t\treturn errs.Internal.Wrap(err)\n\t}\n")
	b.WriteString("\tif rows == 0 {\n\t\treturn conflictOrNotFound(ctx, q, id)\n\t}\n")
	b.WriteString("\taudit.SetResourceID(ctx, id)\n\treturn nil\n}\n\n")
}

// writeHelpers 产出 fromRow / applyDefaults / uniqueOr / notFoundOr / conflictOrNotFound / actorID。
func writeHelpers(b *strings.Builder, def *ModuleDef, entity, table string) {
	// fromRow：sqlc 行 → View，类型一致直接赋值。
	fmt.Fprintf(b, "// fromRow 把 sqlc 行映射成对外视图。\n")
	fmt.Fprintf(b, "func fromRow(row repo.%s) %s {\n", entity, entity)
	fmt.Fprintf(b, "\treturn %s{\n", entity)
	b.WriteString("\t\tID: row.ID,\n")
	for _, f := range def.Fields {
		fmt.Fprintf(b, "\t\t%s: row.%s,\n", pascal(f.Name), pascal(f.Name))
	}
	b.WriteString("\t\tCreatedAt: row.CreatedAt,\n")
	b.WriteString("\t\tUpdatedAt: row.UpdatedAt,\n")
	b.WriteString("\t\tVersion:   row.Version,\n")
	b.WriteString("\t}\n}\n\n")

	// textOr：ref 反查的名字列是可空文本（LEFT JOIN 没命中就是 NULL），取出来 NULL 当空串。
	if len(refLabelFields(def)) > 0 {
		b.WriteString("// textOr 把可空文本取出，NULL 当空串。\n")
		b.WriteString("func textOr(v *string) string {\n\tif v == nil {\n\t\treturn \"\"\n\t}\n\treturn *v\n}\n\n")
	}

	// applyDefaults：可空且带 default 的字段，入参没给就填默认值（narg 传 NULL 会绕过 DB 默认）。
	b.WriteString("// applyDefaults 给没传的可选字段补默认值。\n")
	b.WriteString("//\n")
	b.WriteString("// 为什么在这里补：列可空 + narg，入参 nil 时 INSERT 会显式写 NULL，绕过 DB 的 DEFAULT。\n")
	b.WriteString("func (in *Input) applyDefaults() {\n")
	for _, f := range def.Fields {
		lit, ok := defaultLit(f)
		if !ok {
			continue
		}
		fmt.Fprintf(b, "\tif in.%s == nil {\n\t\tv := %s\n\t\tin.%s = &v\n\t}\n", pascal(f.Name), lit, pascal(f.Name))
	}
	b.WriteString("}\n\n")

	// uniqueOr：唯一冲突翻成人话；无唯一字段时只兜底。
	b.WriteString("// uniqueOr 把唯一索引冲突翻成模块的错误码。\n")
	b.WriteString("func uniqueOr(err error) error {\n")
	var uniques []Field
	for _, f := range def.Fields {
		if f.Unique {
			uniques = append(uniques, f)
		}
	}
	switch len(uniques) {
	case 0:
		// 无唯一字段，只兜底。
	case 1:
		// 单个唯一字段用 if —— 单-case switch 会被 gocritic 拦（singleCaseSwitch）。
		f := uniques[0]
		fmt.Fprintf(b, "\tif repo.IsUniqueViolation(err, \"uk_%s_%s\") {\n\t\treturn Err%sTaken\n\t}\n",
			table, f.Name, pascal(f.Name))
	default:
		b.WriteString("\tswitch {\n")
		for _, f := range uniques {
			fmt.Fprintf(b, "\tcase repo.IsUniqueViolation(err, \"uk_%s_%s\"):\n\t\treturn Err%sTaken\n",
				table, f.Name, pascal(f.Name))
		}
		b.WriteString("\t}\n")
	}
	b.WriteString("\treturn errs.Internal.Wrap(err)\n}\n\n")

	// notFoundOr / conflictOrNotFound。
	b.WriteString("// notFoundOr 把「查不到」翻成 NotFound，其余算内部错误。\n")
	b.WriteString("func notFoundOr(err error) error {\n")
	b.WriteString("\tif errors.Is(err, pgx.ErrNoRows) {\n\t\treturn errs.NotFound\n\t}\n")
	b.WriteString("\treturn errs.Internal.Wrap(err)\n}\n\n")

	b.WriteString("// conflictOrNotFound 区分「版本过期」和「记录不存在」：改/删命中 0 行时再查一次。\n")
	b.WriteString("func conflictOrNotFound(ctx context.Context, q *repo.TenantQueries, id uuid.UUID) error {\n")
	fmt.Fprintf(b, "\tif _, err := q.Get%s(ctx, id); err != nil {\n", entity)
	b.WriteString("\t\tif errors.Is(err, pgx.ErrNoRows) {\n\t\t\treturn errs.NotFound\n\t\t}\n")
	b.WriteString("\t\treturn errs.Internal.Wrap(err)\n\t}\n")
	b.WriteString("\treturn errs.VersionConflict\n}\n\n")

	// actorID：当前操作者（写 created_by / 审计）。
	b.WriteString("// actorID 取当前操作者 id（非用户主体返回 nil）。\n")
	b.WriteString("func actorID(ctx context.Context) *uuid.UUID {\n")
	b.WriteString("\tp, ok := authz.PrincipalFrom(ctx)\n")
	b.WriteString("\tif !ok || !p.IsUser() {\n\t\treturn nil\n\t}\n")
	b.WriteString("\tid := p.ID\n\treturn &id\n}\n")
}

// defaultLit 返回可选字段默认值的 Go 字面量（用于 applyDefaults）。
// 只处理 string/text/enum/int/bool；required 字段（非指针，nil 判不了）和
// decimal/date 的默认值（少见，构造成本高）跳过 —— 返回 ok=false。
func defaultLit(f Field) (string, bool) {
	if f.Required || f.Default == "" {
		return "", false
	}
	switch f.Type {
	case typeString, typeText, typeEnum:
		return fmt.Sprintf("%q", f.Default), true
	case typeInt:
		return fmt.Sprintf("int32(%s)", f.Default), true
	case typeBool:
		return f.Default, true
	default:
		return "", false
	}
}
