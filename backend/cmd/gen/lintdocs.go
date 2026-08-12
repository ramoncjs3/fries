package main

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

// 文档里我们要检查的四类引用。
var (
	rxLink       = regexp.MustCompile(`\[[^\]]*\]\(([^)\s]+)\)`)
	rxInlineCode = regexp.MustCompile("`([^`\n]+)`")
	rxFence      = regexp.MustCompile("(?s)```.*?```")
	rxMakeTarget = regexp.MustCompile(`\bmake\s+([a-z][a-z0-9-]*)`)
	// 章节引用。前面可以带文件名指向别的文档，四种写法都认：
	//   MULTI-TENANCY.md §3.2   `MULTI-TENANCY.md` §3.2
	//   docs/MULTI-TENANCY.md §3.2   `docs/MULTI-TENANCY.md` §3.2
	// 不带文件名就是指**当前这篇文档自己**的章节（找不到再回退 DECISIONS.md）。
	rxSection    = regexp.MustCompile("(?:`?(?:docs/)?([A-Za-z0-9_-]+\\.md)`?\\s*)?§(\\d+(?:\\.\\d+)*)")
	rxMakeRule   = regexp.MustCompile(`(?m)^([a-zA-Z0-9_-]+):`)
	rxHeading    = regexp.MustCompile(`(?m)^(#{1,6})\s+(.+?)\s*$`)
	rxSectionNum = regexp.MustCompile(`^(\d+(?:\.\d+)*)`)
	rxIdent      = regexp.MustCompile(`^([a-z][a-z0-9]*)\.([A-Z][A-Za-z0-9_]*)`)
)

// knownAbsent 是「文档会提到，但仓库里本来就不该有」的路径。
var knownAbsent = map[string]bool{
	"config/config.yaml":    true, // 含密钥，不进版本库
	"frontend/node_modules": true, // 依赖，不进版本库
	"backend/bin":           true, // 工具二进制，不进版本库
}

// runLintDocs 检查文档里的引用是否有效：
//
//  1. markdown 链接指向的文件存在，锚点也存在
//  2. 反引号里写的仓库路径存在
//  3. 提到的 make 目标在 Makefile 里真有
//  4. §x.y 这样的章节号在 DECISIONS.md 里真有
//  5. `pkg.Ident` 这样的标识符在对应的 Go 包里真有
//
// 预防比检查便宜（docs/MEMORY.md），但改坏了总得有人兜底。
func runLintDocs(root string, _ []string) error {
	var p problems

	docs, err := collectMarkdown(root)
	if err != nil {
		return err
	}
	if len(docs) == 0 {
		return fmt.Errorf("一个 markdown 文件都没找到，是不是路径不对")
	}

	makeTargets, err := parseMakeTargets(root)
	if err != nil {
		return err
	}
	sections, anchors, err := parseHeadings(root, docs)
	if err != nil {
		return err
	}
	idents := parseGoIdents(root)

	for _, doc := range docs {
		raw, err := os.ReadFile(doc)
		if err != nil {
			return fmt.Errorf("读 %s: %w", relTo(root, doc), err)
		}
		text := string(raw)
		where := relTo(root, doc)

		checkLinks(root, doc, where, text, anchors, &p)
		checkMakeTargets(where, text, makeTargets, &p)
		checkSections(where, text, sections, &p)

		// 路径和标识符只在正文里查 —— 代码块里都是示例，查了全是噪音。
		body := rxFence.ReplaceAllString(text, "")
		checkPathsAndIdents(root, where, body, idents, &p)
	}

	if err := p.err("文档引用有问题"); err != nil {
		return err
	}
	fmt.Printf("✓ 文档引用有效（%d 个文件）\n", len(docs))
	return nil
}

// collectMarkdown 收集要检查的 markdown：根目录、docs/、.claude/skills/。
func collectMarkdown(root string) ([]string, error) {
	var out []string
	for _, dir := range []string{root, filepath.Join(root, "docs"), filepath.Join(root, ".claude")} {
		err := filepath.WalkDir(dir, func(path string, d os.DirEntry, walkErr error) error {
			if walkErr != nil {
				// 读不了的目录跳过就好，别让 lint 因为一个软链接挂掉。
				return nil //nolint:nilerr // 有意忽略
			}
			if d.IsDir() {
				if treeIgnore[d.Name()] {
					return filepath.SkipDir
				}
				// 根目录只看第一层，子目录由各自的分支处理。
				if path != dir && dir == root {
					return filepath.SkipDir
				}
				return nil
			}
			if strings.HasSuffix(d.Name(), ".md") {
				out = append(out, path)
			}
			return nil
		})
		if err != nil {
			return nil, err
		}
	}
	return dedupe(out), nil
}

// parseMakeTargets 读 Makefile 里定义了哪些目标。
func parseMakeTargets(root string) (map[string]bool, error) {
	raw, err := os.ReadFile(filepath.Join(root, "Makefile"))
	if err != nil {
		return nil, fmt.Errorf("读 Makefile: %w", err)
	}
	targets := map[string]bool{}
	for _, m := range rxMakeRule.FindAllStringSubmatch(string(raw), -1) {
		targets[m[1]] = true
	}
	return targets, nil
}

// parseHeadings 收集 DECISIONS.md 的章节号，以及每个文档的锚点。
// parseHeadings 收集每篇文档的章节号和锚点。
//
// 章节号**按文档分开存**：以前只认 DECISIONS.md，多了第二篇带编号的文档
// （MULTI-TENANCY.md）之后，它自己的 §x 会被拿去 DECISIONS.md 里找，必然误报；
// 而它自己的章节号反倒没人校验。
func parseHeadings(root string, docs []string) (sections map[string]map[string]bool, anchors map[string]map[string]bool, err error) {
	sections = map[string]map[string]bool{}
	anchors = map[string]map[string]bool{}

	for _, doc := range docs {
		raw, err := os.ReadFile(doc)
		if err != nil {
			return nil, nil, err
		}
		rel := relTo(root, doc)
		anchors[rel] = map[string]bool{}
		sections[rel] = map[string]bool{}
		for _, m := range rxHeading.FindAllStringSubmatch(string(raw), -1) {
			title := m[2]
			anchors[rel][normalizeAnchor(slug(title))] = true
			if num := rxSectionNum.FindString(title); num != "" {
				sections[rel][num] = true
			}
		}
	}
	return sections, anchors, nil
}

// slug 按 GitHub 的规则把标题转成锚点。
func slug(title string) string {
	var b strings.Builder
	for _, r := range strings.ToLower(title) {
		switch {
		case r == ' ':
			b.WriteRune('-')
		case r == '-' || r == '_':
			b.WriteRune(r)
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9', r > 0x7f:
			// CJK 直接保留，符号（§ ★ ⚠️ 等）丢掉
			if isPunctCJK(r) {
				continue
			}
			b.WriteRune(r)
		}
	}
	return b.String()
}

// normalizeAnchor 抹平锚点里的不可见字符和重复连字符。
//
// 标题里带 emoji（如「⚠️ 核心设计」）时，GitHub 生成的锚点会留下变体选择符
// U+FE0F 和连着的两个 `-`，肉眼看不出来。两边都归一化再比，才不会因为
// 一个看不见的字符判死。
func normalizeAnchor(s string) string {
	var b strings.Builder
	prevDash := false
	for _, r := range s {
		if r >= 0xfe00 && r <= 0xfe0f {
			continue
		}
		if r == '-' {
			if prevDash {
				continue
			}
			prevDash = true
		} else {
			prevDash = false
		}
		b.WriteRune(r)
	}
	return strings.Trim(b.String(), "-")
}

// isPunctCJK 判断是不是要丢掉的非文字字符（标点、符号、emoji）。
func isPunctCJK(r rune) bool {
	switch {
	case r >= 0x2000 && r <= 0x206f, // 常见标点
		r >= 0x2190 && r <= 0x2bff, // 箭头、符号
		r >= 0x3000 && r <= 0x303f, // CJK 标点
		r >= 0xfe00 && r <= 0xfe0f, // 变体选择符
		r >= 0xff01 && r <= 0xff65, // 全角标点
		r >= 0x1f000:               // emoji
		return true
	}
	return false
}

// checkLinks 检查 markdown 链接指向的文件和锚点。
func checkLinks(root, doc, where, text string, anchors map[string]map[string]bool, p *problems) {
	for _, m := range rxLink.FindAllStringSubmatch(text, -1) {
		target := m[1]
		if strings.HasPrefix(target, "http://") || strings.HasPrefix(target, "https://") ||
			strings.HasPrefix(target, "mailto:") {
			continue
		}

		path, anchor, _ := strings.Cut(target, "#")
		// 形如 file.md:42 的行号后缀
		if i := strings.LastIndex(path, ":"); i > 0 && isDigits(path[i+1:]) {
			path = path[:i]
		}

		rel := relTo(root, doc)
		if path != "" {
			abs := filepath.Join(filepath.Dir(doc), path)
			if _, err := os.Stat(abs); err != nil {
				p.addf("%s: 链接 %q 指向的文件不存在", where, target)
				continue
			}
			rel = relTo(root, abs)
		}

		if anchor == "" {
			continue
		}
		if known, ok := anchors[rel]; ok && !known[normalizeAnchor(anchor)] {
			p.addf("%s: 链接 %q 的锚点在 %s 里不存在", where, target, rel)
		}
	}
}

// checkMakeTargets 检查文档提到的 make 目标真的存在 —— 空头支票就是这么来的。
func checkMakeTargets(where, text string, targets map[string]bool, p *problems) {
	for _, m := range rxMakeTarget.FindAllStringSubmatch(text, -1) {
		if !targets[m[1]] {
			p.addf("%s: 提到了 `make %s`，但 Makefile 里没这个目标", where, m[1])
		}
	}
}

// checkSections 检查 §x.y 指向的章节真的存在。
//
// 解析规则，按优先级：
//
//  1. `X.md §3` —— 明确指定了文档，就查那一篇（推荐写法）
//  2. 光写 `§3`，而当前文档自己有第 3 节 —— 就是指自己（就近原则）
//  3. 光写 `§3`，当前文档没有 —— 回退到 DECISIONS.md
//
// 第 3 条是给存量文档留的：AGENTS.md、README.md 里大量 `§x` 都是省略了
// 「DECISIONS.md」。**新文档请把文件名写出来**，省得两篇都有同号章节时靠猜。
func checkSections(where, text string, sections map[string]map[string]bool, p *problems) {
	for _, m := range rxSection.FindAllStringSubmatch(text, -1) {
		file, num := m[1], m[2]
		target := where
		if file != "" {
			target = "docs/" + file
			if _, ok := sections[target]; !ok {
				p.addf("%s: 引用了 %s §%s，但没有这份文档", where, file, num)
				continue
			}
		}
		if sections[target][num] {
			continue
		}
		// 没写文件名、当前文档也没有这一节 —— 按存量约定回退到 DECISIONS.md
		if file == "" && sections["docs/DECISIONS.md"][num] {
			continue
		}
		p.addf("%s: 引用了 %s 的 §%s，但那篇文档里没有这一节", where, target, num)
	}
}

// checkPathsAndIdents 检查反引号里的仓库路径和 Go 标识符。
func checkPathsAndIdents(root, where, body string, idents map[string]map[string]bool, p *problems) {
	for _, m := range rxInlineCode.FindAllStringSubmatch(body, -1) {
		code := strings.TrimSpace(m[1])

		if id := rxIdent.FindStringSubmatch(code); id != nil {
			pkg, name := id[1], id[2]
			if known, ok := idents[pkg]; ok && !known[name] {
				p.addf("%s: 提到 `%s.%s`，但 internal/%s 里没有这个导出标识符", where, pkg, name, pkg)
			}
			continue
		}

		path, ok := repoPath(code)
		if !ok || knownAbsent[path] {
			continue
		}
		if !existsUnder(root, path) {
			p.addf("%s: 提到路径 `%s`，但仓库里找不到", where, path)
		}
	}
}

// repoPath 判断一段行内代码是不是「仓库里的路径」。
//
// **只认从仓库根算起的完整路径**（backend/... / docs/... / modules/...）。
// 像 `internal/errs`、`src/api/client.ts` 这种简写不查 —— 它们和第三方包名
// （`google/uuid`、`x/time/rate`）长得一样，分不清楚就会误报，而误报比漏报更伤。
func repoPath(s string) (string, bool) {
	s = strings.TrimRight(s, ".,;:。，、")
	if !strings.Contains(s, "/") {
		return "", false
	}
	if strings.ContainsAny(s, " \t<>*?|$(){}[]'\"=~") || strings.Contains(s, "://") {
		return "", false
	}
	first, _, _ := strings.Cut(s, "/")
	if _, ok := allowedTopLevel[first]; !ok {
		return "", false
	}
	return s, true
}

// existsUnder 在仓库根目录下找这个相对路径。
func existsUnder(root, path string) bool {
	_, err := os.Stat(filepath.Join(root, filepath.FromSlash(path)))
	return err == nil
}

// parseGoIdents 收集 backend/internal/<pkg> 里的导出标识符。
//
// 只收有 .go 文件的包 —— 还没实现的包（第 ②⑥ 步的 auth / llm 等）跳过，
// 否则文档里提前写好的设计会被误报。
func parseGoIdents(root string) map[string]map[string]bool {
	out := map[string]map[string]bool{}
	base := filepath.Join(root, "backend", "internal")

	entries, err := os.ReadDir(base)
	if err != nil {
		return out
	}
	fset := token.NewFileSet()
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		dir := filepath.Join(base, e.Name())
		files, err := os.ReadDir(dir)
		if err != nil {
			continue
		}
		names := map[string]bool{}
		for _, f := range files {
			if f.IsDir() || !strings.HasSuffix(f.Name(), ".go") || strings.HasSuffix(f.Name(), "_test.go") {
				continue
			}
			parsed, err := parser.ParseFile(fset, filepath.Join(dir, f.Name()), nil, parser.SkipObjectResolution)
			if err != nil {
				continue
			}
			collectExported(parsed, names)
		}
		if len(names) > 0 {
			out[e.Name()] = names
		}
	}
	return out
}

// collectExported 收集一个文件里的导出标识符（含结构体字段和方法）。
func collectExported(file *ast.File, names map[string]bool) {
	for _, decl := range file.Decls {
		switch d := decl.(type) {
		case *ast.FuncDecl:
			if d.Name.IsExported() {
				names[d.Name.Name] = true
			}
		case *ast.GenDecl:
			for _, spec := range d.Specs {
				switch s := spec.(type) {
				case *ast.TypeSpec:
					if s.Name.IsExported() {
						names[s.Name.Name] = true
					}
				case *ast.ValueSpec:
					for _, n := range s.Names {
						if n.IsExported() {
							names[n.Name] = true
						}
					}
				}
			}
		}
	}
}

// dedupe 去重并保持顺序。
func dedupe(in []string) []string {
	seen := map[string]bool{}
	out := in[:0]
	for _, s := range in {
		if seen[s] {
			continue
		}
		seen[s] = true
		out = append(out, s)
	}
	return out
}

// isDigits 判断是否全是数字。
func isDigits(s string) bool {
	if s == "" {
		return false
	}
	for _, r := range s {
		if r < '0' || r > '9' {
			return false
		}
	}
	return true
}
