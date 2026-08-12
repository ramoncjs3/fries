package main

import (
	"flag"
	"fmt"
	"path/filepath"
	"strings"
)

// runModule 是模块代码生成器。
//
// 第 ⑤ 步才实现 —— 模板要等第 ④ 步四个真实模块做出来才有原料，
// 现在硬写模板等于凭空发明规范（DECISIONS.md §11）。
//
// 🔴 **实现的时候，模板必须是多租户的**（MULTI-TENANCY.md §7.4）：
//
//	建表迁移      带 tenant_id NOT NULL，唯一索引一律 tenant_id 打头（§8.4）
//	queries.sql   每条都带 sqlc.arg('tenant_id')，**「按 id 查一行」也要带**（§11.1，BOLA）
//	service       只拿 Store.ForTenant()，业务代码看不见 TenantID
//	前端页面      不用感知租户（后端注入），登录/权限相关的模板才要改
//
// 漏掉任何一条，`make lint-sql` 会当场报 —— 但那是发现，不是预防。
// 生成器一旦吐出不带租户的模板，后面每个模块都从错的地方起步。
func runModule(root string, args []string) error {
	fs := flag.NewFlagSet("module", flag.ContinueOnError)
	name := fs.String("name", "", "模块 key，对应 modules/<name>.yaml")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *name == "" {
		return fmt.Errorf("用法：gen module -name <模块 key>")
	}

	path := filepath.Join(root, "modules", *name+".yaml")
	def, err := LoadModuleDef(path)
	if err != nil {
		return err
	}

	// 手写模块（generated: false）本来就不该走生成器 —— 它们是第 ④ 步的样板，人手维护。
	if !def.Generated {
		return fmt.Errorf("模块 %s 是 generated: false（手写模块），生成器不动它。"+
			"要生成新模块，YAML 里写 generated: true 并补上 fields", def.Key)
	}

	// ref 字段的目标模块必须真的存在（有 modules/<目标>.yaml）——否则复合外键要等迁移跑起来
	// 才报晦涩的 FK 错。在这里就把 typo 的目标拦下（对齐本项目「校验期就报」的原则）。
	if err := checkRefTargets(root, def); err != nil {
		return err
	}
	// 解析 ref 目标模块的显示字段/动作，供前端选择器（RefSelect）产出用。目标没有可显示的
	// 文本字段会在这里报错（选择器要靠它给人看名字）。
	if err := resolveRefTargets(root, def); err != nil {
		return err
	}

	fmt.Printf("✓ 模块定义 %s 校验通过（%d 个字段，%d 个动作）\n\n",
		def.Key, len(def.Fields), len(def.Actions))

	results, err := writeModule(root, def)
	if err != nil {
		return err
	}
	fmt.Println("写盘：")
	incremental := false
	for _, r := range results {
		fmt.Printf("  %-24s %s\n", r.status, relTo(root, r.path))
		if strings.HasPrefix(r.status, "written(ALTER") {
			incremental = true
		}
	}
	if incremental {
		// 增量：托管文件（queries）自动带上了新列，但种子文件（service/handler/前端）不覆盖。
		fmt.Println("\n⚠️ 这是增量改字段：新列已进迁移和 queries，但**种子文件不会自动带上** ——" +
			"要让新字段真的能录入/显示，手动把它补进 service 的 Input/View、handler 的 Body/toInput、" +
			"前端的 types/schema/页面（照同模块已有字段抄）。不补的话代码照样编译，只是新字段永远是空的。")
	}

	// 登记进现有文件（app.go 装配 / tenantsql 租户表 / 前端路由）。幂等，插不上会报错让人手动加。
	regs, err := registerModule(root, def)
	if err != nil {
		fmt.Printf("\n⚠️ 自动登记没做完：%v\n照下面手动加剩下的。\n", err)
	} else {
		fmt.Println("\n登记：")
		for _, r := range regs {
			fmt.Printf("  %-16s %s\n", r.status, relTo(root, r.path))
		}
	}

	fmt.Println("\n还要手动跑（把 SQL/类型/文档同步出来）：")
	for i, s := range postGenSteps(def) {
		fmt.Printf("  %d. %s\n", i+1, s)
	}
	fmt.Println("\n跑完 make check 确认全绿。")
	return nil
}

// checkRefTargets 确认每个 ref 字段的目标模块真的存在（有 modules/<目标>.yaml）。
// 复合外键指向 <目标复数表>(tenant_id, id)，目标不存在的话迁移一跑就报 FK 错，太晚也太晦涩。
func checkRefTargets(root string, def *ModuleDef) error {
	for _, f := range def.Fields {
		if f.Type != typeRef {
			continue
		}
		if f.Ref == def.Key {
			continue // 自引用：自己的 yaml 刚加载过，一定在
		}
		p := filepath.Join(root, "modules", f.Ref+".yaml")
		if !fileExists(p) {
			return fmt.Errorf("字段 %s 的 ref 目标模块 %q 不存在（找不到 %s）——"+
				"目标模块要先建好（它的表 + (tenant_id, id) 锚点是复合外键的落点）",
				f.Name, f.Ref, relTo(root, p))
		}
	}
	return nil
}
