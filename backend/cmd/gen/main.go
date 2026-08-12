// Command gen（fries-gen）是项目的生成与检查工具。
//
//	gen errdoc          # 由错误码注册表生成 docs/ERROR_CODES.md
//	gen schemadoc       # 由迁移文件生成 docs/SCHEMA.md
//	gen tree            # 打印真实目录树
//	gen lint-docs       # 检查文档里的路径 / 标识符 / 章节号 / make 目标是否有效
//	gen lint-structure  # 检查目录结构是否符合 DECISIONS.md §1.1
//	gen lint-sql        # 检查每条查询有没有带租户条件（MULTI-TENANCY.md §1.2 ③）
//	gen dsn             # 打印数据库连接串（Makefile 给 goose 用）
//	gen dev-admin       # 把本地 admin 密码重置为 admin（只准对本机库跑）
//	gen tenant-queries  # 由 sqlc 产物生成租户绑定的查询句柄（ForTenant）
//	gen module -name x  # 按 modules/x.yaml 生成模块代码（第 ⑤ 步实现）
//
// 加 -check 的生成类命令只比对不写文件，不一致就退出码 1 —— make check 用它
// 确认「生成物没被手改，也没忘了重跑」。
package main

import (
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// command 是一个子命令。
type command struct {
	name  string
	usage string
	run   func(root string, args []string) error
}

// extraCommands 是「要连数据库 / 要读 config 的」子命令。
//
// 它们放在带 `!genonly` 构建标签的文件里单独注册，为的是让
// `go run -tags genonly ./cmd/gen tenant-queries` 只编译生成器自己 ——
// 生成器要能在 internal/repo 还编译不过的时候跑起来（那正是它要去修的状态）。
// 见 Makefile 的 gen-tenant-queries。
var extraCommands []command

// baseCommands 是不依赖数据库和 config 的子命令，任何构建标签下都在。
func baseCommands() []command {
	return []command{
		{"errdoc", "生成 docs/ERROR_CODES.md", runErrdoc},
		{"errcodes-ts", "生成 frontend 错误码联合类型", runErrCodesTS},
		{"schemadoc", "生成 docs/SCHEMA.md", runSchemadoc},
		{"tree", "打印真实目录树", runTree},
		{"lint-docs", "检查文档引用是否有效", runLintDocs},
		{"lint-structure", "检查目录结构是否符合 DECISIONS.md §1.1", runLintStructure},
		{"lint-sql", "检查每条查询有没有带租户条件", runLintSQL},
		{"tenant-queries", "生成租户绑定的查询句柄（ForTenant）", runTenantQueries},
		{"module", "生成模块代码（第 ⑤ 步实现）", runModule},
	}
}

func main() {
	commands := append(baseCommands(), extraCommands...)
	sort.Slice(commands, func(i, j int) bool { return commands[i].name < commands[j].name })

	if len(os.Args) < 2 {
		usage(commands)
		os.Exit(2)
	}

	name := os.Args[1]
	for _, c := range commands {
		if c.name != name {
			continue
		}
		root, err := findRoot()
		if err != nil {
			fail(err)
		}
		if err := c.run(root, os.Args[2:]); err != nil {
			fail(err)
		}
		return
	}

	fmt.Fprintf(os.Stderr, "未知子命令：%s\n\n", name)
	usage(commands)
	os.Exit(2)
}

func usage(commands []command) {
	fmt.Fprintln(os.Stderr, "用法：gen <子命令> [参数]")
	fmt.Fprintln(os.Stderr)
	for _, c := range commands {
		fmt.Fprintf(os.Stderr, "  %-16s %s\n", c.name, c.usage)
	}
}

func fail(err error) {
	fmt.Fprintf(os.Stderr, "%v\n", err)
	os.Exit(1)
}

// rootMarkers 是「这就是仓库根目录」的标志文件。
var rootMarkers = []string{"AGENTS.md", "docs/DECISIONS.md", "Makefile"}

// findRoot 从当前目录逐级往上找仓库根目录 —— 这样在 backend/ 里跑和在根目录跑都行。
func findRoot() (string, error) {
	dir, err := os.Getwd()
	if err != nil {
		return "", err
	}
	for {
		ok := true
		for _, marker := range rootMarkers {
			if _, err := os.Stat(filepath.Join(dir, marker)); err != nil {
				ok = false
				break
			}
		}
		if ok {
			return dir, nil
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return "", fmt.Errorf("找不到仓库根目录（沿着上级目录没找到 %s）", strings.Join(rootMarkers, "、"))
		}
		dir = parent
	}
}

// writeOrCheck 按 -check 决定是写文件还是只比对。
func writeOrCheck(path string, content []byte, check bool, regenCmd string) error {
	rel := path
	if root, err := findRoot(); err == nil {
		if r, err := filepath.Rel(root, path); err == nil {
			rel = r
		}
	}

	if !check {
		if err := os.WriteFile(path, content, 0o644); err != nil {
			return fmt.Errorf("写 %s: %w", rel, err)
		}
		fmt.Printf("已生成 %s\n", rel)
		return nil
	}

	old, err := os.ReadFile(path)
	if err != nil {
		return fmt.Errorf("%s 还不存在，跑一下 %s", rel, regenCmd)
	}
	if string(old) != string(content) {
		return fmt.Errorf("%s 不是最新的（或者被手改了）。跑一下 %s 再提交", rel, regenCmd)
	}
	return nil
}

// checkFlag 给生成类子命令统一加 -check。
func checkFlag(name string, args []string) (bool, error) {
	fs := flag.NewFlagSet(name, flag.ContinueOnError)
	check := fs.Bool("check", false, "只比对不写文件，不一致则退出码 1")
	if err := fs.Parse(args); err != nil {
		return false, err
	}
	return *check, nil
}

// problems 收集一批问题，最后一起报 —— 一次跑完看全，别修一个跑一次。
type problems []string

func (p *problems) addf(format string, a ...any) {
	*p = append(*p, fmt.Sprintf(format, a...))
}

// err 把收集到的问题拼成一个 error（没问题返回 nil）。
func (p problems) err(title string) error {
	if len(p) == 0 {
		return nil
	}
	sorted := append([]string(nil), p...)
	sort.Strings(sorted)
	return fmt.Errorf("%s（%d 项）：\n  - %s", title, len(sorted), strings.Join(sorted, "\n  - "))
}
