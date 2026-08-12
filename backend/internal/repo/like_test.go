package repo_test

import (
	"testing"

	"github.com/ramoncjs3/fries/internal/repo"
)

func TestLikePattern(t *testing.T) {
	cases := []struct {
		name  string
		input string
		want  string // 空字符串表示期望 nil
	}{
		{"普通关键词两边加通配", "li", "%li%"},
		{"去掉首尾空白", "  list  ", "%list%"},
		{"空串不生效", "", ""},
		{"只有空白也不生效", "   ", ""},
		// 下面三条是重点：用户敲进来的元字符必须失去通配能力
		{"百分号被转义", "50%", `%50\%%`},
		{"下划线被转义", "a_b", `%a\_b%`},
		{"反斜杠被转义", `a\b`, `%a\\b%`},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := repo.LikePattern(c.input)
			if c.want == "" {
				if got != nil {
					t.Fatalf("期望 nil，得到 %q", *got)
				}
				return
			}
			if got == nil {
				t.Fatalf("期望 %q，得到 nil", c.want)
			}
			if *got != c.want {
				t.Errorf("期望 %q，得到 %q", c.want, *got)
			}
		})
	}
}
