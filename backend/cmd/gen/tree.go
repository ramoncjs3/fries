package main

import (
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// treeIgnore 是不进目录树的东西：生成物、依赖、编辑器杂物。
var treeIgnore = map[string]bool{
	".git":         true,
	".DS_Store":    true,
	".idea":        true,
	".vscode":      true,
	"node_modules": true,
	"dist":         true,
	dirBin:         true,
	dirTmp:         true,
	".vite":        true,
}

// runTree 打印**当前真实**的目录树。
//
// 文档里不要手抄目录结构 —— 抄了就会漂（DECISIONS.md §1.1）。要看就跑这个。
func runTree(root string, args []string) error {
	fs := flag.NewFlagSet("tree", flag.ContinueOnError)
	depth := fs.Int("depth", 3, "最大层级")
	all := fs.Bool("all", false, "连文件一起列（默认只列目录）")
	if err := fs.Parse(args); err != nil {
		return err
	}

	fmt.Println(filepath.Base(root) + "/")
	return walk(root, "", 1, *depth, *all)
}

func walk(dir, prefix string, level, maxDepth int, withFiles bool) error {
	if level > maxDepth {
		return nil
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		return err
	}

	kept := entries[:0]
	for _, e := range entries {
		if treeIgnore[e.Name()] {
			continue
		}
		if !withFiles && !e.IsDir() {
			continue
		}
		kept = append(kept, e)
	}
	sort.Slice(kept, func(i, j int) bool {
		if kept[i].IsDir() != kept[j].IsDir() {
			return kept[i].IsDir()
		}
		return kept[i].Name() < kept[j].Name()
	})

	for i, e := range kept {
		last := i == len(kept)-1
		branch, indent := "├── ", "│   "
		if last {
			branch, indent = "└── ", "    "
		}
		name := e.Name()
		if e.IsDir() {
			name += "/"
		}
		fmt.Println(prefix + branch + name)
		if e.IsDir() {
			if err := walk(filepath.Join(dir, e.Name()), prefix+indent, level+1, maxDepth, withFiles); err != nil {
				return err
			}
		}
	}
	return nil
}

// hasSuffixAny 判断字符串是否以给定后缀之一结尾。
func hasSuffixAny(s string, suffixes ...string) bool {
	for _, suffix := range suffixes {
		if strings.HasSuffix(s, suffix) {
			return true
		}
	}
	return false
}
