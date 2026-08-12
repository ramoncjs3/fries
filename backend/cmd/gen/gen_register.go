package main

import (
	"fmt"
	"go/format"
	"os"
	"path/filepath"
	"strings"
)

// gen_register.go 把模块**登记**进现有文件（app.go 装配 / tenantsql 租户表 / 前端路由）。
//
// 这是改现有文件、最容易弄坏别的地方，所以：
//   - **幂等**：已经登记过就跳过（按模块的标识符 grep），重跑不会插重复。
//   - **锚点插入**：插在文件里稳定存在的锚点旁（import 块尾、a.registerOps()、tenantTables 的 }、
//     前端的 HomePage 路由），不乱动别的。
//   - 插完由调用方跑 make check 兜底：万一锚点没对上、插歪了，编译/lint 当场红。
//
// 登记不了（找不到锚点 / 文件被改过形状）就报出来，让人手动照 registrationSteps 加，别硬插。

// registerModule 把模块登记进三处，返回每处的结果。
func registerModule(root string, def *ModuleDef) ([]writeResult, error) {
	entity := pascal(def.Key)
	table := pluralize(def.Key)
	alias := def.Key + "svc"
	var out []writeResult
	// do 把一次 editFile 的结果收进 out，出错就短路返回（避免 else-after-return）。
	do := func(res writeResult, err error) error {
		if err != nil {
			return err
		}
		out = append(out, res)
		return nil
	}

	// 1) app.go：import 服务包 + 装配 handler。
	appPath := filepath.Join(root, "backend", "cmd", "server", "app.go")
	if err := do(editFile(appPath, func(s string) (string, string, error) {
		importPath := fmt.Sprintf("%q", "github.com/ramoncjs3/fries/internal/service/"+def.Key)
		importLine := fmt.Sprintf("\t%s %s\n", alias, importPath)
		registerLine := fmt.Sprintf("\thandler.Register%s(api, handler.New%s(%s.New(a.store)))\n", entity, entity, alias)
		// 幂等检查用**对齐无关**的稳定子串：gofmt 会把 import 的别名列对齐（加多空格），
		// 拿整行去 Contains 会匹配不上、于是重复插入。import 认路径、装配认整句（不被对齐）。
		hasImport := strings.Contains(s, importPath)
		if hasImport && strings.Contains(s, registerLine) {
			return s, statusSkippedRegistered, nil
		}
		if !hasImport {
			out, ok := insertBefore(s, "\n)\n", importLine)
			if !ok {
				return s, "", fmt.Errorf("app.go 找不到 import 块收尾，手动加：%s", strings.TrimSpace(importLine))
			}
			s = out
		}
		if !strings.Contains(s, registerLine) {
			out, ok := insertBeforeExact(s, "\ta.registerOps()\n", registerLine)
			if !ok {
				return s, "", fmt.Errorf("app.go 找不到 a.registerOps() 锚点，手动加装配行")
			}
			s = out
		}
		return s, statusWritten, nil
	})); err != nil {
		return out, err
	}

	// 2) tenantsql.go：加进 tenantTables（漏了 lint-sql 会红）。
	tsPath := filepath.Join(root, "backend", "internal", "tenantsql", "tenantsql.go")
	if err := do(editFile(tsPath, func(s string) (string, string, error) {
		// 幂等检查用 key（"products":），不带 value —— gofmt 会把 true 那列对齐加空格。
		if strings.Contains(s, fmt.Sprintf("%q:", table)) {
			return s, statusSkippedRegistered, nil
		}
		next, ok := insertIntoMap(s, "var tenantTables = map[string]bool{", fmt.Sprintf("\t%q: true,\n", table))
		if !ok {
			return s, "", fmt.Errorf("tenantsql.go 找不到 tenantTables 映射，手动加 %q: true", table)
		}
		return next, statusWritten, nil
	})); err != nil {
		return out, err
	}

	// 3) 前端路由：lazy import + 三条路由。
	routesPath := filepath.Join(root, "frontend", "src", "routes", "index.tsx")
	if err := do(editFile(routesPath, func(s string) (string, string, error) {
		if strings.Contains(s, fmt.Sprintf("%sListPage = lazy", entity)) {
			return s, statusSkippedRegistered, nil
		}
		lazyBlock := fmt.Sprintf(
			"const %sListPage = lazy(() => import(\"@/features/%s/ListPage\"));\n"+
				"const %sNewPage = lazy(() => import(\"@/features/%s/NewPage\"));\n"+
				"const %sDetailPage = lazy(() => import(\"@/features/%s/DetailPage\"));\n",
			entity, def.Key, entity, def.Key, entity, def.Key)
		s, ok := insertBeforeExact(s, "\nfunction lazyPage(", "\n"+lazyBlock)
		if !ok {
			return s, "", fmt.Errorf("routes/index.tsx 找不到 lazyPage 锚点，手动加路由")
		}
		s, ok = insertAfter(s, "{ index: true, element: lazyPage(<HomePage />) },\n", routeBlock(def, entity, table))
		if !ok {
			return s, "", fmt.Errorf("routes/index.tsx 找不到 HomePage 路由锚点，手动加路由")
		}
		return s, statusWritten, nil
	})); err != nil {
		return out, err
	}

	return out, nil
}

// routeBlock 产出三条前端路由（缩进对齐 AppShell children 里的既有路由：14 空格）。
func routeBlock(def *ModuleDef, entity, table string) string {
	acts := actionSet(def)
	detailAction := "list"
	if acts[actRead] {
		detailAction = "read"
	}
	ind := strings.Repeat(" ", 14)
	one := func(path, action, page string) string {
		return fmt.Sprintf(ind+"{\n"+ind+"  path: %q,\n"+ind+"  element: lazyPage(\n"+
			ind+"    <RequirePerm resource=%q action=%q>\n"+ind+"      <%s />\n"+
			ind+"    </RequirePerm>,\n"+ind+"  ),\n"+ind+"},\n",
			path, def.Key, action, page)
	}
	return one("/"+table, "list", entity+"ListPage") +
		one("/"+table+"/new", "create", entity+"NewPage") +
		one("/"+table+"/:id", detailAction, entity+"DetailPage")
}

// editFile 读文件、跑 edit、写回。edit 返回 (新内容, 状态, err)。
// **.go 文件写回前过一遍 gofmt** —— 插入的 import 会打乱 import 块对齐，不重排就过不了 gofmt 检查。
func editFile(path string, edit func(string) (string, string, error)) (writeResult, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return writeResult{}, fmt.Errorf("读 %s: %w", path, err)
	}
	next, status, err := edit(string(raw))
	if err != nil {
		return writeResult{}, err
	}
	if status != statusSkippedRegistered {
		if strings.HasSuffix(path, ".go") {
			formatted, ferr := format.Source([]byte(next))
			if ferr != nil {
				return writeResult{}, fmt.Errorf("登记后 %s 无法 gofmt（插入位置可能不对）：%w", path, ferr)
			}
			next = string(formatted)
		}
		if err := os.WriteFile(path, []byte(next), 0o644); err != nil {
			return writeResult{}, fmt.Errorf("写 %s: %w", path, err)
		}
	}
	return writeResult{path: path, status: status}, nil
}

// insertBefore 在 anchor 首次出现处（形如 "\n)\n"）的换行之后、`)` 之前插入 insertion。
func insertBefore(s, anchor, insertion string) (string, bool) {
	i := strings.Index(s, anchor)
	if i < 0 {
		return s, false
	}
	at := i + 1 // 跳过前导 \n
	return s[:at] + insertion + s[at:], true
}

// insertBeforeExact 在 anchor 首次出现处之前插入 insertion。
func insertBeforeExact(s, anchor, insertion string) (string, bool) {
	i := strings.Index(s, anchor)
	if i < 0 {
		return s, false
	}
	return s[:i] + insertion + s[i:], true
}

// insertAfter 在 anchor 首次出现处之后插入 insertion。
func insertAfter(s, anchor, insertion string) (string, bool) {
	i := strings.Index(s, anchor)
	if i < 0 {
		return s, false
	}
	at := i + len(anchor)
	return s[:at] + insertion + s[at:], true
}

// insertIntoMap 在 `mapDecl` 之后、该 map 收尾 `}` 之前插入一行。
func insertIntoMap(s, mapDecl, line string) (string, bool) {
	start := strings.Index(s, mapDecl)
	if start < 0 {
		return s, false
	}
	close := strings.Index(s[start:], "\n}")
	if close < 0 {
		return s, false
	}
	at := start + close + 1 // 指到 `}` 前那个换行之后
	return s[:at] + line + s[at:], true
}
