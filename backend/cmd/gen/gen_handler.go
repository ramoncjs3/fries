package main

import (
	"fmt"
	"sort"
	"strings"
)

// gen_handler.go 产出 internal/handler/<key>.go —— 模块的 HTTP 层（种子文件）。
//
// 这一层只做「HTTP 形状 ↔ service 入参」的翻译，业务判断一律在 service（红线 #6）。
// 校验规则写在 huma 的 struct tag 上（huma 自动校验 + 产 OpenAPI），service 不重复抄格式校验。
//
// Body/query 用 JSON 友好的类型（decimal/date 走 string，前端好传），toInput 里内联解析成
// service 的原生类型（*decimal.Decimal / *time.Time）。**内联不抽 helper**：生成多个模块时
// 每个 handler 自包含，不会撞 handler 包里的同名函数，也不会留下没人调的 helper 触发 unused。
//
// 可空字段（!required）翻成指针，nil 即「没传」。解析失败回 errInvalidField（字段级错误，
// 前端挂到对应表单项）。

// actionSet 把 def.Actions 收成集合，方便按动作产对应的输入/方法/路由。
func actionSet(def *ModuleDef) map[string]bool {
	set := map[string]bool{}
	for _, a := range def.Actions {
		set[a] = true
	}
	return set
}

// hbodyType 是字段在 huma Body 里的 JSON 类型（decimal/date/timestamp 走 string）。
//
// **可选的 int/bool 用指针**：plain int/bool 的零值（0 / false）和「没传」在 JSON 里分不开，
// 而 0/false 常是合法业务值。用 *int/*bool 让 nil 表示「没传」，service.applyDefaults 才能
// 正确兜默认值（否则可选 bool 的 default 永远被显式 false 覆盖）。可选 string 沿用 "" 当哨兵
// （空串本就是「清空」的自然表达，和 role 手写层的 optional() 一致）。
func hbodyType(f Field) string {
	switch f.Type {
	case typeInt:
		if f.Required {
			return "int"
		}
		return "*int"
	case typeBool:
		if f.Required {
			return "bool"
		}
		return "*bool"
	case typeDecimal, typeDate, typeTimestamp:
		return typeString
	default: // string / text / enum
		return typeString
	}
}

// hbodyTags 产出字段的 huma 校验 tag（json + 各类约束）。
func hbodyTags(f Field) string {
	tags := []string{fmt.Sprintf("json:%q", jsonTag(f))}
	switch f.Type {
	case typeString:
		if f.Required {
			tags = append(tags, `minLength:"1"`)
		}
		if f.Max > 0 {
			tags = append(tags, fmt.Sprintf("maxLength:%q", itoa(f.Max)))
		}
	case typeText:
		max := f.Max
		if max <= 0 {
			max = 2000
		}
		tags = append(tags, fmt.Sprintf("maxLength:%q", itoa(max)))
	case typeEnum:
		tags = append(tags, fmt.Sprintf("enum:%q", enumTagValues(f)))
		if f.Default != "" {
			tags = append(tags, fmt.Sprintf("default:%q", f.Default))
		}
	case typeDecimal:
		tags = append(tags, `doc:"数字，字符串传"`)
	case typeDate:
		tags = append(tags, `format:"date"`)
	case typeTimestamp:
		tags = append(tags, `format:"date-time"`)
	case typeRef:
		tags = append(tags, `format:"uuid"`, `doc:"目标记录的 ID"`)
	}
	return strings.Join(tags, " ")
}

// jsonTag 是字段的 json tag 值：可选字段带 omitempty。
func jsonTag(f Field) string {
	if f.Required {
		return f.Name
	}
	return f.Name + ",omitempty"
}

// enumTagValues 把 enum 的 code 排序拼成 "a,b,c"（排序保证产出稳定）。
func enumTagValues(f Field) string {
	codes := make([]string, 0, len(f.Values))
	for code := range f.Values {
		codes = append(codes, code)
	}
	sort.Strings(codes)
	return strings.Join(codes, ",")
}

func itoa(n int) string { return fmt.Sprintf("%d", n) }

// hasFieldType 判断有没有任一给定类型的业务字段。
func hasFieldType(def *ModuleDef, types ...string) bool {
	for _, f := range def.Fields {
		for _, t := range types {
			if f.Type == t {
				return true
			}
		}
	}
	return false
}

// hasFilterType 判断有没有任一给定类型的可筛选字段。
func hasFilterType(def *ModuleDef, types ...string) bool {
	for _, f := range filterFields(def) {
		for _, t := range types {
			if f.Type == t {
				return true
			}
		}
	}
	return false
}

// handlerImports 算 handler.go 的 import（三分组，svc 包带别名）。
// time/decimal 是否要 import，取决于产出的代码里是否真的引用了 —— 只 delete 动作时
// 就算有 date 字段也不产解析代码，import 进来反而是「未用 import」编译不过。
func handlerImports(def *ModuleDef, alias string) [][]string {
	stdlib := []string{"context", "net/http"}
	third := []string{`"github.com/danielgtaylor/huma/v2"`}
	acts := actionSet(def)
	writesBody := acts[actCreate] || acts[actUpdate]            // toInput 解析
	writesFilter := acts[actList] || acts[actExport]            // filter 解析
	needDecimal := writesBody && hasFieldType(def, typeDecimal) // decimal 不可 filter，只来自 body
	needTime := (writesBody && hasFieldType(def, typeDate, typeTimestamp)) ||
		(writesFilter && hasFilterType(def, typeDate, typeTimestamp))
	needUUID := writesBody && hasFieldType(def, typeRef) // ref 的 toInput 要 uuid.Parse
	if needTime {
		stdlib = append(stdlib, "time")
	}
	if needUUID {
		third = append(third, `"github.com/google/uuid"`)
	}
	if needDecimal {
		third = append(third, `"github.com/shopspring/decimal"`)
	}
	local := []string{
		"github.com/ramoncjs3/fries/internal/httpx",
		"github.com/ramoncjs3/fries/internal/perm",
		"github.com/ramoncjs3/fries/internal/perm/modules",
		fmt.Sprintf("%s %q", alias, "github.com/ramoncjs3/fries/internal/service/"+def.Key),
	}
	return [][]string{stdlib, third, local}
}

// genHandler 产出模块 HTTP 层的完整源码。
func genHandler(def *ModuleDef) string {
	entity := pascal(def.Key)
	table := pluralize(def.Key)
	alias := def.Key + "svc"
	acts := actionSet(def)

	var b strings.Builder
	b.WriteString(seedHeader)
	b.WriteString("package handler\n\n")
	writeImports(&b, handlerImports(def, alias))

	// 结构体 + 构造器。
	fmt.Fprintf(&b, "// %s 是%s管理接口。\n", entity, def.Name)
	fmt.Fprintf(&b, "type %s struct {\n\tsvc *%s.Service\n}\n\n", entity, alias)
	fmt.Fprintf(&b, "// New%s 造%s handler。\n", entity, def.Name)
	fmt.Fprintf(&b, "func New%s(svc *%s.Service) *%s { return &%s{svc: svc} }\n\n", entity, alias, entity, entity)

	writeHandlerInputs(&b, def, entity, acts)
	writeRegister(&b, def, entity, table, acts)
	writeHandlerMethods(&b, def, entity, alias, acts)

	return formatGo(b.String())
}

// writeHandlerInputs 产出各动作要用的 huma 输入结构。
func writeHandlerInputs(b *strings.Builder, def *ModuleDef, entity string, acts map[string]bool) {
	// List 输入（分页 + keyword + filter query）。list 或 export 用到。
	if acts[actList] || acts[actExport] {
		fmt.Fprintf(b, "// List%sInput 是%s列表查询入参。\n", entity, def.Name)
		fmt.Fprintf(b, "type List%sInput struct {\n", entity)
		b.WriteString("\tPage     int `query:\"page\" default:\"1\" minimum:\"1\"`\n")
		b.WriteString("\tPageSize int `query:\"page_size\" default:\"20\" minimum:\"1\" maximum:\"100\"`\n")
		if hasSearch(def) {
			b.WriteString("\tKeyword  string `query:\"keyword\" maxLength:\"64\" doc:\"关键字模糊匹配\"`\n")
		}
		for _, f := range filterFields(def) {
			tag := fmt.Sprintf("query:%q", f.Name)
			if f.Type == typeEnum {
				tag += fmt.Sprintf(" enum:%q", enumTagValues(f))
			}
			fmt.Fprintf(b, "\t%s string `%s doc:%q`\n", pascal(f.Name), tag, "按"+f.Label+"筛选")
		}
		b.WriteString("}\n\n")
	}

	// Get 输入。
	if acts[actRead] {
		fmt.Fprintf(b, "// Get%sInput 是查看单条入参。\n", entity)
		fmt.Fprintf(b, "type Get%sInput struct {\n\tID string `path:\"id\" format:\"uuid\"`\n}\n\n", entity)
	}

	// Body（新增/编辑共用）。
	if acts[actCreate] || acts[actUpdate] {
		fmt.Fprintf(b, "// %sBody 是新增/编辑%s的请求体。校验写在 tag 上，service 不重复抄格式校验。\n", entity, def.Name)
		fmt.Fprintf(b, "type %sBody struct {\n", entity)
		for _, f := range def.Fields {
			fmt.Fprintf(b, "\t%s %s `%s`\n", pascal(f.Name), hbodyType(f), hbodyTags(f))
		}
		b.WriteString("}\n\n")
	}

	if acts[actCreate] {
		fmt.Fprintf(b, "// Create%sInput 是新增入参。\n", entity)
		fmt.Fprintf(b, "type Create%sInput struct {\n\tBody %sBody\n}\n\n", entity, entity)
	}
	if acts[actUpdate] {
		fmt.Fprintf(b, "// Update%sInput 是编辑入参（带乐观锁版本号）。\n", entity)
		fmt.Fprintf(b, "type Update%sInput struct {\n\tID   string `path:\"id\" format:\"uuid\"`\n", entity)
		fmt.Fprintf(b, "\tBody struct {\n\t\t%sBody\n\t\tVersion int `json:\"version\" minimum:\"0\" doc:\"乐观锁版本号，取自上次读到的值\"`\n\t}\n}\n\n", entity)
	}
	if acts[actDelete] {
		fmt.Fprintf(b, "// Delete%sInput 是删除入参。\n", entity)
		fmt.Fprintf(b, "type Delete%sInput struct {\n\tID   string `path:\"id\" format:\"uuid\"`\n", entity)
		b.WriteString("\tBody struct {\n\t\tVersion int `json:\"version\" minimum:\"0\" doc:\"乐观锁版本号\"`\n\t}\n}\n\n")
	}
}

// route 是一个动作对应的路由规格。
type route struct {
	action  string
	method  string
	path    string
	opID    string
	summary string
	handler string
	extra   string // 额外的 Operation 字段（如 DefaultStatus）
}

// routesFor 按 canonical 顺序返回模块声明了的动作对应的路由。
func routesFor(def *ModuleDef, table string, acts map[string]bool) []route {
	all := []route{
		{actList, "http.MethodGet", "/" + table, "list-" + table, "查询" + def.Name, "list", ""},
		{actRead, "http.MethodGet", "/" + table + "/{id}", "get-" + def.Key, "查看" + def.Name, "get", ""},
		{actCreate, "http.MethodPost", "/" + table, "create-" + def.Key, "新增" + def.Name, "create",
			"\t\tDefaultStatus: http.StatusCreated,\n"},
		{actUpdate, "http.MethodPut", "/" + table + "/{id}", "update-" + def.Key, "编辑" + def.Name, "update", ""},
		{actDelete, "http.MethodDelete", "/" + table + "/{id}", "delete-" + def.Key, "删除" + def.Name, "remove", ""},
		{actExport, "http.MethodGet", "/" + table + "/export", "export-" + table, "导出" + def.Name, "export", ""},
	}
	var out []route
	for _, r := range all {
		if acts[r.action] {
			out = append(out, r)
		}
	}
	return out
}

// writeRegister 产出 Register<Entity>：每个动作一条 perm.Guard 守着的路由。
func writeRegister(b *strings.Builder, def *ModuleDef, entity, table string, acts map[string]bool) {
	fmt.Fprintf(b, "// Register%s 注册%s路由。\n", entity, def.Name)
	fmt.Fprintf(b, "func Register%s(api huma.API, h *%s) {\n", entity, entity)
	for i, r := range routesFor(def, table, acts) {
		if i > 0 {
			b.WriteString("\n")
		}
		fmt.Fprintf(b, "\tperm.Guard(api, modules.%s.Point(%s), huma.Operation{\n", entity, actionKeyExpr(r.action))
		fmt.Fprintf(b, "\t\tOperationID: %q,\n", r.opID)
		fmt.Fprintf(b, "\t\tMethod:      %s,\n", r.method)
		fmt.Fprintf(b, "\t\tPath:        %q,\n", r.path)
		fmt.Fprintf(b, "\t\tSummary:     %q,\n", r.summary)
		fmt.Fprintf(b, "\t\tTags:        []string{modules.%s.Key},\n", entity)
		b.WriteString(r.extra)
		fmt.Fprintf(b, "\t}, h.%s)\n", r.handler)
	}
	b.WriteString("}\n\n")
}

// writeHandlerMethods 产出各 handler 方法 + toInput 翻译。
func writeHandlerMethods(b *strings.Builder, def *ModuleDef, entity, alias string, acts map[string]bool) {
	viewT := fmt.Sprintf("%s.%s", alias, entity)

	if acts[actList] || acts[actExport] {
		writeListMethod(b, def, entity, alias, viewT)
	}
	if acts[actExport] {
		fmt.Fprintf(b, "// export 导出全部匹配行（不分页，取一个大上限）。\n")
		fmt.Fprintf(b, "func (h *%s) export(ctx context.Context, in *List%sInput) (*httpx.PageResponse[%s], error) {\n", entity, entity, viewT)
		b.WriteString("\tin.Page = 1\n\tin.PageSize = 10000\n\treturn h.list(ctx, in)\n}\n\n")
	}
	if acts[actRead] {
		fmt.Fprintf(b, "// get 查看单条。\n")
		fmt.Fprintf(b, "func (h *%s) get(ctx context.Context, in *Get%sInput) (*httpx.Response[%s], error) {\n", entity, entity, viewT)
		b.WriteString("\tid, err := parsePathID(in.ID)\n\tif err != nil {\n\t\treturn nil, err\n\t}\n")
		b.WriteString("\tfound, err := h.svc.Get(ctx, id)\n\tif err != nil {\n\t\treturn nil, err\n\t}\n")
		b.WriteString("\treturn httpx.OK(found), nil\n}\n\n")
	}
	if acts[actCreate] {
		fmt.Fprintf(b, "// create 新增。\n")
		fmt.Fprintf(b, "func (h *%s) create(ctx context.Context, in *Create%sInput) (*httpx.Response[%s], error) {\n", entity, entity, viewT)
		fmt.Fprintf(b, "\tinput, err := to%sInput(in.Body)\n\tif err != nil {\n\t\treturn nil, err\n\t}\n", entity)
		b.WriteString("\tcreated, err := h.svc.Create(ctx, input)\n\tif err != nil {\n\t\treturn nil, err\n\t}\n")
		b.WriteString("\treturn httpx.OK(created), nil\n}\n\n")
	}
	if acts[actUpdate] {
		fmt.Fprintf(b, "// update 编辑。\n")
		fmt.Fprintf(b, "func (h *%s) update(ctx context.Context, in *Update%sInput) (*httpx.Response[%s], error) {\n", entity, entity, viewT)
		b.WriteString("\tid, err := parsePathID(in.ID)\n\tif err != nil {\n\t\treturn nil, err\n\t}\n")
		fmt.Fprintf(b, "\tinput, err := to%sInput(in.Body.%sBody)\n\tif err != nil {\n\t\treturn nil, err\n\t}\n", entity, entity)
		b.WriteString("\tupdated, err := h.svc.Update(ctx, id, in.Body.Version, input)\n\tif err != nil {\n\t\treturn nil, err\n\t}\n")
		b.WriteString("\treturn httpx.OK(updated), nil\n}\n\n")
	}
	if acts[actDelete] {
		fmt.Fprintf(b, "// remove 软删除。\n")
		fmt.Fprintf(b, "func (h *%s) remove(ctx context.Context, in *Delete%sInput) (*httpx.Response[struct{}], error) {\n", entity, entity)
		b.WriteString("\tid, err := parsePathID(in.ID)\n\tif err != nil {\n\t\treturn nil, err\n\t}\n")
		b.WriteString("\tif err := h.svc.Delete(ctx, id, in.Body.Version); err != nil {\n\t\treturn nil, err\n\t}\n")
		b.WriteString("\treturn httpx.OK(struct{}{}), nil\n}\n\n")
	}
	if acts[actCreate] || acts[actUpdate] {
		writeToInput(b, def, entity, alias)
	}
}

// writeListMethod 产出 list：把 query 翻成 service 的 ListFilter（filter 内联解析成指针）。
func writeListMethod(b *strings.Builder, def *ModuleDef, entity, alias, viewT string) {
	fmt.Fprintf(b, "// list 分页查询。\n")
	fmt.Fprintf(b, "func (h *%s) list(ctx context.Context, in *List%sInput) (*httpx.PageResponse[%s], error) {\n", entity, entity, viewT)
	fmt.Fprintf(b, "\tfilter := %s.ListFilter{\n\t\tPage:     in.Page,\n\t\tPageSize: in.PageSize,\n", alias)
	if hasSearch(def) {
		b.WriteString("\t\tKeyword:  in.Keyword,\n")
	}
	b.WriteString("\t}\n")
	for _, f := range filterFields(def) {
		writeFilterConv(b, f)
	}
	b.WriteString("\titems, total, err := h.svc.List(ctx, filter)\n\tif err != nil {\n\t\treturn nil, err\n\t}\n")
	b.WriteString("\treturn httpx.Paged(items, in.Page, in.PageSize, total), nil\n}\n\n")
}

// writeFilterConv 把一个 filter query（string）内联翻成 service ListFilter 的指针字段。
func writeFilterConv(b *strings.Builder, f Field) {
	field := pascal(f.Name)
	fmt.Fprintf(b, "\tif in.%s != \"\" {\n", field)
	switch f.Type {
	case typeDate:
		fmt.Fprintf(b, "\t\tv, err := time.Parse(\"2006-01-02\", in.%s)\n", field)
		fmt.Fprintf(b, "\t\tif err != nil {\n\t\t\treturn nil, errInvalidField(%q, \"日期格式应为 YYYY-MM-DD\")\n\t\t}\n", "query."+f.Name)
		fmt.Fprintf(b, "\t\tfilter.%s = &v\n", field)
	case typeTimestamp:
		fmt.Fprintf(b, "\t\tv, err := time.Parse(time.RFC3339, in.%s)\n", field)
		fmt.Fprintf(b, "\t\tif err != nil {\n\t\t\treturn nil, errInvalidField(%q, \"时间格式应为 RFC3339\")\n\t\t}\n", "query."+f.Name)
		fmt.Fprintf(b, "\t\tfilter.%s = &v\n", field)
	default: // string / text / enum
		fmt.Fprintf(b, "\t\tv := in.%s\n\t\tfilter.%s = &v\n", field, field)
	}
	b.WriteString("\t}\n")
}

// writeToInput 产出 to<Entity>Input：Body → service.Input，内联解析 decimal/date。
func writeToInput(b *strings.Builder, def *ModuleDef, entity, alias string) {
	fmt.Fprintf(b, "// to%sInput 把请求体翻成 service 入参（decimal/date 在这里解析，格式错回字段级错误）。\n", entity)
	fmt.Fprintf(b, "func to%sInput(body %sBody) (%s.Input, error) {\n", entity, entity, alias)
	fmt.Fprintf(b, "\tin := %s.Input{}\n", alias)
	for _, f := range def.Fields {
		writeFieldConv(b, f)
	}
	b.WriteString("\treturn in, nil\n}\n")
}

// writeFieldConv 把一个 Body 字段内联翻成 service.Input 字段（按类型 + 是否可空分派）。
func writeFieldConv(b *strings.Builder, f Field) {
	field := pascal(f.Name)
	loc := "body." + f.Name
	switch f.Type {
	case typeDecimal:
		if f.Required {
			fmt.Fprintf(b, "\t%sVal, err := decimal.NewFromString(body.%s)\n", lower1(field), field)
			fmt.Fprintf(b, "\tif err != nil {\n\t\treturn in, errInvalidField(%q, \"不是合法的数字\")\n\t}\n", loc)
			fmt.Fprintf(b, "\tin.%s = %sVal\n", field, lower1(field))
		} else {
			fmt.Fprintf(b, "\tif body.%s != \"\" {\n", field)
			fmt.Fprintf(b, "\t\tv, err := decimal.NewFromString(body.%s)\n", field)
			fmt.Fprintf(b, "\t\tif err != nil {\n\t\t\treturn in, errInvalidField(%q, \"不是合法的数字\")\n\t\t}\n", loc)
			fmt.Fprintf(b, "\t\tin.%s = &v\n\t}\n", field)
		}
	case typeDate, typeTimestamp:
		layout, msg := "\"2006-01-02\"", "日期格式应为 YYYY-MM-DD"
		if f.Type == typeTimestamp {
			layout, msg = "time.RFC3339", "时间格式应为 RFC3339"
		}
		if f.Required {
			fmt.Fprintf(b, "\t%sVal, err := time.Parse(%s, body.%s)\n", lower1(field), layout, field)
			fmt.Fprintf(b, "\tif err != nil {\n\t\treturn in, errInvalidField(%q, %q)\n\t}\n", loc, msg)
			fmt.Fprintf(b, "\tin.%s = %sVal\n", field, lower1(field))
		} else {
			fmt.Fprintf(b, "\tif body.%s != \"\" {\n", field)
			fmt.Fprintf(b, "\t\tv, err := time.Parse(%s, body.%s)\n", layout, field)
			fmt.Fprintf(b, "\t\tif err != nil {\n\t\t\treturn in, errInvalidField(%q, %q)\n\t\t}\n", loc, msg)
			fmt.Fprintf(b, "\t\tin.%s = &v\n\t}\n", field)
		}
	case typeInt:
		if f.Required {
			fmt.Fprintf(b, "\tin.%s = int32(body.%s)\n", field, field)
		} else {
			// body.X 是 *int（nil=没传）；转成 service 的 *int32。传了 0 也照样落库。
			fmt.Fprintf(b, "\tif body.%s != nil {\n\t\tv := int32(*body.%s)\n\t\tin.%s = &v\n\t}\n", field, field, field)
		}
	case typeBool:
		// 必填 bool 是值、可选 bool 是 *bool（hbodyType 已按 required 区分），Body 和 Input 类型一致，
		// 两种情况都直接赋。可选时 nil 表示「没传」，留给 service.applyDefaults 兜 default。
		fmt.Fprintf(b, "\tin.%s = body.%s\n", field, field)
	case typeRef:
		// body 里是 uuid 字符串，解析成 uuid.UUID（huma 的 format:"uuid" 拦过一道，这里是第二道）。
		if f.Required {
			fmt.Fprintf(b, "\t%sVal, err := uuid.Parse(body.%s)\n", lower1(field), field)
			fmt.Fprintf(b, "\tif err != nil {\n\t\treturn in, errInvalidField(%q, \"不是合法的 UUID\")\n\t}\n", loc)
			fmt.Fprintf(b, "\tin.%s = %sVal\n", field, lower1(field))
		} else {
			fmt.Fprintf(b, "\tif body.%s != \"\" {\n", field)
			fmt.Fprintf(b, "\t\tv, err := uuid.Parse(body.%s)\n", field)
			fmt.Fprintf(b, "\t\tif err != nil {\n\t\t\treturn in, errInvalidField(%q, \"不是合法的 UUID\")\n\t\t}\n", loc)
			fmt.Fprintf(b, "\t\tin.%s = &v\n\t}\n", field)
		}
	default: // string / text / enum
		if f.Required {
			fmt.Fprintf(b, "\tin.%s = body.%s\n", field, field)
		} else {
			fmt.Fprintf(b, "\tif body.%s != \"\" {\n\t\tv := body.%s\n\t\tin.%s = &v\n\t}\n", field, field, field)
		}
	}
}

// lower1 把首字母小写（Credit → credit），给内联的局部变量名用，避免和字段名撞。
func lower1(s string) string {
	if s == "" {
		return s
	}
	return strings.ToLower(s[:1]) + s[1:]
}
