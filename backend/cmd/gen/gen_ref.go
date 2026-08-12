package main

import (
	"fmt"
	"path/filepath"
	"strings"
)

// gen_ref.go 解析 ref 字段的目标模块信息，供前端选择器（RefSelect）产出用。
//
// ref 存的是目标记录 uuid，前端要「搜名字点一下」而不是让人输 uuid。产选择器要知道目标模块的：
// 显示字段（拿哪个字段给人看/搜）、有没有 read（能不能 get 反查标签）、有没有可搜字段（列表接口
// 认不认 keyword）。这些都在目标的 YAML 里，生成前解析一次存进 def.refResolved。

// conventionalNameField 是「约定俗成的展示字段名」。ref 选择器挑目标的显示字段时优先用它。
const conventionalNameField = "name"

// resolvedRef 是一个 ref 目标模块「产选择器要用到的那点信息」。
type resolvedRef struct {
	Key          string // 目标模块 key（import 路径 @/features/<Key>/api）
	Entity       string // pascal 单数（get<Entity> / <Entity>Query）
	Entities     string // pascal 复数（list<Entities>）
	DisplayField string // 选择器显示 + 搜索用的字段（snake）
	HasRead      bool   // 目标有 read 动作 → 能 get<Entity> 反查标签
	HasSearch    bool   // 目标有可搜字段 → 列表接口认 keyword
}

// hasRef 判断模块有没有 ref 字段。
func hasRef(def *ModuleDef) bool {
	for _, f := range def.Fields {
		if f.Type == typeRef {
			return true
		}
	}
	return false
}

// refTargetKeys 返回 ref 字段指向的**去重**目标 key（按字段出现顺序）。
func refTargetKeys(def *ModuleDef) []string {
	var keys []string
	seen := map[string]bool{}
	for _, f := range def.Fields {
		if f.Type == typeRef && !seen[f.Ref] {
			seen[f.Ref] = true
			keys = append(keys, f.Ref)
		}
	}
	return keys
}

// resolveRefTargets 给每个 ref 目标模块解析出选择器要用的信息，存进 def.refResolved。
// 目标 YAML 前面 checkRefTargets 已确认存在；这里再加载一次拿它的字段/动作。
func resolveRefTargets(root string, def *ModuleDef) error {
	if !hasRef(def) {
		return nil
	}
	def.refResolved = map[string]resolvedRef{}
	for _, key := range refTargetKeys(def) {
		target := def // 自引用：目标就是自己，别再读一次盘
		if key != def.Key {
			t, err := LoadModuleDef(filepath.Join(root, "modules", key+".yaml"))
			if err != nil {
				return fmt.Errorf("解析 ref 目标模块 %q：%w", key, err)
			}
			target = t
		}
		disp, err := refDisplayField(target)
		if err != nil {
			return err
		}
		acts := actionSet(target)
		def.refResolved[key] = resolvedRef{
			Key:          key,
			Entity:       pascal(key),
			Entities:     pascal(pluralize(key)),
			DisplayField: disp.Name,
			HasRead:      acts[actRead],
			HasSearch:    hasSearch(target),
		}
	}
	return nil
}

// refLabelFields 返回「读态要显示名字」的 ref 字段：目标解析出了显示字段的（refResolved 里有）。
// List/Get 查询给这些字段 LEFT JOIN 出 <字段>_label，读态直接显示名字（不用前端逐条反查）。
// 目标显示字段总能解析出来（resolveRefTargets 保证，否则生成期就报了），所以判据是「目标在
// refResolved 里且有显示字段」。
func refLabelFields(def *ModuleDef) []Field {
	var out []Field
	for _, f := range def.Fields {
		if f.Type == typeRef && def.refResolved[f.Ref].DisplayField != "" {
			out = append(out, f)
		}
	}
	return out
}

// labelSelectCols 产 List/Get 里的 <字段>_label 选择列（跟在 sqlc.embed(t) 后面）。
func labelSelectCols(def *ModuleDef, refs []Field) string {
	var b strings.Builder
	for i, f := range refs {
		r := def.refResolved[f.Ref]
		fmt.Fprintf(&b, ",\n       r%d.%s AS %s_label", i, r.DisplayField, f.Name)
	}
	return b.String()
}

// labelJoins 产 List/Get 里反查目标名字的 LEFT JOIN。租户锚在 JOIN 条件上
// （r.tenant_id = t.tenant_id）—— 和 user 模块一样，租户检查器认这个。
func labelJoins(def *ModuleDef, refs []Field) string {
	var b strings.Builder
	for i, f := range refs {
		r := def.refResolved[f.Ref]
		ra := fmt.Sprintf("r%d", i)
		fmt.Fprintf(&b, "\nLEFT JOIN %s %s ON %s.%s = t.%s AND %s.id = t.%s AND %s.deleted_at IS NULL",
			pluralize(r.Key), ra, ra, tenantIDCol, tenantIDCol, ra, f.Name, ra)
	}
	return b.String()
}

// writeRefSelectImport 产 RefSelect 的 import（ref 编辑控件）。放在 @/components 那组里。
func writeRefSelectImport(b *strings.Builder, def *ModuleDef) {
	if hasRef(def) {
		b.WriteString("import { RefSelect } from '@/components/RefSelect'\n")
	}
}

// writeRefTargetAPIImports 产 ref 目标模块的 api import，**只在编辑态要**：RefSelect 搜索用
// list<Entities>、反查当前选中项名字用 get<Entity>（目标有 read 才有）。读态的名字走查询 JOIN
// 出的 <字段>_label 列，不调 api。非编辑态一个都不产（避免未用 import → tsc noUnusedLocals 炸）。
func writeRefTargetAPIImports(b *strings.Builder, def *ModuleDef, edit bool) {
	if !edit {
		return
	}
	for _, key := range refTargetKeys(def) {
		r := def.refResolved[key]
		fns := []string{"list" + r.Entities}
		if r.HasRead {
			fns = append(fns, "get"+r.Entity)
		}
		fmt.Fprintf(b, "import { %s } from '@/features/%s/api'\n", strings.Join(fns, ", "), r.Key)
	}
}

// refLabelValueExpr 是 ref 字段读态显示名字的表达式（列表 cell / 详情读态共用）。
// 名字由查询 LEFT JOIN 出的 <字段>_label 列带回来（见 labelJoins），空（没关联/已删）兜 '—'。
// obj 是 'row'（列表）或 'entity'（详情）。
func refLabelValueExpr(f Field, obj string) string {
	return fmt.Sprintf("%s.%s_label || '—'", obj, f.Name)
}

// detailReadExpr 是详情读态一个字段的展示表达式。ref 且解析出了显示字段 → 显示 JOIN 出的名字；
// 其余走通用 valueExpr。
func detailReadExpr(def *ModuleDef, f Field) string {
	if f.Type == typeRef && def.refResolved[f.Ref].DisplayField != "" {
		return refLabelValueExpr(f, "entity")
	}
	return valueExpr(f, "entity")
}

// writeRefControl 产一个 ref 字段的编辑控件（远程搜索选择器）。
func writeRefControl(b *strings.Builder, def *ModuleDef, f Field) {
	r := def.refResolved[f.Ref]
	fmt.Fprintf(b, "              <Controller\n                control={form.control}\n                name=%q\n", f.Name)
	b.WriteString("                render={({ field }) => (\n                  <RefSelect\n")
	fmt.Fprintf(b, "                    entity=%q\n", r.Key)
	b.WriteString("                    value={field.value ?? ''}\n                    onChange={field.onChange}\n                    inputRef={field.ref}\n")
	fmt.Fprintf(b, "                    placeholder=\"搜索%s…\"\n", f.Label)
	if r.HasSearch {
		fmt.Fprintf(b, "                    search={async (keyword) => {\n                      const page = await list%s({ page: 1, pageSize: 20, keyword: keyword || undefined })\n", r.Entities)
	} else {
		fmt.Fprintf(b, "                    search={async () => {\n                      const page = await list%s({ page: 1, pageSize: 20 })\n", r.Entities)
	}
	// label 兜 ?? ''：目标的显示字段可空时是 string|null，RefOption.label 要 string。
	fmt.Fprintf(b, "                      return page.items.map((it) => ({ value: it.id, label: it.%s ?? '' }))\n                    }}\n", r.DisplayField)
	if r.HasRead {
		fmt.Fprintf(b, "                    resolveLabel={async (id) => (await get%s(id)).%s ?? ''}\n", r.Entity, r.DisplayField)
	}
	b.WriteString("                  />\n                )}\n              />\n")
}

// refDisplayField 挑目标模块用来「显示 + 搜索」的字段。
//
// 优先「name 且可搜」（最理想：标签友好、搜索还真能过滤），其次任意可搜的 string/text（搜索能用），
// 再次名为 name 的（标签友好但搜索不过滤），最后第一个 string/text。一个能显示的文本字段都没有就
// 报错 —— uuid 没法给人看，让开发者给目标加一个（对齐本项目「校验期就报」的原则）。
func refDisplayField(target *ModuleDef) (Field, error) {
	var nameField, firstSearchable, firstText *Field
	for i := range target.Fields {
		f := &target.Fields[i]
		if f.Type != typeString && f.Type != typeText {
			continue
		}
		if firstText == nil {
			firstText = f
		}
		if f.Searchable && firstSearchable == nil {
			firstSearchable = f
		}
		if f.Name == conventionalNameField && nameField == nil {
			nameField = f
		}
	}
	switch {
	case nameField != nil && nameField.Searchable:
		return *nameField, nil
	case firstSearchable != nil:
		return *firstSearchable, nil
	case nameField != nil:
		return *nameField, nil
	case firstText != nil:
		return *firstText, nil
	}
	return Field{}, fmt.Errorf("ref 目标模块 %q 没有可显示的文本字段（string/text）—— 选择器要靠它"+
		"给人看名字，给目标加一个（建议 name 且 searchable）", target.Key)
}
