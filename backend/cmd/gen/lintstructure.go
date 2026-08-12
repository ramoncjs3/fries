package main

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
)

// 目录结构白名单 —— DECISIONS.md §1.1 的五条规则就靠这份清单强制。
//
// 要加新的顶层目录或 internal 包？**先改 DECISIONS.md §1.1，再改这里。**
// 反过来做就等于绕过了约定。
var (
	allowedTopLevel = map[string]string{
		"AGENTS.md":     "AI 开发契约",
		"CLAUDE.md":     "指向 AGENTS.md",
		"README.md":     "给人看的入口",
		"Makefile":      "唯一命令入口",
		".gitignore":    "",
		".dockerignore": "构建上下文排除项",
		".golangci.yml": "Go lint 配置",
		".squawk.toml":  "PostgreSQL 迁移 linter 配置（MULTI-TENANCY.md §13）",
		".claude":       "项目级 skill",
		"backend":       "Go 服务",
		"config":        "配置",
		"deploy":        "部署",
		"docs":          "文档",
		"frontend":      "React 前端",
		"modules":       "模块定义 YAML",
		"scripts":       "开发脚本（如 test-gen 生成器自测）",
	}

	// 本地才有、不进版本库的东西，不算违规。
	ignoredTopLevel = map[string]bool{
		".git": true, ".DS_Store": true, ".idea": true, ".vscode": true,
		"node_modules": true, ".claude-mem": true,
	}

	allowedInternalPackages = map[string]bool{
		"errs": true, "httpx": true, "config": true, "middleware": true, "task": true,
		"auth": true, "authz": true, "perm": true, "audit": true, "crypto": true,
		"storage": true, "notify": true, "repo": true, "service": true, "handler": true,
		"llm": true, "mcp": true,
		// tenantsql 是「一条 SQL 有没有绑到租户上」的唯一判据，被两处调用：
		// 构建期的 gen lint-sql 和运行期的 pgx tracer。两边必须是同一份实现，
		// 所以它不能住在 repo 或 cmd/gen 里面（MULTI-TENANCY.md §12.2）。
		"tenantsql": true,
	}

	// service/<key>/ 下只允许这三个文件（DECISIONS.md §1.1）
	allowedServiceFiles = map[string]bool{
		"service.go": true, "errors.go": true, "service_test.go": true,
	}

	// features/<key>/ 下只允许这七个文件（DECISIONS.md §1.1、§7.4、§10.3）
	//
	// 三个页面对应三条路由：ListPage=/xxx、NewPage=/xxx/new、DetailPage=/xxx/:id
	// （详情页自己带编辑态，§7.6）。**没有 DetailDrawer** —— 抽屉那一版废掉了。
	// FormDialog 只剩批量操作这类「一次动作」在用，不是单条记录的编辑器。
	allowedFeatureFiles = map[string]bool{
		"api.ts": true, "queries.ts": true, "schema.ts": true, "types.ts": true,
		"ListPage.tsx": true, "NewPage.tsx": true, "DetailPage.tsx": true,
		"FormDialog.tsx": true,
	}
)

// rxModuleKey 从 modules/<key>.yaml 里取 key 字段。
var rxModuleKey = regexp.MustCompile(`(?m)^key:\s*([a-z][a-z0-9_]*)\s*$`)

// runLintStructure 检查目录结构是否符合 DECISIONS.md §1.1。
func runLintStructure(root string, _ []string) error {
	var p problems

	checkTopLevel(root, &p)
	checkInternalPackages(root, &p)
	checkNestedInternal(root, &p)
	checkPerModuleFiles(root, filepath.Join(root, "backend", "internal", "service"),
		allowedServiceFiles, "service/<key>", &p)
	checkPerModuleFiles(root, filepath.Join(root, "frontend", "src", "features"),
		allowedFeatureFiles, "features/<key>", &p)
	checkModuleTriples(root, &p)
	if err := checkMenuIcons(root, &p); err != nil {
		return err
	}

	if err := p.err("目录结构不符合 DECISIONS.md §1.1"); err != nil {
		return err
	}
	fmt.Println("✓ 目录结构符合 DECISIONS.md §1.1")
	return nil
}

// checkTopLevel：顶层目录必须在白名单内。
func checkTopLevel(root string, p *problems) {
	entries, err := os.ReadDir(root)
	if err != nil {
		p.addf("读根目录失败：%v", err)
		return
	}
	for _, e := range entries {
		name := e.Name()
		if ignoredTopLevel[name] {
			continue
		}
		if _, ok := allowedTopLevel[name]; !ok {
			p.addf("顶层多了 %q —— 先在 DECISIONS.md §1.1 里说清楚它是什么，再加进 lint 白名单", name)
		}
	}
}

// checkInternalPackages：backend/internal/* 的包名必须在白名单内。
func checkInternalPackages(root string, p *problems) {
	dir := filepath.Join(root, "backend", "internal")
	entries, err := os.ReadDir(dir)
	if err != nil {
		p.addf("读 backend/internal 失败：%v", err)
		return
	}
	for _, e := range entries {
		if !e.IsDir() {
			p.addf("backend/internal 下不该直接放文件：%s", e.Name())
			continue
		}
		if !allowedInternalPackages[e.Name()] {
			p.addf("internal 包 %q 不在白名单里 —— 先改 DECISIONS.md §1.1", e.Name())
		}
	}
}

// nestedInternalPackages 是允许再套一层 internal 的地方。
//
// 目前只有一处：`internal/repo/internal/sqlcgen`。Go 规定 internal 下的包只能被
// 它父目录那棵树 import，所以 sqlc 的裸产物只有 internal/repo/... 碰得到 ——
// service、handler 在**编译期**就拿不到不带租户的查询句柄（MULTI-TENANCY.md §1.2 ①）。
//
// **这是刻意的例外，不是通用手法。** 想再加一处，先说清楚它挡住了什么。
var nestedInternalPackages = map[string]string{
	"repo/internal/sqlcgen": "sqlc 裸产物，用 Go 的 internal 规则把「绕过租户绑定」堵死",
}

// checkNestedInternal：internal 包里再套 internal 的，只允许白名单里那几处。
func checkNestedInternal(root string, p *problems) {
	base := filepath.Join(root, "backend", "internal")
	outer, err := os.ReadDir(base)
	if err != nil {
		return
	}
	for _, pkg := range outer {
		if !pkg.IsDir() {
			continue
		}
		nested := filepath.Join(base, pkg.Name(), "internal")
		entries, err := os.ReadDir(nested)
		if err != nil {
			continue
		}
		for _, e := range entries {
			key := pkg.Name() + "/internal/" + e.Name()
			if _, ok := nestedInternalPackages[key]; !ok {
				p.addf("internal/%s 不在嵌套 internal 白名单里 —— 这层包可见性是刻意的例外，"+
					"要加先在 DECISIONS.md §1.1 里说清楚它挡住了什么", key)
			}
		}
	}
}

// checkPerModuleFiles：每个模块目录下只允许约定的文件名。
func checkPerModuleFiles(root, dir string, allowed map[string]bool, label string, p *problems) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		// 目录还不存在（第 ① 步就是这样），不算问题。
		return
	}
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		sub := filepath.Join(dir, e.Name())
		files, err := os.ReadDir(sub)
		if err != nil {
			p.addf("读 %s 失败：%v", relTo(root, sub), err)
			continue
		}
		for _, f := range files {
			if f.IsDir() {
				p.addf("%s 下不该有子目录：%s", relTo(root, sub), f.Name())
				continue
			}
			// 测试文件跟着被测文件走：Foo.tsx 的测试就叫 Foo.test.tsx。
			// 白名单里不用一个个列，否则每加一个允许的文件都得配一条测试文件名。
			if name, ok := strings.CutSuffix(f.Name(), ".test.tsx"); ok && allowed[name+".tsx"] {
				continue
			}
			if name, ok := strings.CutSuffix(f.Name(), ".test.ts"); ok && allowed[name+".ts"] {
				continue
			}
			if !allowed[f.Name()] {
				p.addf("%s 下只允许 %s（以及它们对应的 .test 文件），多了 %s（%s 的文件名是约定死的）",
					relTo(root, sub), sortedKeys(allowed), f.Name(), label)
			}
		}
	}
}

// notBusinessModules 是 service/ 下**不是业务模块**的那几个。
//
// 「modules/<key>.yaml ↔ service/<key> ↔ features/<key> 三处一一对应」这条规则
// 管的是**租户的业务模块** —— 它们由生成器按 YAML 产出，表、页面、权限点成套。
//
// 平台管理端不是那种东西：它没有业务表（只碰四张平台级表）、没有 modules YAML
// （生成器的模板是给租户业务表用的，每张表带 tenant_id）、前端也不在
// features/ 下（它是另一套外壳，走 routes/platform）。
//
// **要往这里加第二个，先想清楚它为什么不是业务模块。**
var notBusinessModules = map[string]string{
	"platform": "平台管理端：没有业务表、没有 modules YAML、前端是另一套外壳",
	// 配置管理写的是 settings / platform_settings 这两张**基础设施表**，
	// 不是某个业务实体；页面是单表单页（features/ 只放 ListPage/NewPage/DetailPage
	// 那三种），所以它在 routes/ 下。生成器的模板对它没有意义。
	"settings": "配置管理：写的是基础设施表，页面是单表单页、不在 features/ 下",
	"registration": "自助注册：认证流程服务，没有业务表 / 没有 modules YAML，" +
		"页面是登录区的公开表单（routes/），不是 features/ 下的 CRUD 模块",
}

// checkModuleTriples：modules/<key>.yaml、service/<key>、features/<key> 必须一一对应。
func checkModuleTriples(root string, p *problems) {
	keys := map[string]bool{}

	entries, err := os.ReadDir(filepath.Join(root, "modules"))
	if err != nil {
		p.addf("读 modules 失败：%v", err)
		return
	}
	for _, e := range entries {
		name := e.Name()
		if e.IsDir() || !hasSuffixAny(name, ".yaml", ".yml") {
			continue
		}
		key := strings.TrimSuffix(strings.TrimSuffix(name, ".yaml"), ".yml")
		keys[key] = true

		raw, err := os.ReadFile(filepath.Join(root, "modules", name))
		if err != nil {
			p.addf("读 modules/%s 失败：%v", name, err)
			continue
		}
		m := rxModuleKey.FindSubmatch(raw)
		switch {
		case m == nil:
			p.addf("modules/%s 里没有 key 字段", name)
		case string(m[1]) != key:
			p.addf("modules/%s 的 key 是 %q，和文件名对不上", name, m[1])
		}
	}

	for key := range keys {
		for _, want := range []string{
			filepath.Join("backend", "internal", "service", key),
			filepath.Join("frontend", "src", "features", key),
		} {
			if st, err := os.Stat(filepath.Join(root, want)); err != nil || !st.IsDir() {
				p.addf("modules/%s.yaml 声明了模块，但缺少 %s —— 跑 make gen-module name=%s", key, filepath.ToSlash(want), key)
			}
		}
	}

	// 反向：有代码目录却没有 YAML，说明有人绕过生成器手写了模块。
	for _, pair := range []struct{ dir, label string }{
		{filepath.Join(root, "backend", "internal", "service"), "backend/internal/service"},
		{filepath.Join(root, "frontend", "src", "features"), "frontend/src/features"},
	} {
		subs, err := os.ReadDir(pair.dir)
		if err != nil {
			continue
		}
		for _, s := range subs {
			if s.IsDir() && !keys[s.Name()] && notBusinessModules[s.Name()] == "" {
				p.addf("%s/%s 没有对应的 modules/%s.yaml", pair.label, s.Name(), s.Name())
			}
		}
	}
}

// relTo 返回相对仓库根目录的路径。
func relTo(root, path string) string {
	if r, err := filepath.Rel(root, path); err == nil {
		return filepath.ToSlash(r)
	}
	return path
}

// sortedKeys 把白名单排序输出，报错信息里好读。
func sortedKeys(m map[string]bool) string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return strings.Join(out, " / ")
}

// rxMenuIcon 找 perm 模块声明里的 `Icon: "x"`。
var rxMenuIcon = regexp.MustCompile(`Icon:\s*"([a-z0-9-]+)"`)

// rxIconRegistered 找前端图标注册表里的键：`'key-round': KeyRound,` 或 `shield: Shield,`。
var rxIconRegistered = regexp.MustCompile(`(?m)^\s*'?([a-z0-9-]+)'?\s*:\s*[A-Z]\w*,`)

// checkMenuIcons：后端声明的菜单图标，前端必须注册过。
//
// 菜单图标名是**后端给的字符串**，前端按名字去一张显式注册表里取组件
// （不整包 import lucide，那会让首屏从 500 KB 涨到 1.4 MB，MEMORY.md 记过）。
//
// 于是就有了这个缝：新模块用了个没注册的图标名，**不报错、不白屏，
// 只是那一项菜单显示成一个圆点**。这类「看着像好的」最难被发现 ——
// 实际上写这个检查的时候，仓库里就已经有一个了（配置管理的 shield）。
func checkMenuIcons(root string, p *problems) error {
	registry, err := os.ReadFile(filepath.Join(root, "frontend", "src", "components", "MenuIcon.tsx"))
	if err != nil {
		return fmt.Errorf("读图标注册表: %w", err)
	}
	registered := map[string]bool{}
	for _, m := range rxIconRegistered.FindAllStringSubmatch(string(registry), -1) {
		registered[m[1]] = true
	}

	dir := filepath.Join(root, "backend", "internal", "perm", "modules")
	entries, err := os.ReadDir(dir)
	if err != nil {
		return fmt.Errorf("读权限模块目录: %w", err)
	}
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".go") {
			continue
		}
		raw, err := os.ReadFile(filepath.Join(dir, e.Name()))
		if err != nil {
			return fmt.Errorf("读 %s: %w", e.Name(), err)
		}
		for _, m := range rxMenuIcon.FindAllStringSubmatch(string(raw), -1) {
			if !registered[m[1]] {
				p.addf("perm/modules/%s 用了图标 %q，但 frontend/src/components/MenuIcon.tsx 里没注册 —— "+
					"菜单会显示成一个圆点，不报错也不白屏", e.Name(), m[1])
			}
		}
	}
	return nil
}
