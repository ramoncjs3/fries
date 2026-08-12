package errs_test

import (
	"errors"
	"fmt"
	"net/http"
	"testing"

	"github.com/ramoncjs3/fries/internal/errs"
)

func TestDefineRejectsDuplicate(t *testing.T) {
	code := "testdup.same_code"
	errs.Define(code, http.StatusConflict, "重复")

	defer func() {
		if recover() == nil {
			t.Fatal("重复注册同一个错误码必须 panic")
		}
	}()
	errs.Define(code, http.StatusConflict, "重复")
}

func TestDefineRejectsBadInput(t *testing.T) {
	cases := map[string]func(){
		"缺少 domain":   func() { errs.Define("nodomain", 400, "x") },
		"大写字母":        func() { errs.Define("test.BadCase", 400, "x") },
		"中划线":         func() { errs.Define("test.bad-case", 400, "x") },
		"状态码不是 4/5xx": func() { errs.Define("testbad.status", 200, "x") },
		"没有文案":        func() { errs.Define("testbad.nomessage", 400, "  ") },
	}
	for name, fn := range cases {
		t.Run(name, func(t *testing.T) {
			defer func() {
				if recover() == nil {
					t.Fatalf("%s 必须 panic", name)
				}
			}()
			fn()
		})
	}
}

func TestBuiltinAllRegistered(t *testing.T) {
	// DECISIONS.md §4.6 定的 16 个 + 多租户加的 tenant.suspended（MULTI-TENANCY.md §7.6）。
	// 这个数字写死是有意的：内置错误码是全站共用的，前端对每一个都有全局处理，
	// 悄悄多一个就意味着有一类错误前端不认识。加了就要同时改这里和文档。
	const wantBuiltin = 17
	if got := len(errs.Builtin()); got != wantBuiltin {
		t.Fatalf("内置错误码应该有 %d 个，Builtin() 返回了 %d 个", wantBuiltin, got)
	}
	for _, c := range errs.Builtin() {
		found, ok := errs.Lookup(c.Code)
		if !ok {
			t.Errorf("内置错误码 %s 没进注册表", c.Code)
			continue
		}
		if found != c {
			t.Errorf("错误码 %s 在注册表里是另一个实例", c.Code)
		}
	}
}

func TestFromResolvesWrappedCode(t *testing.T) {
	sentinel := errors.New("pq: connection refused")

	t.Run("裸 Code", func(t *testing.T) {
		e, ok := errs.From(errs.NotFound)
		if !ok || e.Code != errs.NotFound {
			t.Fatalf("From(*Code) 应识别出 %s，得到 %+v ok=%v", errs.NotFound.Code, e, ok)
		}
	})

	t.Run("Wrap 过的 Error", func(t *testing.T) {
		wrapped := fmt.Errorf("查库失败: %w", errs.Internal.Wrap(sentinel))
		e, ok := errs.From(wrapped)
		if !ok || e.Code != errs.Internal {
			t.Fatalf("From 应穿透 fmt.Errorf 找到错误码，得到 %+v ok=%v", e, ok)
		}
		if !errors.Is(wrapped, sentinel) {
			t.Error("内部原因应能被 errors.Is 找到（日志需要）")
		}
		if !errors.Is(wrapped, errs.Internal) {
			t.Error("errors.Is 应能匹配错误码本身")
		}
	})

	t.Run("无关 error", func(t *testing.T) {
		if _, ok := errs.From(sentinel); ok {
			t.Fatal("没有错误码的 error 不能被识别成业务错误")
		}
	})
}

func TestErrorChaining(t *testing.T) {
	e := errs.ValidationFailed.
		Detailf("第 %d 行有问题", 3).
		WithField("body.username", "该用户名已被占用")

	if e.Detail != "第 3 行有问题" {
		t.Errorf("Detailf 没生效: %q", e.Detail)
	}
	if len(e.Fields) != 1 || e.Fields[0].Location != "body.username" {
		t.Errorf("WithField 没生效: %+v", e.Fields)
	}
	if e.Code.Status != http.StatusBadRequest {
		t.Errorf("状态码应来自错误码本身，得到 %d", e.Code.Status)
	}
}

func TestForStatusNeverNil(t *testing.T) {
	for _, s := range []int{0, 200, 400, 401, 403, 404, 409, 413, 422, 429, 499, 500, 503, 504, 599} {
		if c := errs.ForStatus(s); c == nil {
			t.Errorf("ForStatus(%d) 返回了 nil", s)
		}
	}
	if errs.ForStatus(http.StatusUnprocessableEntity) != errs.ValidationFailed {
		t.Error("huma 的 422 校验失败要归到 common.validation_failed（§4.6 定义为 400）")
	}
}

func TestAllSorted(t *testing.T) {
	all := errs.All()
	if len(all) < 16 {
		t.Fatalf("注册表里只有 %d 个错误码", len(all))
	}
	for i := 1; i < len(all); i++ {
		if all[i-1].Code >= all[i].Code {
			t.Fatalf("All() 必须按 code 排序，%q 出现在 %q 之前", all[i-1].Code, all[i].Code)
		}
	}
}
