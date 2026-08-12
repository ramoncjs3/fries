package main

import (
	"bytes"
	"fmt"
	"path/filepath"
	"sort"

	"github.com/ramoncjs3/fries/internal/errs"
	// 每个声明错误码的包都要在这里 import 一下，否则它的 init 不跑、
	// 错误码进不了注册表，文档里也就看不到。新增模块时这行由生成器维护：
	//   _ "github.com/ramoncjs3/fries/internal/service/<key>"
)

// domainTitles 给错误码域配个中文标题；没配的按「模块」处理。
var domainTitles = map[string]string{
	"common": "通用",
	"auth":   "认证与会话",
	"perm":   "权限",
}

// runErrdoc 由错误码注册表生成 docs/ERROR_CODES.md。
func runErrdoc(root string, args []string) error {
	check, err := checkFlag("errdoc", args)
	if err != nil {
		return err
	}

	all := errs.All()
	byDomain := map[string][]*errs.Code{}
	for _, c := range all {
		byDomain[c.Domain()] = append(byDomain[c.Domain()], c)
	}

	domains := make([]string, 0, len(byDomain))
	for d := range byDomain {
		domains = append(domains, d)
	}
	// common / auth / perm 排前面，其余模块按字母序跟在后面。
	order := map[string]int{"common": 0, "auth": 1, "perm": 2}
	sort.Slice(domains, func(i, j int) bool {
		oi, oki := order[domains[i]]
		oj, okj := order[domains[j]]
		switch {
		case oki && okj:
			return oi < oj
		case oki:
			return true
		case okj:
			return false
		}
		return domains[i] < domains[j]
	})

	var b bytes.Buffer
	b.WriteString("# 错误码对照表\n\n")
	b.WriteString("> **本文件由 `make errdoc` 自动生成，请勿手改。**\n")
	b.WriteString("> 错误码在代码里用 `errs.Define(...)` 声明，改代码后重跑生成。\n")
	b.WriteString("> `make check` 会校验本文件是最新的。\n\n")
	fmt.Fprintf(&b, "当前共 **%d** 个错误码。前端只读 `code`（机器判断）和 `detail`（中文文案）。\n", len(all))

	for _, d := range domains {
		title, ok := domainTitles[d]
		if !ok {
			title = "模块"
		}
		fmt.Fprintf(&b, "\n## %s —— %s\n\n", d, title)
		b.WriteString("| code | HTTP | 文案 |\n|---|---|---|\n")
		for _, c := range byDomain[d] {
			fmt.Fprintf(&b, "| `%s` | %d | %s |\n", c.Code, c.Status, c.Message)
		}
	}

	return writeOrCheck(filepath.Join(root, "docs", "ERROR_CODES.md"), b.Bytes(), check, "`make errdoc`")
}
