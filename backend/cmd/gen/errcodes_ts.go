package main

import (
	"bytes"
	"fmt"
	"path/filepath"
	"sort"

	"github.com/ramoncjs3/fries/internal/errs"
)

// clientOnlyCodes 是前端自己合成、后端永远不会下发的 code。
//
// 它们进不了后端注册表（后端没有 Define），但前端确实要按它们判分支
// （网络层的失败在拿到 HTTP 响应之前就发生了），所以并进联合类型里。
var clientOnlyCodes = []string{
	"common.network_error", // fetch 失败：连不上 / 超时 / 被浏览器挡下
}

// runErrCodesTS 由错误码注册表生成 frontend/src/api/errorCodes.ts。
//
// 兑现 DECISIONS.md §4.7.3 那句「错误码打错编译期就报」。原先前端把 code 当裸 string
// 比，`err.code === 'commmon.x'`（敲错一个字母）编译不报、运行时静默不匹配 ——
// 正是这个项目反复警告的「看着对、其实坏」。这里把 code 收成一个联合类型：
// 敲错的字面量不在联合里，tsc 当场报。
//
// 作用域和 errdoc 一致（都读 errs.All()）：目前是 common/auth/perm 那批公共码，
// 也正是前端唯一会判的那批。模块自己的码等模块生成器落地时按 errdoc 的老规矩
// 补 blank import，两个生成器一起就全了。
//
// ⚠️ **这个命令绝不能带 `-tags genonly`**（不像 lint-sql / tenant-queries）。genonly 会把
// internal/config、各 service 包的 init 摘掉，errs.All() 随之少码，生成的联合类型静默缩水，
// 前端 `err.code === '少掉的码'` 反而编译报错。Makefile 里用的是不带标签的 $(GEN)，别改。
func runErrCodesTS(root string, args []string) error {
	check, err := checkFlag("errcodes-ts", args)
	if err != nil {
		return err
	}

	server := make([]string, 0, len(errs.All()))
	for _, c := range errs.All() {
		server = append(server, c.Code)
	}
	sort.Strings(server)

	client := append([]string(nil), clientOnlyCodes...)
	sort.Strings(client)

	var b bytes.Buffer
	b.WriteString("// 本文件由 `make errcodes-ts` 自动生成，请勿手改。\n")
	b.WriteString("// 数据源是 Go 侧错误码注册表（internal/errs）+ 前端合成码，改后重跑生成。\n")
	b.WriteString("// `make check` 会校验它是最新的（DECISIONS.md §4.7.3）。\n\n")
	b.WriteString("// 后端注册表里的错误码。前端只按 code 判分支，文案读 detail（后端给的中文）。\n")
	writeTSUnion(&b, "ServerErrorCode", server)
	b.WriteString("\n// 前端自己合成、后端不会下发的 code（网络层失败等）。\n")
	writeTSUnion(&b, "ClientErrorCode", client)
	b.WriteString("\nexport type ErrorCode = ServerErrorCode | ClientErrorCode\n")

	return writeOrCheck(
		filepath.Join(root, "frontend", "src", "api", "errorCodes.ts"),
		b.Bytes(), check, "`make errcodes-ts`")
}

// writeTSUnion 写一个 TS 字符串字面量联合类型。空集写成 never。
func writeTSUnion(b *bytes.Buffer, name string, codes []string) {
	if len(codes) == 0 {
		fmt.Fprintf(b, "export type %s = never\n", name)
		return
	}
	fmt.Fprintf(b, "export type %s =\n", name)
	for _, c := range codes {
		fmt.Fprintf(b, "  | '%s'\n", c)
	}
}
