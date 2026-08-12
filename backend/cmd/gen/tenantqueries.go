package main

import (
	"bytes"
	"fmt"
	"go/ast"
	"go/format"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/ramoncjs3/fries/internal/tenantsql"
)

// runTenantQueries 生成租户绑定的查询句柄（MULTI-TENANCY.md §1.2 ①）。
//
// 这是整套「机械强制」的核心。要解决的问题是：Go 里没有 Rails 的 default_scope，
// sqlc 生成的就是一堆普通函数，没有地方挂钩子；而「参数结构体里有 TenantID 字段」
// 拦不住任何人 —— Go 有零值，少填一个字段照样编译通过，跑出来是 uuid.Nil，
// 查到 0 行，**和 RLS 的静默失败一模一样**。
//
// 真正的办法是让业务代码**根本碰不到那个字段**：
//
//	q := store.ForTenant(id)
//	rows, err := q.ListUsers(ctx, repo.ListUsersArgs{Keyword: kw})   // 没有 TenantID 这个字段
//
// 配合 `internal/repo/internal/sqlcgen/` 这层 internal 包（Go 规定只有
// internal/repo/... 那棵树 import 得到），业务代码在**编译期**就拿不到不带租户的句柄。
// 不是「不该用」，是「用不了」——这是 Go 里能拿到的最强保证。
//
// 生成规则（输入是 sqlc 产出的 Querier 接口，用 go/ast 解析，不做文本匹配）：
//
//   - 参数是 XxxParams 结构体且含 `TenantID uuid.UUID` → 租户绑定。
//     去掉 TenantID 之后：剩 0 个字段 → 只收 ctx；剩 1 个 → 直接收那一个；
//     剩 2 个以上 → 生成一个 XxxArgs 结构体。
//   - 参数就是单个 `tenantID uuid.UUID` → 租户绑定，包装后只收 ctx。
//   - 其余 → **豁免**，进 UnscopedQueries。这批必须在 SQL 里写了 `-- tenant-exempt:`，
//     由 `gen lint-sql` 把关。
func runTenantQueries(root string, args []string) error {
	check, err := checkFlag("tenant-queries", args)
	if err != nil {
		return err
	}

	pkgDir := filepath.Join(root, "backend", "internal", "repo", "internal", "sqlcgen")
	methods, types, err := parseSqlcgen(pkgDir)
	if err != nil {
		return err
	}

	queries, err := loadQueryTables(filepath.Join(root, "backend", "db", "queries"))
	if err != nil {
		return err
	}
	if err := checkUnscopedAreExempt(methods, queries); err != nil {
		return err
	}
	for i := range methods {
		methods[i].platform = queries[methods[i].name].platformOnly
	}

	exemptNames := make([]string, 0, len(queries))
	for name, facts := range queries {
		if facts.exempt {
			exemptNames = append(exemptNames, name)
		}
	}
	sort.Strings(exemptNames)

	content, err := renderTenantQueries(methods, types, exemptNames)
	if err != nil {
		return err
	}
	out := filepath.Join(root, "backend", "internal", "repo", "tenant_queries.go")
	return writeOrCheck(out, content, check, "make gen-tenant-queries")
}

// checkUnscopedAreExempt 确认「落到 UnscopedQueries 上的查询」和
// 「SQL 里写了 `-- tenant-exempt:` 的查询」是同一批。
//
// ⚠️ 这个交叉核对是 review 时补的，补的是两套机制之间的一条缝：
//
//	gen lint-sql  看的是「SQL 里 tenant_id 有没有绑到某个参数上」
//	这个生成器    看的是「参数结构体里有没有叫 TenantID 的字段」
//
// 两者判断的**不是同一件事**。把 `sqlc.arg('tenant_id')` 写成 `sqlc.arg('tid')`，
// lint-sql 照样绿（条件确实绑了参数），而生成器认不出那是租户，
// 于是这条查询**悄悄落到不带租户的句柄上**，两边都不报错。
//
// 改动已有查询时编译器会兜住（方法从租户句柄上消失了，调用方编译不过）；
// 但**新写的查询没有调用方可挂**，一路绿到底。所以要在这里对一次账。
func checkUnscopedAreExempt(methods []method, queries map[string]queryFacts) error {
	var p problems
	for _, m := range methods {
		if m.tenantScoped || queries[m.name].exempt {
			continue
		}
		p.addf("查询 %s 没有租户参数，会落到 UnscopedQueries（绕过租户隔离）上，"+
			"但 SQL 里没写 `-- tenant-exempt: <理由>`。\n"+
			"      两种情况：①它本来就该带租户 —— 那多半是租户参数没叫 %s"+
			"（必须是这个名字，生成器认的就是它）；\n"+
			"      ②它确实不能带租户 —— 那就去 MULTI-TENANCY.md §3.2 ③ 的清单上说明理由，"+
			"再在 SQL 里写上豁免注释", m.name, "sqlc.arg('tenant_id')")
	}
	return p.err("有查询绕过了租户隔离却没说明理由")
}

// queryFacts 是从 SQL 里读出来的、生成器要用的两件事。
type queryFacts struct {
	// exempt 表示这条查询写了 `-- tenant-exempt:`
	exempt bool
	// platformOnly 表示它**只**碰平台级表 —— 那它就该落到 Store.Platform() 上
	platformOnly bool
}

// loadQueryTables 扫一遍 db/queries，记下每条查询的豁免标记和「碰了哪些表」。
func loadQueryTables(dir string) (map[string]queryFacts, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, fmt.Errorf("读 %s: %w", dir, err)
	}
	out := map[string]queryFacts{}
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".sql") {
			continue
		}
		raw, err := os.ReadFile(filepath.Join(dir, e.Name()))
		if err != nil {
			return nil, fmt.Errorf("读 %s: %w", e.Name(), err)
		}
		for _, q := range tenantsql.SplitQueries(string(raw), e.Name()) {
			out[q.Name] = queryFacts{
				exempt:       q.ExemptReason != "",
				platformOnly: tenantsql.TouchesOnlyPlatformTables(tenantsql.StripComments(q.Body)),
			}
		}
	}
	return out, nil
}

// ---------------------------------------------------------------- 解析

// field 是参数结构体里的一个字段。
type field struct {
	name string
	typ  string
	doc  string
}

// method 是 Querier 接口里的一个方法。
type method struct {
	name string
	doc  string
	// paramStruct 是 XxxParams 的类型名；单参数形式为空
	paramStruct string
	// params 是去掉 TenantID 之后剩下的字段（paramStruct 非空时有效）
	params []field
	// bareParams 是非结构体形式的参数（含 ctx 之外的全部）
	bareParams []field
	// results 是返回值类型（不含 error）
	results []string
	// tenantScoped 表示这个方法能被租户绑定
	tenantScoped bool
	// platform 表示它只碰平台级表，该落到 Store.Platform() 上
	platform bool
}

// parseSqlcgen 解析 sqlcgen 包，取出 Querier 的方法表和所有具名类型。
func parseSqlcgen(dir string) ([]method, []string, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, nil, fmt.Errorf("读 %s: %w（sqlc 没生成？跑一下 make gen-sqlc）", dir, err)
	}
	fset := token.NewFileSet()
	var files []*ast.File
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".go") {
			continue
		}
		f, err := parser.ParseFile(fset, filepath.Join(dir, e.Name()), nil, parser.ParseComments)
		if err != nil {
			return nil, nil, fmt.Errorf("解析 %s: %w", e.Name(), err)
		}
		files = append(files, f)
	}
	if len(files) == 0 {
		return nil, nil, fmt.Errorf("%s 里没有 Go 文件（sqlc 没生成？跑一下 make gen-sqlc）", dir)
	}

	// 先收集所有结构体定义，后面查 XxxParams 的字段要用
	structs := map[string][]field{}
	var typeNames []string
	for _, f := range files {
		for _, decl := range f.Decls {
			gd, ok := decl.(*ast.GenDecl)
			if !ok || gd.Tok != token.TYPE {
				continue
			}
			for _, spec := range gd.Specs {
				ts, ok := spec.(*ast.TypeSpec)
				if !ok || !ts.Name.IsExported() {
					continue
				}
				typeNames = append(typeNames, ts.Name.Name)
				st, ok := ts.Type.(*ast.StructType)
				if !ok {
					continue
				}
				var fields []field
				for _, fl := range st.Fields.List {
					typ := exprString(fl.Type)
					doc := commentText(fl.Doc)
					for _, n := range fl.Names {
						fields = append(fields, field{name: n.Name, typ: typ, doc: doc})
					}
				}
				structs[ts.Name.Name] = fields
			}
		}
	}
	sort.Strings(typeNames)

	// 再找 Querier 接口
	var iface *ast.InterfaceType
	for _, f := range files {
		for _, decl := range f.Decls {
			gd, ok := decl.(*ast.GenDecl)
			if !ok || gd.Tok != token.TYPE {
				continue
			}
			for _, spec := range gd.Specs {
				ts, ok := spec.(*ast.TypeSpec)
				if !ok || ts.Name.Name != "Querier" {
					continue
				}
				if it, ok := ts.Type.(*ast.InterfaceType); ok {
					iface = it
				}
			}
		}
	}
	if iface == nil {
		return nil, nil, fmt.Errorf("sqlcgen 里没有 Querier 接口（sqlc.yaml 的 emit_interface 被关了？）")
	}

	var methods []method
	for _, fl := range iface.Methods.List {
		ft, ok := fl.Type.(*ast.FuncType)
		if !ok || len(fl.Names) != 1 {
			continue
		}
		m := method{name: fl.Names[0].Name, doc: commentText(fl.Doc)}

		// 返回值：去掉最后的 error
		if ft.Results != nil {
			for _, r := range ft.Results.List {
				m.results = append(m.results, exprString(r.Type))
			}
			if n := len(m.results); n > 0 && m.results[n-1] == "error" {
				m.results = m.results[:n-1]
			}
		}

		// 参数：第一个一定是 ctx，跳过
		var ps []field
		for _, p := range ft.Params.List {
			typ := exprString(p.Type)
			if len(p.Names) == 0 {
				ps = append(ps, field{typ: typ})
				continue
			}
			for _, n := range p.Names {
				ps = append(ps, field{name: n.Name, typ: typ})
			}
		}
		if len(ps) > 0 && ps[0].typ == "context.Context" {
			ps = ps[1:]
		}

		switch {
		case len(ps) == 1 && strings.HasSuffix(ps[0].typ, "Params"):
			name := strings.TrimPrefix(ps[0].typ, "sqlcgen.")
			fields := structs[name]
			rest := make([]field, 0, len(fields))
			for _, f := range fields {
				if f.name == tenantField && f.typ == tenantType {
					m.tenantScoped = true
					continue
				}
				rest = append(rest, f)
			}
			m.paramStruct = name
			m.params = rest
			if !m.tenantScoped {
				m.bareParams = ps
			}
		case len(ps) == 1 && ps[0].name == tenantParamName && ps[0].typ == tenantType:
			m.tenantScoped = true
		default:
			m.bareParams = ps
		}

		methods = append(methods, m)
	}
	sort.Slice(methods, func(i, j int) bool { return methods[i].name < methods[j].name })
	return methods, typeNames, nil
}

const (
	tenantField     = "TenantID"
	tenantType      = "uuid.UUID"
	tenantParamName = "tenantID"
)

func exprString(e ast.Expr) string {
	var buf bytes.Buffer
	writeExpr(&buf, e)
	return buf.String()
}

func writeExpr(buf *bytes.Buffer, e ast.Expr) {
	switch t := e.(type) {
	case *ast.Ident:
		buf.WriteString(t.Name)
	case *ast.SelectorExpr:
		writeExpr(buf, t.X)
		buf.WriteByte('.')
		buf.WriteString(t.Sel.Name)
	case *ast.StarExpr:
		buf.WriteByte('*')
		writeExpr(buf, t.X)
	case *ast.ArrayType:
		buf.WriteString("[]")
		writeExpr(buf, t.Elt)
	case *ast.MapType:
		buf.WriteString("map[")
		writeExpr(buf, t.Key)
		buf.WriteByte(']')
		writeExpr(buf, t.Value)
	case *ast.InterfaceType:
		buf.WriteString("interface{}")
	default:
		buf.WriteString("any")
	}
}

func commentText(g *ast.CommentGroup) string {
	if g == nil {
		return ""
	}
	return strings.TrimRight(g.Text(), "\n")
}

// ---------------------------------------------------------------- 生成

// qualify 把 sqlcgen 里的类型名写成 repo 包里能用的形式。
//
// 生成物和类型别名（type User = sqlcgen.User）在同一个包里，所以裸类型名直接可用；
// uuid.UUID / time.Time 这种带包名的也原样保留。也就是说这里其实什么都不用改 ——
// 留着这个函数是为了把「类型怎么翻译」这件事集中在一处，将来 sqlc 换了输出形式好改。
func qualify(typ string) string { return typ }

func renderTenantQueries(methods []method, typeNames, exemptNames []string) ([]byte, error) {
	var b strings.Builder

	// 类型别名：让 service 层继续写 repo.User，不用感知 sqlcgen 这层
	// 三个句柄各拿一批方法
	var scoped, unscoped, platform []method
	for _, m := range methods {
		switch {
		case m.tenantScoped:
			scoped = append(scoped, m)
		case m.platform:
			platform = append(platform, m)
		default:
			unscoped = append(unscoped, m)
		}
	}

	b.WriteString("// ---------------------------------------------------------------- 参数结构体\n\n")
	b.WriteString("// 每个 XxxArgs 都是对应 XxxParams **去掉 TenantID** 之后的样子。\n")
	b.WriteString("// 业务代码看到的参数里压根没有租户这一项，填不了也漏不了。\n\n")
	for _, m := range scoped {
		if len(m.params) < 2 {
			continue
		}
		fmt.Fprintf(&b, "type %sArgs struct {\n", m.name)
		for _, f := range m.params {
			if f.doc != "" {
				for _, line := range strings.Split(f.doc, "\n") {
					fmt.Fprintf(&b, "\t// %s\n", line)
				}
			}
			fmt.Fprintf(&b, "\t%s %s\n", f.name, qualify(f.typ))
		}
		b.WriteString("}\n\n")
	}

	// TenantQueries
	b.WriteString(`// ---------------------------------------------------------------- 租户绑定的句柄

// TenantQueries 是业务代码唯一能拿到的查询句柄，从 Store.ForTenant / Store.ForContext 取。
//
// 它上面的每个方法都会把租户填进底层 params，调用方看不到也传不了那个字段。
type TenantQueries struct {
	q        *sqlcgen.Queries
	db       TxBeginner
	tenantID uuid.UUID
}

// TenantID 返回这个句柄绑定的租户。
// 只给需要把租户写进别的地方（审计、日志）的场景用，**不要**拿它去手拼查询条件。
func (q *TenantQueries) TenantID() uuid.UUID { return q.tenantID }

`)

	for _, m := range scoped {
		writeMethod(&b, m)
	}

	// UnscopedQueries
	b.WriteString(`// ---------------------------------------------------------------- 不带租户的句柄

// UnscopedQueries 是**不带租户**的查询句柄，从 Store.Unscoped 取。
//
// ⚠️ 这上面的每一条都是绕过租户隔离的。它们不是被遗漏的，是三类确实做不到的：
//
//  1. **认证链路** —— 认证发生在租户上下文建立之前：拿 cookie 里的 token 查这是谁、
//     拿 API Key 的 prefix 查这是哪个机器身份、拿公司代码查这是哪家公司。
//     那一刻还不知道租户是谁，查出来的那一行才告诉我们（§3.2 ③）。
//  2. **写审计** —— 未认证请求也要写审计，那时 tenant_id 就是 NULL（§7.1）。
//  3. **跨租户的后台任务** —— 清理过期会话、建审计分区，本来就不属于任何租户。
//
// 每一条在 SQL 里都写了 ` + "`-- tenant-exempt: <理由>`" + `，` + "`gen lint-sql`" + ` 会核对。
// **要往这里加东西，先去 MULTI-TENANCY.md §3.2 ③ 那份清单上加，并说明理由。**
type UnscopedQueries struct {
	q  *sqlcgen.Queries
	db TxBeginner
}

`)

	for _, m := range unscoped {
		writeHandleMethod(&b, m, "UnscopedQueries")
	}

	b.WriteString(`// ---------------------------------------------------------------- 平台级句柄

// PlatformQueries 是**平台管理端**的查询句柄，从 Store.Platform() 取。
//
// 它上面只有「引用的表全是平台级表」的查询 —— tenants / platform_admins /
// platform_sessions / platform_settings 这四张。分流是生成器按 SQL 里的表名做的，
// 不是靠人分类：哪天有人在 platform.sql 里写一条去查业务表（users、departments……）
// 的查询，那条查询会掉到别的句柄上，平台服务当场编译不过。
//
// 为什么这么在意（MULTI-TENANCY.md §6、§10.11）：平台管理员开租户、停租户，
// 但**结构上碰不到客户的业务数据**。这个性质是将来跟客户解释隔离时最有力的一句话，
// 而它只有在「代码里根本没有那条路」时才成立 —— 写在文档里的版本不算数。
//
// 「租户列表要显示人数」就是靠 tenants.user_count 冗余绕开的，不是去关联业务表。
type PlatformQueries struct {
	q  *sqlcgen.Queries
	db TxBeginner
}

`)

	for _, m := range platform {
		writeHandleMethod(&b, m, "PlatformQueries")
	}

	writeExemptQueries(&b, exemptNames)

	body := b.String()

	// 类型别名：让 service 层继续写 repo.User，不用感知 sqlcgen 这层。
	//
	// XxxParams **只在生成物自己的签名里出现时**才透出去（也就是 UnscopedQueries
	// 那几条）。租户绑定的那些一律只透 XxxArgs —— 业务代码要是能看见 XxxParams，
	// 「参数里没有 TenantID」这条保证就没那么一目了然了。
	var alias strings.Builder
	alias.WriteString("// 类型别名。sqlcgen 是 internal 包，业务代码 import 不到，\n")
	alias.WriteString("// 这里把它的类型原样透出来 —— service 层继续写 repo.User，不用改。\n")
	alias.WriteString("type (\n")
	for _, n := range typeNames {
		switch {
		case n == "Querier" || n == "Queries" || n == "DBTX":
			continue
		case strings.HasSuffix(n, "Params") && !strings.Contains(body, " "+n+")"):
			continue
		}
		fmt.Fprintf(&alias, "\t%s = sqlcgen.%s\n", n, n)
	}
	alias.WriteString(")\n\n")
	body = alias.String() + body

	// import 按生成物里真正用到的来，少一个多一个都编译不过
	var head strings.Builder
	head.WriteString("// Code generated by cmd/gen tenant-queries. DO NOT EDIT.\n")
	head.WriteString("//\n")
	head.WriteString("// 改这个文件是没用的 —— 它由 db/queries/*.sql 经 sqlc 再经 `make gen-tenant-queries` 生成。\n")
	head.WriteString("// 要加查询就去改 SQL。\n\n")
	head.WriteString("package repo\n\nimport (\n\t\"context\"\n")
	// 只写生成物里真用到的 import，多一个少一个都编译不过。
	// 新的列类型（sqlc 的 overrides 里加了别的包）要在这张表上补一行。
	for _, imp := range []struct{ path, prefix string }{
		{"net/netip", "netip."},
		{"time", "time."},
	} {
		if strings.Contains(body, imp.prefix) {
			fmt.Fprintf(&head, "\t%q\n", imp.path)
		}
	}
	head.WriteString("\n\t\"github.com/google/uuid\"\n")
	// 金额列（sqlc.yaml 把 numeric override 成 shopspring/decimal）用到时才引，和 uuid 同组。
	if strings.Contains(body, "decimal.") {
		head.WriteString("\t\"github.com/shopspring/decimal\"\n")
	}
	head.WriteString("\n\t\"github.com/ramoncjs3/fries/internal/repo/internal/sqlcgen\"\n)\n\n")

	src, err := format.Source([]byte(head.String() + body))
	if err != nil {
		return nil, fmt.Errorf("生成的代码格式化失败（生成器有 bug）: %w", err)
	}
	return src, nil
}

// writeMethod 写一个租户绑定的方法。
func writeMethod(b *strings.Builder, m method) {
	writeDoc(b, m.doc)

	rets := make([]string, 0, len(m.results)+1)
	for _, r := range m.results {
		rets = append(rets, qualify(r))
	}
	rets = append(rets, "error")
	retSig := "error"
	if len(rets) > 1 {
		retSig = "(" + strings.Join(rets, ", ") + ")"
	}

	// 有返回行的方法包一层运行期兜底：把查回来的每一行的 tenant_id 核一遍（§12.2）。
	//
	// `assertTenant` 的签名是 (T, error) -> (T, error)，而 Go 允许把多返回值
	// 整个传进参数匹配的函数 —— 所以这里只要在原来那行外面套个括号，不用拆开写。
	//
	// 只返回 error 的（写操作）没有行可核，原样返回；写的隔离靠「影响行数必须是 0」，
	// 另有测试守（§10.8）。
	lead, tail := "return ", "\n\n"
	if len(m.results) == 1 {
		lead = "row, err := "
		tail = "\treturn assertTenant(q.tenantID, row, err)\n}\n\n"
	}

	switch {
	case m.paramStruct == "":
		// 单参数就是 tenantID
		fmt.Fprintf(b, "func (q *TenantQueries) %s(ctx context.Context) %s {\n", m.name, retSig)
		fmt.Fprintf(b, "\t%sq.q.%s(ctx, q.tenantID)\n", lead, m.name)
		writeMethodTail(b, tail)

	case len(m.params) == 0:
		fmt.Fprintf(b, "func (q *TenantQueries) %s(ctx context.Context) %s {\n", m.name, retSig)
		fmt.Fprintf(b, "\t%sq.q.%s(ctx, sqlcgen.%s{TenantID: q.tenantID})\n",
			lead, m.name, m.paramStruct)
		writeMethodTail(b, tail)

	case len(m.params) == 1:
		f := m.params[0]
		arg := lowerFirst(f.name)
		fmt.Fprintf(b, "func (q *TenantQueries) %s(ctx context.Context, %s %s) %s {\n",
			m.name, arg, qualify(f.typ), retSig)
		fmt.Fprintf(b, "\t%sq.q.%s(ctx, sqlcgen.%s{TenantID: q.tenantID, %s: %s})\n",
			lead, m.name, m.paramStruct, f.name, arg)
		writeMethodTail(b, tail)

	default:
		fmt.Fprintf(b, "func (q *TenantQueries) %s(ctx context.Context, arg %sArgs) %s {\n",
			m.name, m.name, retSig)
		fmt.Fprintf(b, "\t%sq.q.%s(ctx, sqlcgen.%s{\n\t\tTenantID: q.tenantID,\n",
			lead, m.name, m.paramStruct)
		for _, f := range m.params {
			fmt.Fprintf(b, "\t\t%s: arg.%s,\n", f.name, f.name)
		}
		b.WriteString("\t})\n")
		writeMethodTail(b, tail)
	}
}

// writeMethodTail 收尾一个方法：要么直接闭合，要么补上那句运行期兜底。
func writeMethodTail(b *strings.Builder, tail string) {
	if tail == "\n\n" {
		b.WriteString("}\n\n")
		return
	}
	b.WriteString(tail)
}

// writeHandleMethod 把一个方法原样转发到指定的句柄类型上（不注入租户）。
func writeHandleMethod(b *strings.Builder, m method, handle string) {
	writeDoc(b, m.doc)

	rets := make([]string, 0, len(m.results)+1)
	for _, r := range m.results {
		rets = append(rets, qualify(r))
	}
	rets = append(rets, "error")
	retSig := "error"
	if len(rets) > 1 {
		retSig = "(" + strings.Join(rets, ", ") + ")"
	}

	sig := make([]string, 0, len(m.bareParams))
	call := make([]string, 0, len(m.bareParams))
	for i, p := range m.bareParams {
		name := p.name
		if name == "" {
			name = fmt.Sprintf("a%d", i)
		}
		sig = append(sig, name+" "+qualify(p.typ))
		call = append(call, name)
	}
	sigStr := ""
	if len(sig) > 0 {
		sigStr = ", " + strings.Join(sig, ", ")
	}
	callStr := ""
	if len(call) > 0 {
		callStr = ", " + strings.Join(call, ", ")
	}

	fmt.Fprintf(b, "func (q *%s) %s(ctx context.Context%s) %s {\n", handle, m.name, sigStr, retSig)
	fmt.Fprintf(b, "\treturn q.q.%s(ctx%s)\n}\n\n", m.name, callStr)
}

func writeDoc(b *strings.Builder, doc string) {
	if doc == "" {
		return
	}
	for _, line := range strings.Split(doc, "\n") {
		if line == "" {
			b.WriteString("//\n")
			continue
		}
		fmt.Fprintf(b, "// %s\n", line)
	}
}

func lowerFirst(s string) string {
	if s == "" {
		return s
	}
	// ID → id、IP → ip 这类全大写缩写整体转小写，别弄出 iD
	if strings.ToUpper(s) == s {
		return strings.ToLower(s)
	}
	r := []rune(s)
	r[0] = []rune(strings.ToLower(string(r[0])))[0]
	name := string(r)
	if isGoKeyword(name) {
		return name + "_"
	}
	return name
}

func isGoKeyword(s string) bool {
	switch s {
	case "break", "case", "chan", "const", "continue", "default", "defer", "else",
		"fallthrough", "for", "func", "go", "goto", "if", "import", "interface",
		"map", "package", "range", "return", "select", "struct", "switch", "type", "var":
		return true
	}
	return false
}

// writeExemptQueries 把「写了 `-- tenant-exempt:` 的查询名」生成成一张表。
//
// 给**运行期兜底**用（MULTI-TENANCY.md §12.2）：pgx 的 tracer 只拿得到 SQL 文本，
// 靠 sqlc 埋在里面的 `-- name: X :kind` 认出这是哪条查询，再来这张表看有没有豁免。
//
// 🔴 **必须是生成的，不能手写。** 手写就等于同一份白名单存在两处 ——
// 一处在 SQL 注释里、一处在 Go 里，改了一边忘了另一边，结果是
// 「构建期放行、运行期炸」或者更糟的「构建期拦、运行期放行」。
// 这个项目在「两个检查看的不是同一件事」上已经栽过一次（MEMORY.md 记过）。
func writeExemptQueries(b *strings.Builder, names []string) {
	b.WriteString(`// ---------------------------------------------------------------- 运行期兜底的豁免清单

// exemptQueries 是 SQL 里写了 ` + "`-- tenant-exempt: <理由>`" + ` 的查询名。
//
// 运行期的 tracer 拿 SQL 里的查询名来这里对（trace.go）。理由不在这里 ——
// 理由写在 db/queries/*.sql 那一行上，` + "`make lint-sql`" + ` 每次都会打出来。
var exemptQueries = map[string]bool{
`)
	for _, n := range names {
		fmt.Fprintf(b, "\t%q: true,\n", n)
	}
	b.WriteString("}\n\n")
}
