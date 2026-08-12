// Package tenantsql 判断一条 SQL 有没有把每一处取行的地方都绑到租户上。
//
// # 为什么单独成包：一个分析器，两个入口
//
// 同一条规则要在两个时刻生效：
//
//	构建期  cmd/gen lint-sql        扫 db/queries/*.sql 和 Go 里的字符串字面量
//	运行期  internal/repo 的 tracer 扫**真正发给 PostgreSQL 的**那条 SQL
//
// 两边必须是**同一份实现**。分成两份的话，中间那道缝就是下一个漏洞的位置 ——
// 这个项目已经栽过一次：`lint-sql` 查「SQL 里 tenant_id 绑没绑上」，
// 生成器查「参数结构体里有没有 TenantID 字段」，把 `sqlc.arg('tenant_id')` 改名成
// `sqlc.arg('tid')` 就能两边都绿着溜过去（MEMORY.md 记过）。
//
// 所以这里是**唯一**一处定义「什么算带了租户条件」的地方。
//
// # 为什么是叶子包
//
// 只依赖标准库。repo 要 import 它，cmd/gen 也要 import 它 ——
// 放进 repo 会让数据层多出一份「SQL 静态分析」的职责，还得把这些函数导出给
// 业务代码看见；反过来放进 cmd/gen 则运行期用不上。两边都要用的东西就该单独放。
//
// # 它不管什么
//
// 它只看 SQL **文本**：哪些表被引用、租户条件绑在谁身上。它不知道参数的值，
// 也不知道调用方是谁。「传进来的租户对不对」是 repo 那个 tracer 的事，
// 「调用方填了没填」是 ForTenant 包装的事（MULTI-TENANCY.md §1.2）。
package tenantsql

import (
	"regexp"
	"strings"
)

// tenantTables 是带 tenant_id 列的表。碰了它们的查询就必须带租户条件。
//
// **加了新的业务表就要加到这里** —— 但**漏加不再是静默的**：`make lint-sql` 的
// checkTenantTableSchema 会回放迁移，发现「有 tenant_id 列却没登记进这份清单」的表
// 就当场报（cmd/gen/lintsql.go）。真不需要应用层检查的（纯触发器表），
// 加进那边的 schemaExemptTenantTables 并写明理由。
//
// 另一道兜底：查询没有 tenant_id 参数的话，生成器会把它分流到 UnscopedQueries 上，
// 而那要求 SQL 里写了 `-- tenant-exempt:`，否则构建失败。
var tenantTables = map[string]bool{
	"users": true, "roles": true, "role_permissions": true, "user_roles": true,
	"sessions": true, "service_accounts": true, "settings": true,
	"departments": true, "suppliers": true, "audit_logs": true, "password_reset_tokens": true,
}

// exemptTables 是**结构上就不可能**带 tenant_id 的表。
//
// 只有三张是因为「认证发生在租户上下文建立之前」（MULTI-TENANCY.md §3.2 ③）：
// 拿 cookie 里的 token 查这是谁、拿 API Key 的 prefix 查这是哪个机器身份、
// 拿公司代码查这是哪家公司 —— 那一刻还不知道租户是谁。
//
// platform_* 三张是平台级的，本来就没有租户这个维度。
//
// **要往这里加第四张认证表，先去 MULTI-TENANCY.md §3.2 ③ 那份清单上加，并说明理由。**
var exemptTables = map[string]bool{
	"tenants": true, "platform_settings": true,
	"platform_admins": true, "platform_sessions": true,
	// sessions / service_accounts 本身有 tenant_id（大部分查询都要带），
	// 只有认证那两条例外，走 `-- tenant-exempt:` 单条豁免，不整表豁免。
}

// platformTables 是**平台级**的四张表（MULTI-TENANCY.md §6）。
//
// 一条查询引用的表要是全在这里面，它就只碰得到平台自己的东西 ——
// 生成器据此把它分流到 `Store.Platform()` 那个句柄上，于是平台服务
// **在编译期就够不到业务表**。
//
// 这是把 §6 那句「平台端只允许碰这三张平台级表」从文档变成结构。
// 一旦平台端能查 users，「平台端结构上碰不到客户业务数据」这个性质就没了 ——
// 而那是将来跟客户解释隔离时最有力的一句话（§10.11）。
var platformTables = map[string]bool{
	"tenants": true, "platform_admins": true,
	"platform_sessions": true, "platform_settings": true,
}

// IsGuarded 判断一张表要不要检查租户条件。
//
// 用函数而不是把 map 导出：导出的 map 是可写的，谁都能往里塞一张表或者删掉一张，
// 而这份清单正是整套检查的判据。
func IsGuarded(table string) bool {
	name := strings.ToLower(table)
	return tenantTables[name] && !exemptTables[name]
}

// TouchesGuardedTable 判断这段 SQL 有没有碰到需要检查的租户表。
//
// 给运行期的 tracer 用：一张租户表都不碰的语句（探活、平台查询、DDL）直接放过。
//
// 逗号 JOIN 一律当成「碰了」：它可能藏着一张 rxTableWithAlias 看不见的租户表，
// 在这里放过就等于运行期兜底也对它瞎了。放行给 Analyze 去判（那边会直接报逗号 JOIN）。
func TouchesGuardedTable(sql string) bool {
	if rxCommaJoin.MatchString(StripComments(sql)) {
		return true
	}
	for _, m := range rxTableWithAlias.FindAllStringSubmatch(sql, -1) {
		if IsGuarded(m[1]) {
			return true
		}
	}
	return false
}

// rxCommaJoin 抓 FROM 子句里的逗号连接：`FROM a, b` / `FROM a x, b`（也含 UPDATE ... FROM a, b）。
// 检查器对逗号后面的表是盲的，所以把逗号 JOIN 直接判为问题（详见 Analyze）。
//
// 残留边界（都不覆盖，因为全库不用这些写法）：`DELETE ... USING a, b`（USING 不是 FROM）、
// `FROM (子查询) x, guarded`（FROM 后紧跟括号）。真要用到时另行处理，别以为它们被拦住了。
var rxCommaJoin = regexp.MustCompile(`(?is)\bfrom\s+[a-z_][a-z0-9_]*(?:\s+(?:as\s+)?[a-z_][a-z0-9_]*)?\s*,`)

// TouchesOnlyPlatformTables 判断一条 SQL 是不是只碰平台级表。
//
// 一张表都不碰的（探活、建分区这类调函数的）不算平台查询 —— 它们归 Unscoped。
func TouchesOnlyPlatformTables(sql string) bool {
	found := false
	for _, m := range rxTableWithAlias.FindAllStringSubmatch(sql, -1) {
		name := strings.ToLower(m[1])
		switch {
		case platformTables[name]:
			found = true
		case tenantTables[name]:
			return false
		}
	}
	return found
}

// ---------------------------------------------------------------- 分析

// Analyze 检查一条查询，返回问题描述（空切片 = 没问题）。
//
// 思路不是「整条 SQL 里搜得到 tenant_id 就算过」—— 那太松了。真实的漏法是
// **一条查询里有好几处取行的地方，只给其中一处加了条件**（§10.7 的递归 CTE 就是，
// §10.8 的批量按 id 写也是）。
//
// 所以按「每一处租户表引用都必须被绑到租户参数上」来判：
//
//  1. 先把查询按 UNION 切成若干段。递归 CTE 的种子和递归是两段，各查各的 ——
//     这正是为了抓住「种子带了、递归没带」，两段共用同一个别名也糊弄不过去。
//  2. 每段里找出直接绑定：`x.tenant_id = $1` / `tenant_id = sqlc.arg('tenant_id')`。
//  3. 再找出等值传递：`a.tenant_id = b.tenant_id` —— JOIN 条件里的这种也算绑上了。
//  4. 传递闭包跑完之后，段里每一处租户表引用都必须落在已绑定集合里。
//
// 入参可以带注释（内部会去掉），所以运行期直接把 pgx 那条 SQL 丢进来就行。
func Analyze(sql string) []string {
	stripped := StripComments(sql)
	// 逗号 JOIN 先拦：检查器对 `FROM a, b` 里逗号后面的表是盲的（rxTableWithAlias 只认
	// from/join 后紧跟的那一张），第二张表连同它该带的租户条件会整个溜过去。全库本就用
	// 显式 JOIN，这里直接判为问题，把这个纯文本检查器的固有盲点堵死（§8.4）。
	if rxCommaJoin.MatchString(stripped) {
		return []string{"用了逗号 JOIN（FROM a, b）—— 检查器看不见逗号后面的表，" +
			"它的租户条件可能整个漏掉。改成显式 JOIN … ON …"}
	}
	if firstWord(strings.ToLower(stripped)) == "insert" {
		return analyzeInsert(stripped)
	}

	var problems []string
	for _, seg := range splitSetOperations(stripped) {
		// 这一段的表引用只解析一次，往下都传这份
		refs := tenantTableRefs(seg)
		if len(refs) == 0 {
			continue
		}
		bound := boundKeys(seg, refs)
		var missing []string
		for _, r := range refs {
			if !bound[r.key()] {
				missing = append(missing, r.describe())
			}
		}
		if len(missing) > 0 {
			problems = append(problems,
				"有 "+strings.Join(missing, "、")+" 没绑到租户上 —— "+bindHint(seg, refs))
		}
	}
	return problems
}

// analyzeInsert 查 INSERT 的**列名清单**里有没有 tenant_id。
//
// 只看整条 SQL 里有没有这个词是不够的：
// `INSERT INTO users (username) SELECT … WHERE tenant_id = $1` 也会蒙混过关。
func analyzeInsert(sql string) []string {
	refs := tenantTableRefs(sql)
	if len(refs) == 0 {
		return nil
	}
	touched := make([]string, 0, len(refs))
	for _, r := range refs {
		touched = append(touched, r.table)
	}

	cols, ok := insertColumns(sql)
	if !ok {
		return []string{"是 INSERT，但解析不出列名清单 —— 请写成 INSERT INTO 表 (列, …) 的形式"}
	}
	if !containsWord(cols, "tenant_id") {
		return []string{"往 " + strings.Join(dedupe(touched), "、") + " 插数据，列里没有 tenant_id"}
	}
	return nil
}

// tableRef 是一处租户表引用。
type tableRef struct {
	table string
	alias string
}

// key 是这处引用在 SQL 里的称呼：有别名用别名，没有就用表名。
func (r tableRef) key() string {
	if r.alias != "" {
		return r.alias
	}
	return r.table
}

func (r tableRef) describe() string {
	if r.alias != "" {
		return r.table + " " + r.alias
	}
	return r.table
}

var (
	// 表引用：FROM / JOIN / UPDATE / INTO 后面的表名，可选别名（可带 AS）
	rxTableWithAlias = regexp.MustCompile(`(?is)\b(?:from|join|into|update)\s+([a-z_][a-z0-9_]*)(?:\s+(?:as\s+)?([a-z_][a-z0-9_]*))?`)
	// 直接绑定：x.tenant_id = $1 / tenant_id = sqlc.arg('tenant_id')，两侧都允许带 ::类型
	rxBindParam = regexp.MustCompile(`(?is)(?:([a-z_][a-z0-9_]*)\.)?tenant_id\s*(?:::[a-z0-9_\[\]]+\s*)?=\s*(?:sqlc\.(?:n?arg)\([^)]*\)|\$\d+|@[a-z_][a-z0-9_]*)`)
	// coalesce(x.tenant_id, …) = 参数
	rxBindCoalesce = regexp.MustCompile(`(?is)coalesce\(\s*(?:([a-z_][a-z0-9_]*)\.)?tenant_id\s*,[^)]*\)\s*(?:::[a-z0-9_\[\]]+\s*)?=\s*(?:sqlc\.(?:n?arg)\([^)]*\)|\$\d+|@[a-z_][a-z0-9_]*)`)
	// 等值传递：a.tenant_id = b.tenant_id
	rxBindLink = regexp.MustCompile(`(?is)([a-z_][a-z0-9_]*)\.tenant_id\s*=\s*([a-z_][a-z0-9_]*)\.tenant_id`)
	// UNION / INTERSECT / EXCEPT 分段
	rxSetOp = regexp.MustCompile(`(?is)\b(?:union\s+all|union|intersect|except)\b`)
)

// aliasStopWords 是不能被当成别名的关键字。
// `FROM users WHERE …` 里的 WHERE 不是别名。
var aliasStopWords = map[string]bool{
	"where": true, "on": true, "set": true, "values": true, "group": true,
	"order": true, "limit": true, "offset": true, "join": true, "left": true,
	"right": true, "inner": true, "outer": true, "full": true, "cross": true,
	"union": true, "having": true, "using": true, "returning": true, "as": true,
	"and": true, "or": true, "select": true, "from": true, "with": true,
}

// tenantTableRefs 找出这段 SQL 里每一处租户表引用（同一张表出现两次算两处）。
func tenantTableRefs(sql string) []tableRef {
	var out []tableRef
	for _, m := range rxTableWithAlias.FindAllStringSubmatch(sql, -1) {
		if !IsGuarded(m[1]) {
			continue
		}
		alias := strings.ToLower(m[2])
		if aliasStopWords[alias] {
			alias = ""
		}
		out = append(out, tableRef{table: strings.ToLower(m[1]), alias: alias})
	}
	return out
}

// unaliasedRefs 挑出**不带别名**的那些引用。
//
// 裸的 `tenant_id = $1` 只可能绑在它们身上 —— 带别名的表必须写 `别名.tenant_id`。
func unaliasedRefs(refs []tableRef) []tableRef {
	out := make([]tableRef, 0, len(refs))
	for _, r := range refs {
		if r.alias == "" {
			out = append(out, r)
		}
	}
	return out
}

// hasBareBind 判断这段 SQL 里有没有**不带限定名**的 tenant_id 绑定。
func hasBareBind(sql string) bool {
	for _, rx := range []*regexp.Regexp{rxBindParam, rxBindCoalesce} {
		for _, m := range rx.FindAllStringSubmatch(sql, -1) {
			if m[1] == "" {
				return true
			}
		}
	}
	return false
}

// bindHint 挑一句最贴合这段 SQL 的提示。
//
// 分两种是有意的：作者明明写了 `tenant_id = $1` 却被报「没绑上」时，
// 如果只给一句「每处都要带条件」，他会以为是检查器坏了 —— 得直接告诉他歧义在哪。
func bindHint(seg string, refs []tableRef) string {
	if hasBareBind(seg) && len(unaliasedRefs(refs)) > 1 {
		return "这一段里的 tenant_id 条件没写限定名，而不带别名的租户表有好几处 —— " +
			"分不清它绑的是哪一张。给每张表起别名并写成 `u.tenant_id = ...`" +
			"（「批量按 id 写」就是这么漏的，见 §10.8）"
	}
	return "每一处取行的地方都要带 tenant_id 条件" +
		"（只给种子那一半加条件是最常见的漏法，见 §10.7）"
}

// boundKeys 算出这段 SQL 里哪些别名/表名被绑到了租户上（含等值传递的闭包）。
//
// 🔴 **不带限定名的 `tenant_id = $1` 只在「这一段里只有一处不带别名的租户表引用」时才算数。**
//
// 多于一处就是歧义，必须判成没绑上。曾经的写法是「有裸绑定就把所有不带别名的
// 租户表全算成绑好了」，于是下面这条**通过了检查**：
//
//	UPDATE users SET status = 'disabled'
//	WHERE id = ANY(sqlc.arg('user_ids')::uuid[])
//	  AND department_id IN (SELECT id FROM departments WHERE tenant_id = sqlc.arg('tenant_id'))
//
// 那个条件绑的是 departments，users 其实光着 —— 传一串别家公司的 user_id 就能
// 把人一起停掉。而这一类漏法**别的几层都看不见**：ForTenant 包装看到有租户参数、
// 照样注入；查回来的行核对（repo/assert.go）核的是返回行，写操作没有返回行可核。
//
// 要求写限定名（`u.tenant_id = ...`）歧义就消失了，代价只是给表起个别名。
func boundKeys(sql string, refs []tableRef) map[string]bool {
	bound := map[string]bool{}

	for _, rx := range []*regexp.Regexp{rxBindParam, rxBindCoalesce} {
		for _, m := range rx.FindAllStringSubmatch(sql, -1) {
			if m[1] == "" {
				continue // 裸绑定单独处理，见下
			}
			bound[strings.ToLower(m[1])] = true
		}
	}
	if bare := unaliasedRefs(refs); hasBareBind(sql) && len(bare) == 1 {
		bound[bare[0].table] = true
	}

	// 等值传递跑到不动为止：a=b、b=c 时 a 绑上了 c 也算绑上。
	links := rxBindLink.FindAllStringSubmatch(sql, -1)
	for changed := true; changed; {
		changed = false
		for _, m := range links {
			l, r := strings.ToLower(m[1]), strings.ToLower(m[2])
			switch {
			case bound[l] && !bound[r]:
				bound[r], changed = true, true
			case bound[r] && !bound[l]:
				bound[l], changed = true, true
			}
		}
	}
	return bound
}

// splitSetOperations 把查询按 UNION / INTERSECT / EXCEPT 切段。
//
// 递归 CTE 的种子和递归就是靠这一步分开的 —— 两段共用同一个别名，
// 不分开的话「种子带了条件、递归没带」会被算成整体带了。
func splitSetOperations(sql string) []string {
	parts := rxSetOp.Split(sql, -1)
	if len(parts) == 0 {
		return []string{sql}
	}
	return parts
}

// ---------------------------------------------------------------- 文本工具

// StripComments 去掉 `--` 和 `/* */` 注释。
//
// 注释里的 tenant_id 不算数 —— 不去掉的话，一句「这里不用带 tenant_id」的注释
// 就能让检查通过。块注释也要去：`/* tenant_id */` 同样能骗过检查。
func StripComments(sql string) string {
	// 先去块注释。不支持嵌套（PostgreSQL 支持，但查询里出现嵌套块注释的概率
	// 远低于「有人在注释里写了 tenant_id」）。
	for {
		start := strings.Index(sql, "/*")
		if start < 0 {
			break
		}
		end := strings.Index(sql[start+2:], "*/")
		if end < 0 {
			sql = sql[:start]
			break
		}
		sql = sql[:start] + " " + sql[start+2+end+2:]
	}

	var b strings.Builder
	for line := range strings.SplitSeq(sql, "\n") {
		if i := strings.Index(line, "--"); i >= 0 {
			line = line[:i]
		}
		b.WriteString(line)
		b.WriteByte('\n')
	}
	return b.String()
}

// containsWord 判断以逗号/空白分隔的列名清单里有没有某个列。
func containsWord(list, word string) bool {
	for f := range strings.FieldsFuncSeq(list, func(r rune) bool {
		return r == ',' || r == ' ' || r == '\n' || r == '\t' || r == '(' || r == ')'
	}) {
		if f == word {
			return true
		}
	}
	return false
}

// insertColumns 取 INSERT INTO 表 (…) 里那对括号中的内容。
func insertColumns(sql string) (string, bool) {
	lower := strings.ToLower(sql)
	i := strings.Index(lower, "insert into")
	if i < 0 {
		return "", false
	}
	open := strings.Index(sql[i:], "(")
	if open < 0 {
		return "", false
	}
	open += i
	depth := 0
	for j := open; j < len(sql); j++ {
		switch sql[j] {
		case '(':
			depth++
		case ')':
			depth--
			if depth == 0 {
				return strings.ToLower(sql[open+1 : j]), true
			}
		}
	}
	return "", false
}

func firstWord(s string) string {
	for f := range strings.FieldsSeq(s) {
		return f
	}
	return ""
}

func dedupe(in []string) []string {
	seen := make(map[string]bool, len(in))
	out := make([]string, 0, len(in))
	for _, s := range in {
		if !seen[s] {
			seen[s] = true
			out = append(out, s)
		}
	}
	return out
}
