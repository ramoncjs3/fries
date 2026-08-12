package repo

import "strings"

// likeEscaper 转义 ILIKE 的元字符。
//
// **反斜杠必须排在最前面** —— 先换 % 再换 \ 的话，第一步产生的 `\%` 会被第二步
// 再转义成 `\\%`，模式就错了。
var likeEscaper = strings.NewReplacer(`\`, `\\`, `%`, `\%`, `_`, `\_`)

// LikePattern 把用户输入的关键词变成 ILIKE 用的模糊匹配模式：`li` → `%li%`。
//
// 所有列表页的文本筛选都该走它 —— **等值匹配是错的**：用户输入 `li` 期望能筛出
// `list`，而不是什么都没有。
//
// 元字符一律转义：不转义的话用户敲一个 `%` 就等于「匹配全部」，敲 `_` 就变成
// 通配单字符，筛出来的东西会莫名其妙。
//
// 空串（或只有空白）返回 nil，表示「这个条件不生效」，正好喂给 sqlc.narg。
func LikePattern(keyword string) *string {
	keyword = strings.TrimSpace(keyword)
	if keyword == "" {
		return nil
	}
	pattern := "%" + likeEscaper.Replace(keyword) + "%"
	return &pattern
}
