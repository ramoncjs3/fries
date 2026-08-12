package tenantsql

import (
	"regexp"
	"strconv"
	"strings"
)

// Query 是 db/queries/*.sql 里的一条 sqlc 查询。
type Query struct {
	// File / Line 用来把问题报回原处。
	File string
	Line int
	// Name 是 sqlc 的查询名，也是生成出来的方法名。
	Name string
	// Kind 是 sqlc 的返回类型标注：one / many / exec / execrows。
	Kind string
	// Body 是查询正文（不含 `-- name:` 那一行）。
	Body string
	// ExemptReason 是 `-- tenant-exempt:` 后面那句理由，空表示没豁免。
	//
	// 豁免要写理由而不是只写个标记：白名单上的每一条都会被人读到，
	// 没有理由的豁免过一年谁都不敢删。
	ExemptReason string
}

var (
	rxQueryName = regexp.MustCompile(`^--\s*name:\s*(\w+)\s*:(\w+)\s*$`)
	rxExempt    = regexp.MustCompile(`^--\s*tenant-exempt:\s*(.+?)\s*$`)
)

// SplitQueries 把一个 .sql 文件切成一条条查询。
func SplitQueries(src, file string) []Query {
	var out []Query
	var cur *Query

	for i, line := range strings.Split(src, "\n") {
		trimmed := strings.TrimSpace(line)
		if m := rxQueryName.FindStringSubmatch(trimmed); m != nil {
			if cur != nil {
				out = append(out, *cur)
			}
			cur = &Query{File: file, Line: i + 1, Name: m[1], Kind: m[2]}
			continue
		}
		if cur == nil {
			continue
		}
		if m := rxExempt.FindStringSubmatch(trimmed); m != nil {
			cur.ExemptReason = m[1]
			continue
		}
		cur.Body += line + "\n"
	}
	if cur != nil {
		out = append(out, *cur)
	}
	return out
}

// rxExemptComment 找**写在 SQL 里面**的豁免标记。
//
// 和 `rxExempt` 的区别是它不要求整行匹配 —— 手写的 SQL 里这一行前面通常有缩进。
var rxExemptComment = regexp.MustCompile(`(?m)^\s*--\s*tenant-exempt:\s*\S`)

// HasExemptComment 判断这条 SQL 里有没有写豁免标记。
//
// # 为什么标记要写在 SQL 里面，而不是写成 Go 注释
//
// 因为**同一个标记要被两个时刻看见**：构建期的 `gen lint-sql` 和运行期的 tracer。
// Go 注释只有前者看得见（运行期拿到的是字符串的值，注释早就没了）。
// 写在 SQL 里面，一处声明两边都认，而且它**跟着这条 SQL 走** ——
// 有人把这段 SQL 挪到别的函数里，豁免和理由一起挪过去，不会掉队。
//
//	// 这一句的全部意义就是绕过应用层 —— 带上租户条件就不是在模拟攻击了
//	pool.Exec(ctx, `-- tenant-exempt: 模拟攻击者直接改库
//	    UPDATE audit_logs SET action = 'tampered' WHERE id = $1`)
//
// ⚠️ sqlc 的查询用不着这个：它把除 `-- name:` 以外的注释**全部剥掉**，
// 标记到不了运行期。那批走生成出来的豁免清单（exemptQueries），
// 理由留在 db/queries/*.sql 里，`make lint-sql` 每次都打出来。
//
// # 这不是一个后门吗
//
// 是逃生口，和 db/queries 里那个是同一个信任模型：谁都能加，但**加了就留在代码里**，
// `grep -rn "tenant-exempt"` 一次就能数清有几处、每处的理由是什么。
// 没有逃生口的检查会被人直接关掉，那才是真的后门。
func HasExemptComment(sql string) bool {
	return rxExemptComment.MatchString(sql)
}

// rxEmbeddedName 找 sqlc 埋在生成的 SQL 字符串里的那一行 `-- name: X :kind`。
//
// 这一行是运行期能认出「这是哪条查询」的**唯一**线索：pgx 的 tracer 只拿得到
// SQL 文本和参数，拿不到 Go 那边的方法名。好在 sqlc 会把查询名原样留在 SQL 里。
var rxEmbeddedName = regexp.MustCompile(`(?m)^\s*--\s*name:\s*(\w+)\s*:\w+\s*$`)

// QueryName 从一条 SQL 里取出 sqlc 的查询名，取不到返回空串。
//
// 给运行期的 tracer 用：拿到名字才能去对豁免清单。取不到名字的（手写的裸 SQL、
// 迁移、测试里拼的语句）一律按「没有豁免」处理 —— **默认拒绝**，
// 不是「认不出就放过」。
func QueryName(sql string) string {
	m := rxEmbeddedName.FindStringSubmatch(sql)
	if m == nil {
		return ""
	}
	return m[1]
}

// rxBoundTenantArg 找 `tenant_id = $N` 里那个参数序号。
//
// 只认位置参数：运行期看到的是 sqlc 编译之后的 SQL，`sqlc.arg()` 已经变成 `$N` 了。
var rxBoundTenantArg = regexp.MustCompile(`(?is)(?:[a-z_][a-z0-9_]*\.)?tenant_id\s*(?:::[a-z0-9_\[\]]+\s*)?=\s*\$(\d+)`)

// BoundTenantArgIndex 返回租户条件绑的是第几个参数（0 起），没找到返回 false。
//
// 给 tracer 的**值比对**用：SQL 里带了条件不代表带的是**对的**租户。
// 拿着 A 的会话去查 B 的数据，条件在、语法对、静态检查也过 —— 只有比对值才看得出来。
//
// ⚠️ 一条 SQL 里可能有好几处 `tenant_id = $N`（JOIN 多张表时），
// 但 sqlc 对同一个命名参数只生成一个位置参数，所以第一处就够了。
// 真出现绑到不同参数上的情况，那本身就该在 review 里被问住。
func BoundTenantArgIndex(sql string) (int, bool) {
	m := rxBoundTenantArg.FindStringSubmatch(StripComments(sql))
	if m == nil {
		return 0, false
	}
	n, err := strconv.Atoi(m[1])
	if err != nil || n < 1 {
		return 0, false
	}
	return n - 1, true
}

// dmlVerbs 是会读写行的语句。
//
// 只检查这几种：DDL（迁移）、TRUNCATE（测试清场）、SET / BEGIN 这些都不该被拦。
// ⚠️ TRUNCATE 尤其要放过 —— 测试基建每个用例前都会 `TRUNCATE users, ...`，
// 那是**故意跨租户**的清场动作。
var dmlVerbs = map[string]bool{
	"select": true, "insert": true, "update": true, "delete": true, "with": true,
}

// IsDML 判断这条语句会不会读写行。
//
// `WITH` 算进来是因为 CTE 打头的查询（部门取子树那条）第一个词就是它。
func IsDML(sql string) bool {
	return dmlVerbs[strings.ToLower(firstWord(strings.TrimSpace(StripComments(sql))))]
}
