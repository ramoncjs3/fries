package main

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"

	"gopkg.in/yaml.v3"
)

// 模块定义 YAML 的结构（DECISIONS.md §10.1）。一处定义，生成前后端全套。
//
// **手写模块**（generated: false）不需要 fields，生成器直接跳过 —— 它们是第 ④ 步的样板，
// 结构由人手维护。**可生成模块**（generated: true）必须有 fields，生成器照它产出代码。

// ModuleDef 是 modules/<key>.yaml 反序列化出来的模块定义。
type ModuleDef struct {
	Key       string   `yaml:"key"`
	Name      string   `yaml:"name"`
	Generated bool     `yaml:"generated"`
	Scoped    bool     `yaml:"scoped"`
	Menu      Menu     `yaml:"menu"`
	Fields    []Field  `yaml:"fields"`
	Sortable  []string `yaml:"sortable"`
	Actions   []string `yaml:"actions"`

	// refResolved 不来自 YAML —— 生成前由 resolveRefTargets 填：每个 ref 目标模块产前端
	// 选择器要用的那点信息（显示字段、有没有 read/搜索）。见 gen_ref.go。
	refResolved map[string]resolvedRef `yaml:"-"`
}

// Menu 是模块在左侧导航里的入口。
type Menu struct {
	Path  string `yaml:"path"`
	Icon  string `yaml:"icon"`
	Order int    `yaml:"order"` // 菜单排序，小的在前；0 视为默认 500
}

// menuOrder 返回菜单排序值，0 用默认 500（内置模块在 100–300 和 800–900）。
func (m Menu) menuOrder() int {
	if m.Order == 0 {
		return 500
	}
	return m.Order
}

// Field 是一个业务字段。属性 → 产出的映射见 DECISIONS.md §10.1 那张表。
type Field struct {
	Name       string            `yaml:"name"`
	Type       string            `yaml:"type"`
	Label      string            `yaml:"label"`
	Required   bool              `yaml:"required"`
	Unique     bool              `yaml:"unique"`
	Searchable bool              `yaml:"searchable"`
	Filterable bool              `yaml:"filterable"`
	Max        int               `yaml:"max"`       // string 用：varchar(n) + zod .max(n)
	Default    string            `yaml:"default"`   // 有默认值的字段
	Values     map[string]string `yaml:"values"`    // enum 用：code -> 中文标签
	Precision  []int             `yaml:"precision"` // decimal 用：[总位数, 小数位]
	Ref        string            `yaml:"ref"`       // ref 用：目标模块 key（外键指向 <目标复数表>）
}

// 字段类型（DECISIONS.md §10.2）。常出现的抽成常量，其余按字面量。
const (
	typeString    = "string"
	typeText      = "text"
	typeInt       = "int"
	typeEnum      = "enum"
	typeDecimal   = "decimal"
	typeBool      = "bool"
	typeDate      = "date"
	typeTimestamp = "timestamp"
	typeRef       = "ref"
)

// bool 字段 default 的两个合法值（抽常量，goconst 会数重复字面量）。
const (
	boolTrue  = "true"
	boolFalse = "false"
)

// tenantIDCol 是每张租户表都有的那一列。整个包里判它的地方都用这个常量。
const tenantIDCol = "tenant_id"

// 字段类型全集，和 DECISIONS.md §10.2 的类型映射表一一对应。
// file/json 还没做 —— 不列进来（宁可校验期直接拒，也不要偷偷退化成 text 那种「看着配了其实没生效」）。
var fieldTypes = map[string]bool{
	typeString: true, typeText: true, typeInt: true, typeDecimal: true, typeBool: true,
	typeEnum: true, typeDate: true, typeTimestamp: true, typeRef: true,
}

// 生成器自动加的标准列（§2.2），不许被业务字段占用。
var reservedFieldNames = map[string]bool{
	"id": true, tenantIDCol: true, "created_at": true, "updated_at": true,
	"deleted_at": true, "created_by": true, "version": true,
}

// 生成的 ListFilter / 列表 query 里内建的字段名，业务字段占了会和它们撞
// （如字段名 keyword 会和 search 注入的 Keyword 重名 → 结构体重复字段编译不过）。
var reservedQueryNames = map[string]bool{
	"page": true, "page_size": true, "keyword": true,
}

// sqlc.yaml 的 rename 会把这些列名产成和 pascal() 不一样的 Go 字段名（ip→IP、http_status→HTTPStatus），
// 而生成器的 pascal 只认 id 缩略词 —— 撞上就 service/handler 引用的字段名和 sqlc 产的对不上、4e 编译炸，
// 报错点离根因很远。直接在校验期拒。加 sqlc.yaml 的 rename 时，这里也要同步加。
var sqlcRenamedNames = map[string]bool{
	"ip": true, "http_status": true,
}

// 允许的动作（权限点）。
const (
	actList   = "list"
	actRead   = "read"
	actCreate = "create"
	actUpdate = "update"
	actDelete = "delete"
	actExport = "export"
)

var moduleActions = map[string]bool{
	actList: true, actRead: true, actCreate: true, actUpdate: true, actDelete: true, actExport: true,
}

var (
	rxModuleIdent = regexp.MustCompile(`^[a-z][a-z0-9_]*$`)
	rxMenuPath    = regexp.MustCompile(`^/[a-z0-9-]+$`)
	rxEnumCode    = regexp.MustCompile(`^[a-z0-9_]+$`)
)

// LoadModuleDef 读一个 modules/<key>.yaml 并校验。文件名里的 key 必须和内容里的 key 一致。
func LoadModuleDef(path string) (*ModuleDef, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("读 %s: %w", path, err)
	}
	var m ModuleDef
	// KnownFields：YAML 里出现结构体没有的字段就报错 —— 防止把 `filterable` 敲成
	// `filterble` 这类静默失效（那正是这个项目反复警告的「看着配了、其实没生效」）。
	dec := yaml.NewDecoder(strings.NewReader(string(raw)))
	dec.KnownFields(true)
	if err := dec.Decode(&m); err != nil {
		return nil, fmt.Errorf("解析 %s: %w", path, err)
	}

	fileKey := strings.TrimSuffix(filepath.Base(path), ".yaml")
	if m.Key != fileKey {
		return nil, fmt.Errorf("%s 里的 key=%q 和文件名 %q 不一致", path, m.Key, fileKey)
	}
	if errs := m.Validate(); len(errs) > 0 {
		return nil, fmt.Errorf("模块定义 %s 有 %d 处问题：\n  - %s",
			path, len(errs), strings.Join(errs, "\n  - "))
	}
	return &m, nil
}

// Validate 把模块定义的所有问题一次报全（不是遇到一个就停）。
func (m *ModuleDef) Validate() []string {
	var errs []string
	add := func(format string, a ...any) { errs = append(errs, fmt.Sprintf(format, a...)) }

	if !rxModuleIdent.MatchString(m.Key) {
		add("key %q 不合法：小写字母开头，只能用小写字母、数字、下划线", m.Key)
	}
	if strings.TrimSpace(m.Name) == "" {
		add("name 不能为空（模块的中文名）")
	}

	// 手写模块（generated: false）不产代码，字段这些不强求；只校验上面的基本项。
	if !m.Generated {
		return errs
	}

	if m.Menu.Path == "" || !rxMenuPath.MatchString(m.Menu.Path) {
		add("menu.path %q 不合法：形如 /suppliers", m.Menu.Path)
	}
	if strings.TrimSpace(m.Menu.Icon) == "" {
		add("menu.icon 不能为空（要在前端 IconMap 里注册过的图标名）")
	}
	hasList := false
	for _, a := range m.Actions {
		if !moduleActions[a] {
			add("action %q 不认识（可选：list/read/create/update/delete/export）", a)
		}
		if a == actList {
			hasList = true
		}
	}
	// list 是硬性的：前端/service 无条件产 List，缺了它前端会调一个后端没注册的路由。
	if !hasList {
		add("actions 必须包含 list（前端和 service 的列表是无条件生成的）")
	}
	if len(m.Fields) == 0 {
		add("generated: true 的模块必须有 fields")
	}

	seen := map[string]bool{}
	for i, f := range m.Fields {
		where := f.Name
		if where == "" {
			where = fmt.Sprintf("第 %d 个字段", i+1)
		}
		if !rxModuleIdent.MatchString(f.Name) {
			add("字段 %s 的 name 不合法：小写字母开头，只能用小写字母、数字、下划线", where)
		}
		if reservedFieldNames[f.Name] {
			add("字段名 %q 是生成器自动加的标准列，不能当业务字段（§2.2）", f.Name)
		}
		if reservedQueryNames[f.Name] {
			add("字段名 %q 会和列表查询内建的分页/搜索字段撞（page/page_size/keyword），换个名字", f.Name)
		}
		if sqlcRenamedNames[f.Name] {
			add("字段名 %q 会被 sqlc 重命名（见 sqlc.yaml），和生成器的命名对不上导致编译不过，换个名字", f.Name)
		}
		if seen[f.Name] {
			add("字段名 %q 重复", f.Name)
		}
		seen[f.Name] = true
		if strings.TrimSpace(f.Label) == "" {
			add("字段 %s 缺 label（页面上显示的中文名）", where)
		}
		if !fieldTypes[f.Type] {
			add("字段 %s 的 type %q 不认识（见 DECISIONS.md §10.2）", where, f.Type)
		}
		errs = append(errs, f.validateByType(where)...)
	}

	// sortable 白名单里的每一项要么是业务字段，要么是标准排序列。
	for _, s := range m.Sortable {
		if !seen[s] && s != "created_at" && s != "updated_at" {
			add("sortable 里的 %q 不是已声明的字段，也不是标准排序列（created_at/updated_at）", s)
		}
	}
	return errs
}

// validateByType 校验和字段类型强相关的属性。
func (f *Field) validateByType(where string) []string {
	var errs []string
	add := func(format string, a ...any) { errs = append(errs, fmt.Sprintf(format, a...)) }

	switch f.Type {
	case typeEnum:
		if len(f.Values) == 0 {
			add("enum 字段 %s 必须给 values（code: 中文标签）", where)
		}
		// enum code 要像别的标识符一样受字符集约束 —— 否则带引号/特殊字符的 code 会拼进
		// 迁移的 CHECK(...) 和前端标签映射，产出坏 SQL/坏 TS（enumCheck 不转义）。
		for code := range f.Values {
			if !rxEnumCode.MatchString(code) {
				add("enum 字段 %s 的 code %q 不合法：只能用小写字母/数字/下划线", where, code)
			}
		}
		if f.Default != "" && f.Values[f.Default] == "" {
			add("enum 字段 %s 的 default %q 不在 values 里", where, f.Default)
		}
	case typeDecimal:
		if len(f.Precision) != 2 {
			add("decimal 字段 %s 必须给 precision [总位数, 小数位]（金额一般 [18, 2]）", where)
		}
	case typeRef:
		if !rxModuleIdent.MatchString(f.Ref) {
			add("ref 字段 %s 必须给 ref（目标模块 key，形如 supplier），当前 %q", where, f.Ref)
		}
	case typeString:
		if f.Max <= 0 {
			add("string 字段 %s 必须给 max（varchar 长度）", where)
		}
	case typeInt:
		if f.Default != "" {
			if _, err := strconv.Atoi(f.Default); err != nil {
				add("int 字段 %s 的 default %q 不是整数", where, f.Default)
			}
		}
	case typeBool:
		if f.Default != "" && f.Default != boolTrue && f.Default != boolFalse {
			add("bool 字段 %s 的 default %q 只能是 true 或 false", where, f.Default)
		}
	}
	// searchable 只对文本类字段有意义（ILIKE + trgm 索引）。
	if f.Searchable && f.Type != typeString && f.Type != typeText {
		add("字段 %s 是 %s 类型，searchable 只对 string/text 有意义", where, f.Type)
	}
	// decimal/date/timestamp 的 default 目前会静默失效：applyDefaults 不给这几类兜默认值
	// （构造成本高），而可空列走 narg 传 NULL 又绕过 DB 的 DEFAULT。与其「看着配了、其实没生效」，
	// 不如校验期直接拒——要默认值就设成 required，或在生成后的 seed service 里手补。
	if f.Default != "" {
		switch f.Type {
		case typeDecimal, typeDate, typeTimestamp:
			add("字段 %s 是 %s 类型，暂不支持 default（会静默失效）：要么设为 required，要么在 seed service 里手补", where, f.Type)
		}
	}

	// filterable 是精确等值筛选，只支持 string/text/enum/date/timestamp。
	// int/decimal/bool 的「精确等值」几乎无意义（数字/金额要范围、bool 要三态），
	// 生成器暂不产它们的 filter 解析（handler 只认这几类，见 gen_handler.go 的 writeFilterConv）。
	if f.Filterable {
		switch f.Type {
		case typeString, typeText, typeEnum, typeDate, typeTimestamp:
		default:
			add("字段 %s 是 %s 类型，filterable 只支持 string/text/enum/date/timestamp", where, f.Type)
		}
	}
	return errs
}
