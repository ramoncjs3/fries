package audit

import (
	"fmt"
	"strings"
)

// sensitiveHints 是「字段名里出现这些词就脱敏」的清单。
//
// 宁可多脱一个，也不要让密码进审计表 —— 审计表是永久保留、多人可查的
// （DECISIONS.md §6）。
var sensitiveHints = []string{
	"password", "passwd", "secret", "token", "api_key", "apikey",
	"credential", "private_key", "authorization", "cookie", "signature",
}

// maskedValue 是脱敏后的占位值。
const maskedValue = "***"

// maskValue 按字段名决定要不要脱敏，并把过长的值截断。
func maskValue(key string, value any) any {
	lower := strings.ToLower(key)
	for _, hint := range sensitiveHints {
		if strings.Contains(lower, hint) {
			return maskedValue
		}
	}

	s, ok := value.(string)
	if !ok {
		return value
	}
	return truncate(s, maxDetailValueLen)
}

// MaskString 给 handler 用：把一个值脱敏成「只看得出有没有、看不出是什么」。
//
//	audit.Detail(ctx, "new_password", audit.MaskString(pwd))
func MaskString(s string) string {
	if s == "" {
		return ""
	}
	return maskedValue
}

// Preview 把任意值转成适合进审计摘要的短字符串。
func Preview(v any) string {
	return truncate(fmt.Sprintf("%v", v), maxDetailValueLen)
}

func truncate(s string, n int) string {
	runes := []rune(s)
	if len(runes) <= n {
		return s
	}
	return string(runes[:n]) + "…"
}
