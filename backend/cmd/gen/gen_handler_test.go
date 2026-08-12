package main

import (
	"strings"
	"testing"
)

func TestGenHandlerParses(t *testing.T) {
	def := validGenerated()
	mustParseGo(t, genHandler(&def))
}

func TestGenHandlerStructure(t *testing.T) {
	def := validGenerated()
	src := squash(genHandler(&def))

	for _, want := range []string{
		"package handler",
		"Safe to edit",
		`suppliersvc "github.com/ramoncjs3/fries/internal/service/supplier"`,
		"type Supplier struct {",
		"svc *suppliersvc.Service",
		"func NewSupplier(svc *suppliersvc.Service) *Supplier",
		"func RegisterSupplier(api huma.API, h *Supplier)",
		"func (h *Supplier) list(ctx context.Context, in *ListSupplierInput) (*httpx.PageResponse[suppliersvc.Supplier], error)",
		"func (h *Supplier) get(ctx context.Context, in *GetSupplierInput)",
		"func (h *Supplier) create(ctx context.Context, in *CreateSupplierInput)",
		"func (h *Supplier) update(ctx context.Context, in *UpdateSupplierInput)",
		"func (h *Supplier) remove(ctx context.Context, in *DeleteSupplierInput)",
		"func (h *Supplier) export(ctx context.Context, in *ListSupplierInput)",
		"func toSupplierInput(body SupplierBody) (suppliersvc.Input, error)",
	} {
		if !strings.Contains(src, want) {
			t.Errorf("handler 产出里应包含：%s", want)
		}
	}
}

// TestGenHandlerRoutes 每个动作一条路由，perm 点对得上（read/export 用字面量）。
func TestGenHandlerRoutes(t *testing.T) {
	def := validGenerated()
	src := squash(genHandler(&def))

	for _, want := range []string{
		`modules.Supplier.Point(perm.ActionList)`,
		`modules.Supplier.Point("read")`, // 非标准动作用字面量
		`modules.Supplier.Point(perm.ActionCreate)`,
		`modules.Supplier.Point("export")`, // 导出
		`Path: "/suppliers"`,
		`Path: "/suppliers/{id}"`,
		`Path: "/suppliers/export"`,
		`DefaultStatus: http.StatusCreated`,
	} {
		if !strings.Contains(src, want) {
			t.Errorf("handler 路由产出里应包含：%s", want)
		}
	}
}

// TestGenHandlerBody 校验 Body 类型与 tag：decimal/date 走 string，可选带 omitempty，enum 有 enum tag。
func TestGenHandlerBody(t *testing.T) {
	def := validGenerated()
	src := squash(genHandler(&def))

	for _, want := range []string{
		`Name string `,             // string 字段
		`minLength:"1"`,            // required string
		`maxLength:"100"`,          // name max
		`Credit string `,           // decimal 走 string
		`StartedAt string `,        // date 走 string
		`format:"date"`,            // date tag
		`enum:"active,terminated"`, // enum 排序拼
		`json:"remark,omitempty"`,  // 可选带 omitempty
	} {
		if !strings.Contains(src, want) {
			t.Errorf("handler Body 产出里应包含：%s", want)
		}
	}
}

// TestGenHandlerToInput 校验 Body→Input 的内联解析：decimal/date 解析 + 可空判空。
func TestGenHandlerToInput(t *testing.T) {
	def := validGenerated()
	src := squash(genHandler(&def))

	for _, want := range []string{
		"in.Name = body.Name",                      // required string 直接赋
		`if body.Credit != "" {`,                   // 可选 decimal 判空
		"decimal.NewFromString(body.Credit)",       // decimal 解析
		`if body.StartedAt != "" {`,                // 可选 date 判空
		`time.Parse("2006-01-02", body.StartedAt)`, // date 解析
		"errInvalidField",                          // 解析失败字段级错误
	} {
		if !strings.Contains(src, want) {
			t.Errorf("handler toInput 产出里应包含：%s", want)
		}
	}
}

// TestGenHandlerFilterConv list 里把 query filter 内联翻成 service 的指针字段。
func TestGenHandlerFilterConv(t *testing.T) {
	def := validGenerated()
	src := squash(genHandler(&def))

	for _, want := range []string{
		"filter := suppliersvc.ListFilter{",
		"Keyword: in.Keyword,", // 有可搜字段
		`if in.Status != "" {`, // enum filter → 判空取指针
		"filter.Status = &v",
		`if in.StartedAt != "" {`,                // date filter
		`time.Parse("2006-01-02", in.StartedAt)`, // date filter 解析
	} {
		if !strings.Contains(src, want) {
			t.Errorf("handler filter 产出里应包含：%s", want)
		}
	}
}

// TestGenHandlerOptionalNumericBool 可选 int/bool 的 Body 用指针（区分「没传」和 0/false），
// 让 service.applyDefaults 能兜默认值；toInput 里 bool 直接赋、int 解引用转 int32。
func TestGenHandlerOptionalNumericBool(t *testing.T) {
	def := ModuleDef{
		Key: "widget", Name: "部件", Generated: true, Scoped: true,
		Menu: Menu{Path: "/widgets", Icon: "box"},
		Fields: []Field{
			{Name: "name", Type: typeString, Label: "名称", Required: true, Max: 100},
			{Name: "qty", Type: typeInt, Label: "数量"},                      // 可选 int
			{Name: "active", Type: typeBool, Label: "启用", Default: "true"}, // 可选 bool + 默认
			{Name: "level", Type: typeInt, Label: "等级", Required: true},    // 必填 int
		},
		Actions: []string{actList, actCreate},
	}
	src := squash(genHandler(&def))
	for _, want := range []string{
		"Qty *int `json:\"qty,omitempty\"`",        // 可选 int → 指针
		"Active *bool `json:\"active,omitempty\"`", // 可选 bool → 指针
		"Level int `json:\"level\"`",               // 必填 int → 值
		"in.Active = body.Active",                  // bool 直接赋（类型一致）
		"if body.Qty != nil {",                     // 可选 int 判 nil
		"v := int32(*body.Qty)",                    // 解引用转 int32
	} {
		if !strings.Contains(src, want) {
			t.Errorf("handler 指针 body 产出应包含：%s", want)
		}
	}
	if strings.Contains(src, "if body.Qty != 0") {
		t.Error("可选 int 不该再用 != 0 哨兵（合法的 0 会丢）")
	}
}

// TestGenHandlerDeleteOnly 只 delete 动作时不产 time/decimal import（否则未用 import）。
func TestGenHandlerDeleteOnly(t *testing.T) {
	def := ModuleDef{
		Key: "voucher", Name: "凭证", Generated: true, Scoped: true,
		Menu: Menu{Path: "/vouchers", Icon: "file"},
		Fields: []Field{
			{Name: "amount", Type: typeDecimal, Label: "金额", Precision: []int{18, 2}},
			{Name: "issued_at", Type: typeDate, Label: "签发日"},
		},
		Actions: []string{actDelete}, // 只删除，不产解析代码
	}
	src := genHandler(&def)
	mustParseGo(t, src)
	if strings.Contains(src, `"time"`) || strings.Contains(src, "shopspring/decimal") {
		t.Error("只 delete 动作不该 import time/decimal（未用 import 编译不过）")
	}
}
