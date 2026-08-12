package main

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"github.com/ramoncjs3/fries/internal/tenantsql"
)

// runLintSQL 是租户隔离的静态检查（MULTI-TENANCY.md §1.2 ③），查两件事：
//
//  1. 每条查询有没有带租户条件
//  2. 谁在用不带租户的句柄（repo.Store.Unscoped）
//
// 第 2 条是 review 时补的：`Unscoped()` 上那十几个方法都是审过的豁免，
// 但**没有任何东西拦着 service 层去调它** —— 比如 `ListActiveTenants()`
// 就是整份客户名单（§8.1）。包可见性挡得住裸 Queries，挡不住这一层。
//
// 这是**四层机械强制里的第三层**，和别的几层管的是不同的事：
//
//	① ForTenant 包装      —— 保证「调用方填了租户」（业务代码根本碰不到那个字段）
//	② MustTenant          —— 保证「这次请求有租户」（没有就拒绝，不是放行）
//	③ 这里                —— 保证「SQL 里真的用了租户」  ← 前两层都看不见 SQL
//	④ 跨租户测试          —— 保证「跑起来真的隔开了」
//
// ①保证调用方填了、③保证 SQL 里真的用了，是两件不同的事，都要查。
// 纯静态、不连库、秒级，进 make dev-check。
//
// 不用现成的库：搜过一圈，「每条查询必须带 tenant_id」这条规则没有现成实现
// （§13）。好在它很简单 —— 解析 db/queries/*.sql，看表名和条件，纯文本工作。
func runLintSQL(root string, _ []string) error {
	dir := filepath.Join(root, "backend", "db", "queries")
	entries, err := os.ReadDir(dir)
	if err != nil {
		return fmt.Errorf("读 %s: %w", dir, err)
	}

	var p problems
	exemptions := map[string]string{}

	files := make([]string, 0, len(entries))
	for _, e := range entries {
		if !e.IsDir() && strings.HasSuffix(e.Name(), ".sql") {
			files = append(files, e.Name())
		}
	}
	sort.Strings(files)

	for _, name := range files {
		raw, err := os.ReadFile(filepath.Join(dir, name))
		if err != nil {
			return fmt.Errorf("读 %s: %w", name, err)
		}
		for _, q := range tenantsql.SplitQueries(string(raw), name) {
			if q.ExemptReason != "" {
				exemptions[q.Name] = q.ExemptReason
				continue
			}
			for _, why := range tenantsql.Analyze(q.Body) {
				p.addf("%s:%d %s %s", q.File, q.Line, q.Name, why)
			}
		}
	}

	if err := checkGoRawSQL(root, &p); err != nil {
		return err
	}
	if err := checkUnscopedCallers(root, &p); err != nil {
		return err
	}
	if err := checkUniqueViolationNames(root, &p); err != nil {
		return err
	}
	if err := checkTenantTableSchema(root, &p); err != nil {
		return err
	}
	if err := p.err("租户隔离检查没过（MULTI-TENANCY.md §1.2）"); err != nil {
		return err
	}

	// 豁免清单每次都打出来 —— 一份没人看的白名单迟早会悄悄变长。
	fmt.Printf("✓ SQL 都带了租户条件（%d 条豁免）\n", len(exemptions))
	names := make([]string, 0, len(exemptions))
	for n := range exemptions {
		names = append(names, n)
	}
	sort.Strings(names)
	for _, n := range names {
		fmt.Printf("    豁免 %-32s %s\n", n, exemptions[n])
	}
	return nil
}

// rxGoSQLVerb 在 Go 的字符串字面量里找「碰了某张租户表」的 SQL 片段。
//
// 只认 FROM / JOIN / UPDATE / INSERT INTO / DELETE FROM 后面直接跟表名的形式 ——
// 拼出来的 SQL（`"SELECT * FROM " + table`）本来就该另外 review，静态检查抓不了。
var rxGoSQLVerb = regexp.MustCompile(`(?is)\b(?:from|join|into|update)\s+([a-z_][a-z0-9_]*)`)

// rxGoExempt 是 Go 侧的豁免注释：`// tenant-exempt: 理由`。
//
// ⚠️ **必须紧挨着那条 SQL 写**（往上找 goExemptLookback 行）。
// 按整个文件找的话，一处豁免会把这个文件里所有裸 SQL 一起放行 ——
// 豁免的粒度必须小到「一眼能看出它在赦免哪一句」。
var rxGoExempt = regexp.MustCompile(`^\s*//\s*tenant-exempt:\s*\S`)

// goExemptLookback 是往上找豁免注释的行数。
const goExemptLookback = 5

// checkGoRawSQL 查 Go 代码里手写的 SQL 有没有带租户条件。
//
// 判得比 SQL 文件那套松：只要求「碰了租户表的字面量里出现 tenant_id」。
// 松是有意的 —— Go 里的字符串没有查询边界可以切段，苛刻的规则只会逼人乱加豁免。
// 真正的强制在 sqlc 那条路上，这里是**兜住有人绕开 sqlc**。
func checkGoRawSQL(root string, p *problems) error {
	base := filepath.Join(root, "backend")
	return filepath.WalkDir(base, func(path string, d fs.DirEntry, err error) error {
		switch {
		case err != nil:
			return err
		case d.IsDir():
			// sqlcgen 是 sqlc 的产物，它的 SQL 已经在 db/queries 里查过了
			if skipWalkDir(d.Name()) || d.Name() == dirSqlcgen {
				return filepath.SkipDir
			}
			return nil
		case !strings.HasSuffix(path, ".go"):
			return nil
		}

		raw, readErr := os.ReadFile(path)
		if readErr != nil {
			return readErr
		}
		src := string(raw)
		lines := strings.Split(src, "\n")

		for _, lit := range goStringLiterals(src) {
			var touched []string
			for _, m := range rxGoSQLVerb.FindAllStringSubmatch(lit.text, -1) {
				name := strings.ToLower(m[1])
				if tenantsql.IsGuarded(name) {
					touched = append(touched, name)
				}
			}
			if len(touched) == 0 || strings.Contains(strings.ToLower(lit.text), tenantIDCol) {
				continue
			}
			// 两种豁免写法都认：
			//
			//	写在 SQL 里面  ← **推荐**，运行期的 tracer 也看得见（同一个标记两边认）
			//	写成 Go 注释   ← 只有构建期看得见，先前的写法，留着不动
			if tenantsql.HasExemptComment(lit.text) || exemptedNearby(lines, lit.line) {
				continue
			}
			p.addf("%s:%d 手写的 SQL 碰了 %s 但没带 tenant_id —— "+
				"业务数据一律走 db/queries + ForTenant；确实非裸写不可的，"+
				"在同一个文件里写一行 `// tenant-exempt: <理由>`",
				filepath.ToSlash(mustRel(base, path)), lit.line,
				strings.Join(dedupe(touched), "、"))
		}
		return nil
	})
}

// exemptedNearby 看这条 SQL 上面几行里有没有豁免注释。
func exemptedNearby(lines []string, line int) bool {
	// line 是 1 起的
	for i := line - 1; i >= 0 && i > line-1-goExemptLookback; i-- {
		if i < len(lines) && rxGoExempt.MatchString(lines[i]) {
			return true
		}
	}
	return false
}

// goLiteral 是一段 Go 字符串字面量。
type goLiteral struct {
	text string
	line int
}

// goStringLiterals 抠出源码里的字符串字面量（反引号和双引号两种）。
//
// 用 AST 取而不是正则扫全文：注释里的示例 SQL 不该算数。
func goStringLiterals(src string) []goLiteral {
	fset := token.NewFileSet()
	f, err := parser.ParseFile(fset, "", src, 0)
	if err != nil {
		return nil
	}
	var out []goLiteral
	ast.Inspect(f, func(n ast.Node) bool {
		lit, ok := n.(*ast.BasicLit)
		if !ok || lit.Kind != token.STRING {
			return true
		}
		out = append(out, goLiteral{
			text: strings.Trim(lit.Value, "`\""),
			line: fset.Position(lit.Pos()).Line,
		})
		return true
	})
	return out
}

// platformCallers 是允许调用 `repo.Store.Platform()` 的包。
//
// 平台句柄只碰得到平台级表，比 Unscoped 安全得多 —— 但 `ListTenants()`
// 依然是**整份客户名单**（§8.1）。业务模块没有任何理由需要它。
var platformCallers = map[string]string{
	"internal/auth":             "登录要按公司代码找租户；引导首个平台管理员",
	"internal/authz":            "加载权限策略要遍历租户",
	"internal/config":           "加载平台级配置",
	"internal/service/platform": "平台管理端自己",
	"internal/repo":             "包装层自己",
	"cmd/server":                "装配与后台任务",
}

// unscopedCallers 是允许调用 `repo.Store.Unscoped()` 的包。
//
// 三类，和 UnscopedQueries 上那批查询一一对应（MULTI-TENANCY.md §3.2 ③）：
//
//	internal/auth    认证链路 —— 拿 token / API Key / 公司代码定位身份，那一刻还没有租户
//	internal/audit   写审计 —— 未认证请求也要写，那时 tenant_id 是 NULL
//	internal/authz   加载权限策略 —— 要遍历所有租户
//	internal/config  加载配置 —— 同上，外加平台级配置
//	cmd/server       后台任务与探活 —— 清过期会话、建审计分区，本来就跨租户
//
// ⚠️ **service 和 handler 不在里面，这是刻意的。** 业务代码一律走 ForTenant。
// 要往这里加一个包，先想清楚它为什么非得绕过隔离不可。
var unscopedCallers = map[string]string{
	"internal/auth":                 "认证发生在租户上下文建立之前",
	"internal/audit":                "未认证请求也要写审计，那时没有租户",
	"internal/authz":                "加载权限策略要遍历所有租户",
	"internal/config":               "加载各租户配置 + 平台级配置",
	"cmd/server":                    "跨租户的后台任务与探活",
	"internal/repo":                 "包装层自己",
	"internal/service/registration": "自助注册在建租户之前，pending_registrations 是无租户的 infra 表",
}

// 走目录时要跳过的地方：工具二进制、依赖、构建产物。
const (
	dirBin     = "bin"
	dirVendor  = "vendor"
	dirTmp     = "tmp"
	dirSqlcgen = "sqlcgen"
)

// skipWalkDir 判断遍历源码时该不该跳过这个目录。
func skipWalkDir(name string) bool {
	switch name {
	case dirBin, dirVendor, dirTmp:
		return true
	}
	return false
}

// repoImportPath 是 repo 包的 import 路径。
const repoImportPath = "github.com/ramoncjs3/fries/internal/repo"

// 两个句柄的调用点。
var (
	rxUnscopedCall = regexp.MustCompile(`\.Unscoped\(\)`)
	rxPlatformCall = regexp.MustCompile(`\.Platform\(\)`)
)

// checkUnscopedCallers 查有没有不该拿不带租户句柄的地方拿了。
func checkUnscopedCallers(root string, p *problems) error {
	base := filepath.Join(root, "backend")
	return filepath.WalkDir(base, func(path string, d fs.DirEntry, err error) error {
		switch {
		case err != nil:
			return err
		case d.IsDir():
			if skipWalkDir(d.Name()) {
				return filepath.SkipDir
			}
			return nil
		case !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go"):
			return nil
		}

		raw, readErr := os.ReadFile(path)
		if readErr != nil {
			return readErr
		}
		// 要同时满足「真的 import 了 repo」和「出现了这两个句柄之一」才算数。
		// import 用 AST 判而不是搜字符串 —— 不然这个检查器自己的源码
		// （注释和常量里都有这两个词）会被自己报出来。
		//
		// ⚠️ **两个句柄都要看**。这里曾经只判 `.Unscoped()`，于是
		// 「只调 .Platform() 不调 .Unscoped()」的文件在这一行就被放过了，
		// 下面那个循环压根跑不到 —— platformCallers 那份白名单整整是死代码。
		// 实测过：往 internal/service/user 里加一行 `_ = s.store.Platform()`，
		// lint-sql 全绿，而 `Platform().ListTenants()` 就是整份客户名单（§8.1）。
		if !rxUnscopedCall.Match(raw) && !rxPlatformCall.Match(raw) {
			return nil
		}
		if !importsRepo(path) {
			return nil
		}

		rel, relErr := filepath.Rel(base, filepath.Dir(path))
		if relErr != nil {
			return relErr
		}
		pkg := filepath.ToSlash(rel)
		for _, c := range []struct {
			rx      *regexp.Regexp
			allowed map[string]string
			handle  string
			why     string
		}{
			{rxUnscopedCall, unscopedCallers, "Unscoped", "那是**不带租户**的句柄"},
			{rxPlatformCall, platformCallers, "Platform", "那上面有整份客户名单（§8.1）"},
		} {
			if !c.rx.Match(raw) || allowedPkg(c.allowed, pkg) {
				continue
			}
			p.addf("%s 调了 repo.Store.%s() —— %s，业务代码一律走 store.ForTenant()。"+
				"确实非绕不可的话，先在 MULTI-TENANCY.md §3.2 ③ 说明理由，"+
				"再把这个包加进 %sCallers",
				filepath.ToSlash(mustRel(base, path)), c.handle, c.why, strings.ToLower(c.handle))
		}
		return nil
	})
}

// importsRepo 判断一个 Go 文件的 import 块里有没有 internal/repo。
//
// 用 AST 而不是搜字符串：这个检查器自己的源码里就有 repo 的 import 路径
// （常量和注释里都有），搜字符串会把自己报出来。
// 文件语法错误时当成「没 import」—— 那种情况编译器会先报，这里不重复。
func importsRepo(path string) bool {
	f, err := parser.ParseFile(token.NewFileSet(), path, nil, parser.ImportsOnly)
	if err != nil {
		return false
	}
	for _, imp := range f.Imports {
		if imp.Path != nil && strings.Trim(imp.Path.Value, `"`) == repoImportPath {
			return true
		}
	}
	return false
}

// allowedPkg 判断一个包在不在白名单里（含子包）。
func allowedPkg(allowed map[string]string, pkg string) bool {
	for name := range allowed {
		if pkg == name || strings.HasPrefix(pkg, name+"/") {
			return true
		}
	}
	return false
}

func mustRel(base, path string) string {
	rel, err := filepath.Rel(base, path)
	if err != nil {
		return path
	}
	return rel
}

// ---------------------------------------------------------------- 唯一冲突翻译

// rxUniqueViolationRef 找 `repo.IsUniqueViolation(err, "uk_xxx")` 里那个索引名。
var rxUniqueViolationRef = regexp.MustCompile(`IsUniqueViolation\([^,]+,\s*"([a-z_][a-z0-9_]*)"`)

// 迁移里建/删索引的语句。`ADD PRIMARY KEY USING INDEX` 也算建 —— 那条索引还在，
// 只是被提升成了主键约束。
var (
	rxIdxCreate         = regexp.MustCompile(`(?i)create\s+(?:unique\s+)?index\s+(?:if\s+not\s+exists\s+)?([a-z_][a-z0-9_]*)`)
	rxIdxDrop           = regexp.MustCompile(`(?i)drop\s+index\s+(?:if\s+exists\s+)?([a-z_][a-z0-9_]*)`)
	rxIdxUsing          = regexp.MustCompile(`(?i)add\s+primary\s+key\s+using\s+index\s+([a-z_][a-z0-9_]*)`)
	rxIdxDropConstraint = regexp.MustCompile(`(?i)drop\s+constraint\s+(?:if\s+exists\s+)?([a-z_][a-z0-9_]*)`)
)

// checkUniqueViolationNames 查代码里引用的索引名在迁移里真实存在（MULTI-TENANCY.md §8.3）。
//
// `repo.IsUniqueViolation(err, "uk_users_username")` 是**按索引名精确匹配的字符串常量**。
// 多租户这一轮把这些索引全重建成了带 tenant_id 的版本 —— 名字要是顺手改了，
// 所有翻译一起失效，用户看到的从「这个用户名已经被占用」退化成一个通用 500，
// 而**编译期什么都查不出来**。
//
// 这次是靠「重建时刻意沿用原名」躲过去的，但那是自觉。这条检查把它变成机械的。
func checkUniqueViolationNames(root string, p *problems) error {
	live, err := liveIndexNames(filepath.Join(root, "backend", "db", "migrations"))
	if err != nil {
		return err
	}

	base := filepath.Join(root, "backend")
	return filepath.WalkDir(base, func(path string, d fs.DirEntry, err error) error {
		switch {
		case err != nil:
			return err
		case d.IsDir():
			if skipWalkDir(d.Name()) {
				return filepath.SkipDir
			}
			return nil
		case !strings.HasSuffix(path, ".go"):
			return nil
		}

		// 只查真 import 了 repo 的文件：IsUniqueViolation 是 repo 的函数，
		// 真正的调用方一定 import 了它。不加这一条的话，这个检查器自己
		// 注释里那个示例索引名会被自己报出来。
		if !importsRepo(path) {
			return nil
		}
		raw, readErr := os.ReadFile(path)
		if readErr != nil {
			return readErr
		}
		for _, m := range rxUniqueViolationRef.FindAllStringSubmatch(string(raw), -1) {
			if !live[m[1]] {
				p.addf("%s 引用了索引 %q，但迁移里没有这个索引 —— "+
					"唯一冲突的友好提示会静默退化成 500，编译期查不出来（§8.3）",
					filepath.ToSlash(mustRel(base, path)), m[1])
			}
		}
		return nil
	})
}

// ---------------------------------------------------------------- 租户表结构

// 这两条把「漏写就跨租户」从人自觉变成机械卡点（MULTI-TENANCY.md §8.4）。
// 之前它俩都没查，MODULES.md 却承诺 lint-sql 会拦索引 —— 文档比实现多说了一句。

// schemaExemptTenantTables 是「有 tenant_id 列、但故意不做应用层租户检查」的表。
//
// 加一张要有确凿理由：不登记进租户表清单，意味着它的查询不会被 lint-sql 拦租户条件。
// 目前只有链头表 —— 它只由 SECURITY DEFINER 触发器读写，没有任何应用查询。
var schemaExemptTenantTables = map[string]string{
	"audit_chain_head": "哈希链链头，只由 audit_chain() 触发器读写，没有应用查询；" +
		"tenant_id 是每租户一条链的分区键（MULTI-TENANCY.md §10.3）",
}

// schemaGlobalUniqueIndexes 是「建在租户表上、但故意不以 tenant_id 打头」的唯一索引。
//
// 只有认证那两条：登录/鉴权按 session token 或 API Key 前缀定位身份，
// 那一刻还不知道租户是谁（MULTI-TENANCY.md §3.2 ③），所以必须全平台唯一。
// 加第三条要非常谨慎 —— 它等于说「这个值在所有租户之间也不许重复」。
var schemaGlobalUniqueIndexes = map[string]string{
	"uk_sessions_token":             "认证按 session token 定位，此刻租户未知（§3.2③）",
	"uk_service_accounts_prefix":    "认证按 API Key 前缀定位，此刻租户未知（§3.2③）",
	"uk_password_reset_tokens_hash": "忘记密码按 token 定位，此刻用户还没登录、租户未知（§3.2③）",
}

// 表名一律允许可选的 schema 限定（public.orders），用 bareTable 归一成纯表名。
const rxTableName = `((?:[a-z_][a-z0-9_]*\.)?[a-z_][a-z0-9_]*)`

var (
	rxAddTenantCol  = regexp.MustCompile(`(?is)alter\s+table\s+(?:if\s+exists\s+)?` + rxTableName + `\s+add\s+column\s+(?:if\s+not\s+exists\s+)?tenant_id\b`)
	rxDropTenantCol = regexp.MustCompile(`(?is)alter\s+table\s+(?:if\s+exists\s+)?` + rxTableName + `\s+drop\s+column\s+(?:if\s+exists\s+)?tenant_id\b`)
	// 只抓 CREATE UNIQUE INDEX 的名字、表、第一列标识符。第一列是表达式（lower(x)）时
	// 抓到的是函数名，那本来也不是 tenant_id —— 会被正确判为「没打头」。
	rxUniqueIdxCols = regexp.MustCompile(`(?is)create\s+unique\s+index\s+(?:if\s+not\s+exists\s+)?([a-z_][a-z0-9_]*)\s+on\s+` + rxTableName + `\s*\(\s*([a-z_][a-z0-9_]*)`)
	// rxUniqueKeyword 认建表体里的内联 UNIQUE 约束（列级 `x UNIQUE` / 表级 `UNIQUE(...)`）。
	rxUniqueKeyword = regexp.MustCompile(`(?i)\bunique\b`)
)

// bareTable 去掉 schema 限定和引号，取纯表名 —— tenantTables 里存的都是纯名。
func bareTable(name string) string {
	name = strings.Trim(name, `"`)
	if i := strings.LastIndex(name, "."); i >= 0 {
		name = name[i+1:]
	}
	return strings.ToLower(strings.Trim(name, `"`))
}

// stripParenGroups 去掉所有括号里的内容，只留括号外的骨架。
// 用来判断一段列定义有没有 UNIQUE 关键字，又不被 `CHECK (... 'unique' ...)` 里的字符串骗到。
func stripParenGroups(s string) string {
	var b strings.Builder
	depth := 0
	for _, r := range s {
		switch r {
		case '(':
			depth++
		case ')':
			if depth > 0 {
				depth--
			}
		default:
			if depth == 0 {
				b.WriteRune(r)
			}
		}
	}
	return b.String()
}

// uniqueIndexDef 是一个唯一索引最终的样子。
type uniqueIndexDef struct {
	table    string
	firstCol string
}

// checkTenantTableSchema 查两件机械可判、原来却没人查的事（负责人点名的「漏写一个文件就出问题」）：
//
//	B1 登记完整：任何带 tenant_id 列的表都必须登记进 tenantsql 的租户表清单，
//	            否则它的查询一条都不会被租户检查 —— 而那正是最新、最没被 review 的表。
//	B2 索引打头：租户表上的唯一索引必须以 tenant_id 打头，否则唯一性会跨租户串味
//	            （A 租户建不了 B 租户已存在的同名值），而 lint-sql 原来完全没查这个。
func checkTenantTableSchema(root string, p *problems) error {
	schema, err := replayTenantSchema(filepath.Join(root, "backend", "db", "migrations"))
	if err != nil {
		return err
	}
	for _, why := range analyzeTenantSchema(schema) {
		p.addf("%s", why)
	}
	return nil
}

// tenantSchema 是回放迁移后算出来的租户相关结构。
type tenantSchema struct {
	hasTenant    map[string]bool           // 表 → 有没有 tenant_id 列
	idx          map[string]uniqueIndexDef // 唯一索引名 → 最终定义
	inlineUnique map[string]bool           // 表 → 建表体里有没有内联 UNIQUE 约束
}

// analyzeTenantSchema 是纯判定逻辑（不碰文件，好写变异测试）。
func analyzeTenantSchema(s tenantSchema) []string {
	var out []string

	// B1：带 tenant_id 的表要么被 tenantsql 守着，要么在豁免清单里写明理由。
	tables := make([]string, 0, len(s.hasTenant))
	for t := range s.hasTenant {
		tables = append(tables, t)
	}
	sort.Strings(tables)
	for _, table := range tables {
		if !tenantsql.IsGuarded(table) {
			if _, ok := schemaExemptTenantTables[table]; !ok {
				out = append(out, fmt.Sprintf("表 %s 有 tenant_id 列，却没登记进 tenantsql 的租户表清单 —— "+
					"它的查询一条都不会被租户检查（MULTI-TENANCY.md §1.2）。把它加进 internal/tenantsql 的 "+
					"tenantTables；确实不需要应用层检查的（比如纯触发器表），加进 lintsql 的 "+
					"schemaExemptTenantTables 并写明理由", table))
			}
		}

		// B2 补充：租户表的唯一性只能用 CREATE UNIQUE INDEX 声明，不能用内联 UNIQUE 约束 ——
		// 内联 UNIQUE 检查器看不出它的列顺序（架空下面那条 tenant_id 打头的检查），
		// 而且内联 UNIQUE 不能做软删除要的部分索引（§8.4）。
		if _, exempt := schemaExemptTenantTables[table]; s.inlineUnique[table] && !exempt {
			out = append(out, fmt.Sprintf("表 %s 在建表语句里用了内联 UNIQUE 约束 —— 租户表的唯一性一律用 "+
				"CREATE UNIQUE INDEX 声明（能查 tenant_id 打头、也支持软删除的部分索引），别写内联 UNIQUE",
				table))
		}
	}

	// B2：租户表上的唯一索引必须 tenant_id 打头，除非明列为全平台唯一。
	names := make([]string, 0, len(s.idx))
	for n := range s.idx {
		names = append(names, n)
	}
	sort.Strings(names)
	for _, name := range names {
		def := s.idx[name]
		if !s.hasTenant[def.table] {
			continue
		}
		if strings.EqualFold(def.firstCol, tenantIDCol) {
			continue
		}
		if _, ok := schemaGlobalUniqueIndexes[name]; ok {
			continue
		}
		out = append(out, fmt.Sprintf("唯一索引 %s 建在租户表 %s 上，但第一列是 %s、不是 tenant_id —— "+
			"唯一性会跨租户串味，A 租户建不了 B 租户已有的值（MULTI-TENANCY.md §8.4）。改成 (tenant_id, …)；"+
			"确实要全平台唯一的（比如认证索引），加进 lintsql 的 schemaGlobalUniqueIndexes 并写明理由",
			name, def.table, def.firstCol))
	}
	return out
}

// replayTenantSchema 按迁移顺序回放 Up 段，算出：
//   - 每张表最终有没有 tenant_id 列（ALTER 加的、CREATE TABLE 内联的都算，DROP 掉的减掉）
//   - 每个唯一索引最终的定义（DROP 掉又没重建的不算）
//
// 必须回放而不是全文 grep：多租户那次把旧的单列唯一索引 DROP 掉、用带 tenant_id 的
// 同名版本重建了，只看某一处会把历史定义当成现状误报。
func replayTenantSchema(dir string) (tenantSchema, error) {
	s := tenantSchema{
		hasTenant:    map[string]bool{},
		idx:          map[string]uniqueIndexDef{},
		inlineUnique: map[string]bool{},
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		return s, fmt.Errorf("读 %s: %w", dir, err)
	}
	files := make([]string, 0, len(entries))
	for _, e := range entries {
		if !e.IsDir() && strings.HasSuffix(e.Name(), ".sql") {
			files = append(files, e.Name())
		}
	}
	sort.Strings(files)

	for _, name := range files {
		raw, rerr := os.ReadFile(filepath.Join(dir, name))
		if rerr != nil {
			return s, fmt.Errorf("读 %s: %w", name, rerr)
		}
		up := tenantsql.StripComments(upSection(string(raw)))

		// CREATE TABLE 的表体：认内联 tenant_id 列，也认内联 UNIQUE 约束。
		for _, m := range rxCreateTable.FindAllStringSubmatchIndex(up, -1) {
			table := bareTable(up[m[2]:m[3]])
			body, ok := balancedBody(up[m[1]-1:])
			if !ok {
				continue
			}
			for _, col := range splitTopLevel(body) {
				if colName, _ := splitColumn(col); strings.EqualFold(colName, tenantIDCol) {
					s.hasTenant[table] = true
				}
				// 列级 `x UNIQUE` / 表级 `UNIQUE(...)` / `CONSTRAINT ... UNIQUE`。
				// 先剥掉括号内容，免得 CHECK (... 'unique' ...) 里的字符串骗过。
				if rxUniqueKeyword.MatchString(stripParenGroups(col)) {
					s.inlineUnique[table] = true
				}
			}
		}
		// ALTER TABLE 加/减 tenant_id（现有表都是 00007 这么加上的）。
		for _, m := range rxAddTenantCol.FindAllStringSubmatch(up, -1) {
			s.hasTenant[bareTable(m[1])] = true
		}
		for _, m := range rxDropTenantCol.FindAllStringSubmatch(up, -1) {
			delete(s.hasTenant, bareTable(m[1]))
		}

		// 唯一索引：CREATE 记下定义、DROP 抹掉，最后一次为准。
		for _, stmt := range strings.Split(up, ";") {
			switch {
			case rxIdxDrop.MatchString(stmt):
				delete(s.idx, rxIdxDrop.FindStringSubmatch(stmt)[1])
			case rxIdxDropConstraint.MatchString(stmt):
				delete(s.idx, rxIdxDropConstraint.FindStringSubmatch(stmt)[1])
			case rxUniqueIdxCols.MatchString(stmt):
				m := rxUniqueIdxCols.FindStringSubmatch(stmt)
				s.idx[m[1]] = uniqueIndexDef{table: bareTable(m[2]), firstCol: strings.ToLower(m[3])}
			}
		}
	}
	return s, nil
}

// liveIndexNames 按迁移顺序回放，算出最终还活着的索引名。
//
// 只看 Up 段：Down 段是回滚路径，把它算进来会把刚建好的索引又「删掉」。
func liveIndexNames(dir string) (map[string]bool, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, fmt.Errorf("读 %s: %w", dir, err)
	}
	files := make([]string, 0, len(entries))
	for _, e := range entries {
		if !e.IsDir() && strings.HasSuffix(e.Name(), ".sql") {
			files = append(files, e.Name())
		}
	}
	sort.Strings(files)

	live := map[string]bool{}
	for _, name := range files {
		raw, err := os.ReadFile(filepath.Join(dir, name))
		if err != nil {
			return nil, fmt.Errorf("读 %s: %w", name, err)
		}
		up := tenantsql.StripComments(upSection(string(raw)))
		for _, stmt := range strings.Split(up, ";") {
			switch {
			case rxIdxDrop.MatchString(stmt):
				live[rxIdxDrop.FindStringSubmatch(stmt)[1]] = false
			case rxIdxDropConstraint.MatchString(stmt):
				live[rxIdxDropConstraint.FindStringSubmatch(stmt)[1]] = false
			case rxIdxUsing.MatchString(stmt):
				live[rxIdxUsing.FindStringSubmatch(stmt)[1]] = true
			case rxIdxCreate.MatchString(stmt):
				live[rxIdxCreate.FindStringSubmatch(stmt)[1]] = true
			}
		}
	}
	return live, nil
}
