package main

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
)

// 迁移文件里我们关心的三种语句。
var (
	rxCreateTable = regexp.MustCompile(`(?is)CREATE\s+TABLE\s+(?:IF\s+NOT\s+EXISTS\s+)?([a-z0-9_."]+)\s*\(`)
	rxAlterTable  = regexp.MustCompile(`(?is)ALTER\s+TABLE\s+(?:IF\s+EXISTS\s+)?([a-z0-9_."]+)\s+([^;]+);`)
	rxCreateIndex = regexp.MustCompile(`(?is)CREATE\s+(UNIQUE\s+)?INDEX\s+(?:IF\s+NOT\s+EXISTS\s+)?([a-z0-9_]+)\s+ON\s+([a-z0-9_."]+)\s*([^;]+);`)
	rxGooseDown   = regexp.MustCompile(`(?im)^--\s*\+goose\s+Down\s*$`)
	rxStatement   = regexp.MustCompile(`(?im)^--\s*\+goose\s+Statement(Begin|End)\s*$`)
)

// table 是从迁移里还原出来的一张表。
type table struct {
	name    string
	columns []string
	indexes []string
	changes []string
	source  string
}

// runSchemadoc 由迁移文件生成 docs/SCHEMA.md。
//
// 真相永远是 backend/db/migrations —— 这份文档只是给人快速看的索引，
// 所以宁可写得保守（照抄 DDL 片段），也不做花哨的解析。
func runSchemadoc(root string, args []string) error {
	check, err := checkFlag("schemadoc", args)
	if err != nil {
		return err
	}

	dir := filepath.Join(root, "backend", "db", "migrations")
	entries, err := os.ReadDir(dir)
	if err != nil {
		return fmt.Errorf("读迁移目录: %w", err)
	}

	files := make([]string, 0, len(entries))
	for _, e := range entries {
		if !e.IsDir() && strings.HasSuffix(e.Name(), ".sql") {
			files = append(files, e.Name())
		}
	}
	sort.Strings(files)

	tables := map[string]*table{}
	var order []string
	for _, name := range files {
		raw, err := os.ReadFile(filepath.Join(dir, name))
		if err != nil {
			return fmt.Errorf("读 %s: %w", name, err)
		}
		parseMigration(name, upSection(string(raw)), tables, &order)
	}

	var b bytes.Buffer
	b.WriteString("# 数据库表结构总览\n\n")
	b.WriteString("> **本文件由 `make schemadoc` 自动生成，请勿手改。**\n")
	b.WriteString("> 数据源是 `backend/db/migrations/`，改表一律走 goose 迁移（红线 #4）。\n")
	b.WriteString("> `make check` 会校验本文件是最新的。\n\n")
	fmt.Fprintf(&b, "迁移文件 %d 个，表 %d 张。\n", len(files), len(order))

	if len(order) == 0 {
		b.WriteString("\n当前还没有业务表 —— 第 ① 步的迁移只建了公共扩展和 `set_updated_at()` 触发器函数。\n")
		b.WriteString("业务表由 `make gen-module` 按 `modules/<key>.yaml` 生成（DECISIONS.md §10）。\n")
	}

	for _, name := range order {
		t := tables[name]
		fmt.Fprintf(&b, "\n## %s\n\n", t.name)
		fmt.Fprintf(&b, "建表于 `%s`。\n\n", t.source)
		if len(t.columns) > 0 {
			b.WriteString("| 列 | 定义 |\n|---|---|\n")
			for _, col := range t.columns {
				name, rest := splitColumn(col)
				fmt.Fprintf(&b, "| `%s` | `%s` |\n", name, rest)
			}
		}
		if len(t.indexes) > 0 {
			b.WriteString("\n**索引**\n\n")
			for _, idx := range t.indexes {
				fmt.Fprintf(&b, "- `%s`\n", idx)
			}
		}
		if len(t.changes) > 0 {
			b.WriteString("\n**后续变更**\n\n")
			for _, ch := range t.changes {
				fmt.Fprintf(&b, "- `%s`\n", ch)
			}
		}
	}

	return writeOrCheck(filepath.Join(root, "docs", "SCHEMA.md"), b.Bytes(), check, "`make schemadoc`")
}

// upSection 只取 goose 的 Up 段 —— Down 段是回滚用的，不代表最终结构。
func upSection(sql string) string {
	if loc := rxGooseDown.FindStringIndex(sql); loc != nil {
		sql = sql[:loc[0]]
	}
	return rxStatement.ReplaceAllString(sql, "")
}

// parseMigration 从一个迁移文件里抽出建表、加索引、改表三类信息。
func parseMigration(file, sql string, tables map[string]*table, order *[]string) {
	get := func(name string) *table {
		name = strings.Trim(name, `"`)
		t, ok := tables[name]
		if !ok {
			t = &table{name: name, source: file}
			tables[name] = t
			*order = append(*order, name)
		}
		return t
	}

	for _, m := range rxCreateTable.FindAllStringSubmatchIndex(sql, -1) {
		name := sql[m[2]:m[3]]
		body, ok := balancedBody(sql[m[1]-1:])
		if !ok {
			continue
		}
		t := get(name)
		t.columns = splitTopLevel(body)
	}

	for _, m := range rxCreateIndex.FindAllStringSubmatch(sql, -1) {
		unique := strings.TrimSpace(m[1]) != ""
		stmt := "CREATE "
		if unique {
			stmt += "UNIQUE "
		}
		stmt += "INDEX " + m[2] + " ON " + m[3] + " " + normalizeSpace(m[4])
		get(m[3]).indexes = append(get(m[3]).indexes, stmt)
	}

	for _, m := range rxAlterTable.FindAllStringSubmatch(sql, -1) {
		t := get(m[1])
		t.changes = append(t.changes, fmt.Sprintf("%s（%s）", normalizeSpace(m[2]), file))
	}
}

// balancedBody 取出以 '(' 开头的一段括号内容。
func balancedBody(s string) (string, bool) {
	depth := 0
	for i, r := range s {
		switch r {
		case '(':
			depth++
		case ')':
			depth--
			if depth == 0 {
				return s[1:i], true
			}
		}
	}
	return "", false
}

// splitTopLevel 按顶层逗号切分建表语句的列定义。
func splitTopLevel(body string) []string {
	var out []string
	depth, start := 0, 0
	for i, r := range body {
		switch r {
		case '(':
			depth++
		case ')':
			depth--
		case ',':
			if depth == 0 {
				out = append(out, normalizeSpace(body[start:i]))
				start = i + 1
			}
		}
	}
	if last := normalizeSpace(body[start:]); last != "" {
		out = append(out, last)
	}
	return out
}

// splitColumn 把「列名 + 其余定义」拆开。
func splitColumn(col string) (string, string) {
	parts := strings.SplitN(col, " ", 2)
	if len(parts) == 1 {
		return parts[0], ""
	}
	return parts[0], parts[1]
}

// normalizeSpace 去掉 SQL 注释并把连续空白压成一个空格。
func normalizeSpace(s string) string {
	raw := strings.Split(s, "\n")
	lines := make([]string, 0, len(raw))
	for _, line := range raw {
		if i := strings.Index(line, "--"); i >= 0 {
			line = line[:i]
		}
		lines = append(lines, line)
	}
	return strings.Join(strings.Fields(strings.Join(lines, " ")), " ")
}
